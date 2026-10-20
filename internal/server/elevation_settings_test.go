package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// The operator cookie is scoped to approvals.CookiePath, so this drives the
// real router with a real cookie jar: only a route under that path can ever
// see the cookie.
func TestElevationSettingRouteHonoursCookieScopeAndOperatorAssurance(t *testing.T) {
	tangent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("tangent_participant")
		switch r.URL.Path {
		case "/hitl":
			http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "minted", Path: "/"})
			w.Header().Set("Content-Type", "text/html")
		case "/api/session/elevate":
			http.SetCookie(w, &http.Cookie{Name: "tangent_participant", Value: "elevated", Path: "/"})
			_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
		case "/api/session":
			if c != nil && c.Value == "elevated" {
				_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token","elevation_ttl":"always"}`))
			} else {
				_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified","elevation_ttl":"always"}`))
			}
		default:
			t.Errorf("unexpected upstream %s", r.URL.Path)
		}
	}))
	defer tangent.Close()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: tangent.URL})
	s.store = db
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	base, _ := url.Parse(srv.URL)

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	do := func(c *http.Client, method, path, body, origin string, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	put := `{"elevation_ttl":"3d"}`
	path := approvals.ElevationSettingsPath

	// The jar proves the premise: the cookie is delivered to this path.
	op := newClient()
	do(op, "POST", approvals.Prefix+"/session", "", srv.URL, nil)
	do(op, "POST", approvals.Prefix+"/elevate", `{"token":"k"}`, srv.URL, nil)
	var sent bool
	for _, c := range op.Jar.Cookies(&url.URL{Scheme: "http", Host: base.Host, Path: path}) {
		sent = sent || c.Name == approvals.CookieName
	}
	if !sent {
		t.Fatal("browser would not send the operator cookie to the settings route")
	}
	if res := do(op, "PUT", path, put, srv.URL, nil); res.StatusCode != 200 {
		t.Fatalf("elevated operator refused: %d", res.StatusCode)
	}
	if got, _ := (approvals.ElevationSettings{DB: db}).Get(context.Background()); got != "3d" {
		t.Fatalf("not stored: %q", got)
	}
	if res := do(op, "GET", path, "", "", nil); res.StatusCode != 200 {
		t.Fatalf("read refused: %d", res.StatusCode)
	}

	reject := func(name string, c *http.Client, origin string, hdr map[string]string, want int) {
		t.Helper()
		if res := do(c, "PUT", path, `{"elevation_ttl":"never"}`, origin, hdr); res.StatusCode != want {
			t.Fatalf("%s: got %d want %d", name, res.StatusCode, want)
		}
		if got, _ := (approvals.ElevationSettings{DB: db}).Get(context.Background()); got != "3d" {
			t.Fatalf("%s: setting changed to %q", name, got)
		}
	}
	notElevated := newClient()
	do(notElevated, "POST", approvals.Prefix+"/session", "", srv.URL, nil)
	reject("not elevated", notElevated, srv.URL, nil, 403)
	reject("cross-origin", op, "https://evil.test", nil, 403)
	reject("bearer only", newClient(), srv.URL, map[string]string{"Authorization": "Bearer x"}, 401)

	// The old, out-of-scope location is gone.
	if res := do(op, "GET", "/api/settings/elevation-ttl", "", "", nil); res.StatusCode == 200 {
		t.Fatal("old settings path still served")
	}
}
