package chatplan

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are mounted explicitly by the server under chatapi.Prefix, each behind RequireOperator.
var Routes = []string{
	"GET /threads/{ref}/plan", "PUT /threads/{ref}/plan",
	"POST /threads/{ref}/plan/steps", "PATCH /threads/{ref}/plan/steps/{id}", "POST /threads/{ref}/plan/reorder",
}

type Handler struct{ Service *Service }

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, map[string]string{"code": code, "message": msg})
}

func problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "not_found", "No such plan, step or thread.")
	case errors.Is(err, ErrConflict):
		fail(w, 409, "conflict", "The plan changed since you loaded it. Reload and retry.")
	case errors.Is(err, ErrLinked):
		fail(w, 409, "linked_step", "This step is linked to a task and follows that task's status. You can park it (blocked).")
	case errors.Is(err, ErrNoProject):
		fail(w, 409, "no_project", "This chat has no project, so there is no project plan.")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid", "Check the plan: titles are required (200 characters), at most 100 steps, known statuses and step ids.")
	default:
		fail(w, 500, "internal", "The plan could not be saved.")
	}
}

func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	hs := map[string]http.HandlerFunc{
		"GET /threads/{ref}/plan": h.get, "PUT /threads/{ref}/plan": h.put,
		"POST /threads/{ref}/plan/steps": h.addStep, "PATCH /threads/{ref}/plan/steps/{id}": h.patchStep, "POST /threads/{ref}/plan/reorder": h.reorder,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, hs[r])
	}
	return m
}

func ref(w http.ResponseWriter, r *http.Request) (chatapi.ThreadRef, bool) {
	t, err := chatapi.ParseRef(r.PathValue("ref"))
	if err != nil {
		fail(w, 400, "invalid", err.Error())
	}
	return t, err == nil
}

func scopeQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := r.URL.Query().Get("scope")
	if s != "" && s != "thread" && s != "project" {
		fail(w, 400, "invalid", "scope must be thread or project.")
		return "", false
	}
	return s, true
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	scope, ok := scopeQuery(w, r)
	if !ok {
		return
	}
	p, err := h.Service.Get(r.Context(), t, scope)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, p)
}

type stepJSON struct {
	ID         string  `json:"id"`
	Title      *string `json:"title"`
	Status     *string `json:"status"`
	Acceptance *string `json:"acceptance"`
	Notes      *string `json:"notes"`
	Position   *int    `json:"position"`
	// Accepted and ignored so one StepWrite shape serves every route.
	Expected *int64 `json:"expected_revision"`
}

func (s stepJSON) in() StepIn {
	return StepIn{ID: s.ID, Title: s.Title, Status: s.Status, Acceptance: s.Acceptance, Notes: s.Notes, Position: s.Position}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid", "Send a JSON body with the documented fields.")
		return false
	}
	return true
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		fail(w, 400, "invalid", "Send a JSON body with the documented fields.")
		return
	}
	var (
		scope, id, status string
		title             *string
		steps             []stepJSON
		expected          *int64
	)
	in := WriteIn{By: "user"}
	for k, v := range raw {
		var err error
		switch k {
		case "scope":
			err = json.Unmarshal(v, &scope)
		case "id":
			err = json.Unmarshal(v, &id)
		case "status":
			err = json.Unmarshal(v, &status)
		case "title":
			in.HasTitle = true
			if string(v) != "null" {
				var s string
				err = json.Unmarshal(v, &s)
				title = &s
			}
		case "steps":
			err = json.Unmarshal(v, &steps)
		case "expected_revision":
			var n int64
			err = json.Unmarshal(v, &n)
			expected = &n
		default:
			err = ErrInvalid
		}
		if err != nil {
			fail(w, 400, "invalid", "Invalid "+k+".")
			return
		}
	}
	if scope != "thread" && scope != "project" || expected == nil || *expected < 0 {
		fail(w, 400, "invalid", "scope (thread or project) and expected_revision are required.")
		return
	}
	in.Title, in.Status, in.Expected = title, status, *expected
	for _, s := range steps {
		in.Steps = append(in.Steps, s.in())
	}
	if id != "" { // an id must name the plan being replaced
		cur, err := h.Service.Get(r.Context(), t, scope)
		if err != nil || cur.ID != id {
			if err == nil || errors.Is(err, ErrNotFound) {
				err = ErrConflict
			}
			problem(w, err)
			return
		}
	}
	p, err := h.Service.Write(r.Context(), t, scope, in)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, p)
}

func (h *Handler) addStep(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	scope, ok := scopeQuery(w, r)
	if !ok {
		return
	}
	var in stepJSON
	if !decode(w, r, &in) {
		return
	}
	if in.Expected == nil {
		fail(w, 400, "invalid", "expected_revision is required.")
		return
	}
	_, st, err := h.Service.AddStep(r.Context(), t, scope, in.in(), *in.Expected, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 201, st)
}

func (h *Handler) patchStep(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	scope, ok := scopeQuery(w, r)
	if !ok {
		return
	}
	var in stepJSON
	if !decode(w, r, &in) {
		return
	}
	if in.Expected == nil {
		fail(w, 400, "invalid", "expected_revision is required.")
		return
	}
	_, st, err := h.Service.PatchStep(r.Context(), t, scope, r.PathValue("id"), in.in(), *in.Expected, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, st)
}

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	scope, ok := scopeQuery(w, r)
	if !ok {
		return
	}
	var in struct {
		StepIDs  []string `json:"step_ids"`
		Expected *int64   `json:"expected_revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Expected == nil || in.StepIDs == nil {
		fail(w, 400, "invalid", "step_ids and expected_revision are required.")
		return
	}
	p, err := h.Service.Reorder(r.Context(), t, scope, in.StepIDs, *in.Expected, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, p)
}
