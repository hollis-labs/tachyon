package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/accessstatus"
	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

func init() { registerServerMount(mountAccess) }

// accessSpecs lists every app Tachyon talks to, from the host's own configuration. Probes are harmless; a
// write probe only exists where the app can refuse or say "no such thing" without creating anything.
func accessSpecs(s *Server) func() []accessstatus.Spec {
	return func() []accessstatus.Spec {
		u := s.cfg.Upstreams
		nanite := accessstatus.Spec{App: "nanite", Note: "the in-app agent is not configured (TACHYON_APP_AGENT_CONFIG)"}
		if s.cfg.AppAgentConfigFile != "" {
			if c, err := appagent.LoadNaniteConfig(s.cfg.AppAgentConfigFile); err == nil {
				nanite = accessstatus.Spec{App: "nanite", BaseURL: strings.TrimSpace(c.URL), TokenFile: strings.TrimSpace(c.TokenFile),
					Read: &accessstatus.Probe{Path: "/api/agent/v1/sessions"}, WriteNote: "creating a session is not idempotent"}
			}
		}
		return []accessstatus.Spec{
			{App: "tether", BaseURL: u.TetherAddr, TokenFile: u.TetherTokenFile, Headers: map[string]string{"Tether-Protocol": "1"},
				Read: &accessstatus.Probe{Path: "/catalog/history?limit=1"}, WriteNote: "Tether writes (messages, plans) are not idempotent; catalog.write is exercised by a plan"},
			{App: "torque", BaseURL: u.TorqueURL, Token: u.TorqueToken, Read: &accessstatus.Probe{Path: "/api/v1/tasks?limit=1"},
				Write: &accessstatus.Probe{Method: "POST", Path: "/api/v1/tasks/CW-00000000-0000/comments", Body: `{"author":"tachyon-access-probe","content":"access probe"}`}},
			{App: "tesseract", BaseURL: u.TesseractURL, TokenFile: u.TesseractTokenFile, Read: &accessstatus.Probe{Path: "/v1/items/tachyon-access-probe"},
				WriteNote: "Tesseract writes create items"},
			{App: "cerberus", BaseURL: u.CerberusAddr, TokenFile: u.CerberusTokenFile, Read: &accessstatus.Probe{Path: "/hosts"},
				WriteNote: "no harmless Cerberus write"},
			nanite,
			{App: "fragments_engine", BaseURL: s.cfg.FragmentsURL, TokenFile: s.cfg.FragmentsTokenFile, Read: &accessstatus.Probe{Path: "/v1/transcripts/status"},
				WriteNote: "Tachyon only reads Fragments Engine"},
			{App: "forge", BaseURL: "https://api.github.com", Token: u.GitHubToken, Read: &accessstatus.Probe{Path: "/rate_limit"},
				WriteNote: "GitHub writes are not probed"},
			{App: "tangent", Note: "Tachyon has no Tangent integration configured"},
		}
	}
}

// mountAccess serves GET /access/apps: per app, is a credential configured, and do a read and a write probe pass.
func mountAccess(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	c := &accessstatus.Checker{Specs: accessSpecs(s)}
	mux.Handle("GET "+accessstatus.Path, operator.RequireOperator(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := c.Check(r.Context(), r.URL.Query().Get("refresh") == "1")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"apps": res})
	})))
}
