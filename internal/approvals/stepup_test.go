package approvals

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

func TestAccessCarriesDialogFieldsAndStepUpIsUnsupported(t *testing.T) {
	var limited bool
	expiredAt := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session":
			writeValue(w, map[string]any{"elevated": false, "assurance": "browser-session", "elevation_ttl": "30m", "elevation_expires_at": expiredAt})
		case "/api/session/elevate":
			if limited {
				w.WriteHeader(429)
				writeValue(w, map[string]any{"code": "elevation_rate_limited"})
				return
			}
			w.WriteHeader(403)
			writeValue(w, map[string]any{"code": "elevation_refused"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	inbox := NewInbox(nativeStore(t), New(up.URL))

	var a map[string]any
	w := get(inbox, Prefix+"/access", "s1")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &a) != nil {
		t.Fatalf("access: %d %s", w.Code, w.Body)
	}
	if a["state"] != "none" || a["can_elevate"] != true || len(a["methods"].([]any)) != 3 ||
		a["session"].(map[string]any)["state"] != "expired" || a["step_up"].(map[string]any)["supported"] != false ||
		a["lockout"].(map[string]any)["locked"] != false {
		t.Fatalf("extended access: %s", w.Body)
	}
	if m := a["methods"].([]any)[0].(map[string]any); m["id"] != "operator_token" || m["available"] != true {
		t.Fatalf("methods: %v", a["methods"])
	}

	limited = true
	if w := serveJSON(inbox, "POST", Prefix+"/elevate", map[string]string{}, "", "s1"); w.Code != 429 {
		t.Fatalf("elevate while limited: %d", w.Code)
	}
	w = get(inbox, Prefix+"/access", "s1")
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	l := a["lockout"].(map[string]any)
	if l["locked"] != true || l["retry_after_s"].(float64) < 55 || l["retry_after_s"].(float64) > 61 || l["attempts_left"] != nil {
		t.Fatalf("lockout estimate: %v", l)
	}

	for _, p := range []string{"/access/challenge", "/access/verify"} {
		w := serveJSON(inbox, "POST", Prefix+p, map[string]string{"action": "x", "challenge_id": "c"}, "", "s1")
		if w.Code != 501 || !strings.Contains(w.Body.String(), `"unsupported"`) {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
		if w := serveJSON(inbox, "POST", Prefix+p, map[string]string{}, "", ""); w.Code != 401 {
			t.Errorf("%s without a session: %d, want 401", p, w.Code)
		}
	}
}

func TestRefusalCarriesTheStepUpHint(t *testing.T) {
	st := assurance.Status{Level: assurance.LevelBrowserSession}
	_, err := assurance.RequireOn(stubProvider{st}, nil, assurance.ActionResolveHigh)
	if err == nil || err.Status != 403 || err.Code != "operator_access_required" || err.Action != assurance.ActionResolveHigh {
		t.Fatalf("refusal: %+v", err)
	}
	var body struct {
		Code, Message string
		StepUp        struct{ Action, Risk string } `json:"step_up"`
		Methods       []map[string]any
	}
	if json.Unmarshal([]byte(err.Body), &body) != nil || body.Code != err.Code || body.Message == "" ||
		body.StepUp.Action != "resolve_high" || body.StepUp.Risk != "high" || len(body.Methods) != 3 {
		t.Fatalf("body: %s", err.Body)
	}
	if assurance.Risk(assurance.ActionBatch) != "medium" || assurance.Risk(assurance.ActionRead) != "low" || assurance.Risk("settings.write") != "medium" {
		t.Error("risk mapping")
	}
	if assurance.StepUpSupported(&TangentProvider{}) {
		t.Error("TangentProvider must not claim step-up")
	}
}

type stubProvider struct{ st assurance.Status }

func (s stubProvider) Status(*http.Request) (assurance.Status, *assurance.Error) { return s.st, nil }
func (s stubProvider) Require(r *http.Request, a assurance.Action) (assurance.Status, *assurance.Error) {
	return assurance.RequireOn(s, r, a)
}
func (stubProvider) Elevate(http.ResponseWriter, *http.Request)   {}
func (stubProvider) DeElevate(http.ResponseWriter, *http.Request) {}
