package itemstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Handler serves the three /console/state routes at prefix (no trailing slash):
// GET prefix?prefix=, PUT prefix/{key...}, POST prefix/bulk. Responses use the console
// envelope {"data":...} and {"error":{code,message}}; row fields are the UI's ItemState
// (camelCase) plus version. Writes must be mounted behind operator.RequireOperator; an
// Authorization header (an agent) is refused on writes.
func (s *Store) Handler(prefix string) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET "+prefix, s.list)
	m.HandleFunc("PUT "+prefix+"/{key...}", s.put)
	m.HandleFunc("POST "+prefix+"/bulk", s.bulk)
	return m
}

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string, extra ...any) {
	e := map[string]any{"code": code, "message": msg}
	for i := 0; i+1 < len(extra); i += 2 {
		e[extra[i].(string)] = extra[i+1]
	}
	send(w, status, map[string]any{"error": e})
}

func wire(st State) map[string]any {
	m := map[string]any{"key": st.Key, "status": st.Status, "by": st.By, "updatedAt": st.UpdatedAt.Format(time.RFC3339), "version": st.Version}
	if st.SnoozeUtil != nil {
		m["snoozeUntil"] = st.SnoozeUtil.Format(time.RFC3339)
	}
	if st.WakeAt != nil {
		m["wokeAt"] = st.WakeAt.Format(time.RFC3339)
	}
	if st.Note != "" {
		m["note"] = st.Note
	}
	if st.Resolution != "" {
		m["resolution"] = st.Resolution
	}
	return m
}

func mapErr(w http.ResponseWriter, err error) {
	var c *ConflictError
	var inv *InvalidError
	switch {
	case errors.As(err, &c):
		fail(w, 409, "conflict", "The entry changed. Reload it.", "version", c.Current)
	case errors.As(err, &inv):
		fail(w, 400, "invalid_args", inv.Msg)
	default:
		fail(w, 500, "internal", "The state store is unavailable.")
	}
}

func (s *Store) list(w http.ResponseWriter, r *http.Request) {
	rows, err := s.List(r.Context(), r.URL.Query().Get("prefix"))
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, st := range rows {
		out = append(out, wire(st))
	}
	send(w, 200, map[string]any{"data": out})
}

type opBody struct {
	Type            string `json:"type"`
	Note            string `json:"note"`
	Until           string `json:"until"`
	Preset          string `json:"preset"`
	Resolution      string `json:"resolution"`
	ExpectedVersion *int   `json:"expected_version"`
}

func (b opBody) op() Op {
	return Op{Type: b.Type, Note: b.Note, Until: b.Until, Preset: b.Preset, Resolution: b.Resolution, ExpectedVersion: b.ExpectedVersion}
}

func decode(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		fail(w, 400, "invalid_args", "Body is too large or unreadable.")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		fail(w, 400, "invalid_args", "Send a JSON object with the documented fields only.")
		return false
	}
	return true
}

func refuseAgent(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		fail(w, 403, "requester_cannot_approve", "Item state is changed from the operator's browser session only.")
		return true
	}
	return false
}

func (s *Store) put(w http.ResponseWriter, r *http.Request) {
	if refuseAgent(w, r) {
		return
	}
	var b opBody
	if !decode(w, r, &b, 16<<10) {
		return
	}
	st, err := s.Apply(r.Context(), r.PathValue("key"), "operator", b.op())
	if err != nil {
		mapErr(w, err)
		return
	}
	send(w, 200, map[string]any{"data": wire(st)})
}

func (s *Store) bulk(w http.ResponseWriter, r *http.Request) {
	if refuseAgent(w, r) {
		return
	}
	var in struct {
		Ops []struct {
			Key string `json:"key"`
			opBody
		} `json:"ops"`
	}
	if !decode(w, r, &in, 128<<10) {
		return
	}
	if len(in.Ops) == 0 || len(in.Ops) > MaxBulk {
		fail(w, 400, "invalid_args", "Send 1 to 100 ops.")
		return
	}
	ops := make([]BulkOp, len(in.Ops))
	for i, o := range in.Ops {
		ops[i] = BulkOp{Key: o.Key, Op: o.op()}
	}
	results := []map[string]any{}
	for _, res := range s.Bulk(r.Context(), "operator", ops) {
		if res.Err == nil {
			results = append(results, map[string]any{"key": res.Key, "ok": true, "state": wire(*res.State)})
			continue
		}
		code, msg := "internal", "The state store is unavailable."
		var c *ConflictError
		var inv *InvalidError
		switch {
		case errors.As(res.Err, &c):
			code, msg = "conflict", "The entry changed. Reload it."
		case errors.As(res.Err, &inv):
			code, msg = "invalid_args", inv.Msg
		}
		results = append(results, map[string]any{"key": res.Key, "ok": false, "error": map[string]string{"code": code, "message": msg}})
	}
	send(w, 200, map[string]any{"data": map[string]any{"results": results}})
}
