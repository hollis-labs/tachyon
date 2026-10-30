package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/forgeassign"
	"github.com/hollis-labs/tachyon/internal/forgeindex"
)

func init() { registerServerMount(mountForgeAssign) }

const forgeAssignKey = "forgeassign"

// messageTokenFile is the credential with the Tether `messages` scope used to send assignments; it falls back
// to the read token file when no separate one is set.
func messageTokenFile(s *Server) string {
	if f := strings.TrimSpace(os.Getenv("TACHYON_TETHER_MESSAGE_TOKEN_FILE")); f != "" {
		return f
	}
	if f := strings.TrimSpace(os.Getenv("TETHER_TOKEN_FILE")); f != "" {
		return f
	}
	return s.cfg.Upstreams.TetherTokenFile
}

// mountForgeAssign serves the assign-agent write behind the operator route guard (explicit pattern, through
// the API cache so forge reads are invalidated). The forge stub owns it under TACHYON_FORGE_STUBS=1.
func mountForgeAssign(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if forgeStubsOn() || s.store == nil {
		return
	}
	h := &forgeassign.Handler{Token: s.cfg.Upstreams.GitHubToken, DB: s.store, Logins: forgeassign.SettingsLogins{DB: s.store.Reader}}
	if addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr); addr != "" {
		if base, c, err := builderread.NewTetherClient(addr, s.cfg.Upstreams.TetherTokenFile); err == nil {
			h.Agents = &forgeassign.TetherAgents{Base: base, Client: c}
		}
		if base, c, err := builderread.NewTetherClient(addr, messageTokenFile(s)); err == nil {
			h.Mail = &forgeassign.TetherMail{Base: base, Client: c}
		}
	}
	h.Facts = func(ctx context.Context, prKey string) forgeassign.Facts {
		v, ok := s.ext.Load(forgeIndexKey)
		if !ok {
			return forgeassign.Facts{}
		}
		snap, err := v.(*forgeindex.Index).Snapshot(ctx)
		if err != nil {
			return forgeassign.Facts{}
		}
		for _, r := range snap.Rows {
			if r.PRKey == prKey {
				f := forgeassign.Facts{Found: true, Reasons: r.Reasons, Passed: r.Checks.Passed, Failed: r.Checks.Failed, Pending: r.Checks.Pending, Mergeable: r.Mergeable}
				for _, c := range r.Checks.Failing {
					f.Failing = append(f.Failing, forgeassign.Check{Name: c.Name, URL: c.URL})
				}
				return f
			}
		}
		return forgeassign.Facts{}
	}
	h.Changed = func(prKey string) {
		if v, ok := s.ext.Load(forgeIndexKey); ok {
			if x := v.(*forgeindex.Index); x.Bus != nil {
				x.Bus.Publish("pr.updated", map[string]any{"pr_key": prKey, "changed": []string{"assignees"}})
			}
		}
	}
	s.ext.Store(forgeAssignKey, h)
	guarded := operator.RequireOperator(h.Mux())
	if s.cache != nil {
		guarded = s.cache.Wrap(guarded)
	}
	mux.Handle(forgeassign.Route, guarded)
}
