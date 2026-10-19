package approvals

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestElevationRotatesAndScopesBrowserCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/elevate" || r.Method != "POST" {
			t.Errorf("wrong elevation destination: %s %s", r.Method, r.URL.Path)
		}
		cookie, err := r.Cookie("tangent_participant")
		if err != nil || cookie.Value != "before" || r.Header.Get("Authorization") != "" {
			t.Error("wrong participant or ambient authorization forwarded")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"token":"test-only-operator-key"}` {
			t.Error("elevation body changed")
		}
		http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "after", Path: "/", MaxAge: 43200})
		_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
	}))
	defer upstream.Close()
	r := httptest.NewRequest("POST", "https://tachyon.test"+Prefix+"/elevate", strings.NewReader(`{"token":"test-only-operator-key"}`))
	r.Header.Set("Origin", "https://tachyon.test")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer unrelated")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "before"})
	w := httptest.NewRecorder()
	New(upstream.URL).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rotated, retired bool
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name != CookieName {
			t.Fatal("unexpected cookie")
		}
		if cookie.Path == CookiePath {
			rotated = cookie.Value == "after" && cookie.HttpOnly && cookie.Secure && cookie.SameSite == http.SameSiteStrictMode && cookie.MaxAge == 43200
		}
		if cookie.Path == Prefix {
			retired = cookie.MaxAge == -1
		}
	}
	if !rotated || !retired {
		t.Fatal("rotation did not replace and retire browser cookies")
	}
}

func TestOperatorGuardChecksCurrentServerAssuranceBeforeMutation(t *testing.T) {
	status, body := 200, `{"elevated":false,"assurance":"loopback-unverified"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session" || r.Method != "GET" {
			t.Error("not a fresh authority check")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	mutated := false
	handler := New(upstream.URL).RequireOperator(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutated = true
		w.WriteHeader(204)
	}))
	call := func(cookie, origin string) *httptest.ResponseRecorder {
		mutated = false
		r := httptest.NewRequest("POST", "https://tachyon.test/api/plugins/console/v1/chat/agents/demo/send", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("", "https://tachyon.test"); w.Code != 401 || mutated || !strings.Contains(w.Body.String(), "operator_session_required") {
		t.Fatal("missing participant not blocked")
	}
	if w := call("browser", "https://tachyon.test"); w.Code != 403 || mutated || !strings.Contains(w.Body.String(), "assurance_insufficient") {
		t.Fatal("unelevated mutation passed")
	}
	body = `{"elevated":true,"assurance":"operator-token"}`
	if w := call("browser", "https://tachyon.test"); w.Code != 204 || !mutated {
		t.Fatal("valid operator refused")
	}
	if w := call("browser", "https://evil.test"); w.Code != 403 || mutated {
		t.Fatal("cross-origin mutation passed")
	}
	// The same browser loses authority after expiry: no in-process grant survives.
	body = `{"elevated":false,"assurance":"loopback-unverified"}`
	if w := call("browser", "https://tachyon.test"); w.Code != 403 || mutated {
		t.Fatal("expired elevation reused")
	}
	status, body = 503, `{"code":"unreachable","message":"Authorization service unavailable"}`
	if w := call("browser", "https://tachyon.test"); w.Code != 503 || mutated || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatal("service failure disguised as an elevation request")
	}
}
