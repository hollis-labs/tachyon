// Package agentaccess owns incoming service identities and credentials. Tokens
// are opaque, stored only as hashes, and checked against SQLite on every call.
package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Every supported verb has authorization semantics in dataverbs.scopedLocal.
// Existing credentials retain their exact grants; adding support never expands them.
var readVerbs = []string{"planner.list", "objects.list", "objects.get", "objects.history", "objects.lookup_source"}
var portfolioReadVerbs = []string{"planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema", "planner.portfolio.resolve"}
var portfolioWriteVerbs = []string{"planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate", "planner.portfolio.reorder"}

// ActionVerbs are the registered-action tools (request, then await the result).
// They name no object kinds: a credential holding only these needs none.
var actionVerbs = []string{"actions.catalog", "actions.request", "actions.await"}
var plannerKinds = []string{"plan", "task", "epic", "sprint", "collection", "workstream", "inbox_item", "idea", "decision", "risk", "roadmap_item", "strategic_priority"}
var portfolioKinds = []string{"strategic_priority", "workstream", "roadmap_item", "idea", "decision", "risk", "inbox_item"}

type Grants struct {
	Verbs []string `json:"verbs"`
	Kinds []string `json:"kinds"`
}

func PlannerRead() Grants {
	return Grants{append([]string(nil), readVerbs...), append([]string(nil), plannerKinds...)}
}
func PortfolioRead() Grants {
	return Grants{append(append([]string(nil), readVerbs...), portfolioReadVerbs...), append([]string(nil), portfolioKinds...)}
}
func PortfolioWrite() Grants {
	g := PortfolioRead()
	g.Verbs = append(g.Verbs, portfolioWriteVerbs...)
	return g
}
func ActionRequest() Grants    { return Grants{Verbs: append([]string(nil), actionVerbs...)} }
func ActionVerb(v string) bool { return contains(actionVerbs, v) }
func PlannerKinds() []string   { return append([]string(nil), plannerKinds...) }
func ReadVerb(v string) bool   { return contains(readVerbs, v) || contains(portfolioReadVerbs, v) }
func SupportedVerb(v string) bool {
	return ReadVerb(v) || contains(portfolioWriteVerbs, v) || contains(actionVerbs, v)
}
func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
func (g Grants) Validate() error {
	onlyActions := len(g.Verbs) > 0
	for _, v := range g.Verbs {
		onlyActions = onlyActions && contains(actionVerbs, v)
	}
	if len(g.Verbs) == 0 || (len(g.Kinds) == 0 && !onlyActions) {
		return errors.New("explicit verbs and kinds are required")
	}
	for _, v := range g.Verbs {
		if !SupportedVerb(v) {
			return fmt.Errorf("unsupported service verb %q", v)
		}
	}
	for _, k := range g.Kinds {
		if !contains(plannerKinds, k) {
			return fmt.Errorf("unsupported service kind %q", k)
		}
	}
	return nil
}
func (g Grants) scopes() []string {
	out := []string{}
	for _, v := range g.Verbs {
		out = append(out, "verb:"+v)
	}
	for _, k := range g.Kinds {
		out = append(out, "kind:"+k)
	}
	return out
}
func Allows(p auth.Principal, category, value string) bool {
	return p.Verified && contains(p.Scopes, category+":"+value)
}

type Token struct {
	ID         string     `json:"id"`
	IdentityID string     `json:"identity_id"`
	Name       string     `json:"name"`
	Grants     Grants     `json:"grants"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}
type Store struct{ DB *localstore.Store }

// Issue creates a new identity, or adds a token to the exact existing identity.
// The caller must deliver the secret privately. Token metadata never includes it.
func (s *Store) Issue(ctx context.Context, identityID, name string, grants Grants, ttl time.Duration) (Token, string, error) {
	if err := grants.Validate(); err != nil {
		return Token{}, "", err
	}
	if ttl < time.Minute || ttl > 90*24*time.Hour {
		return Token{}, "", errors.New("token lifetime must be between 1 minute and 90 days")
	}
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return Token{}, "", errors.New("identity name is required (maximum 200 bytes)")
	}
	fresh := identityID == ""
	if fresh {
		identityID = uuid.NewString()
	} else if _, err := uuid.Parse(identityID); err != nil {
		return Token{}, "", errors.New("identity must be a UUID")
	}
	secret, err := tokenfile.Generate()
	if err != nil {
		return Token{}, "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	t := Token{ID: uuid.NewString(), IdentityID: identityID, Name: name, Grants: grants, CreatedAt: now, ExpiresAt: now.Add(ttl).Truncate(time.Second)}
	raw, _ := json.Marshal(grants)
	err = s.DB.Writer.Submit(ctx, "service-token.issue", func(ctx context.Context, tx *sql.Tx) error {
		if fresh {
			if _, err := tx.ExecContext(ctx, "INSERT INTO service_identities(id,name,created_at) VALUES(?,?,?)", identityID, name, now.Unix()); err != nil {
				return err
			}
		} else {
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT name FROM service_identities WHERE id=? AND disabled_at IS NULL", identityID).Scan(&stored); err != nil {
				return errors.New("active service identity not found")
			}
			if stored != name {
				return errors.New("identity name does not match")
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO service_tokens(id,identity_id,token_hash,grants,created_at,expires_at) VALUES(?,?,?,?,?,?)", t.ID, identityID, auth.HashKey(secret), string(raw), now.Unix(), t.ExpiresAt.Unix())
		return err
	})
	if err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}
func (s *Store) Verify(ctx context.Context, secret string) (auth.Principal, error) {
	if !tokenfile.ValidHex(secret) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	var id, identity, name, grants string
	var expiry int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT t.id,i.id,i.name,t.grants,t.expires_at FROM service_tokens t JOIN service_identities i ON i.id=t.identity_id WHERE t.token_hash=? AND t.revoked_at IS NULL AND i.disabled_at IS NULL`, auth.HashKey(secret)).Scan(&id, &identity, &name, &grants, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	if err != nil {
		return auth.Principal{}, auth.ErrUnavailable
	}
	if time.Now().Unix() >= expiry {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	var g Grants
	if json.Unmarshal([]byte(grants), &g) != nil || g.Validate() != nil {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	return auth.Principal{Verified: true, ID: identity, Kind: "service", Display: name, TokenID: id, Scopes: g.scopes(), ExpiresAt: time.Unix(expiry, 0).UTC()}, nil
}
func (s *Store) Revoke(ctx context.Context, id string, identity bool) error {
	table, field := "service_tokens", "revoked_at"
	if identity {
		table, field = "service_identities", "disabled_at"
	}
	return s.DB.Writer.Submit(ctx, "service-token.revoke", func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET "+field+"=COALESCE("+field+",?) WHERE id=?", time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return errors.New("credential or identity not found")
		}
		return err
	})
}
func (s *Store) List(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT t.id,i.id,i.name,t.grants,t.created_at,t.expires_at,t.revoked_at,i.disabled_at FROM service_tokens t JOIN service_identities i ON i.id=t.identity_id ORDER BY t.created_at,t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		var raw string
		var created, expires int64
		var revoked, disabled sql.NullInt64
		if err := rows.Scan(&t.ID, &t.IdentityID, &t.Name, &raw, &created, &expires, &revoked, &disabled); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &t.Grants); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		t.ExpiresAt = time.Unix(expires, 0).UTC()
		if revoked.Valid {
			v := time.Unix(revoked.Int64, 0).UTC()
			t.RevokedAt = &v
		}
		if disabled.Valid {
			v := time.Unix(disabled.Int64, 0).UTC()
			t.DisabledAt = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
