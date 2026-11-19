package settingsreg

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin/adminhttp"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

// Routes are the explicit patterns for the registered groups: the manifest plus each group's four routes.
// The server mounts them one by one, so a group registered later needs the mount to run after registration
// (see Mount, which reads the registry at request time instead).
func (r *Registry) Routes() []string {
	out := []string{"GET " + settingsapi.AdminPrefix + "/manifest"}
	for _, id := range r.Groups() {
		for _, x := range settingsapi.AdminRoutes(id) {
			out = append(out, x.Method+" "+x.Path)
		}
	}
	return out
}

// Handler serves the admin manifest and groups. Reads need a browser session; writes also need a same-origin
// request. A bearer token is refused. Operator-only fields read as not editable below an operator session, and a
// write to one is refused with the step-up hint from the requirements table.
func (r *Registry) Handler(p assurance.Provider) (http.Handler, error) {
	h, err := adminhttp.NewHandler(adminhttp.Config{
		BasePath: settingsapi.Root,
		Authorize: func(req *http.Request, _ adminhttp.Resource) error {
			if req.Header.Get("Authorization") != "" {
				return &admin.Failure{Code: admin.Forbidden, Message: "Settings are available to the operator's browser session only."}
			}
			if _, e := assurance.RequireOn(p, req, assurance.ActionRead); e != nil {
				return &admin.Failure{Code: admin.Unauthenticated, Message: "Sign in to continue."}
			}
			return nil
		},
		Resolve: func(req *http.Request) (*admin.Registry, error) {
			st, e := p.Status(req)
			return r.Build(e == nil && st.Level >= assurance.LevelOperatorToken)
		},
		ProtectCommand: func(req *http.Request) error {
			if !sameOrigin(req) {
				return &admin.Failure{Code: admin.Forbidden, Message: "Use the Tachyon browser origin."}
			}
			if _, e := assurance.RequireOn(p, req, assurance.ActionSettingsWrite); e != nil {
				return &admin.Failure{Code: admin.Forbidden, Message: e.Message}
			}
			return nil
		},
		MaxBodyBytes: settingsapi.MaxBodyBytes,
	})
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method == http.MethodPost && req.Header.Get("Authorization") == "" && sameOrigin(req) {
			if g, op, ok := target(req.URL.Path); ok && (op == "update" || op == "reset") {
				if st, e := p.Status(req); e == nil && st.Level < assurance.LevelOperatorToken && st.Level >= assurance.LevelBrowserSession {
					body, _ := io.ReadAll(io.LimitReader(req.Body, settingsapi.MaxBodyBytes+1))
					req.Body = io.NopCloser(bytes.NewReader(body))
					var c struct {
						Set   map[string]json.RawMessage `json:"set"`
						Unset []string                   `json:"unset"`
						Keys  []string                   `json:"keys"`
					}
					if json.Unmarshal(body, &c) == nil {
						keys := append(append([]string{}, c.Unset...), c.Keys...)
						for k := range c.Set {
							keys = append(keys, k)
						}
						if k, hit := r.NeedsOperator(g, keys); hit {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusForbidden)
							_ = json.NewEncoder(w).Encode(map[string]any{"error": settingsapi.Refusal{Code: "operator_access_required",
								Message: "Operator access is needed for this action.",
								StepUp:  settingsapi.StepUp{Action: string(assurance.ActionSettingsOperator), Risk: assurance.Risk(assurance.ActionSettingsOperator), Summary: "Change " + g + "." + k, Ref: g + "." + k},
								Methods: assurance.Methods()}})
							return
						}
					}
				}
			}
		}
		h.ServeHTTP(w, req)
	}), nil
}

func target(path string) (group, op string, ok bool) {
	rest, found := strings.CutPrefix(path, settingsapi.AdminPrefix+"/settings/")
	if !found {
		return "", "", false
	}
	g, o, _ := strings.Cut(rest, "/")
	return g, o, true
}

func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
