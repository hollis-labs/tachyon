package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsderive"
)

// OutputSink receives a session.output payload in memory. It must not keep, store or log the
// payload; it returns after reducing it. Sinks are called from the stream reader: keep them quick.
type OutputSink interface {
	Output(ctx context.Context, sessionID string, at time.Time, payload []byte)
}

// Derived is what the sink saves for a session: derived fields only.
type Derived struct {
	OneLiner    *agentsderive.OneLiner
	CallsInTurn int
	CallsDelta  int
	TokDelta    *[4]int64 // tokens to add to the session total and the minute/hour buckets; nil = none seen
	CtxUsed     int64
	CtxWindow   int64
	CtxModel    string
	Compactions []agentsderive.Marker
	LastClear   *agentsderive.Marker
	At          time.Time
	CtxSeen     bool
}

// SetDerived stores one session's derived fields and a context sample for the minute.
func (s *Store) SetDerived(ctx context.Context, id string, d Derived) error {
	var ol any
	if d.OneLiner != nil {
		b, _ := json.Marshal(map[string]any{"verb": d.OneLiner.Verb, "target": d.OneLiner.Target, "tool": d.OneLiner.Tool, "at": d.At.UnixMilli(), "calls_this_turn": d.CallsInTurn})
		ol = string(b)
	}
	comp, _ := json.Marshal(d.Compactions)
	var used, window, model, clearNS, clearInf any
	if d.CtxSeen {
		used = d.CtxUsed
		if d.CtxWindow > 0 {
			window = d.CtxWindow
		}
		if d.CtxModel != "" {
			model = d.CtxModel
		}
	}
	if d.LastClear != nil {
		clearNS, clearInf = d.LastClear.At.UnixNano(), b2int(d.LastClear.Inferred)
	}
	return s.DB.Writer.Submit(ctx, "agents_derived", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agents_sessions(session_id,started_ns,last_activity_ns,updated_ns,one_liner_json,calls_this_turn,ctx_used,ctx_window,ctx_model,compactions_json,last_clear_ns,last_clear_inferred,tool_calls)
 VALUES(?,?,?,?,?,?,?,?,?,?,COALESCE(?,0),COALESCE(?,0),?)
 ON CONFLICT(session_id) DO UPDATE SET one_liner_json=COALESCE(excluded.one_liner_json,one_liner_json), calls_this_turn=excluded.calls_this_turn,
  ctx_used=COALESCE(excluded.ctx_used,ctx_used), ctx_window=COALESCE(excluded.ctx_window,ctx_window), ctx_model=COALESCE(excluded.ctx_model,ctx_model),
  compactions_json=excluded.compactions_json, tool_calls=COALESCE(tool_calls,0)+excluded.tool_calls,
  last_clear_ns=CASE WHEN excluded.last_clear_ns>0 THEN excluded.last_clear_ns ELSE last_clear_ns END,
  last_clear_inferred=CASE WHEN excluded.last_clear_ns>0 THEN excluded.last_clear_inferred ELSE last_clear_inferred END,
  last_activity_ns=MAX(last_activity_ns,excluded.last_activity_ns), updated_ns=excluded.updated_ns`,
			id, d.At.UnixNano(), d.At.UnixNano(), s.now().UnixNano(), ol, d.CallsInTurn, used, window, model, string(comp), clearNS, clearInf, d.CallsDelta)
		if err == nil && d.TokDelta != nil {
			err = addTokens(ctx, tx, id, d.At, *d.TokDelta)
		}
		if err != nil || !d.CtxSeen {
			return err
		}
		for _, b := range []struct {
			kind string
			ns   int64
		}{{"min", d.At.Truncate(time.Minute).UnixNano()}, {"hour", d.At.Truncate(time.Hour).UnixNano()}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO agents_samples(session_id,bucket_ns,bucket_kind,ctx_used,ctx_window) VALUES(?,?,?,?,?)
 ON CONFLICT(session_id,bucket_kind,bucket_ns) DO UPDATE SET ctx_used=excluded.ctx_used, ctx_window=COALESCE(excluded.ctx_window,ctx_window)`, id, b.ns, b.kind, used, window); err != nil {
				return err
			}
		}
		return nil
	})
}

func b2int(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SessionRow is one session as stored, ready for agentsderive.Derive.
type SessionRow struct {
	ID              string
	SessionState    string
	WorkState       string
	PendingApproval *bool
	PendingQuestion *bool
	Alive           *bool
	WorkerOnline    *bool
	LastActivity    time.Time
	StartedAt       time.Time
	EndedAt         time.Time
	Provider, Model string
	Workroot        string
	OneLiner        json.RawMessage
	CtxUsed         *int64
	CtxWindow       *int64
	Tokens          *[4]int64 // nil = not metered
	ToolCalls       int64
}

func nb(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Int64 != 0
	return &v
}

func ni(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func tm(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

// Sessions reads every stored session. The caller derives state with agentsderive.Derive at read
// time, so changing stale_after_s takes effect on the next read.
func (s *Store) Sessions(ctx context.Context) ([]SessionRow, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT session_id,session_state,work_state,pending_approval,pending_question,alive,worker_online,last_activity_ns,started_ns,ended_ns,provider,model,workroot,one_liner_json,ctx_used,ctx_window,tok_in,tok_out,tok_cr,tok_cw,tool_calls FROM agents_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		var pa, pq, alive, wo, cu, cw, ti, to, tcr, tcw sql.NullInt64
		var la, st, en int64
		var ol sql.NullString
		if err := rows.Scan(&r.ID, &r.SessionState, &r.WorkState, &pa, &pq, &alive, &wo, &la, &st, &en, &r.Provider, &r.Model, &r.Workroot, &ol, &cu, &cw, &ti, &to, &tcr, &tcw, &r.ToolCalls); err != nil {
			return nil, err
		}
		r.PendingApproval, r.PendingQuestion, r.Alive, r.WorkerOnline = nb(pa), nb(pq), nb(alive), nb(wo)
		r.LastActivity, r.StartedAt, r.EndedAt = tm(la), tm(st), tm(en)
		if ol.Valid {
			r.OneLiner = json.RawMessage(ol.String)
		}
		r.CtxUsed, r.CtxWindow = ni(cu), ni(cw)
		if ti.Valid || to.Valid {
			r.Tokens = &[4]int64{ti.Int64, to.Int64, tcr.Int64, tcw.Int64}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// addTokens adds metered tokens to the session total (which turns the session from unmetered to metered) and to
// the minute and hour buckets.
func addTokens(ctx context.Context, tx *sql.Tx, id string, at time.Time, t [4]int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE agents_sessions SET tok_in=COALESCE(tok_in,0)+?, tok_out=COALESCE(tok_out,0)+?, tok_cr=COALESCE(tok_cr,0)+?, tok_cw=COALESCE(tok_cw,0)+? WHERE session_id=?`, t[0], t[1], t[2], t[3], id); err != nil {
		return err
	}
	for _, b := range []struct {
		kind string
		ns   int64
	}{{"min", at.Truncate(time.Minute).UnixNano()}, {"hour", at.Truncate(time.Hour).UnixNano()}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agents_samples(session_id,bucket_ns,bucket_kind,tok_in,tok_out,tok_cr,tok_cw) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(session_id,bucket_kind,bucket_ns) DO UPDATE SET tok_in=tok_in+excluded.tok_in, tok_out=tok_out+excluded.tok_out, tok_cr=tok_cr+excluded.tok_cr, tok_cw=tok_cw+excluded.tok_cw`,
			id, b.ns, b.kind, t[0], t[1], t[2], t[3]); err != nil {
			return err
		}
	}
	return nil
}
