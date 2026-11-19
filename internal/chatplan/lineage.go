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

// ErrAssociationConflict means a session was already bound to a different parent or agent.
var ErrAssociationConflict = errors.New("continuation association conflicts with an existing session")

// Association is the durable result of associating one exact replacement with its parent.
// Replayed reports an already committed pair; a replay never moves a plan again.
type Association struct {
	Source, Replacement, Agent string
	Replayed                   bool
	PlanMoved                  bool
	Carried                    Carried
}

// Carried is the outcome of Carry. Moved is false when there was nothing to carry.
type Carried struct {
	Moved bool
	From  string // thread ref the plan was moved from
	To    string // thread ref it now lives under
	Plan  Plan   // the first plan moved
	Plans []Plan // every plan moved, in order
}

// Stub is a Tether session known from the lineage record, whether or not it has inbox turns yet.
type Stub struct {
	Ref     string
	Agent   string
	SeenNS  int64
	HasPlan bool
}

func sessionOf(ref string) string { return strings.TrimPrefix(ref, "tether:") }

// continueDefault is the Continue value a session's lineage row takes when it is first written:
// the "Default for Continue on new sessions" chat setting, on when unset.
func (s *Store) continueDefault(ctx context.Context) int {
	if s.ContinueDefault == nil || s.ContinueDefault(ctx) {
		return 1
	}
	return 0
}

// Carry records the session in the lineage (first sight wins) and, when it has no plan of its own,
// moves the nearest ancestor's plan to it. The ancestor chain is the one Tether reported
// (continues), walked through the lineage rows; every recorded hop must belong to the same
// logical agent. A session with no continues, a cleared session, an ancestor whose Continue is off,
// or an unrecorded ancestor carries nothing. The move is one transaction: one live plan, revision bumped once.
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
	on := s.continueDefault(ctx)
	err := s.DB.Writer.Submit(ctx, "chat_plan_carry", func(ctx context.Context, tx *sql.Tx) error {
		out = Carried{}
		var cont sql.NullString
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on) VALUES(?,?,?,0,?,?)
 ON CONFLICT(session_id) DO UPDATE SET agent_id=CASE WHEN agent_id='' THEN excluded.agent_id ELSE agent_id END,
 continues_id=CASE WHEN continues_id IS NULL THEN excluded.continues_id ELSE continues_id END`,
			session, agent, nullIf(continues), now.UnixNano(), on); err != nil {
			return err
		}
		var cleared int
		if err := tx.QueryRowContext(ctx, `SELECT continues_id,cleared FROM chat_session_lineage WHERE session_id=?`, session).Scan(&cont, &cleared); err != nil {
			return err
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
			var rowCleared, rowOn int
			err := tx.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared,continue_on FROM chat_session_lineage WHERE session_id=?`, cur).Scan(&rowAgent, &next, &rowCleared, &rowOn)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // never seen through the proxy: nothing to verify, nothing to carry
			}
			if err != nil {
				return err
			}
			if rowAgent == "" {
				return nil // a clear marker with no agent: the hop cannot be verified, so carry nothing
			}
			if rowAgent != agent {
				return ErrAgentMismatch
			}
			if rowOn == 0 {
				return nil // the operator turned Continue off for this session: its replacement starts fresh
			}
			from := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: cur}.String()
			plans, perr := s.plansOf(ctx, tx, from)
			if perr != nil {
				return perr
			}
			if len(plans) > 0 {
				// All of the ancestor's plans move together, in their order.
				for i := range plans {
					p := &plans[i]
					p.Revision++
					p.UpdatedAt = now.UnixMilli()
					if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET thread_ref=?,revision=?,updated_ns=? WHERE id=?`, me, p.Revision, now.UnixNano(), p.ID); err != nil {
						return err
					}
					p.ThreadRef = &me
				}
				out = Carried{Moved: true, From: from, To: me, Plan: plans[0], Plans: plans}
				return nil
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

// AssociateContinuation commits an externally verified, exact continuation.
// The caller must verify Tether session rows and, for workers, native admission.
// A replacement has one immutable parent and agent, but it may later be a source.
// Source seeding, replacement binding and plan movement share one transaction.
func (s *Store) AssociateContinuation(ctx context.Context, source, replacement, agent string) (Association, error) {
	out := Association{Source: source, Replacement: replacement, Agent: agent}
	if source == "" || replacement == "" || source == replacement || agent == "" {
		return Association{}, ErrInvalid
	}
	now := s.now()
	on := s.continueDefault(ctx)
	err := s.DB.Writer.Submit(ctx, "chat_continuation_associate", func(ctx context.Context, tx *sql.Tx) error {
		out.Carried = Carried{}
		out.Replayed = false
		out.PlanMoved = false
		var receiptSource, receiptAgent string
		var receiptMoved int
		receiptErr := tx.QueryRowContext(ctx, `SELECT source_session_id,agent_id,plan_moved FROM chat_continuation_associations WHERE replacement_session_id=?`, replacement).Scan(&receiptSource, &receiptAgent, &receiptMoved)
		if receiptErr == nil {
			if receiptSource != source || receiptAgent != agent {
				return ErrAssociationConflict
			}
			out.Replayed, out.PlanMoved = true, receiptMoved != 0
			return nil
		}
		if !errors.Is(receiptErr, sql.ErrNoRows) {
			return receiptErr
		}
		// Preserve an operator's Continue choice or clear marker, including a
		// marker that was written before the first proxied request.
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on)
 VALUES(?,?,NULL,0,?,?) ON CONFLICT(session_id) DO NOTHING`, source, agent, now.UnixNano(), on); err != nil {
			return err
		}
		var sourceAgent string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM chat_session_lineage WHERE session_id=?`, source).Scan(&sourceAgent); err != nil {
			return err
		}
		if sourceAgent != "" && sourceAgent != agent {
			return ErrAgentMismatch
		}
		if sourceAgent == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE chat_session_lineage SET agent_id=? WHERE session_id=?`, agent, source); err != nil {
				return err
			}
		}
		var rowAgent string
		var rowParent sql.NullString
		var cleared int
		err := tx.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared FROM chat_session_lineage WHERE session_id=?`, replacement).Scan(&rowAgent, &rowParent, &cleared)
		if err == nil {
			if cleared != 0 || rowAgent != "" && rowAgent != agent || rowParent.Valid && rowParent.String != source {
				return ErrAssociationConflict
			}
			if rowAgent == agent && rowParent.Valid && rowParent.String == source {
				// A proxy may have recorded the same pair before this eager receipt.
			} else if rowAgent != "" || rowParent.Valid {
				return ErrAssociationConflict
			} else {
				// A Continue preference pinned an agent-less row first.
				_, err = tx.ExecContext(ctx, `UPDATE chat_session_lineage SET agent_id=?,continues_id=? WHERE session_id=?`, agent, source, replacement)
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on)
 VALUES(?,?,?,0,?,?)`, replacement, agent, source, now.UnixNano(), on)
		}
		if err != nil {
			return err
		}
		me := "tether:" + replacement
		if _, err := s.get(ctx, tx, "scope='thread' AND thread_ref=?", me); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		} else if errors.Is(err, ErrNotFound) {
			// Match Carry: the nearest eligible, same-agent ancestor owns the plan.
			// A planless immediate source must not permanently receipt a no-move.
			cur := source
			for hop := 0; hop < MaxLineageHops && cur != ""; hop++ {
				var rowAgent string
				var next sql.NullString
				var rowCleared, rowOn int
				err := tx.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared,continue_on FROM chat_session_lineage WHERE session_id=?`, cur).Scan(&rowAgent, &next, &rowCleared, &rowOn)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					return err
				}
				if rowAgent == "" || rowOn == 0 {
					break
				}
				if rowAgent != agent {
					return ErrAgentMismatch
				}
				if cur == source && rowCleared != 0 {
					break // preserve the source clear fence
				}
				from := "tether:" + cur
				plans, err := s.plansOf(ctx, tx, from)
				if err != nil {
					return err
				}
				if len(plans) > 0 {
					for i := range plans {
						p := &plans[i]
						p.Revision++
						p.UpdatedAt = now.UnixMilli()
						if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET thread_ref=?,revision=?,updated_ns=? WHERE id=?`, me, p.Revision, now.UnixNano(), p.ID); err != nil {
							return err
						}
						p.ThreadRef = &me
					}
					out.Carried = Carried{Moved: true, From: from, To: me, Plan: plans[0], Plans: plans}
					out.PlanMoved = true
					break
				}
				if rowCleared != 0 || !next.Valid {
					break
				}
				cur = next.String
			}
		}
		moved := 0
		if out.PlanMoved {
			moved = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_continuation_associations(replacement_session_id,source_session_id,agent_id,plan_moved,committed_ns) VALUES(?,?,?,?,?)`, replacement, source, agent, moved, now.UnixNano())
		return err
	})
	if err != nil {
		return Association{}, err
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
	on := s.continueDefault(ctx)
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
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on) VALUES(?,'',NULL,1,?,?)
 ON CONFLICT(session_id) DO UPDATE SET cleared=1`, sessionOf(thread), now.UnixNano(), on)
		return err
	})
}

// Continue reports the session's Continue flag. A session with no lineage row yet gets one now,
// with agent ” (like a clear marker, so a walk cannot pass through it before Tether names the
// agent) and the current default: first sight wins, so a later change of the default never
// flips a session the operator has already seen.
func (s *Store) Continue(ctx context.Context, session string) (bool, error) {
	if session == "" {
		return false, ErrInvalid
	}
	var on int
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT continue_on FROM chat_session_lineage WHERE session_id=?`, session).Scan(&on)
	if err == nil {
		return on != 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	def := s.continueDefault(ctx)
	now := s.now()
	err = s.DB.Writer.Submit(ctx, "chat_session_continue_pin", func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on) VALUES(?,'',NULL,0,?,?)
 ON CONFLICT(session_id) DO NOTHING`, session, now.UnixNano(), def); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, `SELECT continue_on FROM chat_session_lineage WHERE session_id=?`, session).Scan(&on)
	})
	return on != 0, err
}

// SetContinue sets the session's Continue flag; the lineage and any plan are otherwise untouched.
func (s *Store) SetContinue(ctx context.Context, session string, on bool) error {
	if session == "" {
		return ErrInvalid
	}
	v := 0
	if on {
		v = 1
	}
	now := s.now()
	return s.DB.Writer.Submit(ctx, "chat_session_continue_set", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_session_lineage(session_id,agent_id,continues_id,cleared,seen_ns,continue_on) VALUES(?,'',NULL,0,?,?)
 ON CONFLICT(session_id) DO UPDATE SET continue_on=excluded.continue_on`, session, now.UnixNano(), v)
		return err
	})
}

// Ancestors returns the sessions whose conversation the session continues, oldest first. It walks
// the same chain Carry does and stops where Carry would: at a cleared session (the operator's "new
// conversation"), at a session with Continue off, at an unrecorded or agent-less hop, or at a hop
// of another logical agent. A cleared or unrecorded session itself has none.
func (s *Store) Ancestors(ctx context.Context, session string) []string {
	var agent string
	var cont sql.NullString
	var cleared int
	if err := s.DB.Reader.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared FROM chat_session_lineage WHERE session_id=?`, session).Scan(&agent, &cont, &cleared); err != nil || agent == "" || cleared != 0 {
		return nil
	}
	var chain []string
	cur := cont.String
	for hop := 0; hop < MaxLineageHops && cur != "" && cur != session; hop++ {
		var rowAgent string
		var next sql.NullString
		var rowCleared, rowOn int
		if err := s.DB.Reader.QueryRowContext(ctx, `SELECT agent_id,continues_id,cleared,continue_on FROM chat_session_lineage WHERE session_id=?`, cur).Scan(&rowAgent, &next, &rowCleared, &rowOn); err != nil || rowAgent != agent || rowOn == 0 {
			break
		}
		chain = append(chain, cur)
		if rowCleared != 0 || !next.Valid {
			break
		}
		cur = next.String
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}
