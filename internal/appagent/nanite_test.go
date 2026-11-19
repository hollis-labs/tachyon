package appagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Frames captured from Nanite 0.3.0-beta.1 (chatstream/v1) on 2026-10-24.
const sseText = `id: 1
event: run.start
data: {"v":"1","seq":1,"verb":"run.start","provider":"anthropic","model":"claude-opus-5-5"}

id: 2
event: activity
data: {"v":"1","seq":2,"verb":"activity","kind":"nanite.turn_state","value":{"state":"working"}}

: keepalive

id: 4
event: message.start
data: {"v":"1","seq":4,"verb":"message.start","message_id":"m1","role":"assistant"}

id: 5
event: part.start
data: {"v":"1","seq":5,"verb":"part.start","part_id":"text-1","kind":"text","meta":{"phase":""}}

id: 6
event: part.delta
data: {"v":"1","seq":6,"verb":"part.delta","part_id":"text-1","text":"Hello there"}

id: 7
event: part.delta
data: {"v":"1","seq":7,"verb":"part.delta","part_id":"text-1","text":", operator!"}

id: 8
event: part.end
data: {"v":"1","seq":8,"verb":"part.end","part_id":"text-1"}

id: 10
event: run.finish
data: {"v":"1","seq":10,"verb":"run.finish","reason":"stop","usage":{"scope":"final","total":7137}}

`

const sseTool = `data: {"v":"1","seq":7,"verb":"part.start","part_id":"toolu_01","kind":"tool_call","meta":{"call_id":"toolu_01","detail":"","is_error":false,"name":"tachyon_planner_list"}}

data: {"v":"1","seq":8,"verb":"part.end","part_id":"toolu_01"}

data: {"v":"1","seq":10,"verb":"part.start","part_id":"tool_result-2","kind":"tool_result","meta":{"call_id":"toolu_01","detail":"","is_error":false,"name":"tachyon_planner_list"}}

data: {"v":"1","seq":11,"verb":"part.end","part_id":"tool_result-2","final":{"summary":"{}"}}

data: {"v":"1","seq":12,"verb":"part.start","part_id":"tool_result-3","kind":"tool_result","meta":{"call_id":"toolu_02","detail":"","is_error":true,"name":"tachyon_objects_list"}}

data: {"v":"1","seq":13,"verb":"part.start","part_id":"text-3","kind":"text","meta":{}}

data: {"v":"1","seq":14,"verb":"part.delta","part_id":"text-3","text":"Parallax"}

data: {"v":"1","seq":17,"verb":"run.finish","reason":"stop"}

`

const secretToken = "nanite-client-credential-0123456789abcdef"

type fakeNanite struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []string
	auth     []string
	sse      string
	status   map[string]int // path prefix -> forced status
	turnBody string
}

func (f *fakeNanite) setSSE(s string) { f.mu.Lock(); f.sse = s; f.mu.Unlock() }
func (f *fakeNanite) force(path string, code int) {
	f.mu.Lock()
	f.status[path] = code
	f.mu.Unlock()
}
func (f *fakeNanite) lastTurnBody() string { f.mu.Lock(); defer f.mu.Unlock(); return f.turnBody }
func (f *fakeNanite) authLog() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...), append([]string{}, f.auth...)
}

func newNanite(t *testing.T) (*fakeNanite, *Nanite) {
	t.Helper()
	dir := t.TempDir()
	f := &fakeNanite{sse: sseText, status: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		forced := 0
		for p, code := range f.status {
			if strings.HasPrefix(r.URL.Path, p) {
				forced = code
			}
		}
		f.mu.Unlock()
		if forced != 0 {
			w.WriteHeader(forced)
			w.Write([]byte(`{"error":"nope ` + secretToken + `"}`))
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/agent/v1")
		switch {
		case r.Method == "POST" && p == "/sessions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["definition_ref"]; !ok {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"session_view_id":"sess-new"}`))
		case r.Method == "GET" && p == "/sessions/sess-live":
			w.Write([]byte(`{"state":"idle"}`))
		case r.Method == "GET" && p == "/sessions/sess-stopped":
			w.Write([]byte(`{"state":"stopped"}`))
		case r.Method == "POST" && strings.HasSuffix(p, "/turns"):
			var body struct {
				Content  []map[string]string `json:"content"`
				Delivery string              `json:"delivery"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.turnBody = fmt.Sprint(body.Content[0]["text"], "|", body.Delivery)
			f.mu.Unlock()
			w.WriteHeader(202)
			w.Write([]byte(`{"turn_id":"turn-7"}`))
		case r.Method == "GET" && strings.HasSuffix(p, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			f.mu.Lock()
			s := f.sse
			f.mu.Unlock()
			w.Write([]byte(s))
			if s == "" {
				<-r.Context().Done() // silence: a stalled stream
			}
		case r.Method == "GET" && strings.Contains(p, "/turns/"):
			w.Write([]byte(`{"state":"completed","message":{"parts":[
			 {"id":"toolu_01","kind":"tool_call","meta":{"call_id":"toolu_01","name":"tachyon_planner_list"}},
			 {"id":"tool_result-2","kind":"tool_result","meta":{"call_id":"toolu_01","name":"tachyon_planner_list","is_error":false}},
			 {"id":"text-3","kind":"text","text":"first"},{"id":"text-4","kind":"text","final":{"text":"second"}}]}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	tokenFile := filepath.Join(dir, "client.token")
	os.WriteFile(tokenFile, []byte(secretToken+"\n"), 0o600)
	cfg := filepath.Join(dir, "app-agent.json")
	raw, _ := json.Marshal(map[string]any{"url": f.URL, "token_file": tokenFile, "label": "Tachyon", "session": map[string]any{"definition_ref": map[string]string{"definition_id": "def:tachyon/app-chat", "revision": "3"}, "host_settings": map[string]string{"id": "h"}}})
	os.WriteFile(cfg, raw, 0o600)
	n := NewNanite(cfg)
	n.StallAfter = 300 * time.Millisecond
	return f, n
}

func mustCfg(t *testing.T, n *Nanite) NaniteConfig {
	t.Helper()
	c, err := n.config()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func collect(t *testing.T, n *Nanite) ([]TurnEvent, error) {
	t.Helper()
	var evs []TurnEvent
	err := n.Observe(context.Background(), "sess-live", "turn-7", func(e TurnEvent) { evs = append(evs, e) })
	return evs, err
}

func TestObserveMapsRealChatstreamFrames(t *testing.T) {
	_, n := newNanite(t)
	evs, err := collect(t, n)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, e := range evs {
		if e.Kind == EventText {
			text.WriteString(e.Text)
		}
	}
	if text.String() != "Hello there, operator!" || evs[len(evs)-1].Kind != EventFinish || evs[len(evs)-1].Reason != "stop" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestObserveMapsToolCallsAndResults(t *testing.T) {
	f, n := newNanite(t)
	f.setSSE(sseTool)
	evs, err := collect(t, n)
	if err != nil {
		t.Fatal(err)
	}
	want := []TurnEvent{
		{Kind: EventToolStart, Tool: Tool{ID: "toolu_01", Name: "tachyon_planner_list", Status: "running"}},
		{Kind: EventToolEnd, Tool: Tool{ID: "toolu_01", Name: "tachyon_planner_list", Status: "ok"}},
		{Kind: EventToolEnd, Tool: Tool{ID: "toolu_02", Name: "tachyon_objects_list", Status: "error"}},
		{Kind: EventText, Text: "Parallax"},
		{Kind: EventFinish, Reason: "stop"},
	}
	if fmt.Sprint(evs) != fmt.Sprint(want) {
		t.Fatalf("events:\n got %+v\nwant %+v", evs, want)
	}
}

func TestObserveReportsAStalledOrCutStream(t *testing.T) {
	f, n := newNanite(t)
	f.setSSE("") // handler goes silent after headers
	start := time.Now()
	if _, err := collect(t, n); !errors.Is(err, ErrRuntimeDown) || !strings.Contains(err.Error(), "stalled") || time.Since(start) > 3*time.Second {
		t.Fatalf("stall: %v after %v", err, time.Since(start))
	}
	f.setSSE("data: {\"verb\":\"part.delta\",\"part_id\":\"t\",\"text\":\"cut off\"}\n\n")
	if evs, err := collect(t, n); !errors.Is(err, ErrRuntimeDown) || !strings.Contains(err.Error(), "ended early") || len(evs) != 1 {
		t.Fatalf("cut stream: %v %+v", err, evs)
	}
}

func TestEnsureReusesALiveSessionAndReplacesAMissingOne(t *testing.T) {
	f, n := newNanite(t)
	ctx := context.Background()
	cfg, _ := n.config()
	pin := pinOf(cfg.Session)
	if s, err := n.Ensure(ctx, pin+"~sess-live"); err != nil || s != pin+"~sess-live" {
		t.Fatalf("live: %q %v", s, err)
	}
	if s, err := n.Ensure(ctx, pin+"~sess-stopped"); err != nil || s != pin+"~sess-new" {
		t.Fatalf("stopped: %q %v", s, err)
	}
	if s, err := n.Ensure(ctx, pin+"~sess-unknown"); err != nil || s != pin+"~sess-new" {
		t.Fatalf("unknown: %q %v", s, err)
	}
	if s, err := n.Ensure(ctx, ""); err != nil || s != pin+"~sess-new" {
		t.Fatalf("fresh: %q %v", s, err)
	}
	calls, auths := f.authLog()
	for i, a := range auths {
		if a != "Bearer "+secretToken {
			t.Fatalf("call %d (%s) auth %q", i, calls[i], a)
		}
	}
}

func TestSubmitSendsTextAtIdleAndMapsRefusals(t *testing.T) {
	f, n := newNanite(t)
	ctx := context.Background()
	turn, err := n.Submit(ctx, pinOf(mustCfg(t, n).Session)+"~sess-live", "what is up")
	if err != nil || turn != "turn-7" || f.lastTurnBody() != "what is up|at_idle" {
		t.Fatalf("submit: %q %v body=%q", turn, err, f.turnBody)
	}
	f.force("/api/agent/v1/sessions/gone", 404)
	if _, err := n.Submit(ctx, "gone", "x"); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("404: %v", err)
	}
	f.force("/api/agent/v1/sessions/denied", 403)
	_, err = n.Submit(ctx, "denied", "x")
	if !errors.Is(err, ErrRuntimeDown) || !strings.Contains(err.Error(), "refused the credential") || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("403: %v", err)
	}
	f.force("/api/agent/v1/sessions/busy", 429)
	if _, err := n.Submit(ctx, "busy", "x"); !errors.Is(err, ErrRuntimeDown) || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("429: %v", err)
	}
	f.force("/api/agent/v1/sessions/boom", 500)
	if _, err := n.Submit(ctx, "boom", "x"); err == nil || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("500 leaked the body: %v", err)
	}
}

func TestResultReadsTheAuthoritativeTurn(t *testing.T) {
	_, n := newNanite(t)
	r, err := n.Result(context.Background(), pinOf(mustCfg(t, n).Session)+"~sess-live", "turn-7")
	if err != nil || !r.Terminal() || r.State != "completed" || r.Text != "first\n\nsecond" || len(r.Tools) != 1 || r.Tools[0].Status != "ok" {
		t.Fatalf("result: %+v %v", r, err)
	}
}

func TestConfigErrorsAreExplainedAndTokenFilesAreReadEveryTime(t *testing.T) {
	if _, err := LoadNaniteConfig(filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing config: %v", err)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "c.json")
	os.WriteFile(bad, []byte(`{"url":"http://127.0.0.1:1","token_file":"/x"}`), 0o600)
	if _, err := LoadNaniteConfig(bad); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no session: %v", err)
	}
	os.WriteFile(bad, []byte(`{"url":"http://example.com","token_file":"/x","session":{"a":1}}`), 0o600)
	if _, err := LoadNaniteConfig(bad); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("remote http with a credential must be refused: %v", err)
	}
	f, n := newNanite(t)
	var cfg NaniteConfig
	b, _ := os.ReadFile(n.ConfigFile)
	json.Unmarshal(b, &cfg)
	os.WriteFile(cfg.TokenFile, []byte("rotated-token\n"), 0o600)
	if _, err := n.Ensure(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	_, auths := f.authLog()
	if auths[len(auths)-1] != "Bearer rotated-token" {
		t.Fatalf("token not re-read: %q", auths[len(auths)-1])
	}
	os.Remove(cfg.TokenFile)
	if _, err := n.Ensure(context.Background(), ""); !errors.Is(err, ErrRuntimeDown) {
		t.Fatalf("missing token file: %v", err)
	}
}

// A session belongs to the pins it was created under (and to that host row's tool grants).
// After a reinstall the old session would run with no tools, silently; a changed pin, or a
// session id stored before pins were tracked, starts a new session instead.
func TestEnsureStartsANewSessionWhenThePinsChange(t *testing.T) {
	_, n := newNanite(t)
	ctx := context.Background()
	first, err := n.Ensure(ctx, "")
	if err != nil || !strings.HasSuffix(first, "~sess-new") {
		t.Fatalf("first: %q %v", first, err)
	}
	// A legacy id (no pin) is never reused.
	if s, err := n.Ensure(ctx, "sess-live"); err != nil || s == "sess-live" || !strings.HasSuffix(s, "~sess-new") {
		t.Fatalf("legacy id reused: %q %v", s, err)
	}
	// A session created under other pins is never reused, even though Nanite says it is alive.
	if s, err := n.Ensure(ctx, "deadbeef0000~sess-live"); err != nil || strings.Contains(s, "sess-live") {
		t.Fatalf("other-pin session reused: %q %v", s, err)
	}
	// Changing the configured pins changes the fingerprint.
	var cfg NaniteConfig
	b, _ := os.ReadFile(n.ConfigFile)
	json.Unmarshal(b, &cfg)
	oldPin := pinOf(cfg.Session)
	cfg.Session = json.RawMessage(`{"definition_ref":{"definition_id":"def:tachyon/app-chat","revision":"4"},"host_settings":{"id":"h2"}}`)
	raw, _ := json.Marshal(cfg)
	os.WriteFile(n.ConfigFile, raw, 0o600)
	if pinOf(cfg.Session) == oldPin {
		t.Fatal("pin fingerprint did not change")
	}
	if s, err := n.Ensure(ctx, oldPin+"~sess-live"); err != nil || strings.Contains(s, "sess-live") || !strings.HasPrefix(s, pinOf(cfg.Session)+"~") {
		t.Fatalf("session from the old pins reused: %q %v", s, err)
	}
	// Key order in the config file does not change the pin.
	if pinOf(json.RawMessage(`{"a":1,"b":2}`)) != pinOf(json.RawMessage(`{"b":2,"a":1}`)) {
		t.Fatal("pin depends on key order")
	}
}

func TestObserveReadsTheContextMeterFromUsage(t *testing.T) {
	f, n := newNanite(t)
	f.setSSE(`data: {"v":"1","seq":1,"verb":"usage","usage":{"scope":"turn","used":48210,"window":200000,"model":"claude-sonnet-5-5","window_source":"catalog"}}

data: {"v":"1","seq":2,"verb":"run.finish","reason":"stop","usage":{"scope":"final","total":7137,"used":50000,"window":200000,"model":"claude-sonnet-5-5"}}

`)
	evs, err := collect(t, n)
	if err != nil {
		t.Fatal(err)
	}
	var meters []TurnEvent
	for _, e := range evs {
		if e.Kind == EventContext {
			meters = append(meters, e)
		}
	}
	if len(meters) != 2 || meters[0].Used != 48210 || meters[0].Window != 200000 || meters[1].Used != 50000 || meters[1].Model != "claude-sonnet-5-5" || evs[len(evs)-1].Kind != EventFinish {
		t.Fatalf("%+v", evs)
	}
	// An older build (scope/total only) reports no meter.
	f.setSSE(sseOldFinish)
	evs, _ = collect(t, n)
	for _, e := range evs {
		if e.Kind == EventContext {
			t.Fatalf("no meter was reported: %+v", e)
		}
	}
}

const sseOldFinish = `data: {"v":"1","seq":10,"verb":"run.finish","reason":"stop","usage":{"scope":"final","total":7137}}

`

func TestApprovalOfReadsTheFrame(t *testing.T) {
	var f frame
	raw := `{"verb":"approval.request","approval_id":"ap1","call_id":"c1","reason":"writes a file","descriptor":{"tool":"write_file","input":{"path":"/a"},"class":"low"},"expires_at":"2026-10-30T09:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	a := approvalOf(f)
	if a.ID != "ap1" || a.CallID != "c1" || a.Tool != "write_file" || a.Class != "low" || string(a.Input) != `{"path":"/a"}` || a.ExpiresAt.IsZero() {
		t.Fatalf("%+v", a)
	}
}
