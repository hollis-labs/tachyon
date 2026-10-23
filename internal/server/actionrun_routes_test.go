package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type actionRig struct {
	handler  http.Handler
	runID    string
	itemID   string
	secret   string
	elevated *bool
	svc      *actionrun.Service
}

func newActionRig(t *testing.T) *actionRig {
	t.Helper()
	elevated := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if c, err := r.Cookie("tangent_participant"); err == nil && c.Value == "expired" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"session_expired"}`))
			return
		}
		if elevated {
			_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
	}))
	t.Cleanup(auth.Close)
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL})
	s.store = db
	s.objects = objects.New(db)
	s.approvalInbox = approvals.NewInbox(&approvals.Store{DB: db}, approvals.New(auth.URL))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.startActions(ctx, db)
	s.actions.Manager.Labels = func(context.Context, string, string) (actionrun.Labels, error) {
		return actionrun.Labels{Env: "dev", Owner: "self", Admin: "self"}, nil
	}
	h, err := s.actions.Request(ctx, actionrun.RequestInput{Machine: "agent-os", Action: "cerberus.resource.reload", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "route test", AgentID: "service:test"})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := (&agentaccess.Store{DB: db}).Issue(ctx, "", "Requester", agentaccess.ActionRequest(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &actionRig{handler: s.Routes(), runID: h.RunID, itemID: h.ItemID, secret: secret, elevated: &elevated, svc: s.actions}
}

func (r *actionRig) doCookie(method, target, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://tachyon.test"+target, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}

func (r *actionRig) do(method, target string, hdr map[string]string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://tachyon.test"+target, strings.NewReader(""))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "operator-session"})
	}
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}

var wsHeaders = map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Origin": "https://tachyon.test"}

func TestActionRequestCredentialCanOnlyRequestNeverApproveOrAttach(t *testing.T) {
	r := newActionRig(t)
	bearer := map[string]string{"Authorization": "Bearer " + r.secret, "Origin": "https://tachyon.test", "X-Actor": "operator"}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"resolve (operator elevated)": func() *httptest.ResponseRecorder {
			*r.elevated = true
			return r.do("POST", approvals.Prefix+"/items/"+r.itemID+"/resolve", bearer, false)
		}(),
		"resolve with cookie too": r.do("POST", approvals.Prefix+"/items/"+r.itemID+"/resolve", bearer, true),
		"run read":                r.do("GET", actionrun.Prefix+"/"+r.runID, bearer, false),
		"run read with cookie":    r.do("GET", actionrun.Prefix+"/"+r.runID, bearer, true),
		"terminal":                r.do("GET", actionrun.Prefix+"/"+r.runID+"/terminal", mergeHeaders(wsHeaders, bearer), true),
	} {
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("%s: agent credential got %d %s", name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "op_digest") {
			t.Fatalf("%s leaked the run: %s", name, w.Body.String())
		}
	}
	run, _ := r.svc.Store.Get(context.Background(), r.runID)
	if run.State != actionrun.StatePending {
		t.Fatalf("run advanced without an operator decision: %s", run.State)
	}
}

func mergeHeaders(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestActionRunRoutesCheckOperatorAssuranceAndOriginOnTheDecodedPath(t *testing.T) {
	r := newActionRig(t)
	run := r.runID
	variants := []string{
		actionrun.Prefix + "/" + run,
		actionrun.Prefix + "/" + run + "/terminal",
		actionrun.Prefix + "%2F" + run,
		actionrun.Prefix + "%2f" + run + "%2Fterminal",
		actionrun.Prefix + "/" + run + "%2Fterminal",
		actionrun.Prefix + "/%2e%2e/action-runs/" + run,
		actionrun.Prefix + "/./" + run,
		actionrun.Prefix + "/" + run + "/../" + run,
		actionrun.Prefix + "//" + run,
		"/api/plugins/console/v1/%61ction-runs/" + run,
		"/api/plugins/console/v1/action-runs%5C" + run,
		"/api/plugins/console/v1/action-runs/" + run + "%00",
		"/api/plugins/console/v1//action-runs/" + run,
		"/api/plugins/console/v1/x/..%2Faction-runs/" + run,
	}
	// Without operator assurance nothing serves the run or upgrades, however the path is spelled.
	for _, p := range variants {
		for _, hdr := range []map[string]string{nil, wsHeaders} {
			w := r.do("GET", p, hdr, true)
			if w.Code == 200 || w.Code == 101 || strings.Contains(w.Body.String(), "op_digest") {
				t.Fatalf("unelevated %s reached the run: %d %s", p, w.Code, w.Body.String())
			}
		}
	}
	if w := r.do("GET", actionrun.Prefix+"/"+run, nil, true); w.Code != 403 {
		t.Fatalf("unelevated read must be refused by the guard: %d", w.Code)
	}
	if w := r.do("GET", actionrun.Prefix+"/"+run, map[string]string{"Sec-Fetch-Site": "cross-site"}, true); w.Code != 403 {
		t.Fatalf("cross-site: %d", w.Code)
	}

	*r.elevated = true
	// Positive control: an elevated operator reads it.
	w := r.do("GET", actionrun.Prefix+"/"+run, nil, true)
	var got actionrun.Run
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != run {
		t.Fatalf("elevated read: %d %s", w.Code, w.Body.String())
	}
	// Elevated, but a foreign Origin, no Origin, or a bearer header still cannot attach,
	// and encoded-slash spellings of the terminal path are not routed at all.
	for name, hdr := range map[string]map[string]string{
		"foreign origin": mergeHeaders(wsHeaders, map[string]string{"Origin": "https://evil.example"}),
		"no origin":      {"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="},
		"bearer":         mergeHeaders(wsHeaders, map[string]string{"Authorization": "Bearer x"}),
		"cross-site":     mergeHeaders(wsHeaders, map[string]string{"Sec-Fetch-Site": "cross-site"}),
	} {
		if w := r.do("GET", actionrun.Prefix+"/"+run+"/terminal", hdr, true); w.Code != 403 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	for _, p := range variants {
		if !strings.Contains(p, "terminal") {
			continue // a plain read by an elevated operator is legitimate however it is spelled
		}
		w := r.do("GET", p, mergeHeaders(wsHeaders, map[string]string{"Origin": "https://evil.example"}), true)
		if w.Code != 403 && w.Code != 404 && (w.Code < 300 || w.Code > 399) {
			t.Fatalf("elevated but foreign-origin %s was not refused: %d %s", p, w.Code, w.Body.String())
		}
	}
	// An encoded spelling is routed on its decoded path, so it gets exactly the
	// literal route's guards: still refused for a foreign origin (above) and for an
	// unelevated session (the variants loop earlier).
}

// The approval queue's own projections must not carry the private action run
// below the bar of the dedicated run-read route.
func TestApprovalInboxPathsAttachTheActionRunOnlyForAnElevatedOperator(t *testing.T) {
	r := newActionRig(t)
	paths := []string{approvals.Prefix + "/items/" + r.itemID, approvals.Prefix + "/inbox"}
	for _, p := range paths {
		for name, cookie := range map[string]string{"missing": "", "expired": "expired"} {
			w := r.doCookie("GET", p, cookie)
			if w.Code == 200 || strings.Contains(w.Body.String(), "op_digest") || strings.Contains(w.Body.String(), "action_run") {
				t.Fatalf("%s session on %s: %d %s", name, p, w.Code, w.Body.String())
			}
		}
		// A connected but unelevated browser session sees the queue, not the run.
		*r.elevated = false
		w := r.doCookie("GET", p, "operator-session")
		if w.Code != 200 || strings.Contains(w.Body.String(), "action_run") || strings.Contains(w.Body.String(), r.runID) {
			t.Fatalf("unelevated session on %s: %d %s", p, w.Code, w.Body.String())
		}
		*r.elevated = true
		w = r.doCookie("GET", p, "operator-session")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"action_run"`) || !strings.Contains(w.Body.String(), r.runID) {
			t.Fatalf("elevated operator on %s: %d %s", p, w.Code, w.Body.String())
		}
	}
}
