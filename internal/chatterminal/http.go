package chatterminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are the explicit patterns the server mounts under chatapi.TerminalPrefix, each behind
// RequireOperatorReads: the terminal family is operator-only on reads as well as writes.
var Routes = []string{
	"GET /machines",
	"POST /sessions", "DELETE /sessions/{id}", "GET /sessions/{id}/ws",
	"GET /proposals", "POST /proposals/{id}/run", "POST /proposals/{id}/deny", "POST /proposals/{id}/edit",
	"GET /runs", "GET /runs/{run_id}", "GET /runs/{run_id}/output", "POST /runs/{run_id}/stop", "PATCH /runs/{run_id}",
}

type Handler struct{ Svc *Service }

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
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "not_found", "No such terminal record.")
	case errors.Is(err, ErrUnknownMachine):
		fail(w, 404, "unknown_machine", "That machine is not in the list.")
	case errors.Is(err, ErrDigest):
		fail(w, 409, "digest_mismatch", "The command changed since you saw it. Reload and review it again.")
	case errors.Is(err, ErrConflict):
		fail(w, 409, "conflict", "That can no longer be done: the run or proposal has already moved on.")
	case errors.Is(err, ErrDenied):
		fail(w, 403, "denied_by_policy", "The terminal policy denies this command.")
	case errors.Is(err, ErrLimit):
		fail(w, 429, "limit_reached", "Four terminal sessions are already open. Close one first.")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid", "The command must be 1 to 4096 characters.")
	default:
		fail(w, 500, "internal", "The terminal could not complete the request.")
	}
}

// Mux serves the family. Mount each of Routes explicitly; do not mount a prefix.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	p := chatapi.TerminalPrefix
	for pattern, fn := range map[string]http.HandlerFunc{
		"GET " + p + "/machines":             h.machines,
		"POST " + p + "/sessions":            h.openSession,
		"DELETE " + p + "/sessions/{id}":     h.closeSession,
		"GET " + p + "/sessions/{id}/ws":     h.ws,
		"GET " + p + "/proposals":            h.proposals,
		"POST " + p + "/proposals/{id}/run":  h.run,
		"POST " + p + "/proposals/{id}/deny": h.deny,
		"POST " + p + "/proposals/{id}/edit": h.edit,
		"GET " + p + "/runs":                 h.runs,
		"GET " + p + "/runs/{run_id}":        h.getRun,
		"GET " + p + "/runs/{run_id}/output": h.output,
		"POST " + p + "/runs/{run_id}/stop":  h.stop,
		"PATCH " + p + "/runs/{run_id}":      h.patch,
	} {
		m.HandleFunc(pattern, fn)
	}
	// Bearer credentials never reach these: an agent cannot approve, stop, close or edit through here,
	// whatever it holds. The operator's browser session is the only caller.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			fail(w, 403, "requester_cannot_approve", "Use the operator browser session.")
			return
		}
		m.ServeHTTP(w, r)
	})
}

func (h *Handler) machines(w http.ResponseWriter, _ *http.Request) {
	ms := h.Svc.Config.Machines()
	if ms == nil {
		ms = []Machine{}
	}
	reply(w, 200, map[string]any{"machines": ms})
}

func (h *Handler) openSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		MachineID string `json:"machine_id"`
		ThreadRef string `json:"thread_ref"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.MachineID == "" {
		fail(w, 400, "invalid", "machine_id is required.")
		return
	}
	if in.ThreadRef != "" {
		if _, err := chatapi.ParseRef(in.ThreadRef); err != nil {
			fail(w, 400, "invalid", err.Error())
			return
		}
	}
	row, err := h.Svc.Mgr.OpenSession(r.Context(), in.MachineID, in.ThreadRef)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 201, row)
}

func (h *Handler) closeSession(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Mgr.CloseSession(r.Context(), r.PathValue("id")); err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

type control struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type serverEvent struct {
	Type  string `json:"type"` // state | exit
	State string `json:"state,omitempty"`
	Code  *int   `json:"code,omitempty"`
}

func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host == r.Host && (o.Scheme == "https" || o.Scheme == "http") && o.User == nil && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func sendEvent(ctx context.Context, c *websocket.Conn, ev serverEvent) {
	raw, _ := json.Marshal(ev)
	_ = c.Write(ctx, websocket.MessageText, raw)
}

func (h *Handler) ws(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "origin_denied", "Use the Tachyon browser origin.")
		return
	}
	id := r.PathValue("id")
	row, err := h.Svc.Store.Session(r.Context(), id)
	if err != nil {
		h.problem(w, err)
		return
	}
	if row.State == "closed" {
		fail(w, 409, "conflict", "This session has ended.")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx := r.Context()
	// The browser sends its size first so ssh starts at the right size.
	cols, rows := uint16(100), uint16(30)
	var pending []byte
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	typ, data, err := c.Read(rctx)
	cancel()
	if err == nil && typ == websocket.MessageText {
		var ctl control
		if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 && ctl.Cols <= 500 && ctl.Rows <= 500 {
			cols, rows = ctl.Cols, ctl.Rows
		}
	} else if err == nil {
		pending = data
	}
	att, err := h.Svc.Mgr.Attach(ctx, id, cols, rows)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	defer att.Detach()
	if len(att.Replay) > 0 && c.Write(ctx, websocket.MessageBinary, att.Replay) != nil {
		return
	}
	if len(pending) > 0 {
		_ = att.Input(pending)
	}
	sendEvent(ctx, c, serverEvent{Type: "state", State: "open"})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if att.Input(data) != nil {
					return
				}
				continue
			}
			var ctl control
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
				att.Resize(ctl.Cols, ctl.Rows)
			}
		}
	}()
	for {
		select {
		case f, ok := <-att.Frames:
			if !ok {
				select {
				case <-att.Done():
					code := att.ExitCode()
					sendEvent(ctx, c, serverEvent{Type: "exit", Code: &code})
					_ = c.Close(websocket.StatusNormalClosure, "session ended")
				default:
					_ = c.Close(websocket.StatusTryAgainLater, "too slow; reconnect")
				}
				return
			}
			if c.Write(ctx, websocket.MessageBinary, f) != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}

func (h *Handler) proposals(w http.ResponseWriter, r *http.Request) {
	h.Svc.ExpireNow(r.Context())
	ps, err := h.Svc.Store.Proposals(r.Context(), r.URL.Query().Get("thread_ref"), r.URL.Query().Get("state"))
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"proposals": ps})
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		OpDigest string `json:"op_digest"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			fail(w, 400, "invalid", "The body must be JSON.")
			return
		}
	}
	run, err := h.Svc.Approve(r.Context(), r.PathValue("id"), in.OpDigest)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, run)
}

func (h *Handler) deny(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Svc.Deny(r.Context(), r.PathValue("id")); err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Command string `json:"command"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, 400, "invalid", "command is required.")
		return
	}
	p, err := h.Svc.Edit(r.Context(), r.PathValue("id"), in.Command)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 201, p)
}

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	h.Svc.ExpireNow(r.Context())
	q := r.URL.Query()
	var closed *bool
	if v := q.Get("closed"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			fail(w, 400, "invalid", "closed must be true or false.")
			return
		}
		closed = &b
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	rs, err := h.Svc.Store.Runs(r.Context(), q.Get("thread_ref"), q.Get("state"), closed, limit)
	if err != nil {
		h.problem(w, err)
		return
	}
	for i := range rs {
		h.Svc.Mgr.LiveOutput(&rs[i])
	}
	reply(w, 200, map[string]any{"runs": rs})
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.Svc.Store.Run(r.Context(), r.PathValue("run_id"))
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Svc.Mgr.LiveOutput(&run)
	reply(w, 200, run)
}

func (h *Handler) output(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.Svc.Mgr.Output(r.Context(), r.PathValue("run_id"), after, limit)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, out)
}

func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Mgr.Stop(r.Context(), r.PathValue("run_id")); err != nil {
		h.problem(w, err)
		return
	}
	run, err := h.Svc.Store.Run(r.Context(), r.PathValue("run_id"))
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 202, run)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		CloseOnComplete *bool `json:"close_on_complete"`
		Closed          *bool `json:"closed"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || (in.CloseOnComplete == nil && in.Closed == nil) {
		fail(w, 400, "invalid", "Send close_on_complete and/or closed.")
		return
	}
	run, err := h.Svc.Store.Patch(r.Context(), r.PathValue("run_id"), in.CloseOnComplete, in.Closed)
	if err != nil {
		h.problem(w, err)
		return
	}
	h.Svc.publish(run.ThreadRef, run, nil)
	h.Svc.Mgr.LiveOutput(&run)
	reply(w, 200, run)
}
