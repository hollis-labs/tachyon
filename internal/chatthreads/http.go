package chatthreads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
)

// Routes are the explicit patterns the server mounts (relative to chatapi.Prefix). Each
// is mounted individually behind RequireOperator, which lets GETs through and demands
// an operator session and same-origin Origin on writes.
var Routes = []string{
	"GET /threads", "POST /threads",
	"GET /threads/{ref}", "PATCH /threads/{ref}",
	"GET /threads/{ref}/messages", "POST /threads/{ref}/messages",
	"POST /threads/{ref}/read", "POST /threads/{ref}/stop",
	"GET /threads/{ref}/events", "GET /events",
}

var agentID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type Handler struct {
	Agg *Aggregator
	Hub *Hub
	Now func() time.Time

	sendMu sync.Mutex // serializes idempotency check + send
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

// Mux returns the handler for Routes mounted under chatapi.Prefix.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	handlers := map[string]http.HandlerFunc{
		"GET /threads": h.list, "POST /threads": h.create,
		"GET /threads/{ref}": h.get, "PATCH /threads/{ref}": h.patch,
		"GET /threads/{ref}/messages": h.messages, "POST /threads/{ref}/messages": h.send,
		"POST /threads/{ref}/read":  h.read,
		"POST /threads/{ref}/stop":  h.stop,
		"GET /threads/{ref}/events": h.events, "GET /events": h.spine,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, handlers[r])
	}
	return m
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, map[string]string{"code": code, "message": msg})
}

func (h *Handler) problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, appagent.ErrNotFound):
		fail(w, 404, "not_found", "No such thread.")
	case errors.Is(err, appagent.ErrBusy):
		fail(w, 409, "busy", "The agent is still answering. Wait for the reply to finish.")
	case errors.Is(err, appagent.ErrInvalid):
		fail(w, 400, "invalid", err.Error())
	case errors.Is(err, appagent.ErrNotConfigured):
		fail(w, 503, "not_configured", "The app agent is not configured on this Tachyon.")
	case errors.Is(err, chatbridge.ErrNothingToInterrupt):
		fail(w, 409, "nothing_to_stop", "This session has no running turn Tachyon can interrupt.")
	case errors.Is(err, chatbridge.ErrConflict), errors.Is(err, chatbridge.ErrNotReplyable):
		fail(w, 409, "conflict", err.Error())
	default:
		fail(w, 500, "internal", "Chat threads could not complete the request.")
	}
}

// ref validates the path's thread_ref.
func ref(w http.ResponseWriter, r *http.Request) (chatapi.ThreadRef, bool) {
	t, err := chatapi.ParseRef(r.PathValue("ref"))
	if err != nil {
		fail(w, 400, "invalid", err.Error())
		return t, false
	}
	return t, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := Query{Lens: q.Get("lens"), Text: q.Get("q"), NeedYou: q.Get("need_you") == "true"}
	switch query.Lens {
	case "", "all", "agents", "work", "archived":
	default:
		fail(w, 400, "invalid", "lens must be all, agents, work or archived.")
		return
	}
	if v := q.Get("before_at"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "before_at must be a non-negative integer.")
			return
		}
		query.BeforeAt = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid", "limit must be 1 to 200.")
			return
		}
		query.Limit = n
	}
	out, more, err := h.Agg.List(r.Context(), query)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"threads": out, "has_more": more})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	s, err := h.Agg.Get(r.Context(), t.String())
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, s)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Agent     string `json:"agent"`
		Title     string `json:"title"`
		ProjectID string `json:"project_id"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || !agentID.MatchString(in.Agent) || len(in.Title) > 200 || len(in.ProjectID) > 100 {
		fail(w, 400, "invalid", "A JSON body with an agent id (letters, digits, . _ -) is required.")
		return
	}
	if h.Agg.App == nil {
		h.problem(w, appagent.ErrNotConfigured)
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "New chat"
	}
	th, err := h.Agg.App.Store.CreateThread(r.Context(), "", in.Agent, title, h.now())
	if err != nil {
		h.problem(w, err)
		return
	}
	tr := chatapi.ThreadRef{Source: chatapi.SourceApp, ID: th.ID}.String()
	if in.ProjectID != "" {
		if _, err := h.Agg.State.Apply(r.Context(), tr, Patch{SetProject: true, ProjectID: in.ProjectID}, h.now()); err != nil {
			h.problem(w, err)
			return
		}
	}
	s, err := h.Agg.Get(r.Context(), tr)
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Notify(r.Context(), tr)
	reply(w, 201, s)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || len(raw) == 0 {
		fail(w, 400, "invalid", "A JSON body with pinned, archived, title or project_id is required.")
		return
	}
	var p Patch
	for k, v := range raw {
		var err error
		switch k {
		case "pinned":
			var b bool
			err = json.Unmarshal(v, &b)
			p.Pinned = &b
		case "archived":
			var b bool
			err = json.Unmarshal(v, &b)
			p.Archived = &b
		case "title":
			var s string
			err = json.Unmarshal(v, &s)
			s = strings.TrimSpace(s)
			if len(s) > 200 {
				err = errors.New("title too long")
			}
			p.Title = &s
		case "project_id":
			p.SetProject = true
			if string(v) != "null" {
				err = json.Unmarshal(v, &p.ProjectID)
				if len(p.ProjectID) > 100 {
					err = errors.New("project_id too long")
				}
			}
		default:
			err = fmt.Errorf("unknown field %q", k)
		}
		if err != nil {
			fail(w, 400, "invalid", "Invalid "+k+".")
			return
		}
	}
	// The overlay must not grow rows for threads that do not exist.
	if _, err := h.Agg.Get(r.Context(), t.String()); err != nil {
		h.problem(w, err)
		return
	}
	if _, err := h.Agg.State.Apply(r.Context(), t.String(), p, h.now()); err != nil {
		h.problem(w, err)
		return
	}
	s, err := h.Agg.Get(r.Context(), t.String())
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Notify(r.Context(), t.String())
	reply(w, 200, s)
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	if _, err := h.Agg.Get(r.Context(), t.String()); err != nil {
		h.problem(w, err)
		return
	}
	var err error
	if t.Source == chatapi.SourceApp {
		err = h.Agg.App.MarkRead(r.Context(), t.ID)
	} else {
		err = h.Agg.State.MarkRead(r.Context(), t.String(), h.now())
	}
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Notify(r.Context(), t.String())
	reply(w, 200, map[string]bool{"ok": true})
}

// Message is the contract's message shape.
type Message struct {
	Seq  int64          `json:"seq"`
	ID   string         `json:"id"`
	Role string         `json:"role"`
	Kind string         `json:"kind"`
	Text string         `json:"text"`
	At   int64          `json:"at"`
	Meta map[string]any `json:"meta"`
}

func fromUI(m appagent.UIMessage) Message {
	meta := map[string]any{"tools": m.Tools}
	if m.Error != "" {
		meta["error"] = m.Error
	}
	if m.Delivery != "" {
		meta["delivery"] = m.Delivery
	}
	if m.Streaming {
		meta["streaming"] = true
	}
	if m.Blocked {
		meta["blocked"] = true
	}
	role := m.From
	kind := m.Kind
	if kind == "" {
		kind = "text"
	}
	for k, v := range m.Meta { // a card or plan reference rides in meta
		meta[k] = v
	}
	return Message{Seq: m.Seq, ID: m.ID, Role: role, Kind: kind, Text: m.Body, At: m.At, Meta: meta}
}

func (h *Handler) tetherGroup(ctx context.Context, ref string) (tgroup, error) {
	gs, err := h.Agg.tetherGroups(ctx)
	if err != nil {
		return tgroup{}, err
	}
	for _, g := range gs {
		if g.ref == ref {
			return g, nil
		}
	}
	return tgroup{}, ErrNotFound
}

func tetherMessages(g tgroup) []Message {
	var out []Message
	for _, t := range g.turns {
		out = append(out, Message{Seq: int64(len(out) + 1), ID: t.ID, Role: "agent", Kind: "text", Text: t.Content, At: parseTime(t.Created).UnixMilli(),
			Meta: map[string]any{"turn_id": t.TurnID, "turn_kind": t.Kind, "state": t.State, "revision": t.Revision, "replyable": t.Replyable && open(t)}})
		if t.Resolution != nil {
			out = append(out, Message{Seq: int64(len(out) + 1), ID: t.ID + ":reply", Role: "operator", Kind: "text", Text: t.Resolution.Text, At: parseTime(t.Resolution.At).UnixMilli()})
		}
	}
	return out
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var before int64
	if v := r.URL.Query().Get("before_seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "before_seq must be a non-negative integer.")
			return
		}
		before = n
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid", "limit must be 1 to 200.")
			return
		}
		limit = n
	}
	if t.Source == chatapi.SourceApp {
		if h.Agg.App == nil {
			h.problem(w, appagent.ErrNotConfigured)
			return
		}
		ms, more, err := h.Agg.App.Messages(r.Context(), t.ID, before, limit)
		if err != nil {
			h.problem(w, err)
			return
		}
		out := make([]Message, 0, len(ms))
		for _, m := range ms {
			out = append(out, fromUI(m))
		}
		reply(w, 200, map[string]any{"messages": out, "has_more": more})
		return
	}
	g, err := h.tetherGroup(r.Context(), t.String())
	if err != nil {
		h.problem(w, err)
		return
	}
	all := tetherMessages(g)
	if limit == 0 {
		limit = 50
	}
	end := len(all)
	if before > 0 && int(before)-1 < end {
		end = int(before) - 1
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	out := append([]Message{}, all[start:end]...)
	reply(w, 200, map[string]any{"messages": out, "has_more": start > 0})
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<10)
	var in struct {
		Text string `json:"text"`
		Key  string `json:"idempotency_key"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Key == "" || len(in.Key) > 128 || strings.TrimSpace(in.Text) == "" {
		fail(w, 400, "invalid", "A JSON body with text and idempotency_key is required.")
		return
	}
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	ctx := r.Context()
	if t.Source == chatapi.SourceApp {
		if h.Agg.App == nil {
			h.problem(w, appagent.ErrNotConfigured)
			return
		}
		if id, rid, seq, found, err := h.Agg.State.Key(ctx, t.String(), in.Key); err != nil {
			h.problem(w, err)
			return
		} else if found {
			reply(w, 202, map[string]any{"id": id, "reply_id": rid, "seq": seq, "idempotency_key": in.Key, "state": "accepted"})
			return
		}
		op, rep, err := h.Agg.App.Send(ctx, t.ID, in.Text)
		if err != nil {
			h.problem(w, err)
			return
		}
		if err := h.Agg.State.SaveKey(ctx, t.String(), in.Key, op.ID, rep.ID, op.Seq, h.now()); err != nil {
			h.problem(w, err)
			return
		}
		h.Notify(ctx, t.String())
		reply(w, 202, map[string]any{"id": op.ID, "reply_id": rep.ID, "seq": op.Seq, "idempotency_key": in.Key, "state": "accepted"})
		return
	}
	if h.Agg.Chat == nil {
		fail(w, 503, "not_configured", "Tether chat is not configured on this Tachyon.")
		return
	}
	g, err := h.tetherGroup(ctx, t.String())
	if err != nil {
		h.problem(w, err)
		return
	}
	var target *chatbridge.Turn
	for i := len(g.turns) - 1; i >= 0; i-- {
		if open(g.turns[i]) && g.turns[i].Replyable {
			target = &g.turns[i]
			break
		}
	}
	if target == nil {
		fail(w, 409, "conflict", "This session is not waiting for a reply.")
		return
	}
	if _, err := h.Agg.Chat.Reply(ctx, target.ID, target.Revision, in.Text); err != nil {
		h.problem(w, err)
		return
	}
	h.Notify(ctx, t.String())
	reply(w, 202, map[string]any{"id": target.ID + ":reply", "seq": len(tetherMessages(g)) + 1, "idempotency_key": in.Key, "state": "accepted"})
}

// stop interrupts the running turn of a Tether-backed session (Tether reply with interrupt:true). The app agent's
// runtime has no cancel call, so an app thread answers 501.
func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	if t.Source == chatapi.SourceApp {
		fail(w, 501, "not_supported", "The app agent cannot be interrupted; wait for the reply to finish.")
		return
	}
	if h.Agg.Chat == nil {
		fail(w, 503, "not_configured", "Tether chat is not configured on this Tachyon.")
		return
	}
	res, err := h.Agg.Chat.Interrupt(r.Context(), t.ID, "Stop the current turn and wait for my next message.")
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Notify(r.Context(), t.String())
	reply(w, 202, map[string]any{"thread_ref": t.String(), "state": "stop_requested", "interrupt": res})
}

// Notify publishes the thread's summary-visible state on its stream and the spine. It
// never fails the caller: events are signals, and clients refetch on reconnect.
func (h *Handler) Notify(ctx context.Context, ref string) {
	s, err := h.Agg.Get(context.WithoutCancel(ctx), ref)
	if err != nil {
		return
	}
	_, _ = h.Hub.Publish(context.WithoutCancel(ctx), ref, chatapi.EventThread, map[string]any{
		"blocked": s.BlockedOnUser, "blocked_breakdown": s.BlockedBreakdown, "unread": s.Unread, "plan_progress": s.PlanProgress, "pinned": s.Pinned,
		"archived": s.Archived, "title": s.Title, "last_at": s.LastAt, "last_preview": s.LastPreview,
	})
}

func startSSE(w http.ResponseWriter) (*http.ResponseController, bool) {
	rc := http.NewResponseController(w)
	// A stream outlives the server's write timeout; keepalives prove it is alive.
	_ = rc.SetWriteDeadline(time.Time{})
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	hd.Set("Connection", "keep-alive")
	w.WriteHeader(200)
	return rc, rc.Flush() == nil
}

func writeFrame(w http.ResponseWriter, rc *http.ResponseController, f Frame) bool {
	if f.ID > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", f.ID); err != nil {
			return false
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", f.Data); err != nil {
		return false
	}
	return rc.Flush() == nil
}

// spine streams `thread` events for every thread.
func (h *Handler) spine(w http.ResponseWriter, r *http.Request) {
	rc, ok := startSSE(w)
	if !ok {
		return
	}
	sub := h.Hub.Subscribe("")
	defer h.Hub.Unsubscribe(sub)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case f, ok := <-sub.C:
			if !ok || !writeFrame(w, rc, f) {
				return
			}
		}
	}
}

// events streams one thread. Hub events carry ids and replay after Last-Event-ID; app
// threads also relay the agent's live message, delta, tool, end and delivery frames.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var last int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	if _, err := h.Agg.Get(r.Context(), t.String()); err != nil {
		h.problem(w, err)
		return
	}
	// Register before replaying so nothing published in between is lost; frames
	// already replayed are skipped by id.
	sub := h.Hub.Subscribe(t.String())
	defer h.Hub.Unsubscribe(sub)
	var app <-chan appagent.Event
	if t.Source == chatapi.SourceApp && h.Agg.App != nil {
		ch, cancel := h.Agg.App.Subscribe(t.ID)
		defer cancel()
		app = ch
	}
	var replay []Frame
	if last > 0 {
		var err error
		if replay, err = h.Hub.Replay(r.Context(), t.String(), last); err != nil {
			h.problem(w, err)
			return
		}
	}
	rc, ok := startSSE(w)
	if !ok {
		return
	}
	for _, f := range replay {
		if !writeFrame(w, rc, f) {
			return
		}
		last = f.ID
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case f, ok := <-sub.C:
			if !ok {
				return
			}
			if f.ID > 0 && f.ID <= last {
				continue
			}
			if f.ID > 0 {
				last = f.ID
			}
			if !writeFrame(w, rc, f) {
				return
			}
		case e, ok := <-app:
			if !ok {
				return
			}
			raw, err := json.Marshal(e)
			if err != nil {
				continue
			}
			var env map[string]any
			if json.Unmarshal(raw, &env) != nil {
				continue
			}
			env["thread_ref"], env["at"] = t.String(), h.now().UnixMilli()
			body, _ := json.Marshal(env)
			if !writeFrame(w, rc, Frame{Data: body}) {
				return
			}
		}
	}
}

// MessageSeq returns the seq of a message in the thread, or an error when it is not there.
func (h *Handler) MessageSeq(ctx context.Context, t chatapi.ThreadRef, messageRef string) (int64, error) {
	if t.Source == chatapi.SourceApp {
		if h.Agg.App == nil {
			return 0, appagent.ErrNotConfigured
		}
		return h.Agg.App.Store.MessageSeq(ctx, t.ID, messageRef)
	}
	g, err := h.tetherGroup(ctx, t.String())
	if err != nil {
		return 0, err
	}
	for _, m := range tetherMessages(g) {
		if m.ID == messageRef {
			return m.Seq, nil
		}
	}
	return 0, ErrNotFound
}
