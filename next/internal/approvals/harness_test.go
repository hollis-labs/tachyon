package approvals

import (
	"net/http"
	"os"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// The old tests reached the inbox through the Tangent session bridge: a cookie named a browser
// session, and a fake Tangent said whether it was elevated. The port takes the caller from the
// verified principal instead, so this harness turns the same test inputs into one: a cookie is the
// signed-in person "user:<cookie>", holding tachyon.operator when the test's elevation flag is set;
// a bearer token is an API key. No test runs in parallel, so the flag is package state.

// testElevated is the current test's elevation flag (fakeTangent sets it).
var testElevated *bool

// fakeSessions stands in for the old fake Tangent server; only its elevation flag remains.
type fakeSessions struct{ URL string }

// fakeTangent registers the test's elevation flag. The extra arguments are the old fake's (the
// elevation expiry and an elevate-call counter), unused now that there is no elevation route.
func fakeTangent(t *testing.T, elevated *bool, _ string, _ *int) fakeSessions {
	t.Helper()
	prev := testElevated
	testElevated = elevated
	t.Cleanup(func() { testElevated = prev })
	return fakeSessions{URL: "unused"}
}

// New stands in for the old bridge constructor in NewInbox(store, New(url)): the inbox has no
// session bridge now, and these tests give it no secret key (the secrets tests set one).
func New(string) *Secrets { return nil }

// asCaller puts the caller the old cookie or token named on the request.
func asCaller(r *http.Request, token, cookie string) *http.Request {
	var p libauth.Principal
	switch {
	case token != "":
		p = libauth.Principal{Verified: true, ID: "key:k_" + token, Kind: "service", Scopes: []string{"*"}}
	case cookie != "":
		p = libauth.Principal{Verified: true, ID: "user:" + cookie, Kind: "user", Scopes: []string{"tachyon.approvals.read", "tachyon.approvals.resolve"}}
		if testElevated != nil && *testElevated {
			p.Scopes = append(p.Scopes, "tachyon.operator")
		}
	default:
		return r
	}
	return r.WithContext(libauth.WithPrincipal(r.Context(), p))
}

// secretsFromFile is the inbox's secret store sealed with the key in keyFile, as the old build read
// TACHYON_SECRET_KEY_FILE; an empty path is no key.
func secretsFromFile(t *testing.T, in *Inbox, keyFile string) *Secrets {
	t.Helper()
	if keyFile == "" {
		return NewSecrets(in.Store.DB, nil, "tachyon.files.secret_key is not set")
	}
	raw, err := os.ReadFile(keyFile) //nolint:gosec // this test's key file
	if err != nil {
		return NewSecrets(in.Store.DB, nil, "key file unreadable")
	}
	return NewSecrets(in.Store.DB, raw, "")
}
