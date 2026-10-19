package provideractions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func publication(t *testing.T, s *Service) Receipt {
	t.Helper()
	row, e := objects.New(s.Store).Create(ctx, objects.Agent{Common: objects.Common{Name: "Planner"}, Enabled: true, Instructions: "First line\nSecond line", Model: "model-id", Roles: []string{"planner"}, Skills: []string{"planning"}}, nil, "test")
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.PreparePublication(ctx, PublishRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, Provider: "provider-id", Extends: "base-profile"}, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestPublicationUsesSeparateProfileAndPreservesInstructions(t *testing.T) {
	s, _, f := fixture(t)
	r := publication(t, s)
	var bundle CatalogBundle
	var profile map[string]any
	if json.Unmarshal(r.Payload, &bundle) != nil || len(bundle.Files) != 1 || json.Unmarshal([]byte(bundle.Files[0].Content), &profile) != nil {
		t.Fatal(string(r.Payload))
	}
	if bundle.Files[0].Path != "agents/tachyon-"+r.ObjectID+".yaml" || profile["body"] != "First line\nSecond line" || profile["extends"] != "base-profile" || profile["provider"] != "provider-id" || f.calls.Load() != 0 {
		t.Fatal(profile)
	}
	row, e := objects.New(s.Store).Patch(ctx, r.ObjectID, r.ObjectRevision, objects.Patch{Definition: json.RawMessage(`{"tools":["shell"]}`)}, "test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PreparePublication(ctx, PublishRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, Provider: "provider-id", Extends: "base-profile"}, "operator:test"); e == nil {
		t.Fatal("tools silently granted")
	}
}

type planBound struct {
	*fakeBound
	plan  CatalogPlan
	reads int
}

func (f *planBound) CatalogPlan(context.Context, string) (CatalogPlan, error) {
	f.reads++
	return f.plan, nil
}
func TestCatalogUnknownApplyRequiresReadbackNeverReplay(t *testing.T) {
	s, _, f := fixture(t)
	r := publication(t, s)
	f.form = func(string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"plan_id":"cp_123","digest":"pinned","validation_ok":true}`), nil
	}
	plan, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	install, e := s.PrepareCatalogApply(ctx, CatalogApplyRequest{ID: uuid.NewString(), PlanActionID: plan.ID}, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	f.form = func(string, json.RawMessage) (json.RawMessage, error) { return nil, errors.New("response lost") }
	install, e = s.Apply(ctx, ApplyRequest{ID: install.ID, Digest: install.Digest}, "operator:test")
	if e != nil || install.State != "unknown" {
		t.Fatal(install, e)
	}
	calls := f.calls.Load()
	if _, e = s.Apply(ctx, ApplyRequest{ID: install.ID, Digest: install.Digest, Retry: true}, "operator:test"); e == nil || f.calls.Load() != calls {
		t.Fatal("catalog replay accepted")
	}
	reader := &planBound{fakeBound: f, plan: CatalogPlan{ID: "cp_123", Digest: "pinned", State: "approved"}}
	s.Bind = func() (Bound, error) { return reader, nil }
	if _, e = s.Reconcile(ctx, install.ID, "operator:test"); e == nil {
		t.Fatal("unconfirmed installation accepted")
	}
	reader.plan.State = "applied"
	got, e := s.Reconcile(ctx, install.ID, "operator:test")
	if e != nil || got.State != "succeeded" || f.calls.Load() != calls || reader.reads != 2 {
		t.Fatal(got, e)
	}
}
func TestCatalogTransportMatchesFileAndRequiresProviderApproval(t *testing.T) {
	s, _, _ := fixture(t)
	r := publication(t, s)
	var bundle CatalogBundle
	_ = json.Unmarshal(r.Payload, &bundle)
	state := "planned"
	writes := 0
	wrongFile := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Idempotency-Key") != "" {
			t.Error("invented catalog idempotency contract")
		}
		if req.Method == "POST" && req.URL.Path == "/catalog/plans" {
			var got CatalogBundle
			if json.NewDecoder(req.Body).Decode(&got) != nil || got.Files[0].Content != bundle.Files[0].Content {
				t.Error("changed bundle")
			}
			hash := strings.TrimPrefix(digest([]byte(bundle.Files[0].Content)), "sha256:")
			if wrongFile {
				hash = "wrong"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"plan_id": "cp_123", "digest": "pinned", "state": "planned", "validation_ok": true, "files": []any{map[string]string{"path": bundle.Files[0].Path, "new_sha256": hash}}, "expires_at": time.Now().Add(time.Hour)})
		} else if req.Method == "GET" && req.URL.Path == "/catalog/plans/cp_123" {
			_ = json.NewEncoder(w).Encode(CatalogPlan{ID: "cp_123", Digest: "pinned", State: state, ValidationOK: true, ExpiresAt: time.Now().Add(time.Hour)})
		} else if req.Method == "POST" && req.URL.Path == "/catalog/plans/cp_123/apply" {
			writes++
			_, _ = w.Write([]byte(`{"plan_id":"cp_123","applied":true,"secret":"not retained"}`))
		} else {
			t.Error("unexpected request", req.Method, req.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	bound, e := Tether(server.URL, "")()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bound.Execute(ctx, "catalog.plan", "ignored", r.Payload); e != nil {
		t.Fatal(e)
	}
	wrongFile = true
	if _, e = bound.Execute(ctx, "catalog.plan", "ignored", r.Payload); e == nil {
		t.Fatal("mismatched plan accepted")
	}
	payload := json.RawMessage(`{"plan_id":"cp_123","digest":"pinned"}`)
	if _, e = bound.Execute(ctx, "catalog.apply", "ignored", payload); e == nil || !e.(*Failure).NoEffect || writes != 0 {
		t.Fatal("unapproved installation sent", e)
	}
	state = "approved"
	raw, e := bound.Execute(ctx, "catalog.apply", "ignored", payload)
	if e != nil || writes != 1 || strings.Contains(string(raw), "secret") {
		t.Fatal(string(raw), e)
	}
	state = "applied"
	if _, e = bound.Execute(ctx, "catalog.apply", "ignored", payload); e != nil || writes != 1 {
		t.Fatal("applied plan resubmitted", e)
	}
}
func TestCatalogPreflightFailureRetainsPreparedRequest(t *testing.T) {
	s, _, f := fixture(t)
	r := publication(t, s)
	f.form = func(string, json.RawMessage) (json.RawMessage, error) {
		return nil, &Failure{Message: "Approval needed", NoEffect: true}
	}
	got, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || got.State != "prepared" || got.Problem != "Approval needed" {
		t.Fatal(got, e)
	}
}
