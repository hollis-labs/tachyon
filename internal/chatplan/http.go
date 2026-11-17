package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are mounted explicitly by the server under chatapi.Prefix, each behind RequireOperator.
var Routes = []string{
	"GET /threads/{ref}/plan", "PUT /threads/{ref}/plan", "DELETE /threads/{ref}/plan",
	"POST /threads/{ref}/plan/steps", "PATCH /threads/{ref}/plan/steps/{id}", "POST /threads/{ref}/plan/reorder",
	"GET /threads/{ref}/continue", "PUT /threads/{ref}/continue",
	"POST /continuations",
	"GET /threads/{ref}/plans", "POST /threads/{ref}/plans", "POST /threads/{ref}/plans/reorder", "DELETE /threads/{ref}/plans/{id}",
	"POST /threads/{ref}/plans/{id}/steps/{step}/move",
	"POST /threads/{ref}/plans/{plan}/steps", "PATCH /threads/{ref}/plans/{plan}/steps/{id}",
}

type Handler struct {
	Service *Service
	// VerifyContinuation reads server-owned Tether evidence for the exact pair,
	// same agent, explicit parent, and successful native admission. Nil fails closed.
	VerifyContinuation func(ctx context.Context, source, replacement, agent string) error
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
	case errors.Is(err, ErrNotContinuable):
		fail(w, 409, "not_continuable", "Continue applies to Tether sessions only.")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid", "Check the plan: titles are required (200 characters), at most 100 steps, known statuses and step ids.")
	default:
		fail(w, 500, "internal", "The plan could not be saved.")
	}
}

func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	hs := map[string]http.HandlerFunc{
		"GET /threads/{ref}/plan": h.get, "PUT /threads/{ref}/plan": h.put, "DELETE /threads/{ref}/plan": h.clear,
		"POST /threads/{ref}/plan/steps": h.addStep, "PATCH /threads/{ref}/plan/steps/{id}": h.patchStep, "POST /threads/{ref}/plan/reorder": h.reorder,
		"GET /threads/{ref}/continue": h.getContinue, "PUT /threads/{ref}/continue": h.putContinue,
		"POST /continuations":      h.associateContinuation,
		"GET /threads/{ref}/plans": h.plans, "POST /threads/{ref}/plans": h.createPlan, "POST /threads/{ref}/plans/reorder": h.reorderPlans, "DELETE /threads/{ref}/plans/{id}": h.deletePlan,
		"POST /threads/{ref}/plans/{id}/steps/{step}/move": h.moveStep,
		"POST /threads/{ref}/plans/{plan}/steps":           h.addStep, "PATCH /threads/{ref}/plans/{plan}/steps/{id}": h.patchStep,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, hs[r])
	}
	return m
}

// associateContinuation is the supported eager association API for a Restart
// completion or the non-UI Agent Ops caller. The server never trusts client
// claims of readiness; VerifyContinuation must supply Tether-owned proof.
func (h *Handler) associateContinuation(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || h.VerifyContinuation == nil {
		fail(w, 503, "native_proof_unavailable", "Native admission verification is not configured.")
		return
	}
	var in struct {
		Source      string `json:"source_session_id"`
		Replacement string `json:"new_session_id"`
		Agent       string `json:"logical_agent_id"`
		Key         string `json:"idempotency_key"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4097))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
		fail(w, 400, "invalid", "Expected one continuation JSON object.")
		return
	}
	source, errS := uuid.Parse(in.Source)
	replacement, errR := uuid.Parse(in.Replacement)
	if errS != nil || errR != nil || source.String() != in.Source || replacement.String() != in.Replacement || source == replacement || in.Agent == "" || in.Key != "continuation:"+in.Source+":"+in.Replacement {
		fail(w, 400, "invalid", "Exact canonical session UUIDs, agent and pair key are required.")
		return
	}
	if err := h.VerifyContinuation(r.Context(), in.Source, in.Replacement, in.Agent); err != nil {
		fail(w, 409, "native_continuation_unverified", "Tether did not verify this exact native continuation.")
		return
	}
	out, err := h.Service.AssociateContinuation(r.Context(), in.Source, in.Replacement, in.Agent)
	if err != nil {
		if errors.Is(err, ErrAssociationConflict) || errors.Is(err, ErrAgentMismatch) {
			fail(w, 409, "association_conflict", "The replacement or source is bound to another continuation.")
		} else {
			fail(w, 503, "association_unavailable", "The continuation association could not be committed; retry the same exact pair.")
		}
		return
	}
	reply(w, 200, map[string]any{"source_session_id": out.Source, "new_session_id": out.Replacement, "logical_agent_id": out.Agent,
		"idempotency_key": in.Key, "replayed": out.Replayed, "plan_moved": out.PlanMoved})
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

// clear removes the thread's plan (the operator's "clear plan" / "new conversation") and stops
// an older plan from being carried into this thread's replacements.
func (h *Handler) clear(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	if err := h.Service.Clear(r.Context(), t); err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

type continueJSON struct {
	Continue *bool `json:"continue"`
}

// getContinue returns the session's Continue flag (the default when first seen).
func (h *Handler) getContinue(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	on, err := h.Service.Continue(r.Context(), t)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]bool{"continue": on})
}

// putContinue sets the session's Continue flag: {"continue": true|false}.
func (h *Handler) putContinue(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var in continueJSON
	if !decode(w, r, &in) {
		return
	}
	if in.Continue == nil {
		fail(w, 400, "invalid", "Send continue (true or false).")
		return
	}
	if err := h.Service.SetContinue(r.Context(), t, *in.Continue); err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]bool{"continue": *in.Continue})
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
	_, st, err := h.Service.AddStepOf(r.Context(), t, scope, r.PathValue("plan"), in.in(), *in.Expected, "user")
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
	_, st, err := h.Service.PatchStepOf(r.Context(), t, scope, r.PathValue("plan"), r.PathValue("id"), in.in(), *in.Expected, "user")
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

// Multiple plans per thread (CW-20261112-0067). The single-plan routes above act on the thread's first plan until the
// agent verbs and the screen have moved to these.
func (h *Handler) plans(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	ps, err := h.Service.Plans(r.Context(), t)
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"plans": ps})
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var in struct {
		Title *string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := h.Service.CreatePlan(r.Context(), t, in.Title, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 201, p)
}

func (h *Handler) deletePlan(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	exp, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil {
		fail(w, 400, "invalid", "expected_revision is required.")
		return
	}
	if err := h.Service.DeletePlan(r.Context(), t, r.PathValue("id"), exp, "user"); err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func (h *Handler) reorderPlans(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var in struct {
		PlanIDs []string `json:"plan_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.PlanIDs == nil {
		fail(w, 400, "invalid", "plan_ids is required.")
		return
	}
	ps, err := h.Service.ReorderPlans(r.Context(), t, in.PlanIDs, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"plans": ps})
}

func (h *Handler) moveStep(w http.ResponseWriter, r *http.Request) {
	t, ok := ref(w, r)
	if !ok {
		return
	}
	var in struct {
		ToPlan     string `json:"to_plan"`
		Position   int    `json:"position"`
		Expected   *int64 `json:"expected_revision"`
		ToExpected *int64 `json:"to_expected_revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ToPlan == "" || in.Expected == nil || in.ToExpected == nil {
		fail(w, 400, "invalid", "to_plan, expected_revision and to_expected_revision are required.")
		return
	}
	from, to, st, err := h.Service.MoveStep(r.Context(), t, r.PathValue("id"), in.ToPlan, r.PathValue("step"), in.Position, *in.Expected, *in.ToExpected, "user")
	if err != nil {
		problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"from": from, "to": to, "step": st})
}
