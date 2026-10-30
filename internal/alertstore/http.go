package alertstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// API serves the AL1 routes under alertsapi.Prefix: list, counts, detail, ops, bulk and prefs. The
// stream, rules and sources routes belong to AL4/AL2. Responses are flat JSON with {code,message}
// errors, like the contract. Writes need a same-origin browser request (alert ops are low impact, so no
// operator elevation) and refuse an Authorization header: agents have no alert surface.
type API struct {
	Store *Store
	Prefs PrefsStore
	// TZ is the default quiet-hours zone for fresh prefs.
	TZ string
}

// Routes are the patterns this handler serves.
func (a *API) Routes() []string {
	return []string{
		"GET " + alertsapi.Prefix, "GET " + alertsapi.Prefix + "/counts", "GET " + alertsapi.Prefix + "/{id}",
		"POST " + alertsapi.Prefix + "/{id}/ops", "POST " + alertsapi.Prefix + "/ops/bulk",
		"GET " + alertsapi.Prefix + "/prefs", "PUT " + alertsapi.Prefix + "/prefs", "POST " + alertsapi.Prefix + "/prefs/migrate",
	}
}

func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	h := map[string]http.HandlerFunc{
		"GET " + alertsapi.Prefix:                     a.list,
		"GET " + alertsapi.Prefix + "/counts":         a.counts,
		"GET " + alertsapi.Prefix + "/{id}":           a.detail,
		"POST " + alertsapi.Prefix + "/{id}/ops":      a.op,
		"POST " + alertsapi.Prefix + "/ops/bulk":      a.bulk,
		"GET " + alertsapi.Prefix + "/prefs":          a.getPrefs,
		"PUT " + alertsapi.Prefix + "/prefs":          a.putPrefs,
		"POST " + alertsapi.Prefix + "/prefs/migrate": a.migrate,
	}
	for _, pat := range a.Routes() {
		m.HandleFunc(pat, guard(pat[0] != 'G', h[pat]))
	}
	return m
}

func guard(write bool, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Authorization") != "" {
			fail(w, 403, alertsapi.CodeForbidden, "Alerts are available to the operator's browser session only.")
			return
		}
		if write && !SameOrigin(r) {
			fail(w, 403, alertsapi.CodeForbidden, "Writes must come from the Tachyon page (same origin).")
			return
		}
		f(w, r)
	}
}

// SameOrigin reports a browser request from the page's own origin.
func SameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host == r.Host && (o.Scheme == "https" || o.Scheme == "http") && o.User == nil && o.Path == "" && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func serverErr(w http.ResponseWriter) {
	fail(w, 500, alertsapi.CodeUnavailable, "The alert store is unavailable.")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, alertsapi.CodeInvalid, "Send a JSON object with the documented fields only.")
		return false
	}
	return true
}

func (a *API) mapErr(w http.ResponseWriter, err error) {
	var inv *InvalidError
	var c *ConflictError
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, alertsapi.CodeNotFound, "No such alert.")
	case errors.Is(err, ErrSeqConflict):
		fail(w, 409, alertsapi.CodeSeqConflict, "Alerts changed since you looked; refresh and try again.")
	case errors.As(err, &c):
		write(w, 409, map[string]any{"code": alertsapi.CodeVersionConflict, "message": "This alert changed on another device.", "current": c.Current})
	case errors.As(err, &inv):
		fail(w, 400, alertsapi.CodeInvalid, inv.Msg)
	default:
		serverErr(w)
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if !slices.Contains([]string{"view", "needs_you", "min_severity", "source", "q", "limit", "cursor"}, k) {
			fail(w, 400, alertsapi.CodeInvalid, "Unknown parameter "+strconv.Quote(k)+".")
			return
		}
	}
	lq := ListQuery{View: q.Get("view"), MinSeverity: q.Get("min_severity"), Source: q.Get("source"), Q: q.Get("q"), Cursor: q.Get("cursor")}
	if lq.View == "" {
		lq.View = "active"
	}
	if lq.View != "active" && lq.View != "history" {
		fail(w, 400, alertsapi.CodeInvalid, "view must be active or history.")
		return
	}
	switch q.Get("needs_you") {
	case "":
	case "0", "1":
		v := q.Get("needs_you") == "1"
		lq.NeedsYou = &v
	default:
		fail(w, 400, alertsapi.CodeInvalid, "needs_you must be 0 or 1.")
		return
	}
	if lq.MinSeverity != "" && !slices.Contains(alertsapi.Severities, lq.MinSeverity) {
		fail(w, 400, alertsapi.CodeInvalid, "min_severity is not a known severity.")
		return
	}
	if len(lq.Q) > 200 || len(lq.Source) > 64 {
		fail(w, 400, alertsapi.CodeInvalid, "q or source is too long.")
		return
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > alertsapi.ListLimitMax {
			fail(w, 400, alertsapi.CodeInvalid, "limit must be 1..200.")
			return
		}
		lq.Limit = n
	}
	alerts, next, err := a.Store.List(r.Context(), lq)
	if err != nil {
		a.mapErr(w, err)
		return
	}
	counts, err := a.Store.Counts(r.Context())
	if err != nil {
		serverErr(w)
		return
	}
	var nx any
	if next != "" {
		nx = next
	}
	write(w, 200, map[string]any{"alerts": alerts, "next": nx, "counts": counts, "seq": counts.Seq, "server_now_ms": time.Now().UnixMilli()})
}

func (a *API) counts(w http.ResponseWriter, r *http.Request) {
	c, err := a.Store.Counts(r.Context())
	if err != nil {
		serverErr(w)
		return
	}
	write(w, 200, c)
}

func (a *API) detail(w http.ResponseWriter, r *http.Request) {
	d, err := a.Store.Detail(r.Context(), r.PathValue("id"))
	if err != nil {
		a.mapErr(w, err)
		return
	}
	write(w, 200, d)
}

type opBody struct {
	Op              string              `json:"op"`
	Preset          string              `json:"preset"`
	UntilMS         *int64              `json:"until_ms"`
	UntilRefChange  *alertsapi.UntilRef `json:"until_ref_change"`
	Note            string              `json:"note"`
	Resolution      string              `json:"resolution"`
	ExpectedVersion *int64              `json:"expected_version"`
}

func (a *API) op(w http.ResponseWriter, r *http.Request) {
	var b opBody
	if !decode(w, r, &b) {
		return
	}
	al, err := a.Store.Apply(r.Context(), r.PathValue("id"), Op(b))
	if err != nil {
		a.mapErr(w, err)
		return
	}
	write(w, 200, al)
}

func (a *API) bulk(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Op          string   `json:"op"`
		IDs         []string `json:"ids"`
		Scope       string   `json:"scope"`
		ExpectedSeq *int64   `json:"expected_seq"`
	}
	if !decode(w, r, &b) {
		return
	}
	res, err := a.Store.Bulk(r.Context(), b.Op, b.IDs, b.Scope, b.ExpectedSeq)
	if err != nil {
		a.mapErr(w, err)
		return
	}
	c, err := a.Store.Counts(r.Context())
	if err != nil {
		serverErr(w)
		return
	}
	write(w, 200, map[string]any{"updated": res.Updated, "skipped": res.Skipped, "counts": c, "seq": c.Seq})
}

func (a *API) defaults() alertsapi.Prefs {
	tz := a.TZ
	if tz == "" || tz == "Local" {
		tz = "UTC"
	}
	return alertsapi.DefaultPrefs(tz)
}

func (a *API) current(ctx context.Context) (alertsapi.Prefs, bool, error) {
	p, err := a.Prefs.Get(ctx)
	if err != nil {
		return alertsapi.Prefs{}, false, err
	}
	if p == nil {
		return a.defaults(), false, nil
	}
	return *p, true, nil
}

func (a *API) getPrefs(w http.ResponseWriter, r *http.Request) {
	p, _, err := a.current(r.Context())
	if err != nil {
		serverErr(w)
		return
	}
	write(w, 200, p)
}

func (a *API) putPrefs(w http.ResponseWriter, r *http.Request) {
	var p alertsapi.Prefs
	if !decode(w, r, &p) {
		return
	}
	if p.MutedSources == nil {
		p.MutedSources = []string{}
	}
	if msg := alertsapi.ValidPrefs(p); msg != "" {
		fail(w, 400, alertsapi.CodeInvalid, msg)
		return
	}
	if err := a.Prefs.Put(r.Context(), p); err != nil {
		serverErr(w)
		return
	}
	write(w, 200, p)
}

// migrate accepts the old localStorage value once; if server prefs exist, nothing is applied.
func (a *API) migrate(w http.ResponseWriter, r *http.Request) {
	var l alertsapi.LegacyPrefs
	if !decode(w, r, &l) {
		return
	}
	cur, exists, err := a.current(r.Context())
	if err != nil {
		serverErr(w)
		return
	}
	if exists {
		write(w, 200, map[string]any{"prefs": cur, "applied": false})
		return
	}
	p := alertsapi.MigratePrefs(l, cur.QuietHours.TZ)
	if msg := alertsapi.ValidPrefs(p); msg != "" {
		fail(w, 400, alertsapi.CodeInvalid, msg)
		return
	}
	if err := a.Prefs.Put(r.Context(), p); err != nil {
		serverErr(w)
		return
	}
	write(w, 200, map[string]any{"prefs": p, "applied": true})
}
