package actionrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Prefix is under the operator cookie's path. Wrap the handler in the operator
// assurance guard (reads included): a run's output and terminal are sensitive.
const Prefix = "/api/plugins/console/v1/action-runs"

// Handler serves a run's record and its terminal websocket. It can only attach
// to an existing run created by an approved request; it never starts a command
// of its own choosing, and the command it starts is re-derived and digest-checked
// by Manager.Start.
type Handler struct{ Service *Service }

type control struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type serverEvent struct {
	Type  string `json:"type"` // "state" | "exit"
	State string `json:"state,omitempty"`
	Code  *int   `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host == r.Host && (o.Scheme == "https" || o.Scheme == "http") && o.User == nil && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "Use GET.")
		return
	}
	// Like the approval resolve route: bearer credentials never grant this.
	if r.Header.Get("Authorization") != "" {
		fail(w, 403, "requester_cannot_approve", "Use the operator browser session.")
		return
	}
	// Route on the one canonical decoded path: an encoded slash or backslash would
	// let the router and this handler disagree about which route was named.
	if escaped := strings.ToLower(r.URL.EscapedPath()); strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(r.URL.Path, "\\") || !strings.HasPrefix(r.URL.Path, Prefix+"/") {
		fail(w, 404, "not_found", "Unknown action run route.")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		fail(w, 404, "not_found", "Unknown action run route.")
		return
	}
	run, err := h.Service.Get(r.Context(), parts[0])
	if err != nil {
		if err == ErrNotFound {
			fail(w, 404, "not_found", "No such action run.")
		} else {
			fail(w, 500, "unavailable", "Could not read the action run.")
		}
		return
	}
	switch {
	case len(parts) == 1:
		_ = json.NewEncoder(w).Encode(run)
	case parts[1] == "terminal":
		if !sameOrigin(r) {
			fail(w, 403, "origin_denied", "Use the Tachyon browser origin.")
			return
		}
		h.terminal(w, r, run)
	default:
		fail(w, 404, "not_found", "Unknown action run route.")
	}
}

func sendEvent(ctx context.Context, c *websocket.Conn, ev serverEvent) error {
	raw, _ := json.Marshal(ev)
	return c.Write(ctx, websocket.MessageText, raw)
}

func (h *Handler) terminal(w http.ResponseWriter, r *http.Request, run Run) {
	// Accept verifies the Origin host matches again; there are no extra patterns.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx := r.Context()
	m := h.Service.Manager

	if run.State == StatePending || run.State == StateApproved {
		_ = sendEvent(ctx, c, serverEvent{Type: "state", State: run.State})
		_ = c.Close(websocket.StatusTryAgainLater, "not approved yet")
		return
	}
	// The browser sends its terminal size first so the process starts at the
	// right size; fall back to a default if it does not.
	cols, rows := uint16(100), uint16(30)
	var pending []byte
	if run.State == StateWaitingInput {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		typ, data, err := c.Read(rctx)
		cancel()
		if err == nil && typ == websocket.MessageText {
			var ctl control
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 && ctl.Cols <= 500 && ctl.Rows <= 500 {
				cols, rows = ctl.Cols, ctl.Rows
			}
		} else if err == nil {
			pending = data // input before a size: hold it, do not drop or log it
		}
		if started, err := m.Start(ctx, run.ID, cols, rows); err != nil && !m.Live(run.ID) {
			_ = sendEvent(ctx, c, serverEvent{Type: "exit", State: started.State, Error: started.Error})
			_ = c.Close(websocket.StatusNormalClosure, "refused")
			return
		}
	}
	replay, frames, cancel, err := m.Attach(ctx, run.ID)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	defer cancel()
	if len(replay) > 0 {
		if c.Write(ctx, websocket.MessageBinary, replay) != nil {
			return
		}
	}
	if len(pending) > 0 {
		_ = m.Input(run.ID, pending)
	}
	_ = sendEvent(ctx, c, serverEvent{Type: "state", State: StateRunning})

	// Input and control from the browser.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if m.Input(run.ID, data) != nil {
					return
				}
				continue
			}
			var ctl control
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
				_ = m.Resize(run.ID, ctl.Cols, ctl.Rows)
			}
		}
	}()
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				final, _ := h.Service.Store.Get(ctx, run.ID)
				_ = sendEvent(ctx, c, serverEvent{Type: "exit", State: final.State, Code: final.ExitCode, Error: final.Error})
				_ = c.Close(websocket.StatusNormalClosure, "run ended")
				return
			}
			if f.Output != nil {
				if c.Write(ctx, websocket.MessageBinary, f.Output) != nil {
					return
				}
			}
		case <-readDone:
			// The tab went away. The run keeps going and can be reattached.
			return
		}
	}
}
