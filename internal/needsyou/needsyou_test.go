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

func read(m *Message)     { now := t0; m.ReadAt = &now }
func archived(m *Message) { now := t0; m.ArchivedAt = &now }
func highUrgency(m *Message) {
	m.Metadata["urgency"] = "high"
	m.Metadata["from_verification"] = "verified"
}

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
	for _, k := range []string{"asks", "mail", "statuses"} {
		if s := r.Sources[k]; !s.OK || s.Error != "" || s.Truncated {
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

// Critical is earned, not claimed: an approval request at urgency high is critical only when Tether verified its
// sender. Unverified (observe mode) mail at urgency high is still listed, as normal.
func TestNeedsYouCriticalNeedsAVerifiedSender(t *testing.T) {
	unverifiedHigh := func(m *Message) { m.Metadata["urgency"] = "high"; m.Metadata["from_verification"] = "unverified" }
	approval := func(id string, over func(*Message)) Message {
		return msg(id, builder, "request", envelopeType, -time.Minute, map[string]any{"kind": "approval_request", "body": "Deploy?"}, over)
	}
	ft := &fakeTether{
		pending: []Pending{
			{MessageID: "typed-verified", From: builder, Kind: "approval_request", Subject: "Deploy?", CreatedAt: t0.Add(-time.Minute)},
			{MessageID: "typed-unverified", From: builder, Kind: "approval_request", Subject: "Deploy?", CreatedAt: t0.Add(-2 * time.Minute)},
		},
		boxes: map[string][]Message{Operator: {
			approval("typed-verified", highUrgency),
			approval("typed-unverified", unverifiedHigh),
			msg("interim-unverified", builder, "request", "application/json", -3*time.Minute, map[string]any{"kind": "approval_request", "subject": "Wipe it?"}, unverifiedHigh),
			msg("interim-verified", builder, "request", "application/json", -4*time.Minute, map[string]any{"kind": "approval_request", "subject": "Wipe it?"}, highUrgency),
		}},
	}
	r := (&Aggregator{Tether: ft}).Read(context.Background())
	crit := map[string]bool{}
	for _, it := range r.Items {
		crit[it.ID] = it.Critical
	}
	want := map[string]bool{"ask:typed-verified": true, "ask:typed-unverified": false, "mail:interim-verified": true, "mail:interim-unverified": false}
	for id, c := range want {
		if got, listed := crit[id]; !listed || got != c {
			t.Errorf("%s: listed=%v critical=%v, want listed and critical=%v", id, listed, got, c)
		}
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

type fakeChats struct {
	asks []ChatAsk
	err  error
}

func (f fakeChats) OpenAsks(context.Context) ([]ChatAsk, error) { return f.asks, f.err }

type fakeApprovals struct {
	q   ApprovalQueue
	err error
}

func (f fakeApprovals) Waiting(context.Context, time.Time) (ApprovalQueue, error) { return f.q, f.err }

// Inside Tachyon: chat threads with open question or approval turns, one item each with a link to the agent's chat,
// and the approvals queue as one item, critical when any pending approval is high risk.
func TestNeedsYouListsOpenChatAsksAndTheApprovalsQueue(t *testing.T) {
	a := &Aggregator{Tether: &fakeTether{}, Statuses: fakeStatuses{},
		Chats: fakeChats{asks: []ChatAsk{
			{Ref: "tether:s1", Title: "Deploy plan", AgentID: "taskops-manager", Agent: "Task Ops Manager", Questions: 1, Approvals: 2, LastAt: t0.Add(-2 * time.Hour)},
			{Ref: "tether:s2", Title: "", AgentID: "chief", Agent: "Chief", Questions: 3, LastAt: t0.Add(-time.Hour)},
			{Ref: "tether:s3", Title: "Answered", AgentID: "x", Agent: "X", LastAt: t0},
		}},
		Approvals: fakeApprovals{q: ApprovalQueue{Pending: 3, High: 1, Oldest: t0.Add(-30 * time.Minute)}},
	}
	r := a.Read(context.Background())
	if strings.Join(ids(r), ",") != "approvals:queue,chat:tether:s1,chat:tether:s2" {
		t.Fatalf("items %v", ids(r))
	}
	q, c1, c2 := r.Items[0], r.Items[1], r.Items[2]
	if !q.Critical || q.Link != "/approvals" || q.Title != "3 approvals waiting in the approvals queue" || q.Detail != "1 high risk" || q.Why != "decide" {
		t.Fatalf("queue: %+v", q)
	}
	if c1.Kind != "approval" || c1.Detail != "2 approvals, 1 question" || c1.Title != "Deploy plan" || c1.From != "Task Ops Manager" ||
		c1.Link != "/chat/chat:%2F%2Ftether%2Flaunch%2Ftaskops-manager" {
		t.Fatalf("chat with approvals: %+v", c1)
	}
	if c2.Kind != "question" || c2.Detail != "3 questions" || c2.Title != "Chief" {
		t.Fatalf("chat with questions only, untitled: %+v", c2)
	}
	for _, k := range []string{"chat", "approvals"} {
		if !r.Sources[k].OK {
			t.Fatalf("source %s: %+v", k, r.Sources[k])
		}
	}
	// An empty queue adds nothing; an unreadable local source is reported like a remote one.
	a.Approvals, a.Chats = fakeApprovals{}, fakeChats{err: errors.New("database is locked")}
	r = a.Read(context.Background())
	if len(r.Items) != 0 || r.Sources["chat"].OK || !strings.Contains(r.Sources["chat"].Error, "locked") || !r.Sources["approvals"].OK {
		t.Fatalf("%v %+v", ids(r), r.Sources)
	}
}

// The ceremony meter counts asks to the operator in a window: typed asks and interim requests, not other mail and
// not his own.
func TestAsksSinceCountsAsksInTheWindow(t *testing.T) {
	ft := &fakeTether{boxes: map[string][]Message{Operator: {
		msg("typed", lead, "request", envelopeType, -time.Hour, map[string]any{"kind": "decision"}, nil),
		msg("interim", builder, "request", "application/json", -2*time.Hour, map[string]any{"kind": "approval_request"}, nil),
		msg("awaiting", builder, "notice", "application/json", -3*time.Hour, map[string]any{"awaiting": "@chrispian"}, nil),
		msg("update", lead, "status_update", envelopeType, -time.Hour, map[string]any{"kind": "update"}, nil),
		msg("notice", lead, "notice", "application/json", -time.Hour, map[string]any{"text": "fyi"}, nil),
		msg("old", lead, "request", envelopeType, -10*24*time.Hour, map[string]any{"kind": "question"}, nil),
	}}}
	n, err := (&Aggregator{Tether: ft}).AsksSince(context.Background(), t0.Add(-7*24*time.Hour))
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
}
