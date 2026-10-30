package appagent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Store{DB: db}
}

// fakeRuntime scripts a runtime: Observe plays events then returns observeErr.
type fakeRuntime struct {
	mu          sync.Mutex
	ensureErr   error
	ensured     []string
	submitErrs  []error // consumed one per Submit call
	submits     []string
	events      []TurnEvent
	observeErr  error
	hold        chan struct{} // when set, Observe waits for it before playing events
	result      Result
	resultErr   error
	resultCalls int
}

func (f *fakeRuntime) Ensure(_ context.Context, existing string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, existing)
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	if existing != "" {
		return existing, nil
	}
	return "sess-" + string(rune('a'+len(f.ensured))), nil
}
func (f *fakeRuntime) Submit(_ context.Context, session, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits = append(f.submits, session+"|"+text)
	if len(f.submitErrs) > 0 {
		err := f.submitErrs[0]
		f.submitErrs = f.submitErrs[1:]
		if err != nil {
			return "", err
		}
	}
	return "turn-1", nil
}
func (f *fakeRuntime) Observe(ctx context.Context, _, _ string, emit func(TurnEvent)) error {
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, e := range f.events {
		emit(e)
	}
	return f.observeErr
}
func (f *fakeRuntime) Result(context.Context, string, string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resultCalls++
	return f.result, f.resultErr
}

func newService(t *testing.T, rt Runtime) (*Service, *Store) {
	t.Helper()
	st := newStore(t)
	s := &Service{Store: st, Runtime: rt, Label: "Tachyon", PollEvery: 5 * time.Millisecond, PollFor: 2 * time.Second}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, st
}

// drain collects events until one matches stop, or fails after a timeout.
func drain(t *testing.T, ch <-chan Event, stop func(Event) bool) []Event {
	t.Helper()
	var got []Event
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed; got %+v", got)
			}
			got = append(got, e)
			if stop(e) {
				return got
			}
		case <-timeout:
			t.Fatalf("timed out; got %+v", got)
		}
	}
}

func isEnd(e Event) bool { return e.Type == "end" }

func TestSendStreamsStoresAndCompletes(t *testing.T) {
	rt := &fakeRuntime{events: []TurnEvent{
		{Kind: EventToolStart, Tool: Tool{ID: "c1", Name: "tachyon_planner_list", Status: "running"}},
		{Kind: EventToolEnd, Tool: Tool{ID: "c1", Name: "tachyon_planner_list", Status: "ok"}},
		{Kind: EventText, Text: "Hello"}, {Kind: EventText, Text: " world"}, {Kind: EventFinish, Reason: "stop"},
	}}
	s, st := newService(t, rt)
	threads, _ := s.Threads(context.Background())
	if len(threads) != 1 || threads[0].ID != DefaultThread || threads[0].Title != "Tachyon" {
		t.Fatalf("default thread: %+v", threads)
	}
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	op, reply, err := s.Send(context.Background(), DefaultThread, "  what is up?  ")
	if err != nil {
		t.Fatal(err)
	}
	if op.From != "operator" || op.Body != "what is up?" || reply.From != "agent" || !reply.Streaming {
		t.Fatalf("send result: %+v %+v", op, reply)
	}
	events := drain(t, ch, isEnd)
	var body strings.Builder
	var sawTool, delivered, replied bool
	for _, e := range events {
		switch {
		case e.Type == "delta":
			body.WriteString(e.Text)
		case e.Type == "tool":
			sawTool = true
		case e.Type == "delivery" && e.State == "delivered":
			delivered = true
		}
	}
	more := drain(t, ch, func(e Event) bool { return e.Type == "delivery" && e.State == "replied" })
	replied = len(more) > 0
	if body.String() != "Hello world" || !sawTool || !delivered || !replied {
		t.Fatalf("events body=%q tool=%v delivered=%v replied=%v", body.String(), sawTool, delivered, replied)
	}
	msgs, hasMore, err := s.Messages(context.Background(), DefaultThread, 0, 50)
	if err != nil || hasMore || len(msgs) != 2 {
		t.Fatalf("messages: %+v %v %v", msgs, hasMore, err)
	}
	if msgs[0].Delivery != "replied" || msgs[1].Body != "Hello world" || msgs[1].Streaming || len(msgs[1].Tools) != 1 || msgs[1].Tools[0].Status != "ok" {
		t.Fatalf("stored: %+v", msgs)
	}
	th, _ := st.Thread(context.Background(), DefaultThread)
	if th.RuntimeSession == "" || th.Unread != 1 {
		t.Fatalf("thread: session=%q unread=%d", th.RuntimeSession, th.Unread)
	}
	if err := s.MarkRead(context.Background(), DefaultThread); err != nil {
		t.Fatal(err)
	}
	if th, _ = st.Thread(context.Background(), DefaultThread); th.Unread != 0 {
		t.Fatalf("unread after read: %d", th.Unread)
	}
}

func TestSendValidatesAndRefusesWhileAReplyIsInFlight(t *testing.T) {
	rt := &fakeRuntime{hold: make(chan struct{}), events: []TurnEvent{{Kind: EventText, Text: "ok"}, {Kind: EventFinish, Reason: "stop"}}}
	s, _ := newService(t, rt)
	if _, _, err := s.Send(context.Background(), DefaultThread, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread: %v", err)
	}
	s.Threads(context.Background())
	for _, bad := range []string{"", "   ", strings.Repeat("a", MaxBody+1)} {
		if _, _, err := s.Send(context.Background(), DefaultThread, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("body %q accepted: %v", bad[:min(len(bad), 8)], err)
		}
	}
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	if _, _, err := s.Send(context.Background(), DefaultThread, "first"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Send(context.Background(), DefaultThread, "second"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second send while streaming: %v", err)
	}
	close(rt.hold)
	drain(t, ch, isEnd)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, err := s.Send(context.Background(), DefaultThread, "third"); err == nil {
			break
		} else if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("send after reply: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRuntimeFailuresAreShownAndNeverHang(t *testing.T) {
	for name, tc := range map[string]struct {
		rt   *fakeRuntime
		want string
		turn bool
	}{
		"ensure fails": {&fakeRuntime{ensureErr: errors.Join(ErrRuntimeDown, errors.New("x"))}, "unavailable", false},
		"submit fails": {&fakeRuntime{submitErrs: []error{ErrRuntimeDown}}, "unavailable", false},
		"run errors":   {&fakeRuntime{events: []TurnEvent{{Kind: EventText, Text: "part"}, {Kind: EventError, Err: "provider exploded sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}}, "provider exploded", true},
		"early stop":   {&fakeRuntime{events: []TurnEvent{{Kind: EventFinish, Reason: "length"}}}, "stopped early (length)", true},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newService(t, tc.rt)
			s.Threads(context.Background())
			ch, cancel := s.Subscribe(DefaultThread)
			defer cancel()
			op, _, err := s.Send(context.Background(), DefaultThread, "hi")
			if err != nil {
				t.Fatal(err)
			}
			ev := drain(t, ch, isEnd)
			last := ev[len(ev)-1]
			if !strings.Contains(last.Error, tc.want) || strings.Contains(last.Error, "AAAAAAAA") {
				t.Fatalf("end error %q, want %q (and no secret)", last.Error, tc.want)
			}
			msgs, _, _ := s.Messages(context.Background(), DefaultThread, 0, 10)
			if msgs[1].Streaming || msgs[1].Error == "" {
				t.Fatalf("stored reply: %+v", msgs[1])
			}
			wantDelivery := "failed"
			if tc.turn {
				wantDelivery = "delivered"
			}
			if msgs[0].ID != op.ID || msgs[0].Delivery != wantDelivery {
				t.Fatalf("operator delivery %q want %q", msgs[0].Delivery, wantDelivery)
			}
		})
	}
}

func TestAGoneSessionIsReplacedOnce(t *testing.T) {
	rt := &fakeRuntime{submitErrs: []error{ErrSessionGone, nil}, events: []TurnEvent{{Kind: EventText, Text: "ok"}, {Kind: EventFinish, Reason: "stop"}}}
	s, st := newService(t, rt)
	s.Threads(context.Background())
	st.SetSession(context.Background(), DefaultThread, "old-session")
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	if _, _, err := s.Send(context.Background(), DefaultThread, "hi"); err != nil {
		t.Fatal(err)
	}
	drain(t, ch, isEnd)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.submits) != 2 || !strings.HasPrefix(rt.submits[0], "old-session|") || strings.HasPrefix(rt.submits[1], "old-session|") {
		t.Fatalf("submits: %v", rt.submits)
	}
	th, _ := st.Thread(context.Background(), DefaultThread)
	if th.RuntimeSession == "old-session" || th.RuntimeSession == "" {
		t.Fatalf("session not replaced: %q", th.RuntimeSession)
	}
}

func TestALostStreamIsFinishedFromTheRuntimesRecord(t *testing.T) {
	rt := &fakeRuntime{events: []TurnEvent{{Kind: EventText, Text: "par"}}, observeErr: ErrRuntimeDown,
		result: Result{State: "completed", Text: "partial and the rest", Tools: []Tool{{ID: "c", Name: "t", Status: "ok"}}}}
	s, _ := newService(t, rt)
	s.Threads(context.Background())
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	s.Send(context.Background(), DefaultThread, "hi")
	drain(t, ch, isEnd)
	msgs, _, _ := s.Messages(context.Background(), DefaultThread, 0, 10)
	if msgs[1].Body != "partial and the rest" || msgs[1].Error != "" || len(msgs[1].Tools) != 1 || msgs[1].Streaming {
		t.Fatalf("recovered reply: %+v", msgs[1])
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.resultCalls == 0 {
		t.Fatal("Result was never consulted")
	}
}

func TestASubscriberJoiningMidStreamGetsASnapshotWithoutGapOrDuplicate(t *testing.T) {
	rt := &fakeRuntime{hold: make(chan struct{})}
	s, _ := newService(t, rt)
	s.Threads(context.Background())
	_, reply, err := s.Send(context.Background(), DefaultThread, "hi")
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the run reached Observe, then feed some text by hand through apply.
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	l := s.live[reply.ID]
	s.mu.Unlock()
	s.apply(l, TurnEvent{Kind: EventText, Text: "Hello "})
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	first := <-ch
	if first.Type != "message" || first.Message.Body != "Hello " || !first.Message.Streaming {
		t.Fatalf("snapshot: %+v", first)
	}
	s.apply(l, TurnEvent{Kind: EventText, Text: "again"})
	if e := <-ch; e.Type != "delta" || e.Text != "again" {
		t.Fatalf("next event: %+v", e)
	}
	// A reload (messages endpoint) sees the in-memory body, not the lagging database copy.
	msgs, _, _ := s.Messages(context.Background(), DefaultThread, 0, 10)
	if msgs[1].Body != "Hello again" {
		t.Fatalf("history body: %q", msgs[1].Body)
	}
	close(rt.hold)
	s.apply(l, TurnEvent{Kind: EventFinish, Reason: "stop"})
}

func TestRestartFinishesOrFailsRepliesThatWereInFlight(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	st.CreateThread(ctx, "t1", "tachyon", "Tachyon", time.Now())
	st.SetSession(ctx, "t1", "sess-1")
	_, withTurn, _ := st.AppendTurn(ctx, "t1", "one", time.Now())
	st.SaveAgent(ctx, withTurn.ID, "", nil, StateStreaming, "", "turn-9", time.Now())
	st.CreateThread(ctx, "t2", "tachyon", "Other", time.Now())
	_, noTurn, _ := st.AppendTurn(ctx, "t2", "two", time.Now())
	rt := &fakeRuntime{result: Result{State: "completed", Text: "recovered text"}}
	s := &Service{Store: st, Runtime: rt, PollEvery: 5 * time.Millisecond, PollFor: 2 * time.Second}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, _ := st.Message(ctx, withTurn.ID)
		b, _ := st.Message(ctx, noTurn.ID)
		if a.State == StateComplete && b.State == StateFailed {
			if a.Body != "recovered text" || !strings.Contains(b.Error, "restarted") {
				t.Fatalf("a=%+v b=%+v", a, b)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reconciled: a=%s b=%s", a.State, b.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The UI orders messages by time, then id. A turn's two messages are stored with the same
// timestamp, so a reply could render above the message it answers (seen live on 2026-10-24:
// "no response from Tachyon" while the reply sat above the operator's message).
func TestAReplyAlwaysSortsAfterTheMessageItAnswers(t *testing.T) {
	rt := &fakeRuntime{events: []TurnEvent{{Kind: EventText, Text: "ok"}, {Kind: EventFinish, Reason: "stop"}}}
	s, st := newService(t, rt)
	s.Threads(context.Background())
	fixed := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return fixed }
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	op, reply, err := s.Send(context.Background(), DefaultThread, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if !(reply.At > op.At) {
		t.Fatalf("live: reply at %d must be after operator at %d", reply.At, op.At)
	}
	drain(t, ch, isEnd)
	msgs, _, _ := s.Messages(context.Background(), DefaultThread, 0, 10)
	if len(msgs) != 2 || !(msgs[1].At > msgs[0].At) || msgs[0].From != "operator" {
		t.Fatalf("history: %+v", msgs)
	}
	// Existing rows stored with identical timestamps are corrected on read.
	stored, _, _ := st.Messages(context.Background(), DefaultThread, 0, 10)
	if !stored[0].CreatedAt.Equal(stored[1].CreatedAt) {
		t.Fatalf("test premise: rows should share a timestamp: %v %v", stored[0].CreatedAt, stored[1].CreatedAt)
	}
}

func TestInputRequiredBecomesBlockedNotFailed(t *testing.T) {
	rt := &fakeRuntime{observeErr: errors.New("stream lost"), result: Result{State: "input_required", Text: "Which project?"}}
	changed := make(chan string, 4)
	s, st := newService(t, rt)
	s.OnChange = func(id string) { changed <- id }
	ch, cancel := s.Subscribe(DefaultThread)
	defer cancel()
	if _, err := s.Threads(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Send(context.Background(), DefaultThread, "plan it"); err != nil {
		t.Fatal(err)
	}
	events := drain(t, ch, isEnd)
	if end := events[len(events)-1]; end.State != StateBlocked || end.Error != "" {
		t.Fatalf("end event: %+v", end)
	}
	select {
	case id := <-changed:
		if id != DefaultThread {
			t.Fatal(id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange not called")
	}
	th, err := st.Thread(context.Background(), DefaultThread)
	if err != nil || !th.Blocked || th.Unread != 1 {
		t.Fatalf("thread: %+v %v", th, err)
	}
	msgs, _, _ := st.Messages(context.Background(), DefaultThread, 0, 10)
	if got := msgs[len(msgs)-1]; got.State != StateBlocked || got.Body != "Which project?" {
		t.Fatalf("reply: %+v", got)
	}
	// A later operator turn clears the blocker.
	rt.result = Result{State: "completed", Text: "ok"}
	if _, _, err := s.Send(context.Background(), DefaultThread, "Tachyon"); err != nil {
		t.Fatal(err)
	}
	th, _ = st.Thread(context.Background(), DefaultThread)
	if th.Blocked {
		t.Fatal("still blocked after the operator answered")
	}
}
