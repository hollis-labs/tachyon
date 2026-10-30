package plannerassign

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

// Routes are relative to planner.Prefix; they match plannerapi.Routes for assignments and agent work.
var Routes = []string{
	"POST /assignments", "GET /assignments", "POST /assignments/{id}/complete", "POST /assignments/{id}/cancel",
	"GET /agents/{agent_id}/work",
}

// Actor is recorded on writes; the browser session is the only caller today.
const Actor = "operator:browser"

// Handler serves Routes. Mount each pattern under planner.Prefix behind the operator guard.
func (s *Service) Handler() http.Handler {
	m := http.NewServeMux()
	p := planner.Prefix
	m.HandleFunc("POST "+p+"/assignments", s.create)
	m.HandleFunc("GET "+p+"/assignments", s.list)
	m.HandleFunc("POST "+p+"/assignments/{id}/complete", s.end(StateCompleted))
	m.HandleFunc("POST "+p+"/assignments/{id}/cancel", s.end(StateCancelled))
	m.HandleFunc("GET "+p+"/agents/{agent_id}/work", s.work)
	return m
}

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status, code, msg := 500, "internal", "Assignment operation failed."
	switch {
	case errors.Is(err, ErrAssignee):
		status, code, msg = 404, "not_found", err.Error()
	case errors.Is(err, objects.ErrInvalid):
		status, code, msg = 400, "invalid", err.Error()
	case errors.Is(err, objects.ErrConflict):
		status, code, msg = 409, "conflict", "The assignment already ended or changed."
	case errors.Is(err, objects.ErrNotFound):
		status, code, msg = 404, "not_found", "Planner item or route not found."
	}
	send(w, status, map[string]string{"error": msg, "code": code})
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var c Create
	if err := d.Decode(&c); err != nil || d.Decode(&struct{}{}) != io.EOF {
		fail(w, objects.ErrInvalid)
		return
	}
	v, err := s.Create(r.Context(), c, Actor)
	if err != nil {
		fail(w, err)
		return
	}
	send(w, 201, v)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if k != "agent_id" && k != "target" && k != "state" {
			fail(w, objects.ErrInvalid)
			return
		}
	}
	items, err := s.List(r.Context(), Filter{Agent: q.Get("agent_id"), Target: q.Get("target"), State: q.Get("state")})
	if err != nil {
		fail(w, err)
		return
	}
	send(w, 200, map[string]any{"items": items})
}

func (s *Service) end(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.End(r.Context(), r.PathValue("id"), state, Actor)
		if err != nil {
			fail(w, err)
			return
		}
		send(w, 200, v)
	}
}

// WorkItem is one row of the chat Work panel. why is always "assigned" here; "session" and "referenced"
// need the Agents collector and decision links and arrive with PL3/PL5.
type WorkItem struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Links     []any  `json:"links"`
	NeedsUser bool   `json:"needs_user"`
	UpdatedAt string `json:"updated_at"`
	Why       string `json:"why"`
}

var workKinds = map[string]string{"themes": "theme", "decisions": "decision"}

func (s *Service) work(w http.ResponseWriter, r *http.Request) {
	items, err := s.Work(r.Context(), r.PathValue("agent_id"))
	if err != nil {
		fail(w, err)
		return
	}
	send(w, 200, map[string]any{"agent_id": r.PathValue("agent_id"), "items": items})
}

// Work lists the agent's active assignments as work items. Torque task targets are listed by id until
// PL3 can enrich them; targets that are not themes or decisions are left out.
func (s *Service) Work(ctx context.Context, agent string) ([]WorkItem, error) {
	asg, err := s.List(ctx, Filter{Agent: agent, State: StateActive})
	if err != nil {
		return nil, err
	}
	out := []WorkItem{}
	for _, a := range asg {
		switch {
		case a.Target.Ref != "":
			out = append(out, WorkItem{Kind: "task", ID: a.Target.Ref, Title: a.Target.Ref, Links: []any{}, UpdatedAt: a.Since, Why: "assigned"})
		case workKinds[a.Target.DB] != "":
			rec, err := s.Objects.Get(ctx, a.Target.ID)
			if err != nil || rec.Deleted() {
				continue
			}
			row := planner.View(rec)
			status, _ := row["status"].(string)
			out = append(out, WorkItem{Kind: workKinds[a.Target.DB], ID: rec.ID, Title: rec.Name, Status: status, Links: []any{}, UpdatedAt: rec.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"), Why: "assigned"})
		}
	}
	return out, nil
}
