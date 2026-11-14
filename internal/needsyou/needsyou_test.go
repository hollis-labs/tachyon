package needsyou

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
)

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

type fakeTether struct {
	pending    []Pending
	pendingErr error
	boxes      map[string][]Message
	total      int
	boxErr     map[string]error
}

func (f *fakeTether) PendingReplies(context.Context, string) ([]Pending, error) {
	return f.pending, f.pendingErr
}
func (f *fakeTether) Mailbox(_ context.Context, owner string, _ int) ([]Message, int, error) {
	if err := f.boxErr[owner]; err != nil {
		return nil, 0, err
	}
	n := f.total
	if n == 0 {
		n = len(f.boxes[owner])
	}
	return f.boxes[owner], n, nil
}

type fakeStatuses []agentscollector.SeatStatus

func (f fakeStatuses) SeatStatuses(context.Context, time.Time) ([]agentscollector.SeatStatus, error) {
	return f, nil
}

const (
	builder = "msg://agent/agent-mux/agt_builder"
	lead    = "msg://agent/agent-mux/agt_lead"
)

func msg(id, from, kind, ct string, at time.Duration, payload map[string]any, over func(*Message)) Message {
	raw, _ := json.Marshal(payload)
	m := Message{ID: id, From: from, To: Operator, Kind: kind, ContentType: ct, Payload: raw, Metadata: map[string]string{}, CreatedAt: t0.Add(at)}
	if over != nil {
		over(&m)
	}
	return m
}

func read(m *Message)        { now := t0; m.ReadAt = &now }
func archived(m *Message)    { now := t0; m.ArchivedAt = &now }
func highUrgency(m *Message) { m.Metadata["urgency"] = "high" }

var verified = map[string]string{"from_verification": "verified", "from_principal": Operator}

func ids(r Result) []string {
	var out []string
	for _, it := range r.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestNeedsYouListsWhatWaitsOnTheOperatorAndNothingElse(t *testing.T) {
	ft := &fakeTether{
		pending: []Pending{
			{MessageID: "t-question", From: lead, Kind: "question", Subject: "Which port?", CreatedAt: t0.Add(-3 * time.Hour)},
			{MessageID: "t-approval", From: builder, Kind: "approval_request", Subject: "Deploy tachyon now?", CreatedAt: t0.Add(-time.Minute)},
		},
		boxes: map[string][]Message{
			Operator: {
				msg("t-question", lead, "request", envelopeType, -3*time.Hour, map[string]any{"kind": "question", "body": "Which port?", "re": "task:CW-1"}, nil),
				msg("t-approval", builder, "request", envelopeType, -time.Minute, map[string]any{"kind": "approval_request", "body": "Deploy?"}, highUrgency),
				msg("t-update", lead, "status_update", envelopeType, -2*time.Hour, map[string]any{"kind": "update", "body": "Wave 2 shipped.\nDetails follow."}, nil),
				msg("t-update-read", lead, "status_update", envelopeType, -2*time.Hour, map[string]any{"kind": "update", "body": "Old news"}, read),
				msg("t-for-agent", lead, "status_update", envelopeType, -2*time.Hour, map[string]any{"kind": "update", "attention": "agent", "body": "FYI agents"}, nil),
				msg("i-ask", builder, "request", "application/json", -5*time.Hour, map[string]any{"kind": "decision", "attention": "human", "awaiting": "@chrispian", "subject": "Keep or patch the dialog?"}, read),
				msg("i-answered", builder, "request", "application/json", -6*time.Hour, map[string]any{"kind": "decision", "subject": "Answered already"}, nil),
				msg("i-archived", builder, "request", "application/json", -6*time.Hour, map[string]any{"kind": "question", "subject": "Archived"}, archived),
				msg("i-human", lead, "notice", "application/json", -4*time.Hour, map[string]any{"attention": "human", "body": "token ghp_Zx9Qw3Er5Ty7Ui1Op2As4Df6Gh8Jk0Lm leaked"}, nil),
				msg("i-human-read", lead, "notice", "application/json", -4*time.Hour, map[string]any{"attention": "human", "body": "seen"}, read),
				msg("plain", lead, "notice", "application/json", -time.Hour, map[string]any{"text": "deployed"}, nil),
			},
			builder: {{ID: "r1", From: Operator, To: builder, InReplyTo: "i-answered", Metadata: verified}},
		},
	}
	st := fakeStatuses{
		{AgentURN: builder, Handle: "@builder-1", State: "blocked", Owner: "@chrispian", Reason: "needs the deploy token", Ref: "task:CW-2", At: t0.Add(-30 * time.Minute)},
		{AgentURN: lead, Handle: "@lead", State: "waiting-agent", Owner: "@archie", At: t0},
		{AgentURN: "msg://agent/agent-mux/agt_x", Handle: "@x", State: "working", Owner: "@chrispian", At: t0},
	}
	r := (&Aggregator{Tether: ft, Statuses: st, Now: func() time.Time { return t0 }}).Read(context.Background())
	want := []string{"ask:t-approval", "mail:i-ask", "mail:i-human", "ask:t-question", "mail:t-update", "status:" + builder}
	if strings.Join(ids(r), ",") != strings.Join(want, ",") {
		t.Fatalf("items %v, want %v (critical first, then longest waiting)", ids(r), want)
	}
	by := map[string]Item{}
	for _, it := range r.Items {
		by[it.ID] = it
	}
	if a := by["ask:t-approval"]; !a.Critical || a.From != "@builder-1" || a.Why != "reply" || a.Title != "Deploy tachyon now?" {
		t.Fatalf("critical approval: %+v", a)
	}
	if q := by["ask:t-question"]; q.Critical || q.Ref != "task:CW-1" || q.From != "@lead" {
		t.Fatalf("question: %+v", q)
	}
	if i := by["mail:i-ask"]; i.Why != "reply" || i.Kind != "decision" || i.Title != "Keep or patch the dialog?" {
		t.Fatalf("an interim ask waits for a reply even once read: %+v", i)
	}
	if h := by["mail:i-human"]; h.Why != "read" || strings.Contains(h.Title, "ghp_") {
		t.Fatalf("unread human mail, redacted: %+v", h)
	}
	if u := by["mail:t-update"]; u.Why != "read" || u.Title != "Wave 2 shipped." {
		t.Fatalf("a typed message to him needs him while unread: %+v", u)
	}
	if s := by["status:"+builder]; s.Why != "unblock" || s.Kind != "blocked" || s.Title != "needs the deploy token" || s.Ref != "task:CW-2" {
		t.Fatalf("status: %+v", s)
	}
	for k, s := range r.Sources {
		if !s.OK || s.Error != "" || s.Truncated {
			t.Fatalf("source %s: %+v", k, s)
		}
	}
}

// Only Chrispian's verified reply settles an ask. In observe mode any agent can send from=operator, stored
// unverified: that must not clear the ask. A verified reply from his canonical address (tether#554) does.
func TestNeedsYouCountsOnlyAVerifiedOperatorReply(t *testing.T) {
	ask := func(id string) Message {
		return msg(id, builder, "request", "application/json", -time.Hour, map[string]any{"kind": "decision", "subject": "Ship it?"}, nil)
	}
	ft := &fakeTether{boxes: map[string][]Message{
		Operator: {ask("faked"), ask("principal-mismatch"), ask("canonical"), ask("old-form")},
		builder: {
			{ID: "x1", From: Operator, InReplyTo: "faked", Metadata: map[string]string{"from_verification": "unverified", "from_principal": "msg://session/local/s1"}},
			{ID: "x2", From: Operator, InReplyTo: "principal-mismatch", Metadata: map[string]string{"from_verification": "verified", "from_principal": "msg://agent/agent-mux/agt_builder"}},
			{ID: "x3", From: OperatorCanonical, InReplyTo: "canonical", Metadata: verified},
			{ID: "x4", From: Operator, InReplyTo: "old-form", Metadata: verified},
		},
	}}
	r := (&Aggregator{Tether: ft}).Read(context.Background())
	if strings.Join(ids(r), ",") != "mail:faked,mail:principal-mismatch" {
		t.Fatalf("an unverified or foreign-principal reply must not clear an ask; verified replies from either address must: %v", ids(r))
	}
}

// A source that cannot be read is reported, never shown as an empty list, and the others still answer.
func TestNeedsYouReportsAnUnreadableSource(t *testing.T) {
	ft := &fakeTether{pendingErr: errors.New("tether answered 502"), boxErr: map[string]error{Operator: errors.New("tether unreachable")}}
	st := fakeStatuses{{AgentURN: builder, State: "waiting-decision", Owner: "@chrispian", At: t0}}
	r := (&Aggregator{Tether: ft, Statuses: st}).Read(context.Background())
	if r.Sources["asks"].OK || !strings.Contains(r.Sources["asks"].Error, "502") || r.Sources["mail"].OK || !r.Sources["statuses"].OK {
		t.Fatalf("sources: %+v", r.Sources)
	}
	if len(r.Items) != 1 || r.Items[0].Kind != "waiting-decision" || r.Items[0].Title != "waiting decision" {
		t.Fatalf("statuses still answer: %+v", r.Items)
	}
	// A truncated window and a failed reply check are said too; the ask stays listed rather than guessed answered.
	ft = &fakeTether{total: 500, boxes: map[string][]Message{Operator: {msg("i1", builder, "request", "application/json", 0, map[string]any{"kind": "question", "subject": "Q?"}, nil)}},
		boxErr: map[string]error{builder: errors.New("tether answered 403 (forbidden)")}}
	r = (&Aggregator{Tether: ft}).Read(context.Background())
	if m := r.Sources["mail"]; !m.OK || !m.Truncated || !strings.Contains(m.Error, "403") || len(r.Items) != 1 {
		t.Fatalf("mail: %+v items %v", m, ids(r))
	}
	if s := r.Sources["statuses"]; s.OK || s.Error == "" {
		t.Fatalf("no status source: %+v", s)
	}
}
