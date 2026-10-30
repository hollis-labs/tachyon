// Package chatplan holds a chat thread's working plan (and loose todos): Tachyon rows shaped
// to Torque so a plan or step can graduate to a task. Status sync from Torque is one-way.
package chatplan

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	MaxSteps      = 100
	MaxTitle      = 200
	MaxAcceptance = 1000
	MaxNotes      = 2000
)

var (
	ErrNotFound  = errors.New("no such plan or step")
	ErrConflict  = errors.New("plan revision conflict")
	ErrInvalid   = errors.New("invalid plan")
	ErrLinked    = errors.New("a step linked to a task takes its status from the task")
	ErrNoProject = errors.New("this chat has no project")
)

var (
	planStatuses = map[string]bool{"proposed": true, "approved": true, "active": true, "done": true, "cancelled": true}
	stepStatuses = map[string]bool{"todo": true, "doing": true, "blocked": true, "done": true, "cancelled": true}
)

type Step struct {
	ID           string  `json:"id"`
	Position     int     `json:"position"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Acceptance   string  `json:"acceptance"`
	TaskRef      *string `json:"task_ref"`
	Notes        string  `json:"notes"`
	UpdatedAt    int64   `json:"updated_at"`
	PlanRevision int64   `json:"plan_revision,omitempty"`
}

type Plan struct {
	ID        string  `json:"id"`
	Scope     string  `json:"scope"`
	ThreadRef *string `json:"thread_ref"`
	ProjectID *string `json:"project_id"`
	Title     *string `json:"title"`
	Status    string  `json:"status"`
	Revision  int64   `json:"revision"`
	TaskRef   *string `json:"task_ref"`
	CreatedBy string  `json:"created_by"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
	Steps     []Step  `json:"steps"`
}

// Progress counts steps for the thread list: cancelled steps are not part of the total.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

func (p Plan) Progress() Progress {
	var out Progress
	for _, s := range p.Steps {
		if s.Status == "cancelled" {
			continue
		}
		out.Total++
		if s.Status == "done" {
			out.Done++
		}
	}
	return out
}

// StepIn is one step of a whole-plan write or a step patch. Nil fields are untouched on
// patch and defaulted on create. TaskRef is deliberately absent: only linking (the Dispatch
// card) and the Torque sync set it.
type StepIn struct {
	ID         string
	Title      *string
	Status     *string
	Acceptance *string
	Notes      *string
	Position   *int
}

// WriteIn is a whole-plan upsert for a scope owner (a thread or a project).
type WriteIn struct {
	Title    *string // nil keeps; "" clears (loose todos)
	HasTitle bool
	Status   string // "" keeps (or "proposed" on create)
	Steps    []StepIn
	// Expected is the revision the writer last saw; 0 means "there is no plan yet".
	Expected int64
	By       string // "user" | "agent"
}

type Store struct {
	DB  *localstore.Store
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

const planColumns = `id,scope,thread_ref,project_id,title,status,revision,task_ref,created_by,created_ns,updated_ns`

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanPlan(row interface{ Scan(...any) error }) (Plan, error) {
	var p Plan
	var thread, project, title, task sql.NullString
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Scope, &thread, &project, &title, &p.Status, &p.Revision, &task, &p.CreatedBy, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Plan{}, ErrNotFound
		}
		return Plan{}, err
	}
	if thread.Valid {
		p.ThreadRef = &thread.String
	}
	if project.Valid {
		p.ProjectID = &project.String
	}
	if title.Valid {
		p.Title = &title.String
	}
	if task.Valid {
		p.TaskRef = &task.String
	}
	p.CreatedAt, p.UpdatedAt = created/1e6, updated/1e6
	p.Steps = []Step{}
	return p, nil
}

func loadSteps(ctx context.Context, q querier, p *Plan) error {
	rows, err := q.QueryContext(ctx, `SELECT id,position,title,status,acceptance,task_ref,notes,updated_ns FROM chat_plan_steps WHERE plan_id=? ORDER BY position,id`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		var task sql.NullString
		var ns int64
		if err := rows.Scan(&st.ID, &st.Position, &st.Title, &st.Status, &st.Acceptance, &task, &st.Notes, &ns); err != nil {
			return err
		}
		if task.Valid {
			st.TaskRef = &task.String
		}
		st.UpdatedAt = ns / 1e6
		p.Steps = append(p.Steps, st)
	}
	return rows.Err()
}

func (s *Store) get(ctx context.Context, q querier, where string, arg string) (Plan, error) {
	p, err := scanPlan(q.QueryRowContext(ctx, `SELECT `+planColumns+` FROM chat_plans WHERE `+where, arg))
	if err != nil {
		return Plan{}, err
	}
	return p, loadSteps(ctx, q, &p)
}

// ForThread returns the thread's own plan.
func (s *Store) ForThread(ctx context.Context, thread string) (Plan, error) {
	return s.get(ctx, s.DB.Reader, "scope='thread' AND thread_ref=?", thread)
}

// ForProject returns the project's shared plan.
func (s *Store) ForProject(ctx context.Context, project string) (Plan, error) {
	return s.get(ctx, s.DB.Reader, "scope='project' AND project_id=?", project)
}

func (s *Store) Get(ctx context.Context, id string) (Plan, error) {
	return s.get(ctx, s.DB.Reader, "id=?", id)
}

// Progress maps thread_ref to the progress of that thread's plan, for the thread list.
func (s *Store) Progress(ctx context.Context, refs []string) map[string]Progress {
	out := map[string]Progress{}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT p.thread_ref,
 COALESCE(SUM(CASE WHEN st.status<>'cancelled' THEN 1 ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN st.status='done' THEN 1 ELSE 0 END),0)
 FROM chat_plans p LEFT JOIN chat_plan_steps st ON st.plan_id=p.id
 WHERE p.scope='thread' AND p.status<>'cancelled' GROUP BY p.id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		var pr Progress
		if rows.Scan(&ref, &pr.Total, &pr.Done) == nil {
			out[ref] = pr
		}
	}
	return out
}

func clean(v *string, max int) (string, bool) {
	if v == nil {
		return "", true
	}
	t := strings.TrimSpace(*v)
	return t, len(t) <= max
}

// Write upserts the whole plan of one owner. owner is ("thread", ref) or ("project", id).
// The plan is created when none exists (Expected must be 0), otherwise Expected must equal
// the current revision. Steps with a known id keep their task link and, when linked, their
// status (except parking); steps missing from the write are removed.
func (s *Store) Write(ctx context.Context, scope, owner string, in WriteIn) (Plan, error) {
	if (scope != "thread" && scope != "project") || owner == "" || len(in.Steps) > MaxSteps || (in.Status != "" && !planStatuses[in.Status]) || (in.By != "user" && in.By != "agent") {
		return Plan{}, ErrInvalid
	}
	if in.HasTitle && in.Title != nil {
		if _, ok := clean(in.Title, MaxTitle); !ok {
			return Plan{}, ErrInvalid
		}
	}
	now := s.now()
	var out Plan
	err := s.DB.Writer.Submit(ctx, "chat_plan_write", func(ctx context.Context, tx *sql.Tx) error {
		col := "thread_ref"
		if scope == "project" {
			col = "project_id"
		}
		cur, err := s.get(ctx, tx, "scope='"+scope+"' AND "+col+"=?", owner)
		exists := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if exists && in.Expected != cur.Revision || !exists && in.Expected != 0 {
			return ErrConflict
		}
		if !exists {
			cur = Plan{ID: "pl_" + uuid.NewString(), Scope: scope, Status: "proposed", CreatedBy: in.By, CreatedAt: now.UnixMilli()}
			if scope == "thread" {
				cur.ThreadRef = &owner
			} else {
				cur.ProjectID = &owner
			}
		}
		if in.HasTitle {
			t, _ := clean(in.Title, MaxTitle)
			if t == "" {
				cur.Title = nil
			} else {
				cur.Title = &t
			}
		}
		// An agent can propose and revise, never approve its own plan.
		if in.Status != "" && (in.By == "user" || in.Status == "proposed" || in.Status == "cancelled") {
			cur.Status = in.Status
		}
		old := map[string]Step{}
		for _, st := range cur.Steps {
			old[st.ID] = st
		}
		steps := make([]Step, 0, len(in.Steps))
		seen := map[string]bool{}
		for i, si := range in.Steps {
			prev, known := old[si.ID]
			if si.ID != "" && (!known || seen[si.ID]) {
				return ErrInvalid // an unknown or repeated step id is a client bug, not a new step
			}
			st := Step{ID: si.ID, Position: i + 1, Status: "todo", UpdatedAt: now.UnixMilli()}
			if known {
				st = prev
				st.Position, st.UpdatedAt = i+1, now.UnixMilli()
			} else {
				st.ID = "st_" + uuid.NewString()
			}
			seen[st.ID] = true
			if si.Title != nil {
				t, ok := clean(si.Title, MaxTitle)
				if !ok {
					return ErrInvalid
				}
				st.Title = t
			}
			if st.Title == "" {
				return ErrInvalid
			}
			if a, ok := clean(si.Acceptance, MaxAcceptance); !ok {
				return ErrInvalid
			} else if si.Acceptance != nil {
				st.Acceptance = a
			}
			if n, ok := clean(si.Notes, MaxNotes); !ok {
				return ErrInvalid
			} else if si.Notes != nil {
				st.Notes = n
			}
			if si.Status != nil {
				if err := setStatus(&st, *si.Status); err != nil {
					return err
				}
			}
			steps = append(steps, st)
		}
		cur.Steps = steps
		cur.Revision++
		cur.UpdatedAt = now.UnixMilli()
		if exists {
			if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET title=?,status=?,revision=?,updated_ns=? WHERE id=?`, cur.Title, cur.Status, cur.Revision, now.UnixNano(), cur.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id=?`, cur.ID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO chat_plans(`+planColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			cur.ID, cur.Scope, cur.ThreadRef, cur.ProjectID, cur.Title, cur.Status, cur.Revision, cur.TaskRef, cur.CreatedBy, now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		for _, st := range cur.Steps {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chat_plan_steps(plan_id,id,position,title,status,acceptance,task_ref,notes,updated_ns) VALUES(?,?,?,?,?,?,?,?,?)`,
				cur.ID, st.ID, st.Position, st.Title, st.Status, st.Acceptance, st.TaskRef, st.Notes, now.UnixNano()); err != nil {
				return err
			}
		}
		out = cur
		return nil
	})
	return out, err
}

// setStatus applies a step status. A step linked to a Torque task takes its status from
// the task; the only change allowed by hand is parking it (blocked) and unparking (todo).
func setStatus(st *Step, to string) error {
	if !stepStatuses[to] {
		return ErrInvalid
	}
	if st.TaskRef != nil && to != st.Status && !(to == "blocked" || (st.Status == "blocked" && to == "todo")) {
		return ErrLinked
	}
	st.Status = to
	return nil
}

// withPlan runs fn on the plan holding the step inside the writer, enforcing the revision.
func (s *Store) mutate(ctx context.Context, planID string, expected int64, fn func(*Plan, time.Time) error) (Plan, error) {
	now := s.now()
	var out Plan
	err := s.DB.Writer.Submit(ctx, "chat_plan_mutate", func(ctx context.Context, tx *sql.Tx) error {
		p, err := s.get(ctx, tx, "id=?", planID)
		if err != nil {
			return err
		}
		if p.Revision != expected {
			return ErrConflict
		}
		if err := fn(&p, now); err != nil {
			return err
		}
		p.Revision++
		p.UpdatedAt = now.UnixMilli()
		if _, err := tx.ExecContext(ctx, `UPDATE chat_plans SET title=?,status=?,revision=?,task_ref=?,updated_ns=? WHERE id=?`, p.Title, p.Status, p.Revision, p.TaskRef, now.UnixNano(), p.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id=?`, p.ID); err != nil {
			return err
		}
		for i := range p.Steps {
			st := &p.Steps[i]
			st.Position = i + 1
			if _, err := tx.ExecContext(ctx, `INSERT INTO chat_plan_steps(plan_id,id,position,title,status,acceptance,task_ref,notes,updated_ns) VALUES(?,?,?,?,?,?,?,?,?)`,
				p.ID, st.ID, st.Position, st.Title, st.Status, st.Acceptance, st.TaskRef, st.Notes, st.UpdatedAt*1e6); err != nil {
				return err
			}
		}
		out = p
		return nil
	})
	return out, err
}

// PatchStep changes one step; the plan revision must match and increases by one.
func (s *Store) PatchStep(ctx context.Context, planID, stepID string, in StepIn, expected int64) (Plan, Step, error) {
	var stepOut Step
	p, err := s.mutate(ctx, planID, expected, func(p *Plan, now time.Time) error {
		for i := range p.Steps {
			st := &p.Steps[i]
			if st.ID != stepID {
				continue
			}
			if in.Title != nil {
				t, ok := clean(in.Title, MaxTitle)
				if !ok || t == "" {
					return ErrInvalid
				}
				st.Title = t
			}
			if in.Acceptance != nil {
				a, ok := clean(in.Acceptance, MaxAcceptance)
				if !ok {
					return ErrInvalid
				}
				st.Acceptance = a
			}
			if in.Notes != nil {
				n, ok := clean(in.Notes, MaxNotes)
				if !ok {
					return ErrInvalid
				}
				st.Notes = n
			}
			if in.Status != nil {
				if err := setStatus(st, *in.Status); err != nil {
					return err
				}
			}
			st.UpdatedAt = now.UnixMilli()
			if in.Position != nil {
				moveTo(p, i, *in.Position)
			}
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return Plan{}, Step{}, err
	}
	for _, st := range p.Steps {
		if st.ID == stepID {
			stepOut = st
			stepOut.PlanRevision = p.Revision
		}
	}
	return p, stepOut, nil
}

func moveTo(p *Plan, from, pos int) {
	if pos < 1 {
		pos = 1
	}
	if pos > len(p.Steps) {
		pos = len(p.Steps)
	}
	st := p.Steps[from]
	rest := append(append([]Step{}, p.Steps[:from]...), p.Steps[from+1:]...)
	p.Steps = append(append(append([]Step{}, rest[:pos-1]...), st), rest[pos-1:]...)
}

// AddStep appends a step (or inserts at Position).
func (s *Store) AddStep(ctx context.Context, planID string, in StepIn, expected int64) (Plan, Step, error) {
	var newID string
	p, err := s.mutate(ctx, planID, expected, func(p *Plan, now time.Time) error {
		if len(p.Steps) >= MaxSteps || in.Title == nil {
			return ErrInvalid
		}
		t, ok := clean(in.Title, MaxTitle)
		if !ok || t == "" {
			return ErrInvalid
		}
		a, ok1 := clean(in.Acceptance, MaxAcceptance)
		n, ok2 := clean(in.Notes, MaxNotes)
		if !ok1 || !ok2 {
			return ErrInvalid
		}
		st := Step{ID: "st_" + uuid.NewString(), Title: t, Status: "todo", Acceptance: a, Notes: n, UpdatedAt: now.UnixMilli()}
		if in.Status != nil {
			if !stepStatuses[*in.Status] {
				return ErrInvalid
			}
			st.Status = *in.Status
		}
		newID = st.ID
		p.Steps = append(p.Steps, st)
		if in.Position != nil {
			moveTo(p, len(p.Steps)-1, *in.Position)
		}
		return nil
	})
	if err != nil {
		return Plan{}, Step{}, err
	}
	for _, st := range p.Steps {
		if st.ID == newID {
			st.PlanRevision = p.Revision
			return p, st, nil
		}
	}
	return p, Step{}, ErrNotFound
}

// Reorder sets the step order; ids must be exactly the plan's steps.
func (s *Store) Reorder(ctx context.Context, planID string, ids []string, expected int64) (Plan, error) {
	return s.mutate(ctx, planID, expected, func(p *Plan, _ time.Time) error {
		if len(ids) != len(p.Steps) {
			return ErrInvalid
		}
		by := map[string]Step{}
		for _, st := range p.Steps {
			by[st.ID] = st
		}
		next := make([]Step, 0, len(ids))
		for _, id := range ids {
			st, ok := by[id]
			if !ok {
				return ErrInvalid
			}
			delete(by, id)
			next = append(next, st)
		}
		p.Steps = next
		return nil
	})
}

// SetStatus changes the plan's own status (approval by a card, or an operator action).
func (s *Store) SetStatus(ctx context.Context, planID, status string, expected int64) (Plan, error) {
	if !planStatuses[status] {
		return Plan{}, ErrInvalid
	}
	return s.mutate(ctx, planID, expected, func(p *Plan, _ time.Time) error { p.Status = status; return nil })
}

// LinkStep records the Torque task a step was dispatched to. Only the Dispatch card calls it.
func (s *Store) LinkStep(ctx context.Context, planID, stepID, taskRef string, expected int64) (Plan, error) {
	return s.mutate(ctx, planID, expected, func(p *Plan, now time.Time) error {
		for i := range p.Steps {
			if p.Steps[i].ID == stepID {
				p.Steps[i].TaskRef, p.Steps[i].UpdatedAt = &taskRef, now.UnixMilli()
				return nil
			}
		}
		return ErrNotFound
	})
}

// PurgeThread deletes the thread's plan (the explicit thread-delete path).
func (s *Store) PurgeThread(ctx context.Context, thread string) error {
	return s.DB.Writer.Submit(ctx, "chat_plan_purge", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_plan_steps WHERE plan_id IN (SELECT id FROM chat_plans WHERE scope='thread' AND thread_ref=?)`, thread); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM chat_plans WHERE scope='thread' AND thread_ref=?`, thread)
		return err
	})
}
