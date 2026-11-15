package access

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	libstore "github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/modules/identity"
)

// Errors a request or a command can meet.
var (
	ErrNotAgent   = errors.New("access: the caller is not an API key")
	ErrUnmapped   = errors.New("access: this key acts for no service identity")
	ErrInactive   = errors.New("access: this credential or its identity is revoked, disabled or expired")
	ErrNotFound   = errors.New("access: credential or identity not found")
	ErrNoOwner    = errors.New("access: no owner yet; run `tachyon identity setup` first")
	ErrKeysExist  = errors.New("access: identity keys already exist; reissue runs once, on a new data directory, before any key is made")
	ErrNoProxyPin = errors.New("access: no proxy identity is pinned")
)

// Service is service identities and their credentials over the database and identity modules.
// Reads go through the store's read pool, writes through WriteTx.
type Service struct {
	st  *libstore.Store
	ids *identity.Service
	now func() time.Time
}

// New returns the service. now may be nil (time.Now).
func New(st *libstore.Store, ids *identity.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{st: st, ids: ids, now: now}
}

// Identity is one service identity.
type Identity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Disabled  bool      `json:"disabled"`
	Agents    []string  `json:"agents"`
	Proxy     bool      `json:"proxy"`
}

// Credential is one identity key acting for a service identity. It never holds the key itself.
type Credential struct {
	ID            string    `json:"id"`
	IdentityID    string    `json:"identity_id"`
	Grants        Grants    `json:"grants"`
	SourceTokenID string    `json:"source_token_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Revoked       bool      `json:"revoked"`
}

// Agent is a verified caller acting for a service identity.
type Agent struct {
	Identity   Identity
	Credential Credential
	principal  libauth.Principal
}

// Allows reports whether the agent may call verb on an object of kind ("" for a call that names no
// kind): the key holds the verb's scope, the mapping grants the verb, and a kind is granted.
func (a Agent) Allows(verb, kind string) bool {
	if !a.principal.HasScope(ScopeFor(verb)) || !slices.Contains(a.Credential.Grants.Verbs, verb) {
		return false
	}
	return kind == "" || slices.Contains(a.Credential.Grants.Kinds, kind)
}

// AllowSet is every (verb, kind) the agent may use, over the whole catalog.
func (a Agent) AllowSet() map[Pair]bool {
	out := map[Pair]bool{}
	for _, v := range Verbs() {
		for _, k := range append([]string{""}, plannerKinds...) {
			if a.Allows(v, k) {
				out[Pair{v, k}] = true
			}
		}
	}
	return out
}

// CredentialID returns the identity key behind a principal: a raw key ("key:k_..."), or a token
// minted from one (its subject is the key's principal id). ok is false for anything else.
func CredentialID(p libauth.Principal) (string, bool) {
	if !p.Verified || p.Kind != "service" {
		return "", false
	}
	id, ok := strings.CutPrefix(p.ID, "key:")
	return id, ok && id != ""
}

// Resolve maps a verified principal to the agent it acts for. A principal that is not an API key
// is ErrNotAgent; a key without a mapping is ErrUnmapped; a revoked or expired credential or a
// disabled identity is ErrInactive.
func (s *Service) Resolve(ctx context.Context, p libauth.Principal) (Agent, error) {
	id, ok := CredentialID(p)
	if !ok {
		return Agent{}, ErrNotAgent
	}
	c, err := s.credential(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Agent{}, ErrUnmapped
	}
	if err != nil {
		return Agent{}, err
	}
	ident, err := s.identity(ctx, c.IdentityID)
	if err != nil {
		return Agent{}, err
	}
	if c.Revoked || ident.Disabled || !s.now().Before(c.ExpiresAt) {
		return Agent{}, ErrInactive
	}
	return Agent{Identity: ident, Credential: c, principal: p}, nil
}

func (s *Service) credential(ctx context.Context, id string) (Credential, error) {
	var c Credential
	var verbs, kinds string
	var source sql.NullString
	var created, expires int64
	var revoked sql.NullInt64
	err := s.st.Reader().QueryRowContext(ctx, `SELECT credential_id, identity_id, verbs, kinds, source_token_id, created_at, expires_at, revoked_at
		FROM service_credentials WHERE credential_id = ?`, id).Scan(&c.ID, &c.IdentityID, &verbs, &kinds, &source, &created, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, err
	}
	if err = json.Unmarshal([]byte(verbs), &c.Grants.Verbs); err != nil {
		return Credential{}, err
	}
	if err = json.Unmarshal([]byte(kinds), &c.Grants.Kinds); err != nil {
		return Credential{}, err
	}
	c.SourceTokenID, c.Revoked = source.String, revoked.Valid
	c.CreatedAt, c.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(expires, 0).UTC()
	return c, nil
}

func (s *Service) identity(ctx context.Context, id string) (Identity, error) {
	var ident Identity
	var created int64
	var disabled sql.NullInt64
	err := s.st.Reader().QueryRowContext(ctx, `SELECT id, name, created_at, disabled_at FROM service_identities WHERE id = ?`, id).
		Scan(&ident.ID, &ident.Name, &created, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, err
	}
	ident.CreatedAt, ident.Disabled = time.Unix(created, 0).UTC(), disabled.Valid
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT agent_key FROM service_identity_agents WHERE identity_id = ? ORDER BY agent_key`, id)
	if err != nil {
		return Identity{}, err
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			_ = rows.Close()
			return Identity{}, err
		}
		ident.Agents = append(ident.Agents, a)
	}
	if err := rows.Close(); err != nil {
		return Identity{}, err
	}
	var pinned int
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM service_proxy_pin WHERE identity_id = ?`, id).Scan(&pinned); err != nil {
		return Identity{}, err
	}
	ident.Proxy = pinned > 0
	return ident, nil
}

// IssueInput is a new credential.
type IssueInput struct {
	Name       string        // the service identity's name (a role, not a host)
	IdentityID string        // an existing identity; empty creates one
	Grants     Grants        // validated
	TTL        time.Duration // 1 minute .. 90 days, as the old build
}

// Issue creates an identity key with the grant's scopes and maps it to the service identity
// (created when IdentityID is empty). It returns the key once; nothing else ever holds it.
func (s *Service) Issue(ctx context.Context, in IssueInput) (string, Credential, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		return "", Credential{}, errors.New("access: the name must be 1 to 200 bytes")
	}
	if in.TTL < time.Minute || in.TTL > 90*24*time.Hour {
		return "", Credential{}, errors.New("access: the TTL must be between 1 minute and 90 days")
	}
	if err := in.Grants.Validate(); err != nil {
		return "", Credential{}, err
	}
	if in.IdentityID != "" {
		ident, err := s.identity(ctx, in.IdentityID)
		if err != nil {
			return "", Credential{}, err
		}
		if ident.Disabled {
			return "", Credential{}, ErrInactive
		}
		if ident.Name != in.Name {
			return "", Credential{}, fmt.Errorf("access: identity %s is named %q, not %q", ident.ID, ident.Name, in.Name)
		}
	}
	now := s.now().UTC().Truncate(time.Second)
	key, cred, err := s.ids.CreateKey(ctx, identity.KeyInput{Name: in.Name, Scopes: in.Grants.Scopes(), TTL: in.TTL})
	if errors.Is(err, identity.ErrNoOwner) {
		return "", Credential{}, ErrNoOwner
	}
	if err != nil {
		return "", Credential{}, err
	}
	c := Credential{ID: cred.ID, IdentityID: in.IdentityID, Grants: in.Grants.normalized(), CreatedAt: now, ExpiresAt: now.Add(in.TTL)}
	err = s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		if c.IdentityID == "" {
			c.IdentityID = newUUID()
			if _, e := tx.ExecContext(ctx, `INSERT INTO service_identities(id, name, created_at) VALUES (?, ?, ?)`, c.IdentityID, in.Name, now.Unix()); e != nil {
				return e
			}
		}
		return insertCredential(ctx, tx, c)
	})
	if err != nil {
		// The key exists in the identity module without a mapping: the agent API refuses it.
		return "", Credential{}, fmt.Errorf("access: key %s was created but not mapped (it is refused until mapped): %w", cred.ID, err)
	}
	return key, c, nil
}

func insertCredential(ctx context.Context, tx libstore.Tx, c Credential) error {
	verbs, err := json.Marshal(c.Grants.Verbs)
	if err != nil {
		return err
	}
	kinds, err := json.Marshal(c.Grants.Kinds)
	if err != nil {
		return err
	}
	var source any
	if c.SourceTokenID != "" {
		source = c.SourceTokenID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO service_credentials(credential_id, identity_id, verbs, kinds, source_token_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, c.IdentityID, string(verbs), string(kinds), source, c.CreatedAt.Unix(), c.ExpiresAt.Unix())
	return err
}

// newUUID is a random (version 4) UUID, the old build's identity id format.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// AgentKey normalizes an agent reference the way the old build did: no scheme, no "agent/"
// prefix, only the last path segment, lower case.
func AgentKey(s string) string {
	s = strings.TrimSpace(s)
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	s = strings.TrimPrefix(s, "agent/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.ToLower(s)
}

// Bind lets an identity act for agents (additive).
func (s *Service) Bind(ctx context.Context, identityID string, agents []string) error {
	ident, err := s.identity(ctx, identityID)
	if err != nil {
		return err
	}
	if ident.Disabled {
		return ErrInactive
	}
	if len(agents) == 0 {
		return errors.New("access: at least one agent is required")
	}
	now := s.now().Unix()
	return s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		for _, a := range agents {
			k := AgentKey(a)
			if k == "" || len(k) > 200 {
				return fmt.Errorf("access: agent %q is not a usable agent reference", a)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO service_identity_agents(identity_id, agent_key, created_at) VALUES (?, ?, ?)`, identityID, k, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Unbind removes agents from an identity.
func (s *Service) Unbind(ctx context.Context, identityID string, agents []string) error {
	return s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		var removed int64
		for _, a := range agents {
			res, err := tx.ExecContext(ctx, `DELETE FROM service_identity_agents WHERE identity_id = ? AND agent_key = ?`, identityID, AgentKey(a))
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			removed += n
		}
		if removed == 0 {
			return errors.New("access: binding not found")
		}
		return nil
	})
}

// PinProxy makes an identity the one trusted Tether proxy.
func (s *Service) PinProxy(ctx context.Context, identityID string) error {
	ident, err := s.identity(ctx, identityID)
	if err != nil {
		return err
	}
	if ident.Disabled {
		return ErrInactive
	}
	return s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO service_proxy_pin(id, identity_id, pinned_at) VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET identity_id = excluded.identity_id, pinned_at = excluded.pinned_at`, identityID, s.now().Unix())
		return err
	})
}

// UnpinProxy removes the pin.
func (s *Service) UnpinProxy(ctx context.Context) error {
	return s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM service_proxy_pin WHERE id = 1`)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoProxyPin
		}
		return nil
	})
}

// Revoke ends a credential at once: the agent API checks the mapping on every request, so the key
// and every token minted from it stop working. (The identity module's own key row is left; it
// has no exported revoke: Substrate gap, see docs/conversion-notes.md.)
func (s *Service) Revoke(ctx context.Context, credentialID string) error {
	return s.setOnce(ctx, `UPDATE service_credentials SET revoked_at = COALESCE(revoked_at, ?) WHERE credential_id = ?`, credentialID)
}

// Disable ends every credential of an identity at once, and blocks issue, bind and pin for it.
func (s *Service) Disable(ctx context.Context, identityID string) error {
	return s.setOnce(ctx, `UPDATE service_identities SET disabled_at = COALESCE(disabled_at, ?) WHERE id = ?`, identityID)
}

func (s *Service) setOnce(ctx context.Context, q, id string) error {
	return s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error {
		res, err := tx.ExecContext(ctx, q, s.now().Unix(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Listing is every identity with its credentials, and the old tokens. No key or hash is in it.
type Listing struct {
	Identities  []IdentityListing `json:"identities"`
	ProxyPinned string            `json:"proxy_identity,omitempty"`
}

// IdentityListing is one identity with its credentials and old tokens.
type IdentityListing struct {
	Identity
	Credentials []Credential `json:"credentials"`
	OldTokens   []OldToken   `json:"old_tokens"`
}

// OldToken is a token of the old build (no hash), with the credential that superseded it.
type OldToken struct {
	ID           string    `json:"id"`
	Grants       Grants    `json:"grants"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Revoked      bool      `json:"revoked"`
	SupersededBy string    `json:"superseded_by,omitempty"`
}

// List returns every identity, its credentials and its old tokens.
func (s *Service) List(ctx context.Context) (Listing, error) {
	var out Listing
	ids, err := s.column(ctx, `SELECT id FROM service_identities ORDER BY name, id`)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		ident, err := s.identity(ctx, id)
		if err != nil {
			return out, err
		}
		il := IdentityListing{Identity: ident, Credentials: []Credential{}, OldTokens: []OldToken{}}
		creds, err := s.column(ctx, `SELECT credential_id FROM service_credentials WHERE identity_id = ? ORDER BY created_at, credential_id`, id)
		if err != nil {
			return out, err
		}
		for _, cid := range creds {
			c, e := s.credential(ctx, cid)
			if e != nil {
				return out, e
			}
			il.Credentials = append(il.Credentials, c)
		}
		if il.OldTokens, err = s.oldTokens(ctx, `WHERE t.identity_id = ?`, id); err != nil {
			return out, err
		}
		if ident.Proxy {
			out.ProxyPinned = id
		}
		out.Identities = append(out.Identities, il)
	}
	return out, nil
}

func (s *Service) column(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Close()
}

func (s *Service) oldTokens(ctx context.Context, where string, args ...any) ([]OldToken, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT t.id, t.grants, t.created_at, t.expires_at, t.revoked_at IS NOT NULL, coalesce(c.credential_id, '')
		FROM service_tokens t LEFT JOIN service_credentials c ON c.source_token_id = t.id `+where+` ORDER BY t.created_at, t.id`, args...) //nolint:gosec // where is one of this file's fixed clauses
	if err != nil {
		return nil, err
	}
	out := []OldToken{}
	for rows.Next() {
		var t OldToken
		var grants string
		var created, expires int64
		if err := rows.Scan(&t.ID, &grants, &created, &expires, &t.Revoked, &t.SupersededBy); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(grants), &t.Grants); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("old token %s: grants: %w", t.ID, err)
		}
		t.Grants = t.Grants.normalized()
		t.CreatedAt, t.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(expires, 0).UTC()
		out = append(out, t)
	}
	return out, rows.Close()
}
