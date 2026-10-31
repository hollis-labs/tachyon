package agentsserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsapi"
	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/agentsderive"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Server serves the sessions API. Collector supplies the status block and is the only writer.
type Server struct {
	DB        *localstore.Store
	Collector *agentscollector.Collector
	Now       func() time.Time
	// Assigner supplies Session.assignment (A5); nil leaves it null.
	Assigner Assigner
	// Sender delivers nudges as Tether mail (A6); nil turns the nudge capability off.
	Sender Sender
	// Interrupter and Restarter turn the interrupt and restart capabilities on (A6); nil leaves them off.
	Interrupter Interrupter
	Restarter   Restarter

	hub   hub
	skips sync.Map // restart request_id -> chan struct{} closed to skip the handoff wait
}

// Assigner joins a session's task to its Planner theme.
type Assigner interface {
	Assignment(ctx context.Context, sessionID, taskRef string) any
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Routes lists what Mount registers, for the operator-refusal test.
var Routes = []string{
	"GET /sessions", "GET /sessions/{id}", "GET /sessions/{id}/events", "GET /sessions/{id}/context-series",
	"GET /sessions/{id}/tokens", "GET /sessions/{id}/history", "GET /stream", "GET /settings", "PUT /settings",
	"POST /sessions/{id}/nudge", "POST /sessions/{id}/restart", "GET /controls/{request_id}", "POST /controls/{request_id}/skip",
}

// Mount registers each route on the mux under agentsapi.Prefix, wrapped by guard (the server passes
// operator.RequireOperator: GETs open, PUT needs an operator session). Routes are mounted one by
// one so the control routes stay with their own owner and nothing falls into the unguarded catch-all.
func (s *Server) Mount(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	hs := map[string]http.HandlerFunc{
		"GET /sessions": s.list, "GET /sessions/{id}": s.get, "GET /sessions/{id}/events": s.events,
		"GET /sessions/{id}/context-series": s.contextSeries, "GET /sessions/{id}/tokens": s.tokens,
		"GET /sessions/{id}/history": s.history, "GET /stream": s.stream, "GET /settings": s.getSettings, "PUT /settings": s.putSettings,
		"POST /sessions/{id}/nudge": s.nudge, "POST /sessions/{id}/restart": s.restart,
		"GET /controls/{request_id}": s.getControl, "POST /controls/{request_id}/skip": s.skipControl,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		mux.Handle(method+" "+agentsapi.Prefix+path, guard(hs[r]))
	}
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

func internal(w http.ResponseWriter) { fail(w, 500, "internal", "The request could not be completed.") }

func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		fail(w, 400, "invalid", "Body must be a JSON object.")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		fail(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return false
	}
	return true
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func sessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !idRE.MatchString(id) {
		fail(w, 404, "not_found", "No such session.")
		return "", false
	}
	return id, true
}

// one loads and builds a session; ok=false has already answered.
func (s *Server) one(w http.ResponseWriter, r *http.Request) (Session, row, bool) {
	id, ok := sessionID(w, r)
	if !ok {
		return Session{}, row{}, false
	}
	rs, err := s.rows(r.Context(), ` WHERE session_id=?`, id)
	if err != nil {
		internal(w)
		return Session{}, row{}, false
	}
	if len(rs) == 0 {
		fail(w, 404, "not_found", "No such session.")
		return Session{}, row{}, false
	}
	now := s.now()
	ex, err := s.recent(r.Context(), now)
	if err != nil {
		internal(w)
		return Session{}, row{}, false
	}
	return s.withAssignment(r.Context(), build(rs[0], ex[id], now, s.staleAfter(r.Context())), rs[0]), rs[0], true
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	if sess, _, ok := s.one(w, r); ok {
		reply(w, 200, sess)
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	switch state {
	case "", agentsderive.Working, agentsderive.Waiting, agentsderive.Idle, agentsderive.Stale, agentsderive.Unknown:
	default:
		fail(w, 400, "invalid", "state must be working, waiting, idle, stale or unknown.")
		return
	}
	group := q.Get("group")
	switch group {
	case "", "none", "workroot", "agent":
	default:
		fail(w, 400, "invalid", "group must be workroot, agent or none.")
		return
	}
	ended := q.Get("include_ended")
	if ended != "" && ended != "0" && ended != "1" {
		fail(w, 400, "invalid", "include_ended must be 0 or 1.")
		return
	}
	all, err := s.all(r.Context())
	if err != nil {
		internal(w)
		return
	}
	text := strings.ToLower(strings.TrimSpace(q.Get("q")))
	out := make([]Session, 0, len(all))
	for _, sess := range all {
		if sess.State == StateEnded && ended != "1" {
			continue
		}
		if state != "" && sess.State != state {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(sess.ID+" "+sess.Agent.Name+" "+deref(sess.Agent.URN)+" "+sess.Workroot+" "+sess.Provider), text) {
			continue
		}
		out = append(out, sess)
	}
	sortSessions(out, group)
	st, err := s.Collector.Status(r.Context())
	if err != nil {
		internal(w)
		return
	}
	// The summary counts every live session, ignoring the filters.
	reply(w, 200, map[string]any{"sessions": out, "summary": Summary(all), "collector": st})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- timeline ----

var filterTypes = map[string][]string{
	"all": nil, "": nil, "tools": {"tool"}, "messages": {"message"}, "approvals": {"approval"},
	"lifecycle": {"lifecycle", "compaction", "clear"}, "errors": {"error"},
}

type evRow struct {
	id, seq, atNS    int64
	typ, actor, text string
}

// humanize turns a stored derived summary ("tool.call.finished: Read") into timeline text, and
// reports hidden=true for the first half of a tool pair (the finished event represents the call).
func humanize(sum string) (text string, hidden bool) {
	switch {
	case strings.HasPrefix(sum, "tool.call.started"):
		return "", true
	case strings.HasPrefix(sum, "tool.call"):
		if _, tool, ok := strings.Cut(sum, ": "); ok && tool != "" {
			return agentsderive.PastLine(tool), false
		}
		return "Used a tool", false
	case strings.HasPrefix(sum, "session.work_state_changed: "):
		_, to, _ := strings.Cut(strings.TrimPrefix(sum, "session.work_state_changed: "), " -> ")
		switch {
		case to == "idle":
			return "Went idle", false
		case to == "processing" || to == "working" || to == "running":
			return "Started working", false
		case strings.HasPrefix(to, "waiting"):
			return "Waiting on you", false
		}
		return "Work state " + to, false
	case strings.HasPrefix(sum, "session.state_changed: "):
		from, to, _ := strings.Cut(strings.TrimPrefix(sum, "session.state_changed: "), " -> ")
		if from == to {
			return "", true // a no-op transition
		}
		return "Session " + to, false
	case !strings.Contains(sum, ": "):
		if i := strings.Index(sum, "."); i > 0 && !strings.Contains(sum, " ") {
			w := strings.ReplaceAll(strings.TrimPrefix(sum[i+1:], "."), "_", " ")
			if sum[:i] != "session" {
				w = sum[:i] + " " + w
			}
			return strings.ToUpper(w[:1]) + w[1:], false
		}
	}
	return sum, false
}

func toolOf(text string) string {
	return strings.ToLower(text)
}

type timelineEvent struct {
	ID        string  `json:"id"`
	Seq       int64   `json:"seq"`
	SessionID string  `json:"session_id"`
	Type      string  `json:"type"`
	At        int64   `json:"at"`
	Actor     string  `json:"actor"`
	Summary   string  `json:"summary"`
	Cite      []any   `json:"cite"`
	Card      *string `json:"card"`
	Payload   *string `json:"payload"` // always null: raw payloads are never stored
}

type toolRun struct {
	Type      string          `json:"type"`
	RunID     string          `json:"run_id"`
	SessionID string          `json:"session_id"`
	Calls     int             `json:"calls"`
	Summary   string          `json:"summary"`
	StartedAt int64           `json:"started_at"`
	EndedAt   int64           `json:"ended_at"`
	Events    []timelineEvent `json:"events"`
}

type gapOut struct {
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Reason     string `json:"reason"`
	FromSeq    int64  `json:"from_seq"`
	ToSeq      int64  `json:"to_seq"`
	Backfilled bool   `json:"backfilled"`
}

// gapReason maps the collector's reasons onto the contract's enum.
func gapReason(r string) string {
	switch r {
	case "tachyon_offline", "tether_unreachable", "purged", "backfill_limit", "restart":
		return r
	case "ahead", "event_id_invariant":
		return "restart"
	case "too_large", "backfill_in_progress":
		return "backfill_limit"
	}
	return "tether_unreachable"
}

func intParam(w http.ResponseWriter, q map[string][]string, name string, min, max int64) (int64, bool, bool) {
	v := ""
	if vs := q[name]; len(vs) > 0 {
		v = vs[0]
	}
	if v == "" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < min || n > max {
		fail(w, 400, "invalid", fmt.Sprintf("%s must be an integer from %d to %d.", name, min, max))
		return 0, false, false
	}
	return n, true, true
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var exists int
	if err := s.DB.Reader.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM agents_sessions WHERE session_id=?`, id).Scan(&exists); err != nil {
		internal(w)
		return
	}
	if exists == 0 {
		fail(w, 404, "not_found", "No such session.")
		return
	}
	q := r.URL.Query()
	types, known := filterTypes[q.Get("filter")]
	if !known {
		fail(w, 400, "invalid", "filter must be all, tools, messages, approvals, lifecycle or errors.")
		return
	}
	before, _, ok := intParam(w, q, "before_seq", 0, 1<<62)
	if !ok {
		return
	}
	limit, has, ok := intParam(w, q, "limit", 1, 100)
	if !ok {
		return
	}
	if !has {
		limit = 50
	}
	grp := q.Get("group_runs")
	if grp != "" && grp != "0" && grp != "1" {
		fail(w, 400, "invalid", "group_runs must be 0 or 1.")
		return
	}
	sqlq := `SELECT id,tether_seq,at_ns,type,actor,summary FROM agents_events WHERE session_id=? AND summary NOT LIKE 'tool.call.started%'`
	args := []any{id}
	if before > 0 {
		sqlq += ` AND tether_seq<?`
		args = append(args, before)
	}
	if len(types) > 0 {
		sqlq += ` AND type IN (?` + strings.Repeat(`,?`, len(types)-1) + `)`
		for _, t := range types {
			args = append(args, t)
		}
	}
	if t := strings.ToLower(strings.TrimSpace(q.Get("q"))); t != "" {
		sqlq += ` AND LOWER(summary) LIKE ? ESCAPE '\'`
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t)+"%")
	}
	sqlq += ` ORDER BY tether_seq DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rs, err := s.DB.Reader.QueryContext(r.Context(), sqlq, args...)
	if err != nil {
		internal(w)
		return
	}
	defer rs.Close()
	var rows []evRow
	for rs.Next() {
		var e evRow
		if rs.Scan(&e.id, &e.seq, &e.atNS, &e.typ, &e.actor, &e.text) != nil {
			internal(w)
			return
		}
		rows = append(rows, e)
	}
	if rs.Err() != nil {
		internal(w)
		return
	}
	var next any
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].seq
	}
	evs := make([]timelineEvent, 0, len(rows))
	for _, e := range rows {
		text, hidden := humanize(e.text)
		if hidden {
			continue
		}
		evs = append(evs, timelineEvent{ID: "ev_" + strconv.FormatInt(e.id, 10), Seq: e.seq, SessionID: id, Type: e.typ, At: e.atNS / int64(time.Millisecond),
			Actor: e.actor, Summary: text, Cite: []any{}})
	}
	items := make([]any, 0, len(evs))
	if grp == "1" {
		items = groupRuns(evs, rows)
	} else {
		for _, e := range evs {
			items = append(items, e)
		}
	}
	gaps := []gapOut{}
	if len(evs) > 0 {
		lo, hi := evs[len(evs)-1].At, evs[0].At
		if next == nil {
			lo = 0 // the oldest page: show every earlier gap too
		}
		gs, err := s.gaps(r.Context(), lo, hi)
		if err != nil {
			internal(w)
			return
		}
		gaps = gs
	}
	reply(w, 200, map[string]any{"events": items, "gaps": gaps, "next_before": next})
}

// groupRuns folds consecutive tool calls within 30 s of each other (no other event between) into
// one tool_run. Newest first in, newest first out. A lone call stays a plain event.
func groupRuns(evs []timelineEvent, rows []evRow) []any {
	var out []any
	i := 0
	for i < len(evs) {
		if evs[i].Type != "tool" {
			out = append(out, evs[i])
			i++
			continue
		}
		j := i + 1
		for j < len(evs) && evs[j].Type == "tool" && time.Duration(evs[j-1].At-evs[j].At)*time.Millisecond <= agentsderive.RunGap {
			j++
		}
		if j-i == 1 {
			out = append(out, evs[i])
			i = j
			continue
		}
		counts := map[string]int{}
		for _, e := range evs[i:j] {
			counts[toolOf(e.Summary)]++
		}
		out = append(out, toolRun{Type: "tool_run", RunID: "run_" + strings.TrimPrefix(evs[j-1].ID, "ev_"), SessionID: evs[i].SessionID, Calls: j - i,
			Summary: fmt.Sprintf("%d tool calls (%s)", j-i, runCounts(counts)), StartedAt: evs[j-1].At, EndedAt: evs[i].At, Events: evs[i:j]})
		i = j
	}
	return out
}

func runCounts(c map[string]int) string {
	type kv struct {
		k string
		n int
	}
	var l []kv
	for k, n := range c {
		l = append(l, kv{k, n})
	}
	sort.Slice(l, func(i, j int) bool {
		if l[i].n != l[j].n {
			return l[i].n > l[j].n
		}
		return l[i].k < l[j].k
	})
	var parts []string
	for i, e := range l {
		if i == 4 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("%s x%d", e.k, e.n))
	}
	return strings.Join(parts, ", ")
}

func (s *Server) gaps(ctx context.Context, loMS, hiMS int64) ([]gapOut, error) {
	rs, err := s.DB.Reader.QueryContext(ctx, `SELECT start_ns,end_ns,reason,from_seq,to_seq,backfilled FROM agents_gaps WHERE end_ns>=? AND start_ns<=? ORDER BY start_ns DESC LIMIT 200`,
		loMS*int64(time.Millisecond), hiMS*int64(time.Millisecond))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []gapOut{}
	for rs.Next() {
		var g gapOut
		var st, en int64
		var reason string
		var bf int
		if err := rs.Scan(&st, &en, &reason, &g.FromSeq, &g.ToSeq, &bf); err != nil {
			return nil, err
		}
		g.Start, g.End, g.Reason, g.Backfilled = st/int64(time.Millisecond), en/int64(time.Millisecond), gapReason(reason), bf != 0
		out = append(out, g)
	}
	return out, rs.Err()
}

// ---- context series ----

type point struct {
	At     int64 `json:"at"`
	Used   int64 `json:"used"`
	Window int64 `json:"window"`
	Pct    int   `json:"pct"`
}

const maxPoints = 600

func (s *Server) contextSeries(w http.ResponseWriter, r *http.Request) {
	sess, row, ok := s.one(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	step := q.Get("step")
	if step != "" && step != "min" && step != "turn" {
		fail(w, 400, "invalid", "step must be min or turn.")
		return
	}
	now := s.now()
	to, hasTo, ok := intParam(w, q, "to", 0, 1<<62)
	if !ok {
		return
	}
	if !hasTo {
		to = now.UnixMilli()
	}
	from, hasFrom, ok := intParam(w, q, "from", 0, 1<<62)
	if !ok {
		return
	}
	if !hasFrom {
		from = to - 24*3600*1000
	}
	if from > to {
		fail(w, 400, "invalid", "from must not be after to.")
		return
	}
	out := map[string]any{"points": []point{}, "compactions": sess.Compactions, "clears": []Clear{}, "warn_pct": 90}
	if sess.LastClearAt != nil {
		out["clears"] = []Clear{*sess.LastClearAt}
	}
	if sess.Context.Source == "unknown" { // unmetered: nothing to plot, and nothing is invented
		out["compactions"] = []Compaction{}
		reply(w, 200, out)
		return
	}
	pts, err := s.points(r.Context(), row.id, from, to, now)
	if err != nil {
		internal(w)
		return
	}
	out["points"] = pts
	reply(w, 200, out)
}

// points reads minute buckets while they exist (48 h) and hour buckets before that, thinned to a
// bounded number so a long range stays small.
func (s *Server) points(ctx context.Context, id string, fromMS, toMS int64, now time.Time) ([]point, error) {
	cut := now.Add(-agentscollector.MinuteSampleRetention).UnixMilli()
	var pts []point
	for _, k := range []struct {
		kind   string
		lo, hi int64
	}{{"hour", fromMS, min64(toMS, cut-1)}, {"min", max64(fromMS, cut), toMS}} {
		if k.lo > k.hi {
			continue
		}
		rs, err := s.DB.Reader.QueryContext(ctx, `SELECT bucket_ns,ctx_used,ctx_window FROM agents_samples WHERE session_id=? AND bucket_kind=? AND bucket_ns>=? AND bucket_ns<=? AND ctx_used IS NOT NULL AND ctx_window>0 ORDER BY bucket_ns`,
			id, k.kind, k.lo*int64(time.Millisecond), k.hi*int64(time.Millisecond))
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var ns, used, win int64
			if err := rs.Scan(&ns, &used, &win); err != nil {
				rs.Close()
				return nil, err
			}
			pts = append(pts, point{At: ns / int64(time.Millisecond), Used: used, Window: win, Pct: int(used * 100 / win)})
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(pts) > maxPoints {
		stride := (len(pts) + maxPoints - 1) / maxPoints
		thin := pts[:0:0]
		for i := 0; i < len(pts); i += stride {
			thin = append(thin, pts[i])
		}
		if last := pts[len(pts)-1]; thin[len(thin)-1] != last {
			thin = append(thin, last)
		}
		pts = thin
	}
	if pts == nil {
		pts = []point{}
	}
	return pts, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ---- tokens ----

func (s *Server) tokens(w http.ResponseWriter, r *http.Request) {
	sess, row, ok := s.one(w, r)
	if !ok {
		return
	}
	if !sess.Tokens.Metered {
		reply(w, 200, map[string]any{"metered": false, "hour": nil, "session": nil, "share": nil, "trend": nil})
		return
	}
	now := s.now()
	type win struct{ in, out, cr, cw, calls int64 }
	read := func(where string, args ...any) (win, error) {
		var x win
		err := s.DB.Reader.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(tok_in),0),COALESCE(SUM(tok_out),0),COALESCE(SUM(tok_cr),0),COALESCE(SUM(tok_cw),0),COALESCE(SUM(tool_calls),0) FROM agents_samples WHERE bucket_kind='min' AND `+where, args...).Scan(&x.in, &x.out, &x.cr, &x.cw, &x.calls)
		return x, err
	}
	end := now.Truncate(time.Minute)
	ns := func(m int) int64 { return end.Add(-time.Duration(m) * time.Minute).UnixNano() }
	hour, err := read(`session_id=? AND bucket_ns>=?`, row.id, ns(59))
	if err != nil {
		internal(w)
		return
	}
	fleet, err := read(`bucket_ns>=?`, ns(59))
	if err != nil {
		internal(w)
		return
	}
	recent, err := read(`session_id=? AND bucket_ns>=?`, row.id, ns(29))
	if err != nil {
		internal(w)
		return
	}
	prior, err := read(`session_id=? AND bucket_ns>=? AND bucket_ns<?`, row.id, ns(59), ns(29))
	if err != nil {
		internal(w)
		return
	}
	// share: this session's input+output of the last hour over every session's.
	var share any
	if tot := fleet.in + fleet.out; tot > 0 {
		share = float64(hour.in+hour.out) / float64(tot)
	}
	// trend: the last 30 minutes against the 30 before, input+output, with a 20% dead band.
	var trend any
	a, b := recent.in+recent.out, prior.in+prior.out
	switch {
	case a == 0 && b == 0:
	case b == 0 || a*10 > b*12:
		trend = "up"
	case a*10 < b*8:
		trend = "down"
	default:
		trend = "flat"
	}
	reply(w, 200, map[string]any{"metered": true,
		"hour":    map[string]int64{"input": hour.in, "output": hour.out, "cache_read": hour.cr, "cache_write": hour.cw, "tool_calls": hour.calls},
		"session": map[string]int64{"input": *sess.Tokens.Input, "output": *sess.Tokens.Output, "cache_read": *sess.Tokens.CacheRead, "cache_write": *sess.Tokens.CacheWrite, "tool_calls": orZero(sess.ToolCallCount)},
		"share":   share, "trend": trend})
}

// ---- history ----

// history lists the other sessions of the same logical agent (or, failing that, the same agent
// URN). A session that Tether never tied to an agent has no history to show.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	_, me, ok := s.one(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	col, key := "logical_agent_id", me.logical
	if key == "" {
		col, key = "agent_urn", me.urn
	}
	if key != "" {
		rs, err := s.DB.Reader.QueryContext(r.Context(), `SELECT session_id,started_ns,last_activity_ns,ended_ns,end_reason,replaced_by,tok_in,tok_out,tool_calls FROM agents_sessions WHERE `+col+`=? AND session_id<>? ORDER BY started_ns DESC, session_id LIMIT 20`, key, me.id)
		if err != nil {
			internal(w)
			return
		}
		defer rs.Close()
		for rs.Next() {
			var id, reason, rep string
			var st, la, en, calls int64
			var ti, to *int64
			if rs.Scan(&id, &st, &la, &en, &reason, &rep, &ti, &to, &calls) != nil {
				internal(w)
				return
			}
			begin := st
			if begin == 0 {
				begin = la
			}
			stop := en
			if stop == 0 {
				stop = time.Now().UnixNano()
				if s.Now != nil {
					stop = s.Now().UnixNano()
				}
			}
			e := map[string]any{"session_id": id, "started_at": begin / int64(time.Millisecond), "ran_ms": 0, "tokens": nil, "tool_calls": calls, "ended_at": nil, "end_reason": nil, "replaced_by": nil}
			if begin > 0 && stop > begin {
				e["ran_ms"] = (stop - begin) / int64(time.Millisecond)
			}
			if ti != nil || to != nil {
				e["tokens"] = map[string]int64{"input": derefi(ti), "output": derefi(to)}
			}
			if en > 0 {
				e["ended_at"], e["end_reason"] = en/int64(time.Millisecond), reason
			}
			if rep != "" {
				e["replaced_by"] = rep
			}
			out = append(out, e)
		}
		if rs.Err() != nil {
			internal(w)
			return
		}
	}
	reply(w, 200, map[string]any{"sessions": out})
}

func derefi(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
