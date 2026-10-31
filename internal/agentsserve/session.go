// Package agentsserve is the HTTP read side of the Agents monitor (A3): the sessions list and
// detail, the timeline, context series, token rollups, history, settings and the SSE stream. It
// reads the collector's derived read model and applies the state rules at read time, so changing
// stale_after_s takes effect on the next request. It stores nothing but the settings row.
package agentsserve

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsderive"
)

// Session is the one data shape for Row, Card and BoardCard (agentsapi openapi: Session).
type Session struct {
	ID              string       `json:"id"`
	Agent           Agent        `json:"agent"`
	Kind            string       `json:"kind"`
	Provider        string       `json:"provider"`
	Model           *string      `json:"model"`
	Workroot        string       `json:"workroot"`
	State           string       `json:"state"`
	StateInferred   bool         `json:"state_inferred,omitempty"` // working because output is fresh, not because Tether said so
	StaleReason     *string      `json:"stale_reason"`
	StaleSince      *int64       `json:"stale_since"`
	NeedsYou        NeedsYou     `json:"needs_you"`
	StartedAt       int64        `json:"started_at"`
	LastActivityAt  *int64       `json:"last_activity_at"`
	RuntimeMS       int64        `json:"runtime_ms"`
	OneLiner        any          `json:"one_liner"`
	Context         Context      `json:"context"`
	Tokens          Tokens       `json:"tokens"`
	ToolCallCount   *int64       `json:"tool_call_count"` // null until a tool call is collected (0 would read as "none ran")
	LastClearAt     *Clear       `json:"last_clear_at"`
	Compactions     []Compaction `json:"compactions"`
	Pending         Pending      `json:"pending"`
	Assignment      any          `json:"assignment"`
	Spark           [30]int      `json:"spark"`
	ParentSessionID *string      `json:"parent_session_id"`
	ReplacedBy      *string      `json:"replaced_by"`
	ObservedBy      []string     `json:"observed_by"`
	Capabilities    Capabilities `json:"capabilities"`
}

type Agent struct {
	URN            *string `json:"urn"`
	Name           string  `json:"name"`
	LogicalAgentID *string `json:"logical_agent_id"`
}

type NeedsYou struct {
	Reason *string `json:"reason"`
	Since  *int64  `json:"since"`
}

type Context struct {
	Used      *int64  `json:"used"`
	Window    *int64  `json:"window"`
	Pct       *int    `json:"pct"`
	Source    string  `json:"source"`
	Breakdown *string `json:"breakdown"` // always null: no source exists
}

type Hour struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type Tokens struct {
	Metered    bool   `json:"metered"`
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	CacheRead  *int64 `json:"cache_read"`
	CacheWrite *int64 `json:"cache_write"`
	Hour       *Hour  `json:"hour"`
}

type Clear struct {
	At       int64 `json:"at"`
	Inferred bool  `json:"inferred"`
}

type Compaction struct {
	At       int64 `json:"at"`
	Before   int64 `json:"before"`
	After    int64 `json:"after"`
	Inferred bool  `json:"inferred"`
}

type Pending struct {
	Approval bool `json:"approval"`
	Question bool `json:"question"`
	Known    bool `json:"known"`
}

type Capabilities struct {
	Nudge     bool      `json:"nudge"`
	Interrupt Interrupt `json:"interrupt"`
	Restart   bool      `json:"restart"`
}

type Interrupt struct {
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

// StateEnded is only ever returned for a session that has ended (detail, or include_ended=1).
const StateEnded = "ended"

// row is a stored session before derivation.
type row struct {
	id, sessionState, workState, provider, model, workroot string
	urn, logical, name, parent, replaced                   string
	pa, pq, alive, worker                                  *bool
	started, last, ended                                   time.Time
	oneLiner                                               sql.NullString
	ctxUsed, ctxWindow                                     *int64
	compactions                                            string
	lastClear                                              time.Time
	clearInferred                                          bool
	tok                                                    *[4]int64
	toolCalls                                              int64
	taskRef                                                string
}

// extras are the per-minute figures of the last hour: sparkline and the hour token rollup.
type extras struct {
	spark [30]int
	hour  [4]int64
	any   bool
}

const sessionCols = `session_id,session_state,work_state,work_known,pending_approval,pending_question,alive,worker_online,started_ns,last_activity_ns,ended_ns,provider,COALESCE(NULLIF(model,''),ctx_model,'') AS model,workroot,agent_urn,logical_agent_id,agent_name,parent_session_id,replaced_by,one_liner_json,ctx_used,ctx_window,compactions_json,last_clear_ns,last_clear_inferred,tok_in,tok_out,tok_cr,tok_cw,tool_calls,task_ref FROM agents_sessions`

func tri(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Int64 != 0
	return &v
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func at(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func (s *Server) rows(ctx context.Context, where string, args ...any) ([]row, error) {
	rs, err := s.DB.Reader.QueryContext(ctx, `SELECT `+sessionCols+where, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		var known int64
		var pa, pq, al, wo, cu, cw, ti, to, tcr, tcw sql.NullInt64
		var st, la, en, lc, ci int64
		if err := rs.Scan(&r.id, &r.sessionState, &r.workState, &known, &pa, &pq, &al, &wo, &st, &la, &en, &r.provider, &r.model, &r.workroot, &r.urn, &r.logical, &r.name,
			&r.parent, &r.replaced, &r.oneLiner, &cu, &cw, &r.compactions, &lc, &ci, &ti, &to, &tcr, &tcw, &r.toolCalls, &r.taskRef); err != nil {
			return nil, err
		}
		if known == 0 {
			r.workState = "" // a work state Tether never reported is unknown, not idle
		}
		r.pa, r.pq, r.alive, r.worker = tri(pa), tri(pq), tri(al), tri(wo)
		r.started, r.last, r.ended, r.lastClear, r.clearInferred = at(st), at(la), at(en), at(lc), ci != 0
		r.ctxUsed, r.ctxWindow = ptr(cu), ptr(cw)
		if ti.Valid || to.Valid {
			r.tok = &[4]int64{ti.Int64, to.Int64, tcr.Int64, tcw.Int64}
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// recent reads the last hour of minute buckets for every session in one query.
func (s *Server) recent(ctx context.Context, now time.Time) (map[string]*extras, error) {
	end := now.Truncate(time.Minute)
	rs, err := s.DB.Reader.QueryContext(ctx, `SELECT session_id,bucket_ns,activity,tok_in,tok_out,tok_cr,tok_cw FROM agents_samples WHERE bucket_kind='min' AND bucket_ns>=?`, end.Add(-59*time.Minute).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]*extras{}
	for rs.Next() {
		var id string
		var ns, act, in, o, cr, cw int64
		if err := rs.Scan(&id, &ns, &act, &in, &o, &cr, &cw); err != nil {
			return nil, err
		}
		e := out[id]
		if e == nil {
			e = &extras{}
			out[id] = e
		}
		e.any = true
		if back := int(end.Sub(time.Unix(0, ns)) / time.Minute); back >= 0 && back < 30 {
			e.spark[29-back] += int(act)
		}
		e.hour[0] += in
		e.hour[1] += o
		e.hour[2] += cr
		e.hour[3] += cw
	}
	return out, rs.Err()
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ms(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

func agentName(r row) string {
	switch {
	case r.name != "":
		return r.name
	case r.urn != "":
		return r.urn[strings.LastIndexAny(r.urn, "/:")+1:]
	case r.logical != "":
		return r.logical
	}
	short := r.id
	if len(short) > 8 {
		short = short[:8]
	}
	return "session " + short
}

// build derives one Session. now and staleAfter are explicit so reads are deterministic in tests.
func build(r row, ex *extras, now time.Time, staleAfter time.Duration) Session {
	d := agentsderive.Derive(agentsderive.Input{SessionState: r.sessionState, WorkState: r.workState, PendingApproval: r.pa, PendingQuestion: r.pq,
		LastActivity: r.last, Alive: r.alive, WorkerOnline: r.worker}, now, staleAfter)
	started := r.started
	if started.IsZero() {
		started = r.last // a listing without a start time: the first activity we saw
	}
	end := now
	if !r.ended.IsZero() {
		end = r.ended
	}
	s := Session{ID: r.id, Kind: "process", Provider: providerOf(r.provider, r.model), Model: strp(r.model), Workroot: r.workroot, State: d.State,
		Agent:           Agent{URN: strp(r.urn), Name: agentName(r), LogicalAgentID: strp(r.logical)},
		LastActivityAt:  ms(r.last),
		ToolCallCount:   countOrNil(r.toolCalls),
		Compactions:     []Compaction{},
		ParentSessionID: strp(r.parent), ReplacedBy: strp(r.replaced), ObservedBy: []string{"tether"},
		Capabilities: Capabilities{Interrupt: Interrupt{Reason: strp("Controls are not enabled yet.")}},
		Pending:      Pending{Known: r.pa != nil || r.pq != nil},
	}
	if d.State == agentsderive.Ended {
		s.State = StateEnded
	}
	if !started.IsZero() {
		s.StartedAt = started.UnixMilli()
		if end.After(started) {
			s.RuntimeMS = end.Sub(started).Milliseconds()
		}
	}
	s.StateInferred = d.Inferred
	if d.StaleReason != "" {
		s.StaleReason = strp(d.StaleReason)
		s.StaleSince = ms(d.StaleSince)
	}
	if d.NeedsYou != "" {
		// Tether does not say when the wait began; the last event is the best evidence (a waiting
		// session is quiet), so since is the last activity.
		s.NeedsYou = NeedsYou{Reason: strp(d.NeedsYou), Since: ms(r.last)}
	}
	s.Pending.Approval, s.Pending.Question = r.pa != nil && *r.pa, r.pq != nil && *r.pq
	// The line says what the session is doing now, so it shows only while it is working or waiting; an idle,
	// stale or ended session keeps no "Running ..." line.
	if r.oneLiner.Valid && r.oneLiner.String != "" && (d.State == agentsderive.Working || d.State == agentsderive.Waiting) {
		var ol map[string]any
		if json.Unmarshal([]byte(r.oneLiner.String), &ol) == nil {
			s.OneLiner = ol
		}
	}
	s.Context = Context{Source: "unknown"}
	if r.ctxUsed != nil {
		s.Context.Used, s.Context.Source = r.ctxUsed, "stream"
		if r.ctxWindow != nil && *r.ctxWindow > 0 {
			s.Context.Window = r.ctxWindow
			p := int(*r.ctxUsed * 100 / *r.ctxWindow)
			s.Context.Pct = &p
		}
	}
	if r.tok != nil {
		s.Tokens = Tokens{Metered: true, Input: &r.tok[0], Output: &r.tok[1], CacheRead: &r.tok[2], CacheWrite: &r.tok[3]}
		h := Hour{}
		if ex != nil {
			h = Hour{ex.hour[0], ex.hour[1], ex.hour[2], ex.hour[3]}
		}
		s.Tokens.Hour = &h
	}
	var marks []agentsderive.Marker
	if json.Unmarshal([]byte(r.compactions), &marks) == nil {
		for _, m := range marks {
			s.Compactions = append(s.Compactions, Compaction{At: m.At.UnixMilli(), Before: m.Before, After: m.After, Inferred: m.Inferred})
		}
	}
	if !r.lastClear.IsZero() {
		s.LastClearAt = &Clear{At: r.lastClear.UnixMilli(), Inferred: r.clearInferred}
	}
	if ex != nil {
		s.Spark = ex.spark
	}
	return s
}

// statePriority orders the list: what needs the operator first.
var statePriority = map[string]int{agentsderive.Waiting: 0, agentsderive.Working: 1, agentsderive.Idle: 2, agentsderive.Stale: 3, agentsderive.Unknown: 4, StateEnded: 5}

func sortSessions(ss []Session, group string) {
	key := func(s Session) string {
		switch group {
		case "workroot":
			return s.Workroot
		case "agent":
			return strings.ToLower(s.Agent.Name)
		}
		return ""
	}
	sort.SliceStable(ss, func(i, j int) bool {
		if ki, kj := key(ss[i]), key(ss[j]); ki != kj {
			return ki < kj
		}
		if pi, pj := statePriority[ss[i].State], statePriority[ss[j].State]; pi != pj {
			return pi < pj
		}
		li, lj := int64(0), int64(0)
		if ss[i].LastActivityAt != nil {
			li = *ss[i].LastActivityAt
		}
		if ss[j].LastActivityAt != nil {
			lj = *ss[j].LastActivityAt
		}
		if li != lj {
			return li > lj
		}
		return ss[i].ID < ss[j].ID
	})
}

// Summary counts the live sessions by state; ended sessions are not counted.
func Summary(ss []Session) map[string]int {
	out := map[string]int{agentsderive.Working: 0, agentsderive.Waiting: 0, agentsderive.Idle: 0, agentsderive.Stale: 0, agentsderive.Unknown: 0}
	for _, s := range ss {
		if s.State != StateEnded {
			out[s.State]++
		}
	}
	return out
}

// withAssignment fills the A5 join for a session launched with a task; without an Assigner it stays null.
func (s *Server) withAssignment(ctx context.Context, sess Session, r row) Session {
	if s.Assigner != nil && r.taskRef != "" {
		sess.Assignment = s.Assigner.Assignment(ctx, r.id, r.taskRef)
	}
	sess.Capabilities = s.capabilities(sess)
	return sess
}

// all builds every stored session.
func (s *Server) all(ctx context.Context) ([]Session, error) {
	now := s.now()
	rs, err := s.rows(ctx, ``)
	if err != nil {
		return nil, err
	}
	ex, err := s.recent(ctx, now)
	if err != nil {
		return nil, err
	}
	stale := s.staleAfter(ctx)
	out := make([]Session, 0, len(rs))
	for _, r := range rs {
		out = append(out, s.withAssignment(ctx, build(r, ex[r.id], now, stale), r))
	}
	return out, nil
}

func sha256sum(b []byte) [32]byte { return sha256.Sum256(b) }

func countOrNil(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}

// providerOf keeps the stored provider and, when none was collected, names it from the model id's well-known
// prefix. An unrecognised model stays blank rather than guessed.
func providerOf(stored, model string) string {
	if stored != "" {
		return stored
	}
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return "claude"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"), strings.Contains(m, "codex"):
		return "codex"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	}
	return ""
}
