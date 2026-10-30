package alertsource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
)

func TestFromTorque(t *testing.T) {
	e, ok := FromTorque("task.updated", []byte(`{"id":"CW-1","status":"failed","from":"in_progress","title":"Fix\nit","updated_at":"2026-10-30T07:00:00Z"}`))
	if !ok || e.Kind != "task.state_changed" || e.Payload["to"] != "failed" || e.Payload["title"] != "Fix it" || e.Subjects[0].Ref != "CW-1" || e.SourceEventID != "" {
		t.Fatalf("%+v %v", e, ok)
	}
	// The same change replayed after a reconnect has the same dedupe key; a new revision does not.
	e2, _ := FromTorque("task.updated", []byte(`{"id":"CW-1","status":"failed","title":"Fix it","updated_at":"2026-10-30T07:00:00Z"}`))
	e3, _ := FromTorque("task.updated", []byte(`{"id":"CW-1","status":"done","updated_at":"2026-10-30T08:00:00Z"}`))
	if DedupeKey(e) != DedupeKey(e2) || DedupeKey(e) == DedupeKey(e3) {
		t.Fatal("dedupe")
	}
	if e, ok := FromTorque("", []byte(`{"type":"checkpoint.created","data":{"id":"cp1","status":"pending","title":"OK?","question":"Proceed?"}}`)); !ok || e.Kind != "checkpoint.pending" {
		t.Fatalf("checkpoint: %+v", e)
	}
	for name, in := range map[string][2]string{
		"no state":    {"task.updated", `{"id":"CW-1","title":"x"}`},
		"other type":  {"comment.added", `{"id":"CW-1","status":"x"}`},
		"no id":       {"task.updated", `{"status":"done"}`},
		"bad json":    {"task.updated", `{`},
		"cp not pend": {"checkpoint.updated", `{"id":"c","status":"resolved"}`},
	} {
		if _, ok := FromTorque(in[0], []byte(in[1])); ok {
			t.Errorf("%s should be skipped", name)
		}
	}
}

func TestTorqueRun(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": hi\n\nevent: task.updated\ndata: {\"id\":\"CW-1\",\"status\":\"review\",\"title\":\"T\"}\n\nevent: task.updated\ndata: {\"id\":\"CW-2\"}\n\n")
	}))
	defer srv.Close()
	sink, h := &memSink{}, NewHealth()
	tq := &Torque{URL: srv.URL, Token: "tok", Sink: sink, Health: h}
	if err := tq.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" || len(sink.events) != 1 || sink.events[0].Kind != "task.state_changed" {
		t.Fatalf("auth %q events %+v", auth, sink.events)
	}
	if s := h.Snapshot(); len(s) != 1 || s[0].State != "degraded" || !strings.Contains(s[0].Note, "no event ids") {
		t.Fatalf("health: %+v", s)
	}
	// A refusing upstream is an error the supervisor backs off on; a token is never sent over plain http to a remote host.
	if err := (&Torque{URL: "http://torque.example.com/api/v1/events", Token: "tok", Sink: sink}).Run(context.Background()); err == nil {
		t.Fatal("token over plain remote http must be refused")
	}
}

type fakeHistory struct {
	head   int64
	frames []agentscollector.Frame
}

func (f fakeHistory) Head(context.Context) (int64, error) { return f.head, nil }
func (f fakeHistory) History(_ context.Context, since, _ int64, _ int) ([]agentscollector.Frame, int64, error) {
	var out []agentscollector.Frame
	for i := len(f.frames) - 1; i >= 0; i-- { // newest first
		if f.frames[i].Seq > since {
			out = append(out, f.frames[i])
		}
	}
	return out, 0, nil
}

func TestTetherPoll(t *testing.T) {
	yes := true
	at := time.Now()
	src := fakeHistory{head: 10, frames: []agentscollector.Frame{
		{Seq: 9, Kind: "session.state_changed", WorkState: "waiting", SessionID: "s0", At: at}, // before first run: not replayed
		{Seq: 11, Kind: "session.state_changed", WorkState: "working", SessionID: "s1", At: at},
		{Seq: 12, Kind: "session.state_changed", WorkState: "waiting", SessionID: "s1", AgentName: "Builder", PendingApproval: &yes, At: at},
		{Seq: 13, Kind: "message.dead_lettered", At: at},
		{Seq: 14, Kind: "session.output", At: at},
	}}
	sink, cur, h := &memSink{}, memCursor{}, NewHealth()
	p := &TetherPoll{Src: src, Sink: sink, Cursor: cur, Health: h}
	if err := p.Poll(context.Background()); err != nil || cur["tether"] != "10" || len(sink.events) != 0 {
		t.Fatalf("first run starts at head: %v %v %d", err, cur, len(sink.events))
	}
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[0].Kind != "agent.state" || sink.events[0].Payload["waitingFor"] != "Waiting on a permission" || sink.events[0].SourceEventID != "12" || sink.events[1].Kind != "message.dead_lettered" {
		t.Fatalf("events: %+v", sink.events)
	}
	if cur["tether"] != "14" {
		t.Fatalf("cursor %v", cur)
	}
	if err := p.Poll(context.Background()); err != nil || len(sink.events) != 2 {
		t.Fatalf("nothing new: %v %d", err, len(sink.events))
	}
}
