package builderchange

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

// HTTP serves the change routes. Reads and writes are wrapped separately; every pattern is mounted by itself.
type HTTP struct {
	Service   func() *Service // looked up per request; nil result means not configured
	Principal func(*http.Request) string
}

// Mount registers the change routes under builderapi.Prefix.
func (h *HTTP) Mount(mux *http.ServeMux, reads, writes func(http.Handler) http.Handler) {
	id := func(f http.HandlerFunc) http.Handler { return writes(f) }
	mux.Handle("GET "+builderapi.Prefix+"/changes", reads(http.HandlerFunc(h.list)))
	mux.Handle("GET "+builderapi.Prefix+"/changes/{id}", reads(http.HandlerFunc(h.get)))
	mux.Handle("POST "+builderapi.Prefix+"/changes", id(h.create))
	mux.Handle("POST "+builderapi.Prefix+"/changes/{id}/submit", id(h.submit))
	mux.Handle("POST "+builderapi.Prefix+"/changes/{id}/cancel", id(h.cancel))
	mux.Handle("POST "+builderapi.Prefix+"/changes/{id}/rearm", id(h.rearm))
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, e *APIError) {
	write(w, e.Status, map[string]string{"code": e.Code, "message": e.Message})
}

func decode(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil && err != io.EOF || d.More() {
		fail(w, refuse(400, "invalid", "Send a JSON object with the documented fields only."))
		return false
	}
	return true
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	for k := range r.URL.Query() {
		if k != "state" {
			fail(w, refuse(400, "invalid", "Unknown parameter."))
			return
		}
	}
	out, e := h.Service().List(r.Context(), r.URL.Query().Get("state"))
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, map[string]any{"changes": out})
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	c, e := h.Service().Get(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, c)
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request) {
	var req Request
	if !decode(w, r, &req, 2<<20) {
		return
	}
	who := ""
	if h.Principal != nil {
		who = h.Principal(r)
	}
	c, created, e := h.Service().Create(r.Context(), who, req)
	if e != nil {
		fail(w, e)
		return
	}
	if !created {
		w.Header().Set("X-Tachyon-Replay", "1")
		write(w, 200, c)
		return
	}
	write(w, 201, c)
}

func (h *HTTP) submit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ack bool `json:"ack_privileged"`
	}
	if !decode(w, r, &in, 1<<10) {
		return
	}
	c, e := h.Service().Submit(r.Context(), r.PathValue("id"), in.Ack)
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, c)
}

func (h *HTTP) cancel(w http.ResponseWriter, r *http.Request) {
	c, e := h.Service().Cancel(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, c)
}

func (h *HTTP) rearm(w http.ResponseWriter, r *http.Request) {
	c, e := h.Service().Rearm(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, c)
}
