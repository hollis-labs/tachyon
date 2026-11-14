package routes

import (
	"net/http"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

var ok = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

func TestScopeNames(t *testing.T) {
	for _, s := range []string{Authenticated, "tachyon.planner.read", "tachyon.approvals.resolve", "tachyon.agents.control", "tachyon.hitl.respond", "tachyon.messages.send"} {
		if err := ValidateScope(s); err != nil {
			t.Errorf("%q refused: %v", s, err)
		}
	}
	for _, s := range []string{"", Operator, "public", "*", "tachyon.read", "tachyon.planner.delete", "planner.read", "tachyon.Planner.read", "tachyon_planner_list"} {
		if err := ValidateScope(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

// A deliberately unscoped route is refused, with every other defect, and nothing is registered.
func TestUnscopedRouteFails(t *testing.T) {
	table := libauth.NewScopeTable()
	_, err := Register(table, []Route{
		{Method: "GET", Pattern: "/api/plugins/console/v1/planner/items", Scope: "tachyon.planner.read", Handler: ok},
		{Method: "POST", Pattern: "/api/plugins/console/v1/planner/items", Handler: ok},
		{Method: "GET", Pattern: "/api/x", Scope: Operator, Handler: ok},
		{Method: "GET", Pattern: "/outside", Scope: Authenticated, Handler: ok},
		{Method: "get", Pattern: "/api/y", Scope: Authenticated, Handler: ok},
		{Method: "GET", Pattern: "/api/z", Scope: Authenticated},
	})
	if err == nil {
		t.Fatal("an unscoped route was registered")
	}
	for _, want := range []string{
		"POST /api/plugins/console/v1/planner/items: no scope",
		"GET /api/x: tachyon.operator is an assurance check",
		"GET /outside: the path is outside /api/",
		"get /api/y: the method",
		"GET /api/z: no handler",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
	if _, known := table.ForRoute("GET", "/api/plugins/console/v1/planner/items"); known {
		t.Error("a valid route was mapped although the list was refused")
	}
}

func TestRegisterMapsAndServesTheSameList(t *testing.T) {
	table := libauth.NewScopeTable()
	rs := []Route{
		{Method: "GET", Pattern: "/api/plugins/console/v1/planner/items", Scope: "tachyon.planner.read", Handler: ok},
		{Method: "POST", Pattern: "/api/plugins/console/v1/planner/items", Scope: "tachyon.planner.write", Handler: ok},
		{Method: "GET", Pattern: "/api/plugins/console/v1/account", Scope: Authenticated, Handler: ok},
	}
	mux, err := Register(table, rs)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if got, known := table.ForRoute(r.Method, r.Pattern); !known || got != TableScope(r.Scope) {
			t.Errorf("%s %s: scope %q known %v, want %q", r.Method, r.Pattern, got, known, r.Scope)
		}
		req, _ := http.NewRequest(r.Method, r.Pattern, nil)
		if _, pattern := mux.Handler(req); pattern != r.Method+" "+r.Pattern {
			t.Errorf("%s %s served by %q", r.Method, r.Pattern, pattern)
		}
	}
	if _, known := table.ForRoute("DELETE", "/api/plugins/console/v1/planner/items"); known {
		t.Error("an undeclared method is mapped")
	}
}

func TestDuplicateRouteFails(t *testing.T) {
	r := Route{Method: "GET", Pattern: "/api/a", Scope: Authenticated, Handler: ok}
	if err := Check([]Route{r, r}); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("err = %v", err)
	}
}
