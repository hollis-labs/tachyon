package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvalsapi"
)

// Facts are the queue-facing properties of one record, computed from the stored
// request. They feed both the projection on each item and the derived index.
type Facts struct {
	Kind        string
	Group       string
	Risk        string
	Title       string
	Summary     string
	ReqID       string
	ReqLabel    string
	ReqKind     string
	SessionRef  string
	EffectOps   []string
	Interactive bool
	HasSecret   bool
	CommandExec bool
	Destructive bool
	Reasons     []string
	Recommend   string
	ReasonReq   bool
}

var destructiveOps = []string{"delete", "purge", "drop"}

func riskRank(r string) int {
	switch r {
	case "high":
		return 3
	case "medium":
		return 2
	}
	return 1
}

// effectClass normalizes an effect op for display and filtering.
func effectClass(op string) string {
	switch op {
	case "create", "update", "restart", "delete", "archive", "run":
		return op
	}
	return "other"
}

func rawString(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}

func rawBool(m map[string]json.RawMessage, k string) bool {
	var b bool
	_ = json.Unmarshal(m[k], &b)
	return b
}

// factsOf derives Facts from a record. Unknown or malformed parts degrade to the
// safe reading: an unreadable class is medium, missing effects mean not batchable.
func factsOf(rec hitl.Record) Facts {
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &req)
	f := Facts{Kind: rec.Kind, Title: rawString(req.Extra, "title"), Summary: rawString(req.Extra, "summary"), Recommend: rawString(req.Extra, "recommendation"), ReasonReq: rawBool(req.Extra, "reason_required")}
	if f.Kind == "" {
		f.Kind = req.Kind
	}
	f.Risk = rawString(req.Extra, "class")
	if f.Risk != "low" && f.Risk != "medium" && f.Risk != "high" {
		f.Risk = "medium"
	}
	f.ReqID = req.Source.AgentID
	f.ReqKind = "agent"
	if f.ReqID == "" {
		f.ReqID, f.ReqKind = req.Source.ApplicationID, "app"
	}
	f.ReqLabel = req.Source.AgentLabel
	if f.ReqLabel == "" {
		f.ReqLabel = f.ReqID
	}
	var corr struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(req.Correlations, &corr)
	f.SessionRef = corr.SessionID

	var effects []struct {
		Op string `json:"op"`
	}
	_ = json.Unmarshal(req.Extra["effects"], &effects)
	for _, e := range effects {
		op := e.Op
		if op == "" {
			op = "other"
		}
		f.EffectOps = append(f.EffectOps, op)
		if slices.Contains(destructiveOps, op) || strings.HasPrefix(op, "force_") {
			f.Destructive = true
		}
	}
	if raw := req.Extra["destructive"]; len(raw) > 0 && string(raw) != "null" && string(raw) != "false" {
		f.Destructive = true
	}
	var data struct {
		Fields []struct {
			Type string `json:"type"`
		} `json:"fields"`
	}
	_ = json.Unmarshal(req.Extra["data"], &data)
	for _, fd := range data.Fields {
		f.HasSecret = f.HasSecret || fd.Type == "secret"
	}
	_, hasArgv := req.Extra["argv"]
	_, hasAction := req.Extra["action"]
	f.CommandExec = hasArgv || hasAction
	switch f.Kind {
	case "card.question", "card.form":
		f.Interactive = true
	}
	f.Interactive = f.Interactive || rawBool(req.Extra, "interactive")

	switch {
	case f.Kind == "attention" || f.Kind == "card.notice":
		f.Group = "notice"
	case f.Kind == "card.info":
		f.Group = "info"
	case f.HasSecret:
		f.Group = "secret"
	case f.Kind == "card.form":
		f.Group = "form"
	case f.Kind == "card.question":
		f.Group = "question"
	case f.Destructive:
		f.Group = "destructive"
	case f.CommandExec:
		f.Group = "command"
	default:
		f.Group = "change"
	}
	if len(f.EffectOps) > 0 {
		f.Reasons = append(f.Reasons, "effects_present")
	}
	for _, r := range []struct {
		on   bool
		code string
	}{{f.HasSecret, "secret_field"}, {f.Interactive, "interactive"}, {f.Destructive, "destructive"}, {f.CommandExec, "command_exec"}} {
		if r.on {
			f.Reasons = append(f.Reasons, r.code)
		}
	}
	return f
}

// statusOf is the history status of a record: pending, or how it ended.
func statusOf(rec hitl.Record) string {
	switch o := rec.Outcome.(type) {
	case nil:
		return "pending"
	case hitl.Resolved:
		switch o.Resolution.Response.Decision {
		case "approved", "denied":
			return o.Resolution.Response.Decision
		}
		return "answered"
	case hitl.Expired:
		return "expired"
	case hitl.Canceled:
		return "withdrawn"
	case hitl.Failed:
		return "failed"
	case hitl.Superseded:
		return "superseded"
	}
	return "pending"
}

func decidedAt(rec hitl.Record) any {
	if rec.Outcome == nil {
		return nil
	}
	return rec.Outcome.OutcomeTime().UnixNano()
}

func (f Facts) subject(state string, expired, deferred, operator bool) approvalsapi.Subject {
	return approvalsapi.Subject{Kind: f.Kind, Class: f.Risk, Interactive: f.Interactive, HasSecret: f.HasSecret, CommandExec: f.CommandExec,
		Destructive: f.Destructive, EffectOps: f.EffectOps, State: state, Expired: expired, Deferred: deferred, OperatorAccess: operator}
}

// Projection is the set of fields added to each item. Fields whose slice has not
// landed (destructive/confirm/alternatives, evidence, run_state, defer) are absent,
// not faked.
type Projection struct {
	Kind          string
	Title         string
	Summary       string
	Requester     map[string]string
	SessionRef    string
	AskedAt       time.Time
	Risk          string
	RiskReasons   []string
	Group         string
	Batchable     bool
	BatchBlockers []string
	EffectClasses []string
	ExpiresAt     *time.Time
	ServerNowMs   int64
	Recommend     string
	ReasonReq     bool
	DeferredUntil *time.Time
	Gate          Gate
	// Destructive-request members, as stored (and digested) by the card validator.
	AffectedItems, Destructive, Alternatives json.RawMessage
}

// Project builds the projection of a record at now. batchable and batch_blockers
// describe the item itself; whether the caller may batch at all (elevated access)
// is /access's answer, so needs_operator_access never appears here.
func Project(rec hitl.Record, now time.Time, deferredUntil *time.Time) Projection {
	f := factsOf(rec)
	expired := rec.ExpiresAt != nil && !rec.ExpiresAt.After(now)
	blockers := approvalsapi.Eligibility(f.subject(string(rec.State), expired, deferredUntil != nil, true))
	classes := []string{}
	for _, op := range f.EffectOps {
		if c := effectClass(op); !slices.Contains(classes, c) {
			classes = append(classes, c)
		}
	}
	reasons := f.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	if blockers == nil {
		blockers = []string{}
	}
	var stored hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &stored)
	return Projection{Kind: f.Kind, Title: f.Title, Summary: f.Summary,
		Requester:  map[string]string{"id": f.ReqID, "label": f.ReqLabel, "kind": f.ReqKind},
		SessionRef: f.SessionRef, AskedAt: rec.CreatedAt, Risk: f.Risk, RiskReasons: reasons, Group: f.Group,
		Batchable: len(blockers) == 0, BatchBlockers: blockers, EffectClasses: classes, ExpiresAt: rec.ExpiresAt,
		ServerNowMs: now.UnixMilli(), Recommend: f.Recommend, ReasonReq: f.ReasonReq, DeferredUntil: deferredUntil, Gate: CanResolve(rec, now),
		AffectedItems: stored.Extra["affected_items"], Destructive: stored.Extra["destructive"], Alternatives: stored.Extra["alternatives"]}
}

func (p Projection) fields() map[string]any {
	m := map[string]any{"kind": p.Kind, "title": p.Title, "summary": p.Summary, "requester": p.Requester, "session_ref": p.SessionRef,
		"asked_at": p.AskedAt.UTC().Format(time.RFC3339), "risk": p.Risk, "risk_reasons": p.RiskReasons, "group": p.Group,
		"batchable": p.Batchable, "batch_blockers": p.BatchBlockers, "effect_classes": p.EffectClasses, "server_now_ms": p.ServerNowMs,
		"reason_required": p.ReasonReq}
	if p.ExpiresAt != nil {
		m["expires_at"] = p.ExpiresAt.UTC().Format(time.RFC3339)
	} else {
		m["expires_at"] = nil
	}
	if p.DeferredUntil != nil {
		m["deferred_until"] = p.DeferredUntil.UTC().Format(time.RFC3339)
	} else {
		m["deferred_until"] = nil
	}
	if p.Recommend != "" {
		m["recommendation"] = p.Recommend
	}
	m["affected_items"] = rawOr(p.AffectedItems, "[]")
	m["affected_total"] = affectedCount(p.AffectedItems)
	m["destructive"] = rawOr(p.Destructive, "null")
	m["alternatives"] = rawOr(p.Alternatives, "[]")
	m["approvable"] = p.Gate.Approvable
	if !p.Gate.Approvable {
		m["approvable_reason"] = p.Gate.Reason
	}
	if p.Gate.Phrase != "" {
		m["confirm"] = map[string]string{"phrase": p.Gate.Phrase}
	}
	return m
}

const indexUpsert = `INSERT INTO approval_item_index(item_id,revision,state,terminal,status,kind,grp,risk,risk_rank,title,summary,requester_id,requester_label,requester_kind,session_ref,created_ns,expires_ns,decided_ns,interactive,has_secret,command_exec,destructive,effect_ops,search)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(item_id) DO UPDATE SET revision=excluded.revision,state=excluded.state,terminal=excluded.terminal,status=excluded.status,kind=excluded.kind,grp=excluded.grp,risk=excluded.risk,risk_rank=excluded.risk_rank,title=excluded.title,summary=excluded.summary,requester_id=excluded.requester_id,requester_label=excluded.requester_label,requester_kind=excluded.requester_kind,session_ref=excluded.session_ref,created_ns=excluded.created_ns,expires_ns=excluded.expires_ns,decided_ns=excluded.decided_ns,interactive=excluded.interactive,has_secret=excluded.has_secret,command_exec=excluded.command_exec,destructive=excluded.destructive,effect_ops=excluded.effect_ops,search=excluded.search`

// indexTx writes the index row inside the record's own transaction. A failure here
// must not block a decision, so it is dropped: the row is derived, and Reindex repairs it.
func indexTx(ctx context.Context, tx *sql.Tx, rec hitl.Record) {
	f := factsOf(rec)
	var expires any
	if rec.ExpiresAt != nil {
		expires = rec.ExpiresAt.UnixNano()
	}
	ops, _ := json.Marshal(f.EffectOps)
	if f.EffectOps == nil {
		ops = []byte("[]")
	}
	search := strings.ToLower(strings.Join([]string{f.Title, f.Summary, f.ReqID, f.ReqLabel, rec.ItemID, f.Kind}, " "))
	_, _ = tx.ExecContext(ctx, indexUpsert, rec.ItemID, rec.Revision, string(rec.State), rec.State.IsTerminal(), statusOf(rec), f.Kind, f.Group, f.Risk, riskRank(f.Risk),
		f.Title, f.Summary, f.ReqID, f.ReqLabel, f.ReqKind, f.SessionRef, rec.CreatedAt.UnixNano(), expires, decidedAt(rec),
		f.Interactive, f.HasSecret, f.CommandExec, f.Destructive, string(ops), search)
}

// Reindex adds or refreshes every index row that is missing or behind its record.
// Safe to run any time; it is the backfill for items that predate the index.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	total := 0
	for {
		rows, err := s.DB.Reader.QueryContext(ctx, `SELECT i.record FROM approval_items i LEFT JOIN approval_item_index x ON x.item_id=i.id WHERE x.item_id IS NULL OR x.revision<>i.revision ORDER BY i.created_ns LIMIT 200`)
		if err != nil {
			return total, err
		}
		var recs []hitl.Record
		for rows.Next() {
			rec, err := readRecord(rows)
			if err != nil {
				rows.Close()
				return total, err
			}
			recs = append(recs, rec)
		}
		rows.Close()
		if len(recs) == 0 {
			return total, nil
		}
		err = s.DB.Writer.Submit(ctx, "approval_reindex", func(ctx context.Context, tx *sql.Tx) error {
			for _, rec := range recs {
				indexTx(ctx, tx, rec)
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += len(recs)
		if len(recs) < 200 || total >= 100000 { // the cap stops a row that never indexes from looping forever
			return total, nil
		}
	}
}

// Filter selects queue rows. Zero values mean no filter.
type Filter struct {
	Group, Risk, Q, Requester, Status string
	Batchable                         bool
	Order                             string // urgency (default) | created
	After                             string // item id cursor
	Limit                             int
	Operator                          bool
}

// Row is one pending row with what eligibility and counts need.
type Row struct {
	ID        string
	Group     string
	Risk      string
	Batchable bool
	Deferred  bool
	created   int64
	expires   sql.NullInt64
	rank      int
}

// Pending lists the pending queue (not terminal, not logically expired) filtered and
// ordered, plus counts over the whole unfiltered pending set. Pending is small, so
// this reads all rows and filters and pages in Go.
func (s *Store) Pending(ctx context.Context, f Filter, now time.Time) (ids []string, next string, counts map[string]any, err error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT item_id,state,kind,grp,risk,risk_rank,requester_id,created_ns,expires_ns,deferred_until_ns,interactive,has_secret,command_exec,destructive,effect_ops,search
FROM approval_item_index WHERE terminal=0 AND (expires_ns IS NULL OR expires_ns>?)`, now.UnixNano())
	if err != nil {
		return nil, "", nil, err
	}
	defer rows.Close()
	type pr struct {
		Row
		requester, search string
		match             bool
	}
	var all []pr
	byGroup, byRisk := map[string]int{}, map[string]int{}
	batchable, deferred := 0, 0
	q := strings.ToLower(strings.TrimSpace(f.Q))
	for rows.Next() {
		var r pr
		var state, kind, ops string
		var defUntil sql.NullInt64
		var inter, sec, cmd, des bool
		if err = rows.Scan(&r.ID, &state, &kind, &r.Group, &r.Risk, &r.rank, &r.requester, &r.created, &r.expires, &defUntil, &inter, &sec, &cmd, &des, &ops, &r.search); err != nil {
			return nil, "", nil, err
		}
		var effOps []string
		_ = json.Unmarshal([]byte(ops), &effOps)
		r.Deferred = defUntil.Valid && defUntil.Int64 > now.UnixNano()
		sub := approvalsapi.Subject{Kind: kind, Class: r.Risk, Interactive: inter, HasSecret: sec, CommandExec: cmd, Destructive: des, EffectOps: effOps, State: state, Deferred: r.Deferred, OperatorAccess: true}
		r.Batchable = len(approvalsapi.Eligibility(sub)) == 0
		byGroup[r.Group]++
		byRisk[r.Risk]++
		if r.Batchable {
			batchable++
		}
		if r.Deferred {
			deferred++
		}
		r.match = (f.Group == "" || r.Group == f.Group) && (f.Risk == "" || r.Risk == f.Risk) && (!f.Batchable || r.Batchable) &&
			(f.Requester == "" || r.requester == f.Requester) && (q == "" || strings.Contains(r.search, q))
		all = append(all, r)
	}
	if err = rows.Err(); err != nil {
		return nil, "", nil, err
	}
	counts = map[string]any{"pending": len(all), "batchable": batchable, "deferred": deferred, "by_group": byGroup, "by_risk": byRisk}
	var keep []pr
	for _, r := range all {
		if r.match {
			keep = append(keep, r)
		}
	}
	if f.Order == "created" {
		sort.SliceStable(keep, func(i, j int) bool {
			if keep[i].created != keep[j].created {
				return keep[i].created > keep[j].created
			}
			return keep[i].ID > keep[j].ID
		})
	} else {
		// urgency: deferred last, soonest expiry first (none last), higher risk first, oldest first
		sort.SliceStable(keep, func(i, j int) bool {
			a, b := keep[i], keep[j]
			if a.Deferred != b.Deferred {
				return b.Deferred
			}
			if a.expires.Valid != b.expires.Valid {
				return a.expires.Valid
			}
			if a.expires.Valid && a.expires.Int64 != b.expires.Int64 {
				return a.expires.Int64 < b.expires.Int64
			}
			if a.rank != b.rank {
				return a.rank > b.rank
			}
			if a.created != b.created {
				return a.created < b.created
			}
			return a.ID < b.ID
		})
	}
	start := 0
	if f.After != "" {
		start = -1
		for i, r := range keep {
			if r.ID == f.After {
				start = i + 1
			}
		}
		if start < 0 {
			return nil, "", nil, hitl.ErrInvalidRequest
		}
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	end := min(start+limit, len(keep))
	for _, r := range keep[start:end] {
		ids = append(ids, r.ID)
	}
	if end < len(keep) && len(ids) > 0 {
		next = ids[len(ids)-1]
	}
	return ids, next, counts, nil
}

// History lists finished (or logically expired) items newest first, with filters.
// Status: approved|denied|answered|expired|withdrawn|failed|superseded.
func (s *Store) History(ctx context.Context, f Filter, now time.Time) ([]string, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	where := []string{"(terminal=1 OR expires_ns<=?)"}
	args := []any{now.UnixNano()}
	if f.Group != "" {
		where, args = append(where, "grp=?"), append(args, f.Group)
	}
	if f.Risk != "" {
		where, args = append(where, "risk=?"), append(args, f.Risk)
	}
	if f.Requester != "" {
		where, args = append(where, "requester_id=?"), append(args, f.Requester)
	}
	if q := strings.ToLower(strings.TrimSpace(f.Q)); q != "" {
		where, args = append(where, "instr(search,?)>0"), append(args, q)
	}
	if f.Status == "expired" {
		where = append(where, "(status='expired' OR terminal=0)")
	} else if f.Status != "" {
		where, args = append(where, "status=?"), append(args, f.Status)
	}
	if f.After != "" {
		var created int64
		if err := s.DB.Reader.QueryRowContext(ctx, `SELECT created_ns FROM approval_item_index WHERE item_id=?`, f.After).Scan(&created); err != nil {
			return nil, "", hitl.ErrInvalidRequest
		}
		where, args = append(where, "(created_ns,item_id)<(?,?)"), append(args, created, f.After)
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT item_id FROM approval_item_index WHERE `+strings.Join(where, " AND ")+` ORDER BY created_ns DESC,item_id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		next = ids[len(ids)-1]
	}
	return ids, next, rows.Err()
}

// ExpireDue materializes expired items through the hitl service so they reach
// History with an outcome (and an item.expired event) without anyone asking.
// Maintain runs it on a ticker after a first Reindex.
func (i *Inbox) Maintain(ctx context.Context, every time.Duration, logf func(msg string, args ...any)) {
	if n, err := i.Store.Reindex(ctx); err != nil {
		logf("approval index backfill failed", "error", err)
	} else if n > 0 {
		logf("approval index backfilled", "rows", n)
	}
	if n, err := i.Store.BackfillLog(ctx); err != nil {
		logf("approval decision log backfill failed", "error", err)
	} else if n > 0 {
		logf("approval decision log backfilled", "events", n)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := i.Service.ExpireDue(ctx); err != nil && ctx.Err() == nil {
			logf("approval expiry sweep failed", "error", err)
		} else if n > 0 {
			logf("approval items expired", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func rawOr(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(fallback)
	}
	return raw
}

func affectedCount(raw json.RawMessage) int {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	return len(items)
}
