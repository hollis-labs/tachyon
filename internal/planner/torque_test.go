package planner

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestTorqueImportSafeSubsetAndExplicitRelationships(t *testing.T) {
	ctx := context.Background()
	store, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := objects.New(store)
	var item TorqueRecord
	if err := json.Unmarshal([]byte(`{"id":"CW-1","kind":"agent","title":"Build storage","status":"todo","priority":2,"project_id":"PRJ-1","parent_id":"PLAN-1","depends_on":["CW-0"],"tags":["backend"],"environment":{"TOKEN":"do-not-import"},"metadata":{"secret":"do-not-import"},"system_prompt":"do-not-import","manual":false,"on_done":"dispatch"}`), &item); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	r, err := ImportTorque(ctx, s, "https://torque.test/", at, item, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ownership != "tachyon" || r.Kind != objects.TaskKind || r.Source.Key.Instance != "https://torque.test" || strings.Contains(string(r.Definition), "do-not-import") || strings.Contains(string(r.Definition), "dispatch") {
		t.Fatal("unsafe import")
	}
	var task objects.Task
	if err = json.Unmarshal(r.Definition, &task); err != nil {
		t.Fatal(err)
	}
	if task.ProjectID != "" || task.PlanID != "" || task.ParentID != "" || len(task.DependsOn) != 0 || !strings.Contains(string(r.Definition), "PRJ-1") {
		t.Fatal("remote IDs became local references or were dropped")
	}
	plan, err := s.Create(ctx, objects.Plan{Common: objects.Common{Name: "Authored plan"}, Status: "draft"}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	patch, _ := json.Marshal(map[string]string{"plan_id": plan.ID, "name": "Local title"})
	r, err = s.Patch(ctx, r.ID, r.Revision, objects.Patch{Definition: patch}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	item.Title = "Changed remotely"
	item.ParentID = "PLAN-OTHER"
	item.Status = "done"
	r, err = ImportTorque(ctx, s, "https://torque.test", at.Add(time.Second), item, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(r.Definition, &task); err != nil {
		t.Fatal(err)
	}
	if task.Name != "Local title" || task.PlanID != plan.ID || task.Status != "todo" || !strings.Contains(string(r.Source.Definition), "PLAN-OTHER") {
		t.Fatal("refresh overrode authorship")
	}
	item.ID, item.Kind = "PLAN-1", "plan"
	plan, err = ImportTorque(ctx, s, "https://torque.test", at, item, &objects.Correlation{ID: plan.ID, ExpectedRevision: plan.Revision}, "operator")
	if err != nil || plan.Name != "Authored plan" || plan.Source == nil {
		t.Fatal("explicit plan correlation failed", err)
	}
	for _, instance := range []string{"", "https://user:password@torque.test", "https://torque.test?token=secret", "file:///tmp/torque"} {
		if _, err = ImportTorque(ctx, s, instance, at, item, nil, "operator"); !errors.Is(err, objects.ErrInvalid) {
			t.Fatal("unsafe instance accepted", err)
		}
	}
	item.Kind = "future-unknown"
	if _, err = ImportTorque(ctx, s, "https://torque.test", at, item, nil, "operator"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("unknown kind accepted", err)
	}
}
