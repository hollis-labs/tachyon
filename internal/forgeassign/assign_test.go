package forgeassign

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

type agents map[string]Agent

func (a agents) Lookup(_ context.Context, id string) (Agent, error) {
	g, ok := a[id]
	if !ok {
		return Agent{}, ErrAgentNotFound
	}
	if g.URN == "" {
		return Agent{}, ErrAgentNotAddressable
	}
	return g, nil
}

type mail struct {
	sent []string
	err  error
}

func (m *mail) Send(_ context.Context, to, subject, body string, meta map[string]string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	m.sent = append(m.sent, to+"|"+subject+"|"+body)
	return "msg_1", nil
}

type logins map[string]string

func (l logins) Login(_ context.Context, id string) string { return l[id] }

type fixture struct {
	h        *Handler
	mux      http.Handler
	mail     *mail
	state    string
	assigned []string
	changed  []string
	close    func()
}

func setup(t *testing.T, state string, lg logins) *fixture {
	f := &fixture{mail: &mail{}, state: state}
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pulls/7"):
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "T", "state": f.state, "html_url": "https://x/7", "user": map[string]any{"login": "au"}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/issues/7/assignees"):
			var b struct{ Assignees []string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.assigned = append(f.assigned, b.Assignees...)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/users/"):
			w.WriteHeader(404)
		default:
			w.WriteHeader(404)
		}
	}))
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.h = &Handler{Token: "tok", Base: gh.URL, DB: db, Mail: f.mail, Logins: lg,
		Agents:  agents{"builder": {ID: "builder", URN: "msg://agent/x/builder"}, "mute": {ID: "mute"}},
		Changed: func(k string) { f.changed = append(f.changed, k) }}
	f.mux = f.h.Mux()
	f.close = func() { gh.Close(); db.Close() }
	t.Cleanup(f.close)
	return f
}

func post(h http.Handler, body string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/api/plugins/console/v1/forge/repos/github/o/r/pulls/7/assign-agent", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func link(out map[string]any) map[string]any {
	d, _ := out["data"].(map[string]any)
	l, _ := d["link"].(map[string]any)
	return l
}

func code(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestAssignSendsAndSetsAssignee(t *testing.T) {
	f := setup(t, "open", logins{"builder": "bot"})
	st, out := post(f.mux, `{"agent_id":"builder","task":"fix_checks","checks":["ci"]}`)
	l := link(out)
	if st != 200 || l["status"] != "sent" || l["forge_assigned"] != true || l["message_id"] != "msg_1" {
		t.Fatalf("%d %v", st, out)
	}
	if len(f.assigned) != 1 || f.assigned[0] != "bot" || len(f.mail.sent) != 1 || len(f.changed) != 1 {
		t.Fatalf("assigned=%v sent=%d changed=%v", f.assigned, len(f.mail.sent), f.changed)
	}
	m := f.mail.sent[0]
	if !strings.Contains(m, "msg://agent/x/builder|[PR] Fix failing checks: o/r#7|") || !strings.Contains(m, "```console-pr-assignment") || !strings.Contains(m, "console.pr_assignment/v1") || !strings.Contains(m, `"checks_failing"`) {
		t.Fatalf("message: %s", m)
	}
}

func TestAssignReplayAndReminder(t *testing.T) {
	f := setup(t, "open", logins{"builder": "bot"})
	post(f.mux, `{"agent_id":"builder","task":"review"}`)
	st, out := post(f.mux, `{"agent_id":"builder","task":"review"}`)
	if st != 200 || out["data"].(map[string]any)["replayed"] != true || len(f.mail.sent) != 1 {
		t.Fatalf("replay must not resend: %d %v sent=%d", st, out, len(f.mail.sent))
	}
	st, out = post(f.mux, `{"agent_id":"builder","task":"review","force":true}`)
	d := out["data"].(map[string]any)
	if st != 200 || d["reminded"] != true || link(out)["reminders"] != float64(1) || len(f.mail.sent) != 2 || len(f.assigned) != 1 {
		t.Fatalf("reminder: %d %v sent=%d assigned=%v", st, out, len(f.mail.sent), f.assigned)
	}
	if !strings.Contains(f.mail.sent[1], "[Reminder] [PR] Review") {
		t.Fatal(f.mail.sent[1])
	}
}

func TestAssignNoLoginIsNotAnError(t *testing.T) {
	f := setup(t, "open", nil)
	st, out := post(f.mux, `{"agent_id":"builder","task":"review"}`)
	l := link(out)
	if st != 200 || l["status"] != "sent" || l["forge_assigned"] != false || len(f.assigned) != 0 {
		t.Fatalf("%d %v", st, out)
	}
}

func TestAssignFailedSendRetriesOnlyTheMessage(t *testing.T) {
	f := setup(t, "open", logins{"builder": "bot"})
	f.mail.err = errors.New("Tether could not be reached")
	st, out := post(f.mux, `{"agent_id":"builder","task":"review"}`)
	l := link(out)
	if st != 200 || l["status"] != "failed" || l["forge_assigned"] != true || l["error"] == "" {
		t.Fatalf("%d %v", st, out)
	}
	f.mail.err = nil
	_, out = post(f.mux, `{"agent_id":"builder","task":"review"}`)
	if link(out)["status"] != "sent" || len(f.assigned) != 1 {
		t.Fatalf("retry must not reassign: %v assigned=%v", out, f.assigned)
	}
}

func TestAssignRefusals(t *testing.T) {
	f := setup(t, "closed", nil)
	for body, want := range map[string]string{
		`{"agent_id":"builder","task":"review"}`:       "pr_not_open",
		`{"agent_id":"nobody","task":"review"}`:        "agent_not_found",
		`{"agent_id":"mute","task":"review"}`:          "agent_not_addressable",
		`{"agent_id":"builder","task":"nope"}`:         "invalid_args",
		`{"agent_id":"builder","task":"review","x":1}`: "invalid_args",
	} {
		if _, out := post(f.mux, body); code(out) != want {
			t.Errorf("%s: %v", body, out)
		}
	}
	if n := len(f.mail.sent); n != 0 {
		t.Fatalf("nothing is sent on a refusal: %d", n)
	}
}

func TestMarkDoneAndAssignees(t *testing.T) {
	f := setup(t, "open", nil)
	post(f.mux, `{"agent_id":"builder","task":"review"}`)
	m, err := f.h.Assignees(context.Background())
	if err != nil || len(m["github:o/r#7"]) != 1 {
		t.Fatalf("%v %v", m, err)
	}
	if n := f.h.MarkDone(context.Background(), "github:o/r#7"); n != 1 {
		t.Fatalf("done %d", n)
	}
	if m, _ := f.h.Assignees(context.Background()); len(m) != 0 {
		t.Fatalf("done links are not listed: %v", m)
	}
}
