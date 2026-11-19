package approvals

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Secret store limits (design section 10).
const (
	SecretRefPrefix     = "secret://tachyon/"
	MaxSecretValue      = 16 << 10
	MaxSecretFile       = 64 << 10
	MaxLiveSecrets      = 20
	SecretsPerMinute    = 10
	secretGrace         = 24 * time.Hour
	secretNoExpiryTTL   = 30 * 24 * time.Hour
	SecretKeyFileEnv    = "TACHYON_SECRET_KEY_FILE"
	maxIdempotencyChars = 128
)

var (
	// ErrSecretsUnavailable means no usable key file is configured.
	ErrSecretsUnavailable = errors.New("secret store unavailable")
	ErrSecretRefInvalid   = errors.New("secret reference is not valid for this item and field")
	ErrSecretLimit        = errors.New("too many secrets for this item")
	ErrSecretRate         = errors.New("too many secret submissions")
	ErrIdempotencyReuse   = errors.New("idempotency key reused with a different secret")
)

// Secrets keeps submitted secret values encrypted at rest. Redeem is a Go method for Tachyon-run
// consumers only (G4); there is no HTTP route that returns a value.
type Secrets struct {
	DB  *localstore.Store
	Now func() time.Time

	aead   cipher.AEAD
	macKey []byte
	reason string

	mu   sync.Mutex
	hits map[string][]time.Time
}

// SecretMeta is what a submission returns. It never holds the value.
type SecretMeta struct {
	Ref         string    `json:"secret_ref"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Size        int       `json:"size"`
	Fingerprint string    `json:"fingerprint"`
}

// NewSecrets loads the key from keyFile: 32 raw bytes, or 64 hex characters. With no usable key the store
// still exists and answers ErrSecretsUnavailable (503 secrets_unavailable) so the UI shows secret fields as unsupported.
func NewSecrets(db *localstore.Store, keyFile string) *Secrets {
	s := &Secrets{DB: db, Now: time.Now, hits: map[string][]time.Time{}}
	key, err := readSecretKey(keyFile)
	if err != nil {
		s.reason = err.Error()
		return s
	}
	block, err := aes.NewCipher(key)
	if err == nil {
		s.aead, err = cipher.NewGCM(block)
	}
	if err != nil {
		s.reason = err.Error()
		return s
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("tachyon-secret-idempotency"))
	s.macKey = mac.Sum(nil)
	return s
}

func readSecretKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New(SecretKeyFileEnv + " is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("key file unreadable")
	}
	if len(raw) == 32 {
		return raw, nil
	}
	if k, err := hex.DecodeString(strings.TrimSpace(string(raw))); err == nil && len(k) == 32 {
		return k, nil
	}
	return nil, errors.New("key file must hold 32 bytes or 64 hex characters")
}

// Available reports whether submissions can be accepted. Reason says why not, without any key material.
func (s *Secrets) Available() bool { return s != nil && s.aead != nil }
func (s *Secrets) Reason() string {
	if s == nil {
		return "not configured"
	}
	return s.reason
}

// PutRequest is one submission. Expires is the item's expiry (zero for none).
type PutRequest struct {
	ItemID, Field, Session, IdempotencyKey string
	Value                                  []byte
	Expires                                time.Time
}

func (s *Secrets) rate(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	keep := s.hits[session][:0]
	for _, t := range s.hits[session] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= SecretsPerMinute {
		s.hits[session] = keep
		return false
	}
	s.hits[session] = append(keep, now)
	return true
}

func (s *Secrets) idemMAC(r PutRequest) []byte {
	m := hmac.New(sha256.New, s.macKey)
	fmt.Fprintf(m, "%s\x00%s\x00", r.ItemID, r.Field)
	m.Write(r.Value)
	return m.Sum(nil)
}

func fingerprint(v []byte) string {
	h := sha256.Sum256(v)
	return "sha256:" + hex.EncodeToString(h[:4])
}

func (s *Secrets) aad(id, item, field string) []byte {
	return []byte(id + "\x00" + item + "\x00" + field)
}

// Put encrypts and stores a value. With an Idempotency-Key the same (session, key, item, field, value)
// replays the first response; the same key with a different value is ErrIdempotencyReuse.
func (s *Secrets) Put(ctx context.Context, r PutRequest) (SecretMeta, error) {
	if !s.Available() {
		return SecretMeta{}, ErrSecretsUnavailable
	}
	if len(r.IdempotencyKey) > maxIdempotencyChars {
		return SecretMeta{}, fmt.Errorf("idempotency key too long")
	}
	now := s.Now()
	var mac []byte
	if r.IdempotencyKey != "" {
		mac = s.idemMAC(r)
		var meta SecretMeta
		var old []byte
		var created, expires int64
		err := s.DB.Reader.QueryRowContext(ctx, `SELECT id,created_ns,expires_ns,size,fingerprint,idem_mac FROM approval_secrets WHERE session_ref=? AND idem_key=?`, r.Session, r.IdempotencyKey).
			Scan(&meta.Ref, &created, &expires, &meta.Size, &meta.Fingerprint, &old)
		if err == nil {
			if !hmac.Equal(old, mac) {
				return SecretMeta{}, ErrIdempotencyReuse
			}
			meta.Ref = SecretRefPrefix + meta.Ref
			meta.CreatedAt, meta.ExpiresAt = time.Unix(0, created).UTC(), time.Unix(0, expires).UTC()
			return meta, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return SecretMeta{}, err
		}
	}
	if !s.rate(r.Session) {
		return SecretMeta{}, ErrSecretRate
	}
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return SecretMeta{}, err
	}
	id := hex.EncodeToString(idb)
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SecretMeta{}, err
	}
	ct := s.aead.Seal(nil, nonce, r.Value, s.aad(id, r.ItemID, r.Field))
	expires := now.Add(secretNoExpiryTTL)
	if !r.Expires.IsZero() {
		expires = r.Expires.Add(secretGrace)
	}
	meta := SecretMeta{Ref: SecretRefPrefix + id, CreatedAt: now.UTC(), ExpiresAt: expires.UTC(), Size: len(r.Value), Fingerprint: fingerprint(r.Value)}
	err := s.DB.Writer.Submit(ctx, "approval_secret_put", func(ctx context.Context, tx *sql.Tx) error {
		var live int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_secrets WHERE item_id=? AND consumed_ns IS NULL AND expires_ns>?`, r.ItemID, now.UnixNano()).Scan(&live); err != nil {
			return err
		}
		if live >= MaxLiveSecrets {
			return ErrSecretLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_secrets(id,item_id,field,nonce,ciphertext,size,fingerprint,created_ns,expires_ns,session_ref,idem_key,idem_mac) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, r.ItemID, r.Field, nonce, ct, meta.Size, meta.Fingerprint, now.UnixNano(), expires.UnixNano(), r.Session, r.IdempotencyKey, mac)
		return err
	})
	if err != nil {
		return SecretMeta{}, err
	}
	return meta, nil
}

func refID(ref string) (string, bool) {
	id, ok := strings.CutPrefix(ref, SecretRefPrefix)
	if !ok || len(id) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", false
	}
	return id, true
}

// Validate checks that ref exists, is bound to this item and field, is not expired and not already consumed.
func (s *Secrets) Validate(ctx context.Context, itemID, field, ref string) error {
	if !s.Available() {
		return ErrSecretsUnavailable
	}
	id, ok := refID(ref)
	if !ok {
		return ErrSecretRefInvalid
	}
	var consumed sql.NullInt64
	var expires int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT consumed_ns,expires_ns FROM approval_secrets WHERE id=? AND item_id=? AND field=?`, id, itemID, field).Scan(&consumed, &expires)
	if err != nil || consumed.Valid || expires <= s.Now().UnixNano() {
		return ErrSecretRefInvalid
	}
	return nil
}

// Consume marks the references of a resolved item as used so they cannot answer another item.
func (s *Secrets) Consume(ctx context.Context, itemID string, refs []string) error {
	if !s.Available() || len(refs) == 0 {
		return nil
	}
	return s.DB.Writer.Submit(ctx, "approval_secret_consume", func(ctx context.Context, tx *sql.Tx) error {
		for _, ref := range refs {
			if id, ok := refID(ref); ok {
				if _, err := tx.ExecContext(ctx, `UPDATE approval_secrets SET consumed_ns=? WHERE id=? AND item_id=? AND consumed_ns IS NULL`, s.Now().UnixNano(), id, itemID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Redeem returns the value for a Tachyon-run consumer (a card effect or an action-run environment declared
// in the digest-bound request). It is deliberately not reachable over HTTP.
func (s *Secrets) Redeem(ctx context.Context, itemID, field, ref string) ([]byte, error) {
	if !s.Available() {
		return nil, ErrSecretsUnavailable
	}
	id, ok := refID(ref)
	if !ok {
		return nil, ErrSecretRefInvalid
	}
	var nonce, ct []byte
	var expires int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT nonce,ciphertext,expires_ns FROM approval_secrets WHERE id=? AND item_id=? AND field=?`, id, itemID, field).Scan(&nonce, &ct, &expires)
	if err != nil || expires <= s.Now().UnixNano() {
		return nil, ErrSecretRefInvalid
	}
	v, err := s.aead.Open(nil, nonce, ct, s.aad(id, itemID, field))
	if err != nil {
		return nil, ErrSecretRefInvalid
	}
	return v, nil
}

// Purge deletes expired rows, and unconsumed rows of items that are finished (denied, expired, withdrawn,
// or approved with a different answer) once they are five minutes old, so a resolve in flight can still consume
// its references. It runs from the inbox maintenance loop.
func (s *Secrets) Purge(ctx context.Context) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	var n int64
	err := s.DB.Writer.Submit(ctx, "approval_secret_purge", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM approval_secrets WHERE expires_ns<=? OR (consumed_ns IS NULL AND created_ns<? AND item_id IN (SELECT id FROM approval_items WHERE terminal=1))`, s.Now().UnixNano(), s.Now().Add(-5*time.Minute).UnixNano())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
