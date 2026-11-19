package access

import (
	"context"
	"slices"
)

// ActiveGateProducer re-reads the pinned identity and its current credential
// mappings. It is evidence of a currently mapped producer, never a user role or
// permission to answer. Existing presets do not include any gate grants.
func (s *Service) ActiveGateProducer(ctx context.Context, id string) error {
	i, err := s.identity(ctx, id)
	if err != nil {
		return err
	}
	if i.Disabled {
		return ErrInactive
	}
	ids, err := s.column(ctx, `SELECT credential_id FROM service_credentials WHERE identity_id=?`, id)
	if err != nil {
		return err
	}
	granted := map[string]bool{}
	for _, cid := range ids {
		c, err := s.credential(ctx, cid)
		if err != nil {
			return err
		}
		if c.Revoked || !s.now().Before(c.ExpiresAt) {
			continue
		}
		for _, v := range gateVerbs {
			if slices.Contains(c.Grants.Verbs, v) {
				granted[v] = true
			}
		}
	}
	for _, v := range gateVerbs {
		if !granted[v] {
			return ErrInactive
		}
	}
	return nil
}
