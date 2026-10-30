package cards

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes"
)

func req(kind string, extra map[string]any) hitl.EnqueueRequest {
	r := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: kind, IdempotencyKey: "k1", Extra: map[string]json.RawMessage{}}
	for k, v := range extra {
		r.Extra[k], _ = json.Marshal(v)
	}
	return r
}

func class(t *testing.T, r hitl.EnqueueRequest) string {
	t.Helper()
	var c string
	_ = json.Unmarshal(r.Extra["class"], &c)
	return c
}

func TestPrepareFixesServerOwnedMembers(t *testing.T) {
	reg := NewRegistry()
	r := req("card.approval", map[string]any{"title": "Apply 3 changes", "data": map[string]any{"changes": 3}})
	r.Source.ApplicationID = "forged-app"
	if err := reg.Prepare(&r, "planner"); err != nil {
		t.Fatal(err)
	}
	if r.Source.ApplicationID != "planner" {
		t.Fatalf("source must come from the verified credential, got %q", r.Source.ApplicationID)
	}
	if class(t, r) != "low" {
		t.Fatalf("approval without effects defaults to its type floor, got %q", class(t, r))
	}
	var digest, completion string
	_ = json.Unmarshal(r.Extra["op_digest"], &digest)
	_ = json.Unmarshal(r.Extra["completion"], &completion)
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 || completion != CompletionAsync {
		t.Fatalf("digest %q completion %q", digest, completion)
	}
}

func TestClassIsRaisedNeverLowered(t *testing.T) {
	reg := NewRegistry()
	r := req("card.approval", map[string]any{
		"title": "t", "data": map[string]any{}, "class": "low",
		"effects": []map[string]any{{"target": "torque.task.transition"}},
	})
	if err := reg.Prepare(&r, "planner"); err != nil {
		t.Fatal(err)
	}
	if got := class(t, r); got != "medium" {
		t.Fatalf("an effect-bearing card cannot be low, got %q", got)
	}
	r = req("card.notice", map[string]any{"title": "t", "data": map[string]any{}, "class": "high"})
	if err := reg.Prepare(&r, "planner"); err != nil || class(t, r) != "high" {
		t.Fatalf("asker may raise the class: %v %q", err, class(t, r))
	}
}

func TestPrepareRefuses(t *testing.T) {
	reg := NewRegistry()
	ok := map[string]any{"title": "t", "data": map[string]any{}}
	cases := map[string]struct {
		kind  string
		extra map[string]any
		app   string
	}{
		"unknown kind":           {"card.mystery", ok, "a"},
		"not a card":             {"approval", ok, "a"},
		"no verified source":     {"card.notice", ok, ""},
		"no title":               {"card.notice", map[string]any{"data": map[string]any{}}, "a"},
		"no data":                {"card.notice", map[string]any{"title": "t"}, "a"},
		"bad class":              {"card.notice", map[string]any{"title": "t", "data": map[string]any{}, "class": "extreme"}, "a"},
		"bad completion":         {"card.notice", map[string]any{"title": "t", "data": map[string]any{}, "completion": "later"}, "a"},
		"info is not answerable": {"card.info", map[string]any{"title": "t", "data": map[string]any{}, "completion": "wait"}, "a"},
		"digest mismatch":        {"card.notice", map[string]any{"title": "t", "data": map[string]any{}, "op_digest": "sha256:" + strings.Repeat("0", 64)}, "a"},
		"effect w/o target":      {"card.approval", map[string]any{"title": "t", "data": map[string]any{}, "effects": []map[string]any{{"op": "x"}}}, "a"},
	}
	for name, c := range cases {
		r := req(c.kind, c.extra)
		err := reg.Prepare(&r, c.app)
		if !errors.Is(err, hitl.ErrInvalidRequest) {
			t.Errorf("%s: want ErrInvalidRequest, got %v", name, err)
		}
	}
}

func TestInfoDefaultsToNotify(t *testing.T) {
	reg := NewRegistry()
	r := req("card.info", map[string]any{"title": "Deployed", "data": map[string]any{}})
	if err := reg.Prepare(&r, "cerberus"); err != nil {
		t.Fatal(err)
	}
	var c string
	_ = json.Unmarshal(r.Extra["completion"], &c)
	if c != CompletionNotify {
		t.Fatalf("display-only card must notify, got %q", c)
	}
}

func TestDigestBindsContent(t *testing.T) {
	a, _ := Digest("card.approval", json.RawMessage(`{"b":1,"a":2}`), nil)
	b, _ := Digest("card.approval", json.RawMessage(`{"a":2,"b":1}`), nil)
	c, _ := Digest("card.approval", json.RawMessage(`{"a":2,"b":2}`), nil)
	d, _ := Digest("card.approval", json.RawMessage(`{"a":2,"b":1}`), json.RawMessage(`[{"target":"x"}]`))
	if a != b {
		t.Fatal("key order must not change the digest")
	}
	if a == c || a == d {
		t.Fatal("data and effects must change the digest")
	}
}

func formReq(fields []map[string]any) hitl.EnqueueRequest {
	return req("card.form", map[string]any{"title": "Need input", "data": map[string]any{"fields": fields}})
}

func TestFormFieldRules(t *testing.T) {
	reg := NewRegistry()
	bad := [][]map[string]any{
		nil,
		{{"id": "a", "type": "text"}, {"id": "a", "type": "text"}},
		{{"id": "a", "type": "carousel"}},
		{{"id": "a", "type": "enum"}},
		{{"id": "", "type": "text"}},
	}
	for i, f := range bad {
		r := formReq(f)
		if err := reg.Prepare(&r, "a"); !errors.Is(err, hitl.ErrInvalidRequest) {
			t.Errorf("case %d: want ErrInvalidRequest, got %v", i, err)
		}
	}
	r := formReq([]map[string]any{{"id": "name", "type": "text"}, {"id": "key", "type": "secret"}})
	if err := reg.Prepare(&r, "a"); err != nil {
		t.Fatal(err)
	}
	if class(t, r) != "medium" {
		t.Fatalf("a form that collects a secret is not low risk, got %q", class(t, r))
	}
}

func TestValidateResponseAndAnswers(t *testing.T) {
	reg := NewRegistry()
	form := formReq([]map[string]any{
		{"id": "name", "type": "text", "required": true},
		{"id": "env", "type": "enum", "options": []string{"dev", "prod"}},
		{"id": "token", "type": "secret"},
	})
	if err := reg.Prepare(&form, "a"); err != nil {
		t.Fatal(err)
	}
	resp := func(answers any) hitl.Response {
		raw, _ := json.Marshal(answers)
		return hitl.Response{Kind: "card.form", Decision: "submitted", Extra: map[string]json.RawMessage{"answers": raw}}
	}
	good := resp([]map[string]any{{"questionId": "name", "value": "x"}, {"questionId": "env", "value": "dev"}, {"questionId": "token", "value": map[string]string{"secret_ref": "ref-1"}}})
	if err := reg.ValidateResponse("card.form", good); err != nil {
		t.Fatal(err)
	}
	if err := reg.ValidateAnswers(form, good); err != nil {
		t.Fatal(err)
	}
	for name, answers := range map[string]any{
		"missing required":     []map[string]any{{"questionId": "env", "value": "dev"}},
		"unknown field":        []map[string]any{{"questionId": "name", "value": "x"}, {"questionId": "ghost", "value": "x"}},
		"wrong type":           []map[string]any{{"questionId": "name", "value": 7}},
		"enum outside options": []map[string]any{{"questionId": "name", "value": "x"}, {"questionId": "env", "value": "qa"}},
		"secret as a value":    []map[string]any{{"questionId": "name", "value": "x"}, {"questionId": "token", "value": "hunter2"}},
		"duplicate":            []map[string]any{{"questionId": "name", "value": "x"}, {"questionId": "name", "value": "y"}},
		"no answers":           []map[string]any{},
	} {
		if err := reg.ValidateAnswers(form, resp(answers)); !errors.Is(err, hitl.ErrInvalidRequest) {
			t.Errorf("%s: want ErrInvalidRequest, got %v", name, err)
		}
	}
	if err := reg.ValidateResponse("card.approval", hitl.Response{Kind: "card.approval", Decision: "maybe"}); err == nil {
		t.Error("a decision outside the kind's set must be refused")
	}
	if err := reg.ValidateResponse("card.approval", hitl.Response{Kind: "card.approval", Decision: "approved", Extra: map[string]json.RawMessage{"answers": json.RawMessage(`[]`)}}); err == nil {
		t.Error("an approval takes no answers")
	}
	if err := reg.ValidateResponse("card.approval", hitl.Response{Kind: "card.approval", Decision: "approved", Extra: map[string]json.RawMessage{"identity": json.RawMessage(`"me"`)}}); err == nil {
		t.Error("unexpected response members must be refused (no identity in the payload)")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	env := envelopes.Envelope{
		V: 1, ID: "env-1", Type: "card.approval", TypeVersion: "1", Title: "Apply changes", Context: "Planner proposal",
		Presentation: envelopes.PresentationModal, Data: map[string]any{"changes": 2},
		Trace: &envelopes.Trace{AgentID: "planner-1"},
		Meta:  map[string]any{"completion": "wait", "expires_at": "2026-10-24T00:00:00Z"},
	}
	r, err := FromEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "card.approval" || r.IdempotencyKey != "env-1" || r.Source.AgentID != "planner-1" || r.ExpiresAt == nil {
		t.Fatalf("mapping lost a member: %+v", r)
	}
	if r.Source.ApplicationID != "" {
		t.Fatal("the envelope must not supply the source application")
	}
	reg := NewRegistry()
	if err := reg.Prepare(&r, "planner"); err != nil {
		t.Fatal(err)
	}
	back := ToEnvelope(r)
	if back.ID != "env-1" || back.Title != "Apply changes" || back.Presentation != envelopes.PresentationModal || back.Meta["completion"] != "wait" || back.Meta["class"] != "low" {
		t.Fatalf("round trip: %+v", back)
	}
	if _, err := FromEnvelope(envelopes.Envelope{ID: "x"}); err == nil {
		t.Fatal("an envelope without a type is refused")
	}
}

func TestResponseMapping(t *testing.T) {
	r, err := ResponseFromEnvelope("card.approval", envelopes.Response{
		Status: envelopes.ResponseStatusSubmitted, Decisions: []envelopes.Decision{{ItemID: "e", Action: "approved", Note: "ok"}},
	})
	if err != nil || r.Decision != "approved" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := ResponseFromEnvelope("card.form", envelopes.Response{Status: envelopes.ResponseStatusPartial}); err == nil {
		t.Fatal("partial has no home in the queue")
	}
	if _, err := ResponseFromEnvelope("card.form", envelopes.Response{Status: envelopes.ResponseStatusCanceled}); err == nil {
		t.Fatal("cancel is not an answer")
	}
	at := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	out := ResponseToEnvelope("e", hitl.StateResolved, &r, at)
	if out.Status != envelopes.ResponseStatusSubmitted || len(out.Decisions) != 1 || out.Decisions[0].Action != "approved" || out.Decisions[0].Note != "ok" {
		t.Fatalf("%+v", out)
	}
	if exp := ResponseToEnvelope("e", hitl.StateExpired, nil, at); exp.Error == nil || exp.Error.Code != envelopes.ErrorCodeTimeout {
		t.Fatalf("%+v", exp)
	}
	if c := ResponseToEnvelope("e", hitl.StateCanceled, nil, at); c.Status != envelopes.ResponseStatusCanceled {
		t.Fatalf("%+v", c)
	}
}

type fakeSource struct {
	wrote    int
	settle   bool
	resolved bool
}

func (f *fakeSource) App() string { return "torque" }
func (f *fakeSource) Mirror(context.Context, SourceRef) (SourceState, error) {
	return SourceState{Terminal: f.settle, Resolved: f.resolved}, nil
}
func (f *fakeSource) Respond(context.Context, SourceRef, hitl.Response, hitl.Participant) error {
	f.wrote++
	return nil
}

func TestRespondThroughVerifiesAtTheSource(t *testing.T) {
	ref := SourceRef{App: "torque", ID: "cp-1"}
	ctx := context.Background()
	ok := &fakeSource{settle: true, resolved: true}
	if _, err := RespondThrough(ctx, ok, ref, hitl.Response{}, hitl.Participant{}); err != nil || ok.wrote != 1 {
		t.Fatalf("%v writes=%d", err, ok.wrote)
	}
	unconfirmed := &fakeSource{}
	if _, err := RespondThrough(ctx, unconfirmed, ref, hitl.Response{}, hitl.Participant{}); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("an unconfirmed write must not count as answered: %v", err)
	}
	wrong := &fakeSource{settle: true, resolved: true}
	if _, err := RespondThrough(ctx, wrong, SourceRef{App: "tangent", ID: "i"}, hitl.Response{}, hitl.Participant{}); err == nil || wrong.wrote != 0 {
		t.Fatalf("a reference for another app must not be written: %v writes=%d", err, wrong.wrote)
	}
}

func TestRegisterRules(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(Type{Kind: "approval", MinClass: "low"}); err == nil {
		t.Fatal("kinds outside card. are refused")
	}
	if err := reg.Register(Type{Kind: "card.run", MinClass: "urgent"}); err == nil {
		t.Fatal("unknown classes are refused")
	}
	if err := reg.Register(Type{Kind: "card.run", MinClass: "high", Decisions: []string{"approved", "denied"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("card.run"); !ok {
		t.Fatal("registered kind not found")
	}
}

func TestDestructiveMembersAreValidatedServerRaisedAndDigested(t *testing.T) {
	reg := NewRegistry()
	prep := func(extra map[string]any) (hitl.EnqueueRequest, error) {
		extra["title"], extra["data"] = "Purge", map[string]any{}
		r := req("card.approval", extra)
		return r, reg.Prepare(&r, "service:x")
	}
	del := []map[string]any{{"target": "fake:a", "op": "delete"}}
	// A destructive op raises the flag even when the asker says nothing or tries to clear it.
	for _, d := range []any{nil, false, map[string]any{"irreversible": false, "summary": "tidy"}} {
		extra := map[string]any{"effects": del}
		if d != nil {
			extra["destructive"] = d
		}
		r, err := prep(extra)
		var got struct {
			Irreversible bool   `json:"irreversible"`
			Summary      string `json:"summary"`
		}
		if err != nil || json.Unmarshal(r.Extra["destructive"], &got) != nil || !got.Irreversible || got.Summary == "" || class(t, r) != "medium" {
			t.Fatalf("%v: %v %s", d, err, r.Extra["destructive"])
		}
	}
	// The asker may raise it.
	r, err := prep(map[string]any{"effects": []map[string]any{{"target": "fake:a", "op": "set"}}, "destructive": map[string]any{"irreversible": true, "summary": "x"}})
	if err != nil || r.Extra["destructive"] == nil {
		t.Fatalf("raise: %v", err)
	}
	// Everything is covered by the digest, and a client digest that disagrees is refused.
	base := map[string]any{"effects": del, "affected_items": []map[string]any{{"ref": "a", "label": "A", "kind": "file"}}, "alternatives": []map[string]any{{"id": "archive", "label": "Archive instead", "effects": []map[string]any{{"target": "fake:a", "op": "set"}}}}}
	a, err := prep(base)
	if err != nil {
		t.Fatal(err)
	}
	da, _ := DigestOf(a.Kind, a.Extra)
	for name, mut := range map[string]func(map[string]json.RawMessage){
		"affected": func(m map[string]json.RawMessage) {
			m["affected_items"] = json.RawMessage(`[{"ref":"b","label":"A","kind":"file"}]`)
		},
		"alternative": func(m map[string]json.RawMessage) { delete(m, "alternatives") },
		"destructive": func(m map[string]json.RawMessage) {
			m["destructive"] = json.RawMessage(`{"irreversible":false,"summary":"x"}`)
		},
	} {
		cp := map[string]json.RawMessage{}
		for k, v := range a.Extra {
			cp[k] = v
		}
		mut(cp)
		if d, _ := DigestOf(a.Kind, cp); d == da {
			t.Errorf("%s change left the digest unchanged", name)
		}
	}
	// No new members: the digest is what it always was.
	plain, _ := prep(map[string]any{"effects": []map[string]any{{"target": "fake:a", "op": "set"}}})
	old, _ := Digest(plain.Kind, plain.Extra["data"], plain.Extra["effects"])
	if now, _ := DigestOf(plain.Kind, plain.Extra); now != old {
		t.Error("a request without the new members must keep its old digest")
	}
	// Refusals.
	many := make([]map[string]any, MaxAffectedItems+1)
	for i := range many {
		many[i] = map[string]any{"ref": "r", "label": "l", "kind": "k"}
	}
	alt := func(id string) map[string]any {
		return map[string]any{"id": id, "label": "l", "effects": []map[string]any{{"target": "fake:a"}}}
	}
	for name, extra := range map[string]map[string]any{
		"too many affected": {"affected_items": many},
		"affected no label": {"affected_items": []map[string]any{{"ref": "r", "kind": "k"}}},
		"four alternatives": {"alternatives": []map[string]any{alt("a1"), alt("a2"), alt("a3"), alt("a4")}},
		"duplicate alt id":  {"alternatives": []map[string]any{alt("a1"), alt("a1")}},
		"bad alt id":        {"alternatives": []map[string]any{alt("Bad Id")}},
		"alt without fx":    {"alternatives": []map[string]any{{"id": "a1", "label": "l"}}},
		"destructive str":   {"destructive": "yes"},
	} {
		if _, err := prep(extra); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
