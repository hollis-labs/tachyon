package chatactivity

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are mounted explicitly by the server under chatapi.Prefix, each behind RequireOperator.
var Routes = []string{
	"GET /threads/{ref}/activity", "POST /threads/{ref}/activity/comments", "GET /threads/{ref}/context",
}

type Handler struct {
	Rec *Recorder
	// Exists reports whether the thread exists (nil error).
	Exists func(ctx context.Context, ref string) error
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

func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	hs := map[string]http.HandlerFunc{
		"GET /threads/{ref}/activity": h.list, "POST /threads/{ref}/activity/comments": h.comment, "GET /threads/{ref}/context": h.context,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, hs[r])
	}
	return m
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	qq := Query{Type: q.Get("type"), Actor: q.Get("actor"), Q: strings.TrimSpace(q.Get("q"))}
	if qq.Type != "" && !Types[qq.Type] {
		fail(w, 400, "invalid", "Unknown activity type.")
		return
	}
	for key, dst := range map[string]*int64{"before_seq": &qq.Before} {
		if v := q.Get(key); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				fail(w, 400, "invalid", key+" must be a non-negative integer.")
				return
			}
			*dst = n
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "invalid", "limit must be 1 to 100.")
			return
		}
		qq.Limit = n
	}
	items, more, err := h.Rec.Store.List(r.Context(), t.String(), qq)
	if err != nil {
		fail(w, 500, "internal", "The request could not be completed.")
		return
	}
	reply(w, 200, map[string]any{"activity": items, "has_more": more})
}

// comment is the operator's own note on the feed. The actor is always "user": this route sits
// behind RequireOperator, and no field of the body can name another actor.
func (h *Handler) comment(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in struct {
		Body string `json:"body"`
	}
	if dec.Decode(&in) != nil {
		fail(w, 400, "invalid", "Send {\"body\": \"...\"}.")
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" || len([]rune(body)) > MaxComment {
		fail(w, 400, "invalid", "A comment is 1 to 2000 characters.")
		return
	}
	payload, _ := json.Marshal(map[string]string{"body": redact.Text(body)})
	e, err := h.Rec.Record(r.Context(), Entry{ThreadRef: t.String(), Type: "comment", Actor: "user", Source: "tachyon", Payload: payload})
	if err != nil {
		fail(w, 500, "internal", "The request could not be completed.")
		return
	}
	reply(w, 201, e)
}

func (h *Handler) context(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	// Tether threads have no meter yet: the Agents collector will feed it (design 9).
	c, err := h.Rec.Store.Context(r.Context(), t.String())
	if err != nil || t.Source != chatapi.SourceApp {
		c = Context{Source: "unknown", Breakdown: []BreakdownRow{}, At: h.Rec.Store.now().UnixMilli()}
	}
	reply(w, 200, c)
}
