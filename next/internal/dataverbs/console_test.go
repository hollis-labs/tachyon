package dataverbs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/provideractions"
)

// post sends a console write as p.
func post(h http.Handler, p *libauth.Principal, provider, verb string, input any) (int, string) {
	b, _ := json.Marshal(input)
	req := httptest.NewRequest(http.MethodPost, Prefix+"/providers/"+provider+"/verbs/"+verb, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(libauth.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func flat(body string) string {
	var e struct{ Code string }
	_ = json.Unmarshal([]byte(body), &e)
	return e.Code
}

// Each console family is refused without its scope, mutations without the operator check, and a key
// whatever its scopes: the object writes (tachyon.objects.write), the Tether provider's reads
// (tachyon.objects.read) and its writes (tachyon.objects.write). None of the refusals reaches Tether.
func TestConsoleWriteFamiliesNeedScopeAndOperator(t *testing.T) {
	f := setup(t)
	binds := 0
	actions := &Actions{Service: &provideractions.Service{Store: f.db, Bind: func() (provideractions.Bound, error) {
		binds++
		return nil, fmt.Errorf("off")
	}}}
	h := NewConsoleHTTP(f.local, actions)
	person := func(scopes ...string) *libauth.Principal {
		return &libauth.Principal{Verified: true, ID: "user:usr_p", Kind: "user", Scopes: scopes}
	}
	key := &libauth.Principal{Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{"*"}}
	operator := person("tachyon.objects.read", "tachyon.objects.write", "tachyon.operator")
	create := map[string]any{"id": "0a6f3c51-6f1c-4d5e-9b8e-2b1d2e7c9f10", "kind": "task", "definition": map[string]any{"name": "A task", "status": "todo"}}
	preview := map[string]any{"id": "7d1c2a0e-3b4f-4c5d-8e9f-0a1b2c3d4e5f", "object_id": "0a6f3c51-6f1c-4d5e-9b8e-2b1d2e7c9f10", "expected_revision": 1, "mission": "m"}
	for _, c := range []struct {
		name, provider, verb string
		input                any
	}{{"object writes", "tachyon", "objects.create", create}, {"team writes", "tether", "teams.preview", preview}} {
		for who, p := range map[string]*libauth.Principal{"read scope only": person("tachyon.objects.read", "tachyon.operator"), "another area's write": person("tachyon.planner.write", "tachyon.operator")} {
			if code, body := post(h, p, c.provider, c.verb, c.input); code != 403 || flat(body) != "scope_required" {
				t.Errorf("%s, %s: %d %s", c.name, who, code, body)
			}
		}
		if code, body := post(h, person("tachyon.objects.write"), c.provider, c.verb, c.input); code != 403 || flat(body) != "operator_required" {
			t.Errorf("%s without the operator check: %d %s", c.name, code, body)
		}
		if code, body := post(h, key, c.provider, c.verb, c.input); code != 403 {
			t.Errorf("%s for a key: %d %s", c.name, code, body)
		}
	}
	runs := Prefix + "/providers/tether/verbs/runtime.runs?input=%7B%7D"
	if code, body := call(h, person("tachyon.planner.read"), runs); code != 403 || flat(body) != "scope_required" {
		t.Errorf("team reads without tachyon.objects.read: %d %s", code, body)
	}
	if code, body := call(h, key, runs); code != 403 {
		t.Errorf("team reads for a key: %d %s", code, body)
	}
	if code, body := call(h, person("tachyon.objects.read"), runs); code != 200 {
		t.Errorf("team reads: %d %s", code, body)
	}
	if binds != 0 {
		t.Fatalf("a refused call bound to Tether %d times", binds)
	}
	// The operator creates the task the preview names; with the binding refused the preview is
	// unavailable after one bind attempt and nothing is retained.
	if code, body := post(h, operator, "tachyon", "objects.create", create); code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, body := post(h, operator, "tether", "teams.preview", preview); code != 503 || binds != 1 {
		t.Errorf("preview with the binding off: %d %s (binds %d)", code, body, binds)
	}
}

// The object writes on the console, as the old build served them: create with a retained id, patch,
// delete and restore with a revision check, all recorded with the browser operator as actor.
func TestConsoleObjectWrites(t *testing.T) {
	f := setup(t)
	h := NewConsoleHTTP(f.local, nil)
	op := &libauth.Principal{Verified: true, ID: "user:usr_o", Kind: "user", Scopes: []string{"*"}}
	id := "0a6f3c51-6f1c-4d5e-9b8e-2b1d2e7c9f11"
	steps := []struct {
		verb  string
		input map[string]any
		rev   int64
	}{
		{"objects.create", map[string]any{"id": id, "kind": "task", "definition": map[string]any{"name": "A task", "status": "todo"}}, 1},
		{"objects.patch", map[string]any{"id": id, "expected_revision": 1, "definition": map[string]any{"name": "Renamed"}}, 2},
		{"objects.delete", map[string]any{"id": id, "expected_revision": 2}, 3},
		{"objects.restore", map[string]any{"id": id, "expected_revision": 3}, 4},
	}
	for _, s := range steps {
		code, body := post(h, op, "tachyon", s.verb, s.input)
		var out struct{ Data objects.Record }
		_ = json.Unmarshal([]byte(body), &out)
		if code != 200 || out.Data.Revision != s.rev {
			t.Fatalf("%s: %d %s", s.verb, code, body)
		}
	}
	if code, body := post(h, op, "tachyon", "objects.patch", map[string]any{"id": id, "expected_revision": 1, "definition": map[string]any{"name": "Stale"}}); code != 409 {
		t.Errorf("a stale patch: %d %s", code, body)
	}
	if code, body := post(h, op, "tachyon", "objects.delete", map[string]any{"id": id, "expected_revision": 0}); code != 400 {
		t.Errorf("a non-positive revision: %d %s", code, body)
	}
	got, err := f.objs.Get(context.Background(), id)
	if err != nil || got.Name != "Renamed" || got.DeletedAt != nil {
		t.Fatalf("after the writes: %+v %v", got, err)
	}
	hist, err := f.objs.History(context.Background(), id, 0, 10)
	if err != nil || len(hist) != 4 || hist[0].Actor != "operator:browser" {
		t.Fatalf("history: %+v %v", hist, err)
	}
}
