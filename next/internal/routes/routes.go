// Package routes is the one declaration of Tachyon's own HTTP routes. Each route carries its scope,
// and the mux and the auth scope table are built from the same list, so a route cannot be served
// without a scope and a scope cannot name a route that is not served.
//
// Scopes (docs/api-contract.md):
//
//   - "authenticated": any verified caller (the handler narrows it, for example to the caller's
//     own account). It is mapped on the table as "" (authentication only), as Substrate's
//     module.AddRoutes does.
//   - "tachyon.<area>.<verb>": verbs read, write, resolve, control, send, respond.
//
// An app route cannot be public: the guard skips authentication only for routes a module declares
// public (module.IsPublic), and Tachyon's public routes (/healthz, setup, login, JWKS) are all the
// framework's.
//
// "tachyon.operator" is never a route scope: it is the assurance check a handler makes on a
// signed-in person (port plan section 4), and Validate refuses it here.
package routes

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// Scope names with a meaning of their own.
const (
	Authenticated = "authenticated"
	Operator      = "tachyon.operator"
	public        = "public"
)

// Route is one served route.
type Route struct {
	Method  string // GET, POST, ...; never empty
	Pattern string // a net/http pattern path under /api/
	Scope   string
	Handler http.Handler
}

var scopeName = regexp.MustCompile(`^tachyon\.[a-z][a-z0-9_]*\.(read|write|resolve|control|send|respond)$`)

// ValidateScope refuses an empty scope, tachyon.operator, and any name outside the convention.
func ValidateScope(scope string) error {
	switch {
	case scope == "":
		return errors.New("no scope")
	case scope == Operator:
		return fmt.Errorf("%s is an assurance check made in the handler, not a route scope", Operator)
	case scope == public:
		return errors.New("an app route cannot be public; only framework modules declare public routes")
	case scope == Authenticated, scopeName.MatchString(scope):
		return nil
	}
	return fmt.Errorf("scope %q is not authenticated or tachyon.<area>.<verb>", scope)
}

// Check returns one error per route that cannot be registered: no method, a path outside /api/,
// a bad scope, no handler, or a duplicate.
func Check(rs []Route) error {
	var errs []error
	seen := map[string]bool{}
	for _, r := range rs {
		id := r.Method + " " + r.Pattern
		switch {
		case r.Method == "" || strings.ToUpper(r.Method) != r.Method:
			errs = append(errs, fmt.Errorf("%s: the method must be an upper-case HTTP method", id))
		case !strings.HasPrefix(r.Pattern, "/api/"):
			errs = append(errs, fmt.Errorf("%s: the path is outside /api/, where the guard does not look", id))
		case r.Handler == nil:
			errs = append(errs, fmt.Errorf("%s: no handler", id))
		case seen[id]:
			errs = append(errs, fmt.Errorf("%s: declared twice", id))
		}
		if err := ValidateScope(r.Scope); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
		seen[id] = true
	}
	return errors.Join(errs...)
}

// Register checks the list, then maps every route's scope on the table and serves it on a new
// mux, which it returns for httphost.Options.Routes.
func Register(table *libauth.ScopeTable, rs []Route) (*http.ServeMux, error) {
	if err := Check(rs); err != nil {
		return nil, err
	}
	if table == nil {
		return nil, errors.New("routes: no scope table (is the auth module started?)")
	}
	mux := http.NewServeMux()
	for _, r := range rs {
		table.Route(r.Method, TablePattern(r.Pattern), TableScope(r.Scope))
		mux.Handle(r.Method+" "+r.Pattern, r.Handler)
	}
	return mux, nil
}

// TableScope is the scope as the auth table holds it: "authenticated" is "" (authentication only).
func TableScope(scope string) string {
	if scope == Authenticated {
		return ""
	}
	return scope
}

// TablePattern is the pattern as the auth table matches it: a subtree pattern ("/api/x/") covers
// the rest of the path ("/api/x/*"), as net/http's mux does.
func TablePattern(pattern string) string {
	if strings.HasSuffix(pattern, "/") {
		return pattern + "*"
	}
	return pattern
}
