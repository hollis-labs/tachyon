package assurance

import (
	"net/http"
	"net/http/httptest"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

func request(p *libauth.Principal) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/plugins/console/v1/approval-inbox/items/x/resolve", nil)
	if p != nil {
		r = r.WithContext(libauth.WithPrincipal(r.Context(), *p))
	}
	return r
}

// A key, or a token minted from one, is never browser-session or operator-token, whatever its
// scopes: every action in the table is refused, "*" and tachyon.operator included.
func TestKeysNeverQualify(t *testing.T) {
	for name, p := range map[string]libauth.Principal{
		"key with *":                {Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{"*"}},
		"key with tachyon.operator": {Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{OperatorScope}},
		"token minted from a key":   {Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{"*"}, Parent: "k_a", TokenID: "tok_x"},
		"kind user but a key id":    {Verified: true, ID: "key:k_a", Kind: "user", Scopes: []string{"*"}},
		"an operator-kind caller":   {Verified: true, ID: "local:operator", Kind: libauth.KindOperator, Scopes: []string{"*"}},
		"unverified user":           {Verified: false, ID: "user:usr_a", Kind: "user", Scopes: []string{"*"}},
	} {
		if Of(p) != None {
			t.Errorf("%s: level %s", name, Of(p))
		}
		for _, a := range append(Actions(), "unknown_action") {
			err := Require(request(&p), a)
			if err == nil {
				t.Errorf("%s: %s allowed", name, a)
				continue
			}
			if p.Verified && (err.Status != http.StatusForbidden || err.Code != "operator_required") {
				t.Errorf("%s: %s refused with %d %s", name, a, err.Status, err.Code)
			}
		}
	}
}

func TestPeople(t *testing.T) {
	person := libauth.Principal{Verified: true, ID: "user:usr_a", Kind: "user", Scopes: []string{"tachyon.planner.read"}, SessionID: "ss_a"}
	owner := libauth.Principal{Verified: true, ID: "user:usr_o", Kind: "user", Scopes: []string{"*"}, SessionID: "ss_o"}
	operator := libauth.Principal{Verified: true, ID: "user:usr_b", Kind: "user", Scopes: []string{OperatorScope}}
	if Of(person) != BrowserSession || Of(owner) != OperatorToken || Of(operator) != OperatorToken {
		t.Fatalf("levels %s %s %s", Of(person), Of(owner), Of(operator))
	}
	for _, a := range Actions() {
		gotPerson := Require(request(&person), a) == nil
		if want := Required(a) == BrowserSession; gotPerson != want {
			t.Errorf("person, %s: allowed %v, want %v", a, gotPerson, want)
		}
		if Require(request(&owner), a) != nil || Require(request(&operator), a) != nil {
			t.Errorf("operator refused %s", a)
		}
	}
	if Required("unknown_action") != OperatorToken || Require(request(&person), "unknown_action") == nil {
		t.Error("an unknown action must need operator-token")
	}
	if err := Require(request(nil), "read"); err == nil || err.Status != http.StatusUnauthorized {
		t.Errorf("no principal: %v", err)
	}
}

func TestMiddlewareFlatBody(t *testing.T) {
	key := libauth.Principal{Verified: true, ID: "key:k_a", Kind: "service", Scopes: []string{"*"}}
	rec := httptest.NewRecorder()
	Middleware("resolve_high", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached") })).ServeHTTP(rec, request(&key))
	if rec.Code != 403 || rec.Body.String() != "{\"code\":\"operator_required\",\"message\":\"This action needs a signed-in person holding tachyon.operator; an API key never qualifies.\",\"action\":\"resolve_high\"}\n" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
