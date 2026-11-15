package tetherpass

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func serve(h http.Handler, method, body string) (int, map[string]string) {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/x", nil)
	} else {
		r = httptest.NewRequest(method, "/x", strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func fixed(path string) func(*http.Request) (string, url.Values, error) {
	return func(*http.Request) (string, url.Values, error) { return path, nil, nil }
}

// No address: 503 configuration, nothing dialed. An unknown operation: 404 not_found. A redirect from
// Tether is never followed: 502. Only the reviewed fields pass, and none may be null.
func TestPassthroughEdges(t *testing.T) {
	if code, out := serve(New("", nil).Handler(Op{Target: fixed("/workers")}), "GET", ""); code != 503 || out["code"] != "configuration" {
		t.Errorf("no address: %d %v", code, out)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Tether-Protocol") != "1" {
			t.Errorf("no Tether-Protocol header")
		}
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":"yes"}`))
	}))
	defer srv.Close()
	p := New(srv.URL, nil)
	unknown := func(*http.Request) (string, url.Values, error) { return "", nil, ErrUnknown }
	if code, out := serve(p.Handler(Op{Target: unknown}), "GET", ""); code != 404 || out["code"] != "not_found" {
		t.Errorf("unknown: %d %v", code, out)
	}
	if code, out := serve(p.Handler(Op{Target: fixed("/moved")}), "GET", ""); code != 502 || out["code"] != "upstream_redirect" {
		t.Errorf("redirect: %d %v", code, out)
	}
	post := p.Handler(Op{Target: fixed("/sessions"), Fields: "launch"})
	for body, want := range map[string]int{`{"launch":"pm"}`: 200, `{"launch":null}`: 400, `{"other":1}`: 400, `[1]`: 400, `not json`: 400} {
		if code, _ := serve(post, "POST", body); code != want {
			t.Errorf("body %s: %d, want %d", body, code, want)
		}
	}
	empty := p.Handler(Op{Target: fixed("/sessions/s1/launch"), Fields: Empty})
	if code, _ := serve(empty, "POST", `{}`); code != 200 {
		t.Errorf("empty object: %d", code)
	}
	if code, _ := serve(empty, "POST", `{"x":1}`); code != 400 {
		t.Errorf("a field on an empty-only operation: %d", code)
	}
	// The redirect, the one accepted launch and the empty launch reach Tether; no refused request does.
	if hits != 3 {
		t.Errorf("Tether was called %d times, want 3", hits)
	}
}
