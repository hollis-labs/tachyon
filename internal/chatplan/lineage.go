package chatplan

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// MaxLineageHops bounds the walk from a replacement session up to the session that holds the plan.
const MaxLineageHops = 16

// ErrAgentMismatch refuses a carry-over whose ancestor belongs to a different logical agent.
var ErrAgentMismatch = errors.New("plan carry-over refused: the ancestor session belongs to another agent")

// Carried is the outcome of Carry. Moved is false when there was nothing to carry.
type Carried struct {
	Moved bool
	From  string // thread ref the plan was moved from
	To    string // thread ref it now lives under
	Plan  Plan
}

// Stub is a Tether session known from the lineage record, whether or not it has inbox turns yet.
type Stub struct {
	Ref     string
	Agent   string
	SeenNS  int64
	HasPlan bool
}

func sessionOf(ref string) string { return strings.TrimPrefix(ref, "tether:") }

// Carry records the session in the lineage (first sight wins) and, when it has no plan of its own,
// moves the nearest ancestor's plan to it. The ancestor chain is the one Tether reported
// (continues), walked through the lineage rows; every recorded hop must belong to the same
// logical agent. A session with no continues, a cleared session, or an unrecorded ancestor
// carries nothing. The move is one transaction: one live plan, revision bumped once.
func (s *Store) Carry(ctx context.Context, session, agent, continues string) (Carried, error) {
	var out Carried
	if session == "" || agent == "" {
		return out, ErrInvalid
	}
	if continues == session {
		continues = ""
	}
	me := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: session}.String()
	if s.recorded(ctx, session) {
		return out, nil
	}
	now := s.now()
	err := s.DB.Writer.Submit(ctx, "chat_plan_carry", func(ctx context.Context, tx *sql.Tx) error {
		out = Carried{}
		var cont sql.NullString
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns) VALUES(?,?,?,0,?)
 ON CONFLICT(session_id) DO UPDATE SET agent_id=CASE WHEN agent_id='' THEN excluded.agent_id ELSE agent_id END,
 continues_id=CASE WHEN continues_id IS NULL THEN excluded.continues_id ELSE continues_id END`,
			session, agent, nullIf(continues), now.UnixNano()); err != nil {
			return err
		}
		var cleared int
		var stored string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared FROM chat_session_lineage WHERE session_id=?`, session).Scan(&stored, &cont, &cleared); err != nil {
			return err
		}
		if stored != agent {
			return ErrAgentMismatch
		}
		if cleared != 0 || !cont.Valid {
			return nil
		}
		if _, err := s.get(ctx, tx, "scope='thread' AND thread_ref=?", me); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		cur := cont.String
		for hop := 0; hop < MaxLineageHops && cur != ""; hop++ {
			var rowAgent string
			var next sql.NullString
			var rowCleared int
			err := tx.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared FROM chat_session_lineage WHERE session_id=?`, cur).Scan(&rowAgent, &next, &rowCleared)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // never seen through the proxy: nothing to verify, nothing to carry
			}
			if err != nil {
				return err
			}
			if rowAgent != "" && rowAgent != agent {
				return ErrAgentMismatch
			}
			from := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: cur}.String()
			p, perr := s.get(ctx, tx, "scope='thread' AND thread_ref=?", from)
			if perr == nil {
				p.Revision++
				p.UpdatedAt = now.UnixMilli()
				if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET thread_ref=?,revision=?,updated_ns=? WHERE id=?`, me, p.Revision, now.UnixNano(), p.ID); err != nil {
					return err
				}
				p.ThreadRef = &me
				out = Carried{Moved: true, From: from, To: me, Plan: p}
				return nil
			}
			if !errors.Is(perr, ErrNotFound) {
				return perr
			}
			if rowCleared != 0 || !next.Valid {
				return nil
			}
			cur = next.String
		}
		return nil
	})
	if err != nil {
		return Carried{}, err
	}
	return out, nil
}

// recorded reports the fast path: the session is already in the lineage with its agent, so a
// later call has nothing to record or carry (a carry only ever happens on first sight).
func (s *Store) recorded(ctx context.Context, session string) bool {
	var agent string
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT agent_id FROM chat_session_lineage WHERE session_id=?`, session).Scan(&agent)
	return err == nil && agent != ""
}

func nullIf(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// Sessions lists every Tether session recorded in the lineage with its agent, for threads that
// have no inbox turn yet. Cleared-marker rows with no agent are left out.
func (s *Store) Sessions(ctx context.Context) []Stub {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT l.session_id,l.agent_id,l.seen_ns,
 EXISTS(SELECT 1 FROM chat_plans p WHERE p.scope='thread' AND p.thread_ref='tether:'||l.session_id)
 FROM chat_session_lineage l WHERE l.agent_id<>''`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []Stub
	for rows.Next() {
		var st Stub
		var id string
		if rows.Scan(&id, &st.Agent, &st.SeenNS, &st.HasPlan) == nil {
			st.Ref = "tether:" + id
			out = append(out, st)
		}
	}
	return out
}

// Clear deletes the thread's plan and, for a Tether thread, writes the clear marker on its
// lineage row so a later replacement never resurrects an older plan through it.
func (s *Store) Clear(ctx context.Context, thread string) error {
	now := s.now()
	return s.DB.Writer.Submit(ctx, "chat_plan_clear", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id IN (SELECT id FROM chat_plans WHERE scope='thread' AND thread_ref=?)`, thread); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plans WHERE scope='thread' AND thread_ref=?`, thread); err != nil {
			return err
		}
		if !strings.HasPrefix(thread, "tether:") {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns) VALUES(?,'',NULL,1,?)
 ON CONFLICT(session_id) DO UPDATE SET cleared=1`, sessionOf(thread), now.UnixNano())
		return err
	})
}
