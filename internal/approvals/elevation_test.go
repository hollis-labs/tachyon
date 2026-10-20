package approvals

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestParseElevationTTLModes(t *testing.T) {
	for in, want := range map[string]string{"always": "always", " NEVER ": "never", "7d": "7d", "1d": "1d", "365d": "365d"} {
		if got, err := ParseElevationTTL(in); err != nil || got != want {
			t.Fatalf("%q -> %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "366d", "-1d", "12h", "1.5d", "07d", "d", "forever", "5"} {
		if _, err := ParseElevationTTL(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestElevationSettingPersistsAcrossRestartAndDefaultsToAlways(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := localstore.Open(ctx, filepath.Join(dir, "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := ElevationSettings{DB: db}
	if v, err := s.Get(ctx); err != nil || v != "always" {
		t.Fatal("default", v, err)
	}
	if _, err := s.Set(ctx, "12h"); err == nil {
		t.Fatal("invalid value stored")
	}
	if _, err := s.Set(ctx, "3d"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = ElevationCommand(ctx, dir, []string{"env"}, &out); err != nil || out.String() != "TANGENT_ELEVATION_TTL=3d\n" {
		t.Fatalf("restart lost the setting: %q %v", out.String(), err)
	}
	out.Reset()
	if err = ElevationCommand(ctx, dir, []string{"set", "never"}, &out); err != nil || !strings.HasPrefix(out.String(), "never\n") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err = ElevationCommand(ctx, dir, []string{"get"}, &out); err != nil || out.String() != "never\n" {
		t.Fatal(out.String(), err)
	}
	if ElevationCommand(ctx, dir, []string{"set", "bogus"}, &out) == nil {
		t.Fatal("CLI accepted an invalid value")
	}
}

func TestElevationSettingsAPI(t *testing.T) {
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := http.HandlerFunc(ElevationSettings{DB: db}.Handler)
	do := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/api/settings/elevation-ttl", strings.NewReader(body)))
		return w
	}
	if w := do("GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"elevation_ttl":"always"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do("PUT", `{"elevation_ttl":"2d"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"elevation_ttl":"2d"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do("PUT", `{"elevation_ttl":"12h"}`); w.Code != 400 {
		t.Fatal("invalid value accepted", w.Code)
	}
	if w := do("GET", ""); !strings.Contains(w.Body.String(), `"elevation_ttl":"2d"`) {
		t.Fatal("invalid write clobbered the setting", w.Body.String())
	}
	if w := do("DELETE", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestElevationCopyPerMode(t *testing.T) {
	for _, c := range []struct{ ttl, expires, want string }{
		{"always", "", "until you revoke it"},
		{"never", "", "not remembered"},
		{"1d", "", "lasts 1 day."},
		{"7d", "", "lasts 7 days."},
		{"7d", "2026-10-27T10:30:00Z", "until 2026-10-27 10:30 UTC"},
		{"always", "2026-11-19T00:00:00Z", "until 2026-11-19 00:00 UTC"},
		{"", "", "set by the authorization service"},
	} {
		if got := ElevationCopy(c.ttl, c.expires); !strings.Contains(got, c.want) || strings.Contains(got, "12 hours") {
			t.Fatalf("%+v -> %q", c, got)
		}
	}
}

func TestOperatorRefusalCopyFollowsTangentStatus(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{`{"elevated":false,"assurance":"loopback-unverified","elevation_ttl":"always"}`, "until you revoke it"},
		{`{"elevated":false,"assurance":"loopback-unverified","elevation_ttl":"never"}`, "not remembered"},
		{`{"elevated":false,"assurance":"loopback-unverified","elevation_ttl":"5d"}`, "lasts 5 days"},
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(c.body)) }))
		h := New(upstream.URL).RequireOperator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("passed") }))
		r := httptest.NewRequest("POST", "https://tachyon.test/x", strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://tachyon.test")
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "browser"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		upstream.Close()
		if w.Code != 403 || !strings.Contains(w.Body.String(), c.want) || strings.Contains(w.Body.String(), "12 hours") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestDeElevateAdoptsRotatedCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/de-elevate" || r.Method != "POST" {
			t.Errorf("wrong destination: %s %s", r.Method, r.URL.Path)
		}
		if c, err := r.Cookie("tangent_participant"); err != nil || c.Value != "elevated" {
			t.Error("old session cookie not forwarded")
		}
		http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "fresh", Path: "/", MaxAge: 1000})
		_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified","operator_token_configured":true,"elevation_ttl":"always"}`))
	}))
	defer upstream.Close()
	r := httptest.NewRequest("POST", "https://tachyon.test"+Prefix+"/de-elevate", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://tachyon.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "elevated"})
	w := httptest.NewRecorder()
	New(upstream.URL).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"elevated":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	adopted := false
	for _, c := range w.Result().Cookies() {
		if c.Path == CookiePath && c.Value == "fresh" && c.HttpOnly {
			adopted = true
		}
	}
	if !adopted {
		t.Fatal("rotated cookie not adopted")
	}
	// A cross-site revoke must never reach Tangent.
	r = httptest.NewRequest("POST", "https://tachyon.test"+Prefix+"/de-elevate", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "elevated"})
	w = httptest.NewRecorder()
	New(upstream.URL).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin revoke", w.Code)
	}
}
