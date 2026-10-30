package alertsapi

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed fixtures/*.json rulepack/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// DefaultRulePack and Conformance are the shared rule-pack data. The AL2 Go evaluator loads the pack
// and runs the conformance cases; the TS evaluator runs the same cases until it is deleted (AL8).
func DefaultRulePack() []byte { b, _ := assets.ReadFile("rulepack/default-pack.json"); return b }
func Conformance() []byte     { b, _ := assets.ReadFile("rulepack/conformance.json"); return b }

// fixtureAnchorMS is the "now" the fixtures were written at; the stub shifts every *_ms field so
// timers (snooze wake, escalation, expiry) look live.
const fixtureAnchorMS = 1793332800000

// Stub serves the fixtures with in-memory state, so a GUI can try read, dismiss, snooze, defer, settle,
// reopen, prefs and the stream. Nothing is durable; a restart resets it. Writes need a same-origin
// browser request (no operator elevation: alert ops are low impact, see design section 6).
//
// Controls: X-Stub-Status forces an error from any route except /stream. X-Stub-Scenario:
// /sources "degraded"; /stream "gap" (answers resync).
type Stub struct {
	mu       sync.Mutex
	delta    int64
	alerts   []Alert
	seq      int64
	prefs    Prefs
	migrated bool
}

func NewStub() *Stub {
	s := &Stub{delta: time.Now().UnixMilli() - fixtureAnchorMS}
	var f struct {
		Alerts []Alert `json:"alerts"`
		Counts Counts  `json:"counts"`
	}
	loadInto("alerts", s, &f)
	s.alerts, s.seq = f.Alerts, f.Counts.Seq
	loadInto("prefs", s, &s.prefs)
	return s
}

// loadInto reads a fixture, shifts its *_ms fields to the live clock and decodes it.
func loadInto(name string, s *Stub, v any) {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(s.rebase(b), v); err != nil {
		panic(name + ": " + err.Error())
	}
}

func (s *Stub) rebase(b []byte) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return b
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, e := range t {
				if n, ok := e.(json.Number); ok && strings.HasSuffix(k, "_ms") {
					if i, err := n.Int64(); err == nil && i > 1e12 {
						t[k] = i + s.delta
						continue
					}
				}
				t[k] = walk(e)
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		}
		return x
	}
	out, _ := json.Marshal(walk(v))
	return out
}

func (s *Stub) Handler() http.Handler {
	m := http.NewServeMux()
	h := map[string]http.HandlerFunc{
		"GET " + Prefix:                     s.list,
		"GET " + Prefix + "/counts":         s.counts,
		"GET " + Prefix + "/{id}":           s.detail,
		"POST " + Prefix + "/{id}/ops":      s.op,
		"POST " + Prefix + "/ops/bulk":      s.bulk,
		"GET " + Prefix + "/prefs":          s.getPrefs,
		"PUT " + Prefix + "/prefs":          s.putPrefs,
		"POST " + Prefix + "/prefs/migrate": s.migrate,
		"GET " + Prefix + "/stream":         s.stream,
		"GET " + Prefix + "/rules":          s.fixtureRoute("rules"),
		"GET " + Prefix + "/sources":        s.sources,
		"GET " + Prefix + "/openapi.yaml":   openapi,
		"GET " + StatePrefix:                s.stateList,
		"PUT " + StatePrefix + "/{key}":     s.statePut,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r.Pattern, " ")
		pat := method + " " + r.Base + path
		if path == "" {
			pat = method + " " + r.Base
		}
		f, ok := h[pat]
		if !ok {
			panic("alertsapi: no stub handler for " + pat)
		}
		m.HandleFunc(pat, s.guard(method != "GET", pat, f))
	}
	return m
}

func (s *Stub) guard(write bool, pat string, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Alerts-Stub", "1")
		if r.Header.Get("Authorization") != "" {
			fail(w, 403, CodeForbidden, "Alerts are available to the operator's browser session only.")
			return
		}
		if write && !sameOrigin(r) {
			fail(w, 403, CodeForbidden, "Writes must come from the Tachyon page (same origin).")
			return
		}
		if n, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); n >= 400 && n < 600 && !strings.HasSuffix(pat, "/stream") {
			fail(w, n, map[int]string{400: CodeInvalid, 403: CodeForbidden, 404: CodeNotFound, 409: CodeVersionConflict, 429: CodeRateLimited, 503: CodeUnavailable}[n], "Forced by X-Stub-Status.")
			return
		}
		f(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
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

func openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(OpenAPI())
}

func (s *Stub) fixtureRoute(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, _ := assets.ReadFile("fixtures/" + name + ".json")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.rebase(b))
	}
}

func (s *Stub) sources(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Stub-Scenario") == "degraded" {
		s.fixtureRoute("sources_degraded")(w, r)
		return
	}
	s.fixtureRoute("sources")(w, r)
}

func (s *Stub) now() int64 { return time.Now().UnixMilli() }

func (s *Stub) countsLocked() Counts {
	c := Counts{BySeverity: map[string]int{}, Seq: s.seq}
	for _, sv := range Severities {
		c.BySeverity[sv] = 0
	}
	for _, a := range s.alerts {
		if a.Status != "active" {
			continue
		}
		c.Active++
		c.BySeverity[a.Severity]++
		if !a.Read {
			c.Unread++
			if a.NeedsYou {
				c.NeedsYou++
			}
		}
	}
	return c
}

func (s *Stub) counts(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	write(w, 200, s.countsLocked())
}

func (s *Stub) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if !slices.Contains([]string{"view", "needs_you", "min_severity", "source", "q", "limit", "cursor"}, k) {
			fail(w, 400, CodeInvalid, "Unknown parameter "+strconv.Quote(k)+".")
			return
		}
	}
	view := q.Get("view")
	if view == "" {
		view = "active"
	}
	if view != "active" && view != "history" {
		fail(w, 400, CodeInvalid, "view must be active or history.")
		return
	}
	if v := q.Get("needs_you"); v != "" && v != "0" && v != "1" {
		fail(w, 400, CodeInvalid, "needs_you must be 0 or 1.")
		return
	}
	if v := q.Get("min_severity"); v != "" && idx(v) < 0 {
		fail(w, 400, CodeInvalid, "min_severity must be one of "+strings.Join(Severities, ", ")+".")
		return
	}
	limit := ListLimitDef
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > ListLimitMax {
			fail(w, 400, CodeInvalid, "limit must be 1..200.")
			return
		}
		limit = n
	}
	offset := 0
	if v := q.Get("cursor"); v != "" {
		b, err := base64.RawURLEncoding.DecodeString(v)
		n, err2 := strconv.Atoi(string(b))
		if err != nil || err2 != nil || n < 0 {
			fail(w, 400, CodeInvalid, "cursor is not valid.")
			return
		}
		offset = n
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Alert
	for _, a := range s.alerts {
		if (view == "active") != (a.Status == "active") {
			continue
		}
		if q.Get("needs_you") == "1" && !a.NeedsYou {
			continue
		}
		if q.Get("needs_you") == "0" && a.NeedsYou {
			continue
		}
		if m := q.Get("min_severity"); m != "" && idx(a.Severity) < idx(m) {
			continue
		}
		if v := q.Get("source"); v != "" && !strings.EqualFold(a.Source, v) {
			continue
		}
		if v := strings.ToLower(q.Get("q")); v != "" && !strings.Contains(strings.ToLower(a.Title+" "+a.Summary+" "+a.Source), v) {
			continue
		}
		out = append(out, a)
	}
	// Needs-you first, then newest.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NeedsYou != out[j].NeedsYou {
			return out[i].NeedsYou
		}
		return out[i].LastAtMS > out[j].LastAtMS
	})
	var next any
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	if len(out) > limit {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
		out = out[:limit]
	}
	if out == nil {
		out = []Alert{}
	}
	write(w, 200, map[string]any{"alerts": out, "next": next, "counts": s.countsLocked(), "seq": s.seq, "server_now_ms": s.now()})
}

type detailFixture struct {
	Timeline  []json.RawMessage `json:"timeline"`
	Linked    []Subject         `json:"linked"`
	Technical json.RawMessage   `json:"technical"`
	Actions   []json.RawMessage `json:"actions"`
	Approval  json.RawMessage   `json:"approval"`
}

func (s *Stub) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(id)
	if a == nil {
		fail(w, 404, CodeNotFound, "No alert "+strconv.Quote(id)+".")
		return
	}
	var all map[string]detailFixture
	loadInto("details", s, &all)
	d, ok := all[id]
	out := map[string]any{"alert": a, "server_now_ms": s.now()}
	if !ok {
		// An alert with no shared subject or correlation id: just itself, never an empty "What happened".
		out["timeline"] = []any{map[string]any{"at_ms": a.LastAtMS, "label": "This alert", "ref": nil}}
		out["linked"] = a.Subjects
		out["technical"] = map[string]any{"fields": []any{}}
		act := []any{}
		if a.Primary != nil {
			act = append(act, map[string]any{"id": "open", "label": a.Primary.Label, "kind": "link"})
		}
		out["actions"] = act
		out["approval"] = nil
		write(w, 200, out)
		return
	}
	if len(d.Timeline) == 0 {
		d.Timeline = []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"at_ms":%d,"label":"This alert","ref":null}`, a.LastAtMS))}
	}
	if len(d.Timeline) > TimelineMax {
		d.Timeline = d.Timeline[:TimelineMax]
	}
	if d.Approval == nil {
		d.Approval = json.RawMessage("null")
	}
	out["timeline"], out["linked"], out["technical"], out["actions"], out["approval"] = d.Timeline, d.Linked, d.Technical, d.Actions, d.Approval
	write(w, 200, out)
}

func (s *Stub) find(id string) *Alert {
	for i := range s.alerts {
		if s.alerts[i].AlertID == id {
			return &s.alerts[i]
		}
	}
	return nil
}

type opBody struct {
	Op              string    `json:"op"`
	Preset          string    `json:"preset"`
	UntilMS         *int64    `json:"until_ms"`
	UntilRefChange  *UntilRef `json:"until_ref_change"`
	Note            string    `json:"note"`
	Resolution      string    `json:"resolution"`
	ExpectedVersion *int64    `json:"expected_version"`
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, CodeInvalid, "Body is not valid JSON for this route: "+err.Error())
		return false
	}
	return true
}

func (s *Stub) op(w http.ResponseWriter, r *http.Request) {
	var b opBody
	if !decode(w, r, &b) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(r.PathValue("id"))
	if a == nil {
		fail(w, 404, CodeNotFound, "No alert "+strconv.Quote(r.PathValue("id"))+".")
		return
	}
	if b.ExpectedVersion == nil {
		fail(w, 400, CodeInvalid, "expected_version is required.")
		return
	}
	if *b.ExpectedVersion != a.Version {
		write(w, 409, map[string]any{"code": CodeVersionConflict, "message": "This alert changed on another device.", "current": a})
		return
	}
	if msg := s.apply(a, b); msg != "" {
		fail(w, 400, CodeInvalid, msg)
		return
	}
	write(w, 200, a)
}

// apply validates and performs one op on a. Caller holds the lock and has checked the version.
func (s *Stub) apply(a *Alert, b opBody) string {
	now := s.now()
	if len(b.Note) > NoteMax {
		return "note is too long."
	}
	switch b.Op {
	case "read":
		a.Read = true
	case "unread":
		a.Read = false
	case "dismiss":
		a.Status, a.Read, a.Note, a.ResolvedBy = "dismissed", true, b.Note, "operator"
		a.SnoozeUntil, a.WakeAtMS, a.Defer = nil, nil, nil
	case "snooze":
		var until int64
		switch {
		case b.Preset != "" && b.UntilMS != nil:
			return "Give preset or until_ms, not both."
		case b.Preset != "":
			d := map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour, "tomorrow": 18 * time.Hour}[b.Preset]
			if d == 0 {
				return "preset must be one of " + strings.Join(SnoozePresets, ", ") + "."
			}
			until = now + d.Milliseconds()
		case b.UntilMS != nil:
			until = *b.UntilMS
			if until <= now || until > now+30*24*time.Hour.Milliseconds() {
				return "until_ms must be in the future and within 30 days."
			}
		default:
			return "snooze needs preset or until_ms."
		}
		a.Status, a.Read, a.Note = "snoozed", true, b.Note
		a.SnoozeUntil, a.WakeAtMS, a.Defer = &until, &until, nil
	case "defer":
		if b.UntilMS == nil && b.UntilRefChange == nil {
			return "defer needs until_ms or until_ref_change."
		}
		if b.UntilMS != nil && *b.UntilMS <= now {
			return "until_ms must be in the future."
		}
		d := &Defer{UntilMS: b.UntilMS, UntilRefChange: b.UntilRefChange}
		if d.UntilRefChange != nil && (d.UntilRefChange.Kind == "" || d.UntilRefChange.Ref == "") {
			return "until_ref_change needs kind and ref."
		}
		a.Status, a.Read, a.Note, a.Defer = "deferred", true, b.Note, d
		a.SnoozeUntil, a.WakeAtMS = nil, b.UntilMS
	case "settle":
		a.Status, a.Read, a.Note, a.ResolvedBy = "settled", true, b.Note, "operator"
		a.Resolution = strings.TrimSpace(b.Resolution)
		if a.Resolution == "" {
			a.Resolution = "Handled"
		}
		a.SnoozeUntil, a.WakeAtMS, a.Defer = nil, nil, nil
	case "reopen":
		a.Status, a.Read, a.Resolution, a.ResolvedBy = "active", false, "", ""
		a.SnoozeUntil, a.WakeAtMS, a.Defer = nil, nil, nil
	default:
		return "op must be one of " + strings.Join(Ops, ", ") + "."
	}
	s.seq++
	a.Seq, a.Version, a.UpdatedMS = s.seq, a.Version+1, now
	return ""
}

func (s *Stub) bulk(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Op          string   `json:"op"`
		IDs         []string `json:"ids"`
		Scope       string   `json:"scope"`
		ExpectedSeq *int64   `json:"expected_seq"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !slices.Contains(BulkOps, b.Op) {
		fail(w, 400, CodeInvalid, "bulk op must be read or dismiss.")
		return
	}
	if (len(b.IDs) == 0) == (b.Scope == "") || (b.Scope != "" && b.Scope != "all_active") || len(b.IDs) > ListLimitMax {
		fail(w, 400, CodeInvalid, "Give ids (up to 200) or scope all_active.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.ExpectedSeq != nil && *b.ExpectedSeq != s.seq {
		fail(w, 409, CodeSeqConflict, "Alerts changed since you looked; refresh and try again.")
		return
	}
	updated, skipped := []string{}, []string{}
	for i := range s.alerts {
		a := &s.alerts[i]
		pick := a.Status == "active" && b.Scope == "all_active" || slices.Contains(b.IDs, a.AlertID)
		if !pick {
			continue
		}
		if b.Op == "read" && a.Read || b.Op == "dismiss" && a.Status != "active" {
			skipped = append(skipped, a.AlertID)
			continue
		}
		s.apply(a, opBody{Op: b.Op})
		updated = append(updated, a.AlertID)
	}
	write(w, 200, map[string]any{"updated": updated, "skipped": skipped, "counts": s.countsLocked(), "seq": s.seq})
}

func (s *Stub) getPrefs(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	write(w, 200, s.prefs)
}

func (s *Stub) putPrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs
	if !decode(w, r, &p) {
		return
	}
	if p.MutedSources == nil {
		p.MutedSources = []string{}
	}
	if msg := ValidPrefs(p); msg != "" {
		fail(w, 400, CodeInvalid, msg)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs, s.migrated = p, true
	write(w, 200, p)
}

// migrate accepts the old localStorage value once. If server prefs already exist (saved or migrated),
// nothing is applied and the current prefs come back with applied=false: the call is idempotent.
func (s *Stub) migrate(w http.ResponseWriter, r *http.Request) {
	var l LegacyPrefs
	if !decode(w, r, &l) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.migrated {
		s.prefs, s.migrated = MigratePrefs(l, s.prefs.QuietHours.TZ), true
		write(w, 200, map[string]any{"prefs": s.prefs, "applied": true})
		return
	}
	write(w, 200, map[string]any{"prefs": s.prefs, "applied": false})
}

// ItemState is the shape the existing client StateStore reads and writes (camelCase, epoch ms).
type ItemState struct {
	Key         string `json:"key"`
	Status      string `json:"status"`
	SnoozeUntil *int64 `json:"snoozeUntil,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	Note        string `json:"note,omitempty"`
	By          string `json:"by,omitempty"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// ToItemState maps an alert onto the legacy StateStore status vocabulary. Deferred shows as snoozed.
func ToItemState(a Alert) ItemState {
	st := ItemState{Key: a.AlertID, Status: "unread", Resolution: a.Resolution, Note: a.Note, By: a.ResolvedBy, UpdatedAt: a.UpdatedMS}
	switch a.Status {
	case "active":
		if a.Read {
			st.Status = "read"
		}
	case "snoozed", "deferred":
		st.Status, st.SnoozeUntil = "snoozed", a.WakeAtMS
	default:
		st.Status = a.Status
	}
	return st
}

func (s *Stub) stateList(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []ItemState{}
	for _, a := range s.alerts {
		if strings.HasPrefix(a.AlertID, prefix) {
			items = append(items, ToItemState(a))
		}
	}
	write(w, 200, map[string]any{"items": items})
}

func (s *Stub) statePut(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Type       string `json:"type"`
		Until      *int64 `json:"until"`
		Note       string `json:"note"`
		Resolution string `json:"resolution"`
	}
	if !decode(w, r, &b) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.find(r.PathValue("key"))
	if a == nil {
		fail(w, 404, CodeNotFound, "No alert "+strconv.Quote(r.PathValue("key"))+".")
		return
	}
	if msg := s.apply(a, opBody{Op: b.Type, UntilMS: b.Until, Note: b.Note, Resolution: b.Resolution}); msg != "" {
		fail(w, 400, CodeInvalid, msg)
		return
	}
	write(w, 200, ToItemState(*a))
}

// stream replays stream.json after Last-Event-ID (or ?after=), then idles with a heartbeat.
// ids are alert seq values; the counts and prefs frames share the id of the change that caused them.
func (s *Stub) stream(w http.ResponseWriter, r *http.Request) {
	var f struct {
		Events []map[string]any `json:"events"`
	}
	loadInto("stream", s, &f)
	var last int64
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, CodeInvalid, "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "internal", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Tachyon-Alerts-Stub", "1")
	w.WriteHeader(200)
	top := int64(f.Events[len(f.Events)-1]["id"].(float64))
	if r.Header.Get("X-Stub-Scenario") == "gap" || last > top {
		body, _ := json.Marshal(map[string]any{"type": "resync", "at_ms": s.now(), "reason": "gap"})
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", top, body)
	} else {
		for i, e := range f.Events {
			id := int64(e["id"].(float64))
			// Frames sharing one id (alert.state then counts) replay together: skip only ids <= last.
			if id <= last {
				continue
			}
			e["at_ms"] = s.now() + int64(i)
			body, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", id, body)
		}
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
