// Package chatwork is the chat Work panel read model (T7): GET chat/threads/{ref}/work. It holds no data
// of its own. Rows come from the Planner's assignments for the thread's agent (PL2) and from the Torque
// tasks the thread's own plan links to, enriched through torqueread. needs_user mirrors the Planner's
// decision status; it is not recomputed here.
package chatwork

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/plannerassign"
	"github.com/hollis-labs/tachyon/internal/torqueread"
)

// Routes is mounted explicitly by the server under chatapi.Prefix.
var Routes = []string{"GET /threads/{ref}/work"}

const maxTaskLookups = 40

// Cite is a link on a row (chatapi Cite schema).
type Cite struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// Item is one Work panel row (chatapi Work schema).
type Item struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Progress  *float64 `json:"progress"`
	Links     []Cite   `json:"links"`
	NeedsUser bool     `json:"needs_user"`
	UpdatedAt int64    `json:"updated_at"`
	Why       string   `json:"why"`
}

// Result is the response body.
type Result struct {
	Items            []Item `json:"items"`
	Source           string `json:"source"`
	AssigneeResolved bool   `json:"assignee_resolved"`
}

// Service joins the thread's plan links with the agent's Planner assignments.
type Service struct {
	// Plan returns the thread's plan (chatplan.IsNotFound when none yet).
	Plan func(ctx context.Context, ref chatapi.ThreadRef) (chatplan.Plan, error)
	// Assigned lists the agent's active assignments; nil means assignments are unavailable.
	Assigned func(ctx context.Context, agent string) ([]plannerassign.WorkItem, error)
	// Task reads one Torque task; nil means Torque is unavailable (rows keep their id as title).
	Task func(ctx context.Context, id string) (torqueread.Task, error)
}

func ms(s string) int64 {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// Work builds the panel for a thread. Failures of the optional sources (Torque, assignments) leave the
// rows they would have enriched or added; they never fail the panel.
func (s *Service) Work(ctx context.Context, ref chatapi.ThreadRef, agent string) (Result, error) {
	out := Result{Items: []Item{}, Source: "planner", AssigneeResolved: true}
	seen := map[string]int{}
	lookups := 0
	add := func(it Item) {
		if _, ok := seen[it.Kind+":"+it.ID]; ok {
			return // assignments are added first, so "assigned" wins over a plan link
		}
		seen[it.Kind+":"+it.ID] = len(out.Items)
		out.Items = append(out.Items, it)
	}
	enrich := func(it *Item) {
		if it.Title == "" {
			it.Title = it.ID
		}
		it.Links = []Cite{{Kind: "task", Ref: it.ID, Label: it.Title}}
		if s.Task == nil || lookups >= maxTaskLookups {
			return
		}
		lookups++
		t, err := s.Task(ctx, it.ID)
		if err != nil {
			return
		}
		if t.Title != "" {
			it.Title = t.Title
		}
		it.Status = t.Status
		it.UpdatedAt = ms(t.UpdatedAt)
		it.Links[0].Label = it.Title
	}

	if s.Assigned != nil && agent != "" {
		rows, err := s.Assigned(ctx, agent)
		if err == nil {
			for _, r := range rows {
				it := Item{ID: r.ID, Kind: r.Kind, Title: r.Title, Status: r.Status, Links: []Cite{}, UpdatedAt: ms(r.UpdatedAt), Why: "assigned"}
				if r.Kind == "task" {
					enrich(&it)
				} else if r.Kind == "decision" {
					it.NeedsUser = r.NeedsUser || r.Status == "proposed"
				}
				add(it)
			}
		}
	}
	if s.Plan != nil {
		if p, err := s.Plan(ctx, ref); err == nil {
			done, total := 0, 0
			for _, st := range p.Steps {
				if st.Status == "cancelled" || st.Status == "skipped" {
					continue
				}
				total++
				if st.Status == "done" {
					done++
				}
			}
			if p.TaskRef != nil && *p.TaskRef != "" {
				it := Item{ID: *p.TaskRef, Kind: "task", Title: *p.TaskRef, Status: p.Status, UpdatedAt: p.UpdatedAt, Why: "plan"}
				enrich(&it)
				if total > 0 {
					f := float64(done) / float64(total)
					it.Progress = &f
				}
				add(it)
			}
			for _, st := range p.Steps {
				if st.TaskRef == nil || *st.TaskRef == "" {
					continue
				}
				it := Item{ID: *st.TaskRef, Kind: "task", Title: st.Title, Status: st.Status, UpdatedAt: st.UpdatedAt, Why: "plan"}
				enrich(&it)
				add(it)
			}
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.NeedsUser != b.NeedsUser {
			return a.NeedsUser
		}
		return a.UpdatedAt > b.UpdatedAt
	})
	return out, nil
}

// Handler serves the route; exists reports whether the thread is known (nil error).
func Handler(s *Service, exists func(ctx context.Context, ref string) error) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET "+chatapi.Prefix+"/threads/{ref}/work", func(w http.ResponseWriter, r *http.Request) {
		ref, err := chatapi.ParseRef(r.PathValue("ref"))
		if err != nil {
			reply(w, 400, map[string]string{"code": "invalid", "message": err.Error()})
			return
		}
		if exists != nil {
			if err := exists(r.Context(), ref.String()); err != nil {
				reply(w, 404, map[string]string{"code": "not_found", "message": "That chat was not found."})
				return
			}
		}
		res, err := s.Work(r.Context(), ref, r.URL.Query().Get("agent"))
		if err != nil {
			reply(w, 500, map[string]string{"code": "internal", "message": "The work list could not be built."})
			return
		}
		reply(w, 200, res)
	})
	return m
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
