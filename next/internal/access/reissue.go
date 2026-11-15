package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/modules/identity"
)

// Reissued is one old token turned into an identity key. Key is the secret; it is never logged,
// printed or written anywhere but the 0600 file the command creates for it.
type Reissued struct {
	Identity      Identity   `json:"identity"`
	OldTokenID    string     `json:"old_token_id"`
	OldExpiresAt  time.Time  `json:"old_expires_at"`
	Credential    Credential `json:"credential"`
	Pairs         int        `json:"allowed_pairs"`
	AllowSetEqual bool       `json:"allow_set_equal"`
	ExpiryKept    bool       `json:"expiry_not_extended"`
	Key           string     `json:"-"`
}

type candidate struct {
	identity Identity
	token    OldToken
}

// Reissue turns every active old token into one identity key with exactly its grant, keyed on the
// identity id (several identities may share a name): one key per token whose own row is not
// revoked, whose identity is not disabled and that has not expired; the key's TTL is the token's
// remaining time less one second, so no expiry is ever extended. It runs once, after the owner exists and before
// any identity key does (Substrate driver's rule: no double issue). After creating the keys it
// verifies each one through the identity module and the request path: the old allow-set of
// (verb, kind) must equal the new one, and the expiry must not be later. A failed check is an
// error; the keys are then reported, not handed out.
func (s *Service) Reissue(ctx context.Context) ([]Reissued, error) {
	users, keys, err := s.identityCounts(ctx)
	if err != nil {
		return nil, err
	}
	if users == 0 {
		return nil, ErrNoOwner
	}
	var mapped int
	if e := s.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM service_credentials`).Scan(&mapped); e != nil {
		return nil, e
	}
	if keys > 0 || mapped > 0 {
		return nil, ErrKeysExist
	}
	now := s.now().UTC().Truncate(time.Second)
	cands, err := s.candidates(ctx, now)
	if err != nil {
		return nil, err
	}
	// Every grant must validate before anything is created.
	for _, c := range cands {
		if err := c.token.Grants.Validate(); err != nil {
			return nil, fmt.Errorf("access: old token %s of identity %s: %w; nothing was created", c.token.ID, c.identity.ID, err)
		}
	}
	var out []Reissued
	for _, c := range cands {
		// The identity module sets the key's expiry from its own clock (time.Now) plus the TTL, so the
		// TTL is measured on that clock, aimed one second before the old expiry and rounded down:
		// the key can never outlive the token, by any fraction of a second.
		deadline := c.token.ExpiresAt.Add(-time.Second)
		ttl := time.Until(deadline).Truncate(time.Second)
		if ttl <= 0 {
			continue // expired while this ran
		}
		key, kc, err := s.ids.CreateKey(ctx, identity.KeyInput{Name: c.identity.Name, Scopes: c.token.Grants.Scopes(), TTL: ttl})
		if err != nil {
			return out, fmt.Errorf("access: create the key for old token %s: %w", c.token.ID, err)
		}
		cred := Credential{ID: kc.ID, IdentityID: c.identity.ID, Grants: c.token.Grants.normalized(), SourceTokenID: c.token.ID,
			CreatedAt: now, ExpiresAt: deadline}
		if err := s.st.WriteTx(ctx, func(ctx context.Context, tx libstore.Tx) error { return insertCredential(ctx, tx, cred) }); err != nil {
			return out, fmt.Errorf("access: map key %s to identity %s: %w", kc.ID, c.identity.ID, err)
		}
		out = append(out, Reissued{Identity: c.identity, OldTokenID: c.token.ID, OldExpiresAt: c.token.ExpiresAt, Credential: cred, Key: key})
	}
	var bad []error
	for i := range out {
		if err := s.check(ctx, &out[i]); err != nil {
			bad = append(bad, err)
		}
	}
	return out, errors.Join(bad...)
}

// check verifies one reissued key the way a request would see it.
func (s *Service) check(ctx context.Context, r *Reissued) error {
	p, err := s.ids.Verify(ctx, r.Key)
	if err != nil {
		return fmt.Errorf("access: key %s does not verify: %w", r.Credential.ID, err)
	}
	agent, err := s.Resolve(ctx, p)
	if err != nil {
		return fmt.Errorf("access: key %s does not resolve to its identity: %w", r.Credential.ID, err)
	}
	if agent.Identity.ID != r.Identity.ID {
		return fmt.Errorf("access: key %s resolves to identity %s, want %s", r.Credential.ID, agent.Identity.ID, r.Identity.ID)
	}
	old := r.Credential.Grants.AllowSet()
	got := agent.AllowSet()
	r.Pairs, r.AllowSetEqual = len(old), maps.Equal(old, got)
	r.ExpiryKept = !r.Credential.ExpiresAt.After(r.OldExpiresAt) && (p.ExpiresAt.IsZero() || !p.ExpiresAt.After(r.OldExpiresAt))
	if p.ExpiresAt.IsZero() {
		r.ExpiryKept = false // the key must expire
	}
	switch {
	case !r.AllowSetEqual:
		return fmt.Errorf("access: key %s allows %d (verb, kind) pairs, the old token %s allowed %d", r.Credential.ID, len(got), r.OldTokenID, len(old))
	case !r.ExpiryKept:
		return fmt.Errorf("access: key %s expires %s, later than the old token %s (%s)", r.Credential.ID, p.ExpiresAt, r.OldTokenID, r.OldExpiresAt)
	}
	return nil
}

// candidates are the active old tokens, with their identities, in a fixed order.
func (s *Service) candidates(ctx context.Context, now time.Time) ([]candidate, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT t.id, t.identity_id, t.grants, t.created_at, t.expires_at
		FROM service_tokens t JOIN service_identities i ON i.id = t.identity_id
		WHERE t.revoked_at IS NULL AND i.disabled_at IS NULL AND t.expires_at > ?
		ORDER BY i.name, t.identity_id, t.created_at, t.id`, now.Unix())
	if err != nil {
		return nil, err
	}
	type row struct {
		tok     OldToken
		ident   string
		grantsJ string
	}
	var raw []row
	for rows.Next() {
		var r row
		var created, expires int64
		if err := rows.Scan(&r.tok.ID, &r.ident, &r.grantsJ, &created, &expires); err != nil {
			_ = rows.Close()
			return nil, err
		}
		r.tok.CreatedAt, r.tok.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(expires, 0).UTC()
		raw = append(raw, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []candidate
	for _, r := range raw {
		if err := json.Unmarshal([]byte(r.grantsJ), &r.tok.Grants); err != nil {
			return nil, fmt.Errorf("access: old token %s: grants: %w", r.tok.ID, err)
		}
		r.tok.Grants = r.tok.Grants.normalized()
		ident, err := s.identity(ctx, r.ident)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate{identity: ident, token: r.tok})
	}
	return out, nil
}

// identityCounts returns how many users (an owner exists when > 0) and live API keys the identity
// module holds. The module exports neither count nor a key list, so this reads its two tables
// read-only; it is the one place Tachyon does, and the Substrate gap task in
// docs/conversion-notes.md asks for an exported answer.
func (s *Service) identityCounts(ctx context.Context) (users, keys int, err error) {
	err = s.st.Reader().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM identity_users),
		(SELECT count(*) FROM identity_credentials WHERE revoked_at = 0)`).Scan(&users, &keys)
	return users, keys, err
}
