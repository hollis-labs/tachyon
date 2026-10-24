package appagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Prefix is where the handler is mounted. Reads need an authenticated operator
// session; sends need operator elevation (the server wraps them).
const Prefix = "/api/app-agent/v1"

// Handler serves the app agent chat to Tachyon's own UI.
type Handler struct {
	Service *Service
	// Disabled explains why the feature is off when Service is nil.
	Disabled string
}

type threadJSON struct {
	ID          string       `json:"id"`
	Agent       agentJSON    `json:"agent"`
	Title       string       `json:"title"`
	Capability  capabilityJS `json:"capability"`
	Unread      int          `json:"unread"`
	LastAt      int64        `json:"lastAt"`
	LastPreview string       `json:"lastPreview,omitempty"`
	Owner       string       `json:"owner"`
	Runtime     string       `json:"runtime"`
}
type agentJSON struct {
	Address string `json:"address"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	AgentID string `json:"agentId"`
}
type capabilityJS struct {
	Mode    string `json:"mode"`
	CanSend bool   `json:"canSend"`
	Detail  string `json:"detail"`
}

func threadView(t Thread) threadJSON {
	return threadJSON{
		ID: t.ID, Title: t.Title, Unread: t.Unread, LastAt: t.LastAt.UnixMilli(), LastPreview: t.LastPreview, Owner: t.Owner, Runtime: t.Runtime,
		Agent:      agentJSON{Address: "tachyon://agent/" + t.Agent, Label: t.Title, Kind: "agent", AgentID: t.Agent},
		Capability: capabilityJS{Mode: "live", CanSend: true, Detail: "Tachyon's in-app agent. Replies stream in real time. Read-only."},
	}
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "not_found", "No such conversation.")
	case errors.Is(err, ErrBusy):
		fail(w, 409, "busy", "The agent is still answering. Wait for the reply to finish.")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid", err.Error())
	case errors.Is(err, ErrNotConfigured):
		fail(w, 503, "not_configured", "The app agent is not configured on this Tachyon.")
	default:
		fail(w, 500, "internal", "The app agent could not complete the request.")
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil {
		msg := "The app agent is not configured on this Tachyon."
		if h != nil && h.Disabled != "" {
			msg = h.Disabled
		}
		fail(w, 503, "not_configured", msg)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Prefix+"/threads", h.threads)
	mux.HandleFunc("GET "+Prefix+"/threads/{id}/messages", h.messages)
	mux.HandleFunc("POST "+Prefix+"/threads/{id}/messages", h.sendMessage)
	mux.HandleFunc("POST "+Prefix+"/threads/{id}/read", h.read)
	mux.HandleFunc("GET "+Prefix+"/threads/{id}/events", h.events)
	mux.ServeHTTP(w, r)
}

func (h *Handler) threads(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Service.Threads(r.Context())
	if err != nil {
		h.problem(w, err)
		return
	}
	out := make([]threadJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, threadView(t))
	}
	send(w, 200, map[string]any{"threads": out})
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request) {
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
	msgs, more, err := h.Service.Messages(r.Context(), r.PathValue("id"), before, limit)
	if err != nil {
		h.problem(w, err)
		return
	}
	send(w, 200, map[string]any{"messages": msgs, "has_more": more})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Body string `json:"body"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, 400, "invalid", "A JSON body with a message is required.")
		return
	}
	op, reply, err := h.Service.Send(r.Context(), r.PathValue("id"), in.Body)
	if err != nil {
		h.problem(w, err)
		return
	}
	send(w, 202, map[string]any{"message": op, "reply": reply})
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.MarkRead(r.Context(), r.PathValue("id")); err != nil {
		h.problem(w, err)
		return
	}
	send(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.Service.Store.Thread(r.Context(), id); err != nil {
		h.problem(w, err)
		return
	}
	rc := http.NewResponseController(w)
	// A stream outlives the server's write timeout; keepalives prove it is alive.
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	if err := rc.Flush(); err != nil {
		return
	}
	ch, cancel := h.Service.Subscribe(id)
	defer cancel()
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
		case e, ok := <-ch:
			if !ok {
				return // dropped as too slow, or the service stopped; the client reconnects and resyncs
			}
			raw, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
