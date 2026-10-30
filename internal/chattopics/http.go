package chattopics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are mounted explicitly by the server under chatapi.Prefix, each behind RequireOperator.
var Routes = []string{
	"GET /threads/{ref}/topics", "POST /threads/{ref}/topics", "DELETE /threads/{ref}/topics/{id}",
	"GET /threads/{ref}/bookmarks", "POST /threads/{ref}/bookmarks", "DELETE /threads/{ref}/bookmarks/{id}",
}

type Handler struct {
	Store *Store
	// Exists reports whether the thread exists (nil error).
	Exists func(ctx context.Context, ref string) error
	// MessageSeq resolves a message_ref in the thread to its seq. An error means the
	// message is not in that thread.
	MessageSeq func(ctx context.Context, ref chatapi.ThreadRef, messageRef string) (int64, error)
	// Published is called after a change; typ is "topic" or "bookmark". May be nil.
	Published func(ctx context.Context, thread, typ, op, id string)
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

func problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "not_found", "No such topic or bookmark.")
	case errors.Is(err, ErrLimit):
		fail(w, 413, "limit_reached", "This chat has reached its limit.")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid", "Check the title, message and note.")
	default:
		fail(w, 500, "internal", "The request could not be completed.")
	}
}

func (h *Handler) publish(ctx context.Context, thread, typ, op, id string) {
	if h.Published != nil {
		h.Published(context.WithoutCancel(ctx), thread, typ, op, id)
	}
}

func (h *Handler) thread(w http.ResponseWriter, r *http.Request) (chatapi.ThreadRef, bool) {
	t, err := chatapi.ParseRef(r.PathValue("ref"))
	if err != nil {
		fail(w, 400, "invalid", err.Error())
		return t, false
	}
	if h.Exists != nil && h.Exists(r.Context(), t.String()) != nil {
		fail(w, 404, "not_found", "No such thread.")
		return t, false
	}
	return t, true
}

// Mux returns the handler for Routes mounted under chatapi.Prefix.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	hs := map[string]http.HandlerFunc{
		"GET /threads/{ref}/topics": h.listTopics, "POST /threads/{ref}/topics": h.addTopic, "DELETE /threads/{ref}/topics/{id}": h.delTopic,
		"GET /threads/{ref}/bookmarks": h.listBookmarks, "POST /threads/{ref}/bookmarks": h.addBookmark, "DELETE /threads/{ref}/bookmarks/{id}": h.delBookmark,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, hs[r])
	}
	return m
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	ts, err := h.Store.Topics(r.Context(), t.String())
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"topics": ts})
}

// ResolveSeq returns the message's seq, or an error when it is not in the thread.
func (h *Handler) ResolveSeq(ctx context.Context, t chatapi.ThreadRef, messageRef string) (*int64, error) {
	if h.MessageSeq == nil {
		return nil, nil
	}
	seq, err := h.MessageSeq(ctx, t, messageRef)
	if err != nil {
		return nil, ErrInvalid
	}
	return &seq, nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid", "Send a JSON body with the documented fields.")
		return false
	}
	return true
}

func (h *Handler) addTopic(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	var in struct {
		Title      string `json:"title"`
		MessageRef string `json:"message_ref"`
	}
	if !decode(w, r, &in) {
		return
	}
	seq, err := h.ResolveSeq(r.Context(), t, in.MessageRef)
	if err != nil {
		problem(w, err)
		return
	}
	topic, err := h.Store.AddTopic(r.Context(), t.String(), in.Title, in.MessageRef, seq, "user")
	if err != nil {
		problem(w, err)
		return
	}
	h.publish(r.Context(), t.String(), chatapi.EventTopic, "add", topic.ID)
	reply(w, 201, topic)
}

func (h *Handler) delTopic(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	if err := h.Store.DeleteTopic(r.Context(), t.String(), r.PathValue("id")); err != nil {
		problem(w, err)
		return
	}
	h.publish(r.Context(), t.String(), chatapi.EventTopic, "remove", r.PathValue("id"))
	reply(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) listBookmarks(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	bs, err := h.Store.Bookmarks(r.Context(), t.String())
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"bookmarks": bs})
}

func (h *Handler) addBookmark(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	var in struct {
		MessageRef string `json:"message_ref"`
		Note       string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := h.ResolveSeq(r.Context(), t, in.MessageRef); err != nil {
		problem(w, err)
		return
	}
	b, created, err := h.Store.AddBookmark(r.Context(), t.String(), in.MessageRef, in.Note)
	if err != nil {
		problem(w, err)
		return
	}
	status := 200 // a repeat returns the existing bookmark
	if created {
		status = 201
		h.publish(r.Context(), t.String(), chatapi.EventBookmark, "add", b.ID)
	}
	reply(w, status, b)
}

func (h *Handler) delBookmark(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	if err := h.Store.DeleteBookmark(r.Context(), t.String(), r.PathValue("id")); err != nil {
		problem(w, err)
		return
	}
	h.publish(r.Context(), t.String(), chatapi.EventBookmark, "remove", r.PathValue("id"))
	reply(w, 200, map[string]bool{"ok": true})
}
