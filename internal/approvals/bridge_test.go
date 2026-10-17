package approvals

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParticipantIsolationAndDecisionTransport(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), "unrelated") {
			t.Error("forwarded unrelated credentials")
		}
		if r.URL.Path == "/hitl" {
			http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "browser-a", Path: "/", HttpOnly: true})
			return
		}
		c, e := r.Cookie("tangent_participant")
		if e != nil || c.Value != "browser-a" {
			t.Error("participant not preserved")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/resolve") {
			if r.Method != "POST" || r.Header.Get("Origin") == "" {
				t.Error("invalid upstream decision")
			}
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"expected_revision":3`) {
				t.Error("revision lost")
			}
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"code":"revision_conflict","message":"Request changed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"pending":[],"history":[]}`))
	}))
	defer upstream.Close()
	bridge := New(upstream.URL)
	call := func(method, path, origin, cookie, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://tachyon.test"+Prefix+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer must-not-forward")
		w := httptest.NewRecorder()
		bridge.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/session", "https://evil.test", "", ""); w.Code != 403 || len(paths) != 0 {
		t.Fatal("cross-origin bootstrap reached upstream")
	}
	if w := call("GET", "/inbox", "", "", ""); w.Code != 401 || len(paths) != 0 {
		t.Fatal("missing participant reached upstream")
	}
	w := call("POST", "/session", "https://tachyon.test", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cs := w.Result().Cookies()
	if len(cs) != 2 || cs[0].Name != CookieName || !cs[0].HttpOnly || !cs[0].Secure || cs[0].Path != CookiePath {
		t.Fatal("unsafe participant cookie", cs)
	}
	cookie := CookieName + "=browser-a; unrelated=private"
	if w = call("GET", "/inbox", "", cookie, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	before := len(paths)
	if w = call("POST", "/items/a/resolve", "https://evil.test", cookie, `{}`); w.Code != 403 || len(paths) != before {
		t.Fatal("cross-site decision forwarded")
	}
	if w = call("POST", "/items/a/resolve", "https://tachyon.test", cookie, `{"expected_revision":3,"presented_projection_revision":2,"response":{"kind":"approval","decision":"approved"}}`); w.Code != 409 || !strings.Contains(w.Body.String(), "revision_conflict") {
		t.Fatal("upstream conflict concealed", w.Code, w.Body.String())
	}
	if w = call("POST", "/items/a/delete", "https://tachyon.test", cookie, `{}`); w.Code != 404 {
		t.Fatal("unknown operation allowed")
	}
}
func TestInvalidConfigurationAndResponses(t *testing.T) {
	b := New("http://remote.example")
	r := httptest.NewRequest("GET", "http://tachyon.test"+Prefix+"/inbox", nil)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>login</html>")) }))
	defer upstream.Close()
	b = New(upstream.URL)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "test"})
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "invalid_response") {
		t.Fatal(w.Code, w.Body.String())
	}
}
