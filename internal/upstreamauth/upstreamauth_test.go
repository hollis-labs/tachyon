package upstreamauth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeToken(t *testing.T, mode os.FileMode, token string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(token+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWrapSendsBearerAndFollowsRotation(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	path := writeToken(t, 0o600, "first-token")
	rt, err := Wrap(nil, srv.URL, path)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: rt}
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer first-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if err := os.WriteFile(path, []byte("second-token-longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer second-token-longer" {
		t.Fatalf("after rotation Authorization = %q", got)
	}
}

func TestWrapFailsClosedWithoutLeakingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request must not be sent")
	}))
	defer srv.Close()
	path := writeToken(t, 0o644, "secret-value")
	rt, err := Wrap(nil, srv.URL, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&http.Client{Transport: rt}).Get(srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 0644 token file")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestWrapPinsOrigin(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("token reached another origin")
	}))
	defer b.Close()
	rt, err := Wrap(nil, a.URL, writeToken(t, 0o600, "tok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&http.Client{Transport: rt}).Get(b.URL); err == nil {
		t.Fatal("expected a cross-origin request to be refused")
	}
}

func TestGuard(t *testing.T) {
	cases := []struct {
		url, file string
		ok        bool
	}{
		{"http://127.0.0.1:8990", "f", true},
		{"http://localhost:8990", "f", true},
		{"http://[::1]:8990", "f", true},
		{"unix:/tmp/tetherd.sock", "f", true},
		{"tcp:127.0.0.1:8947", "f", true},
		{"https://hub.tail1234.ts.net", "f", true},
		{"http://hub.tail1234.ts.net", "f", false},
		{"tcp:100.64.0.7:8947", "f", false},
		{"http://hub.tail1234.ts.net", "", true}, // no credential, nothing to protect
		{"ftp://x", "f", false},
	}
	for _, c := range cases {
		if err := Guard(c.url, c.file); (err == nil) != c.ok {
			t.Errorf("Guard(%q, %q) err=%v, want ok=%v", c.url, c.file, err, c.ok)
		}
	}
}

func TestWrapWithoutTokenFileIsPassthrough(t *testing.T) {
	base := http.DefaultTransport
	rt, err := Wrap(base, "http://example.invalid", "")
	if err != nil || rt != base {
		t.Fatalf("rt=%v err=%v", rt, err)
	}
}

func TestTokenFilePrefersConfig(t *testing.T) {
	t.Setenv("X_TOKEN_FILE", "/env/path")
	if got := TokenFile(map[string]string{"k": " /cfg/path "}, "k", "X_TOKEN_FILE"); got != "/cfg/path" {
		t.Fatalf("got %q", got)
	}
	if got := TokenFile(nil, "k", "X_TOKEN_FILE"); got != "/env/path" {
		t.Fatalf("got %q", got)
	}
}

func TestWithHeaderSetsMarkerUnderWrap(t *testing.T) {
	var proto, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto, auth = r.Header.Get("Tether-Protocol"), r.Header.Get("Authorization")
	}))
	defer srv.Close()
	rt, err := Wrap(WithHeader(nil, "Tether-Protocol", "1"), srv.URL, writeToken(t, 0o600, "tok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&http.Client{Transport: rt}).Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if proto != "1" || auth != "Bearer tok" {
		t.Fatalf("protocol=%q auth=%q", proto, auth)
	}
}
