package alertstore

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// StateCompat lets the console client keep using /console/state for alerts. Keys that start with "al_"
// are alert ids: PUT maps the client's item-state op onto an alert op (taking the current version, since
// the client sends none) and GET list adds one ItemState row per recent alert. Everything else, and the
// bulk route, passes to next unchanged. Alert writes follow the alert guard (same origin, no
// Authorization header, no operator elevation); next keeps its own guard.
func (a *API) StateCompat(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == prefix:
			a.stateList(w, r, next)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, prefix+"/"):
			key, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), prefix+"/"))
			if err == nil && strings.HasPrefix(key, "al_") {
				guard(true, func(w http.ResponseWriter, r *http.Request) { a.statePut(w, r, key) })(w, r)
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func stateFail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// ItemState maps an alert onto the console client's item-state row (epoch-ms numbers).
func ItemState(al alertsapi.Alert) map[string]any {
	m := map[string]any{"key": al.AlertID, "by": "operator", "updatedAt": al.UpdatedMS, "version": al.Version}
	switch al.Status {
	case "snoozed", "deferred":
		m["status"] = "snoozed"
		until := al.WakeAtMS
		if until == nil {
			until = al.SnoozeUntil
		}
		if until != nil {
			m["snoozeUntil"] = *until
		}
	case "dismissed", "settled":
		m["status"] = al.Status
	default:
		m["status"] = "unread"
		if al.Read {
			m["status"] = "read"
		}
	}
	if al.Note != "" {
		m["note"] = al.Note
	}
	if al.Resolution != "" {
		m["resolution"] = al.Resolution
	}
	return m
}

func (a *API) stateList(w http.ResponseWriter, r *http.Request, next http.Handler) {
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, r)
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &env) != nil {
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	rows := env.Data
	if rows == nil {
		rows = []json.RawMessage{}
	}
	prefix := r.URL.Query().Get("prefix")
	if strings.HasPrefix("al_", prefix) || strings.HasPrefix(prefix, "al_") {
		for _, view := range []string{"active", "history"} {
			alerts, _, err := a.Store.List(r.Context(), ListQuery{View: view, Limit: alertsapi.ListLimitMax})
			if err != nil {
				stateFail(w, 500, "internal", "The alert store is unavailable.")
				return
			}
			for _, al := range alerts {
				if !strings.HasPrefix(al.AlertID, prefix) {
					continue
				}
				b, _ := json.Marshal(ItemState(al))
				rows = append(rows, b)
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, map[string]any{"data": rows})
}

func (a *API) statePut(w http.ResponseWriter, r *http.Request, key string) {
	var b struct {
		Type       string          `json:"type"`
		Note       string          `json:"note"`
		Until      json.RawMessage `json:"until"`
		Preset     string          `json:"preset"`
		Resolution string          `json:"resolution"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&b) != nil {
		stateFail(w, 400, "invalid_args", "Send a JSON object with the documented fields only.")
		return
	}
	switch b.Type {
	case "read", "dismiss", "snooze", "settle", "reopen":
	default:
		stateFail(w, 400, "invalid_args", "Unsupported op for an alert.")
		return
	}
	op := Op{Op: b.Type, Preset: b.Preset, Note: b.Note, Resolution: b.Resolution}
	if len(b.Until) > 0 {
		var n int64
		var s string
		if json.Unmarshal(b.Until, &n) == nil {
			op.UntilMS = &n
		} else if json.Unmarshal(b.Until, &s) == nil {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				n = t.UnixMilli()
				op.UntilMS = &n
			}
		}
	}
	cur, err := a.Store.Get(r.Context(), key)
	if err != nil {
		a.stateErr(w, err)
		return
	}
	op.ExpectedVersion = &cur.Version
	al, err := a.Store.Apply(r.Context(), key, op)
	if err != nil {
		a.stateErr(w, err)
		return
	}
	write(w, 200, map[string]any{"data": ItemState(al)})
}

func (a *API) stateErr(w http.ResponseWriter, err error) {
	var inv *InvalidError
	var c *ConflictError
	switch {
	case errors.Is(err, ErrNotFound):
		stateFail(w, 404, "not_found", "No such alert.")
	case errors.As(err, &c):
		stateFail(w, 409, "conflict", "The entry changed. Reload it.")
	case errors.As(err, &inv):
		stateFail(w, 400, "invalid_args", inv.Msg)
	default:
		stateFail(w, 500, "internal", "The alert store is unavailable.")
	}
}
