package appagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/security/redact"
)

const (
	MaxBody       = 4000
	DefaultThread = "app-tachyon"
	DefaultAgent  = "tachyon"
)

// UIMessage is a message in the shape the chat UI reads (ChatMessage).
type UIMessage struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	ThreadID  string `json:"threadId"`
	From      string `json:"from"`
	Body      string `json:"body"`
	At        int64  `json:"at"`
	Delivery  string `json:"delivery,omitempty"`
	Streaming bool   `json:"streaming,omitempty"`
	Tools     []Tool `json:"tools"`
	Error     string `json:"error,omitempty"`
}

// Event is one change a subscriber sees (ChatEvent).
type Event struct {
	Type      string     `json:"type"`
	Message   *UIMessage `json:"message,omitempty"`
	MessageID string     `json:"messageId,omitempty"`
	Text      string     `json:"text,omitempty"`
	Tool      *Tool      `json:"tool,omitempty"`
	Error     string     `json:"error,omitempty"`
	State     string     `json:"state,omitempty"`
}

type sub struct {
	thread string
	ch     chan Event
}

// live is an agent message while its reply is in flight. The accumulated body is
// authoritative until the message is finished; the database copy lags it.
type live struct {
	agent     Message
	operator  string
	body      strings.Builder
	tools     []Tool
	textParts int
	flushed   time.Time
}

type Service struct {
	Store     *Store
	Runtime   Runtime
	Label     string // title of the default thread
	Now       func() time.Time
	PollEvery time.Duration
	PollFor   time.Duration

	mu   sync.Mutex
	subs map[*sub]struct{}
	live map[string]*live
	bg   context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Start begins background work and reconciles replies that were in flight when
// Tachyon last stopped: each is finished from the runtime's own record of the
// turn, or marked failed if it never reached the runtime.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	s.subs = map[*sub]struct{}{}
	s.live = map[string]*live{}
	s.bg, s.stop = context.WithCancel(context.Background())
	s.mu.Unlock()
	pending, err := s.Store.Streaming(ctx)
	if err != nil {
		return err
	}
	for _, m := range pending {
		th, err := s.Store.Thread(ctx, m.ThreadID)
		if err != nil {
			continue
		}
		if m.RuntimeTurn == "" || th.RuntimeSession == "" {
			s.fail(m, "", "Tachyon restarted before this message reached the agent. Send it again.")
			continue
		}
		l := &live{agent: m, tools: m.Tools}
		l.body.WriteString(m.Body)
		s.mu.Lock()
		s.live[m.ID] = l
		s.mu.Unlock()
		s.wg.Add(1)
		go func(sess, turn string) {
			defer s.wg.Done()
			s.poll(s.bg, l, sess, turn)
		}(th.RuntimeSession, m.RuntimeTurn)
	}
	return nil
}

func (s *Service) Stop() {
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
}

func (s *Service) pollEvery() time.Duration {
	if s.PollEvery > 0 {
		return s.PollEvery
	}
	return 3 * time.Second
}

func (s *Service) pollFor() time.Duration {
	if s.PollFor > 0 {
		return s.PollFor
	}
	return 15 * time.Minute
}

func ui(m Message, delivery string) UIMessage {
	// The operator message and its reply are stored in one transaction with the same
	// timestamp, and the UI orders by time then id, so the reply could sort above the
	// message it answers. The reply follows its message by a millisecond.
	at := m.CreatedAt.UnixMilli()
	if m.Role == RoleAgent {
		at++
	}
	u := UIMessage{ID: m.ID, Seq: m.Seq, ThreadID: m.ThreadID, From: m.Role, Body: m.Body, At: at, Tools: m.Tools, Error: m.Error}
	if u.Tools == nil {
		u.Tools = []Tool{}
	}
	if m.Role == RoleOperator {
		u.Delivery = delivery
	} else {
		u.From = "agent"
		u.Streaming = m.State == StateStreaming
	}
	return u
}

// delivery is an operator message's state, read off the agent reply that follows it.
func delivery(reply *Message) string {
	switch {
	case reply == nil:
		return "delivered"
	case reply.State == StateFailed && reply.RuntimeTurn == "":
		return "failed"
	case reply.State == StateComplete:
		return "replied"
	case reply.RuntimeTurn != "":
		return "delivered"
	}
	return "queued"
}

func (s *Service) publish(thread string, e Event) {
	for c := range s.subs {
		if c.thread != thread {
			continue
		}
		select {
		case c.ch <- e:
		default:
			// A subscriber that cannot keep up is dropped; it reconnects and resyncs.
			delete(s.subs, c)
			close(c.ch)
		}
	}
}

// Subscribe returns a channel of changes to one thread. It starts with the current
// state of any reply in flight, taken atomically with registration, so a client
// that reloaded mid-stream sees no gap and no duplicate.
func (s *Service) Subscribe(threadID string) (<-chan Event, func()) {
	c := &sub{thread: threadID, ch: make(chan Event, 256)}
	s.mu.Lock()
	for _, l := range s.live {
		if l.agent.ThreadID != threadID {
			continue
		}
		m := l.agent
		m.Body, m.Tools = l.body.String(), append([]Tool{}, l.tools...)
		u := ui(m, "")
		c.ch <- Event{Type: "message", Message: &u}
	}
	s.subs[c] = struct{}{}
	s.mu.Unlock()
	return c.ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[c]; ok {
			delete(s.subs, c)
			close(c.ch)
		}
		s.mu.Unlock()
	}
}

func (s *Service) Threads(ctx context.Context) ([]Thread, error) {
	if _, err := s.Store.CreateThread(ctx, DefaultThread, DefaultAgent, firstNonEmpty(s.Label, "Tachyon"), s.now()); err != nil {
		return nil, err
	}
	return s.Store.Threads(ctx)
}

func (s *Service) Messages(ctx context.Context, threadID string, before int64, limit int) ([]UIMessage, bool, error) {
	if _, err := s.Store.Thread(ctx, threadID); err != nil {
		return nil, false, err
	}
	msgs, more, err := s.Store.Messages(ctx, threadID, before, limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]UIMessage, 0, len(msgs))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range msgs {
		if l, ok := s.live[m.ID]; ok {
			m.Body, m.Tools = l.body.String(), append([]Tool{}, l.tools...)
		}
		var next *Message
		if m.Role == RoleOperator && i+1 < len(msgs) {
			next = &msgs[i+1]
		}
		out = append(out, ui(m, delivery(next)))
	}
	return out, more, nil
}

func (s *Service) MarkRead(ctx context.Context, threadID string) error {
	return s.Store.MarkRead(ctx, threadID)
}

// Send stores the operator's message and starts the agent's reply in the background.
func (s *Service) Send(ctx context.Context, threadID, body string) (UIMessage, UIMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > MaxBody {
		return UIMessage{}, UIMessage{}, fmt.Errorf("%w: a message is 1 to %d characters", ErrInvalid, MaxBody)
	}
	if s.bg == nil {
		return UIMessage{}, UIMessage{}, ErrNotConfigured
	}
	th, err := s.Store.Thread(ctx, threadID)
	if err != nil {
		return UIMessage{}, UIMessage{}, err
	}
	op, ag, err := s.Store.AppendTurn(ctx, threadID, body, s.now())
	if err != nil {
		return UIMessage{}, UIMessage{}, err
	}
	l := &live{agent: ag, operator: op.ID}
	s.mu.Lock()
	s.live[ag.ID] = l
	opUI, agUI := ui(op, "queued"), ui(ag, "")
	s.publish(threadID, Event{Type: "message", Message: &opUI})
	s.publish(threadID, Event{Type: "message", Message: &agUI})
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(s.bg, th, l, body)
	}()
	return opUI, agUI, nil
}

func (s *Service) run(ctx context.Context, th Thread, l *live, text string) {
	sess, err := s.Runtime.Ensure(ctx, th.RuntimeSession)
	if err != nil {
		s.fail(l.agent, l.operator, errText(err))
		return
	}
	if sess != th.RuntimeSession {
		if err := s.Store.SetSession(ctx, th.ID, sess); err != nil {
			s.fail(l.agent, l.operator, "Tachyon could not save the session.")
			return
		}
	}
	turn, err := s.Runtime.Submit(ctx, sess, text)
	if errors.Is(err, ErrSessionGone) {
		if sess, err = s.Runtime.Ensure(ctx, ""); err == nil {
			if err = s.Store.SetSession(ctx, th.ID, sess); err == nil {
				turn, err = s.Runtime.Submit(ctx, sess, text)
			}
		}
	}
	if err != nil {
		s.fail(l.agent, l.operator, errText(err))
		return
	}
	l.agent.RuntimeTurn = turn
	if err := s.Store.SaveAgent(ctx, l.agent.ID, "", nil, StateStreaming, "", turn, s.now()); err != nil {
		s.fail(l.agent, l.operator, "Tachyon could not record the turn.")
		return
	}
	s.mu.Lock()
	s.publish(th.ID, Event{Type: "delivery", MessageID: l.operator, State: "delivered"})
	s.mu.Unlock()
	err = s.Runtime.Observe(ctx, sess, turn, func(e TurnEvent) { s.apply(l, e) })
	s.mu.Lock()
	_, stillLive := s.live[l.agent.ID]
	s.mu.Unlock()
	if err != nil && stillLive {
		// The stream was lost. The turn may have finished; the runtime's own record decides.
		s.poll(ctx, l, sess, turn)
	}
}

func errText(err error) string {
	switch {
	case errors.Is(err, ErrNotConfigured):
		return "The app agent is not configured on this Tachyon."
	case errors.Is(err, ErrRuntimeDown), errors.Is(err, ErrSessionGone):
		return err.Error()
	}
	return "The agent could not be reached."
}

// apply folds one runtime event into the live message, publishes it, and
// finishes the message when the turn ends.
func (s *Service) apply(l *live, e TurnEvent) {
	switch e.Kind {
	case EventText:
		s.mu.Lock()
		text := e.Text
		if l.textParts == 0 && l.body.Len() > 0 {
			text = "\n\n" + text
		}
		l.textParts++
		l.body.WriteString(text)
		s.publish(l.agent.ThreadID, Event{Type: "delta", MessageID: l.agent.ID, Text: text})
		flush := s.now().Sub(l.flushed) > 750*time.Millisecond
		body, tools := l.body.String(), append([]Tool{}, l.tools...)
		if flush {
			l.flushed = s.now()
		}
		s.mu.Unlock()
		if flush {
			_ = s.Store.SaveAgent(s.bg, l.agent.ID, body, tools, StateStreaming, "", "", s.now())
		}
	case EventToolStart, EventToolEnd:
		s.mu.Lock()
		l.textParts = 0 // the next text starts a new paragraph
		found := false
		for i := range l.tools {
			if l.tools[i].ID == e.Tool.ID {
				if e.Tool.Name != "" {
					l.tools[i].Name = e.Tool.Name
				}
				l.tools[i].Status, l.tools[i].Detail = e.Tool.Status, e.Tool.Detail
				e.Tool = l.tools[i]
				found = true
			}
		}
		if !found {
			l.tools = append(l.tools, e.Tool)
		}
		t := e.Tool
		s.publish(l.agent.ThreadID, Event{Type: "tool", MessageID: l.agent.ID, Tool: &t})
		s.mu.Unlock()
	case EventFinish:
		if e.Reason == "" || e.Reason == "stop" || e.Reason == "end_turn" || e.Reason == "complete" {
			s.finish(l, StateComplete, "")
		} else {
			s.finish(l, StateFailed, "The agent stopped early ("+e.Reason+").")
		}
	case EventError:
		s.finish(l, StateFailed, redact.Text(e.Err))
	}
}

// finish writes the final state, drops the live copy and tells subscribers.
func (s *Service) finish(l *live, state, errText string) {
	s.mu.Lock()
	if _, ok := s.live[l.agent.ID]; !ok {
		s.mu.Unlock()
		return // already finished by another path
	}
	body, tools := l.body.String(), append([]Tool{}, l.tools...)
	delete(s.live, l.agent.ID)
	s.mu.Unlock()
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Store.SaveAgent(saveCtx, l.agent.ID, body, tools, state, errText, "", s.now()); err != nil {
		errText = firstNonEmpty(errText, "Tachyon could not save the reply.")
		state = StateFailed
	}
	d := "replied"
	if state == StateFailed {
		d = "failed"
		if l.agent.RuntimeTurn != "" {
			d = "delivered"
		}
	}
	s.mu.Lock()
	s.publish(l.agent.ThreadID, Event{Type: "end", MessageID: l.agent.ID, Error: errText})
	if l.operator != "" {
		s.publish(l.agent.ThreadID, Event{Type: "delivery", MessageID: l.operator, State: d})
	}
	s.mu.Unlock()
}

// fail marks a message that never reached or never got an answer from the runtime.
func (s *Service) fail(m Message, operator, text string) {
	l := &live{agent: m, operator: operator}
	s.mu.Lock()
	if cur, ok := s.live[m.ID]; ok {
		l = cur
	} else {
		s.live[m.ID] = l
	}
	s.mu.Unlock()
	s.finish(l, StateFailed, text)
}

// poll finishes a turn from the runtime's own record of it: used when a stream is
// lost and after a Tachyon restart.
func (s *Service) poll(ctx context.Context, l *live, session, turn string) {
	deadline := time.Now().Add(s.pollFor())
	var last error
	for time.Now().Before(deadline) {
		res, err := s.Runtime.Result(ctx, session, turn)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrSessionGone):
			s.finish(l, StateFailed, "The agent session was lost before the reply arrived. Send it again.")
			return
		case err != nil:
			last = err
		case res.Terminal():
			s.mu.Lock()
			l.body.Reset()
			l.body.WriteString(res.Text)
			l.tools = res.Tools
			m := l.agent
			m.Body, m.Tools = res.Text, res.Tools
			u := ui(m, "")
			s.publish(l.agent.ThreadID, Event{Type: "message", Message: &u})
			s.mu.Unlock()
			if res.State == "completed" {
				s.finish(l, StateComplete, "")
			} else {
				s.finish(l, StateFailed, redact.Text(firstNonEmpty(res.Error, "The agent run "+res.State+".")))
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.pollEvery()):
		}
	}
	msg := "The agent did not answer in time."
	if last != nil {
		msg = errText(last)
	}
	s.finish(l, StateFailed, msg)
}
