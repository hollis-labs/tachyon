package fileview

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// Mount registers GET /settings, PUT /settings and GET /roots under fileviewapi.Prefix, one pattern each (the
// /api/plugins/ catch-all is unguarded). reads wraps the two GETs (RequireOperatorReads) and writes wraps the
// PUT (RequireOperator); either may be nil in tests.
func (s *Service) Mount(mux *http.ServeMux, reads, writes func(http.Handler) http.Handler) {
	id := func(h http.Handler) http.Handler { return h }
	if reads == nil {
		reads = id
	}
	if writes == nil {
		writes = id
	}
	mux.Handle("GET "+fileviewapi.Prefix+"/settings", reads(http.HandlerFunc(s.getSettings)))
	mux.Handle("PUT "+fileviewapi.Prefix+"/settings", writes(http.HandlerFunc(s.putSettings)))
	mux.Handle("GET "+fileviewapi.Prefix+"/roots", reads(http.HandlerFunc(s.getRoots)))
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

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.Load(r.Context())
	if err != nil {
		fail(w, 500, "internal", "The request could not be completed.")
		return
	}
	reply(w, 200, set)
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, fileviewapi.MaxSettingsBody))
	if err != nil {
		fail(w, 400, "invalid", "Body must be a JSON object of at most 16 KiB.")
		return
	}
	var u Update
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&u) != nil || dec.More() {
		fail(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return
	}
	set, err := s.Apply(r.Context(), u)
	var ve *Error
	switch {
	case errors.As(err, &ve):
		fail(w, ve.Status, ve.Code, ve.Msg)
	case err != nil:
		fail(w, 500, "internal", "The request could not be completed.")
	default:
		reply(w, 200, set)
	}
}

func (s *Service) getRoots(w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r.Context())
	if err != nil {
		fail(w, 500, "internal", "The request could not be completed.")
		return
	}
	reply(w, 200, v)
}
