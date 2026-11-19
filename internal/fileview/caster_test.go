package fileview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

type fakeArt struct {
	mime  string
	revs  []string // content per rev, rev = index+1
	title string
}

// fakeCaster speaks the three caster routes the adapter uses and records the requests it saw.
type fakeCaster struct {
	srv   *httptest.Server
	mu    sync.Mutex
	arts  map[string]*fakeArt // "ns/name"
	token string
	paths []string
	auths []string
	skew  bool // answer /v1/get with different bytes than the head hash
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func newFakeCaster(t *testing.T) *fakeCaster {
	f := &fakeCaster{arts: map[string]*fakeArt{}, token: "cst_secret_token"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCaster) parse(ref string) (*fakeArt, string, int) {
	rest := strings.TrimPrefix(ref, "caster://")
	rev := 0
	if i := strings.Index(rest, "@"); i >= 0 {
		rev, _ = strconv.Atoi(rest[i+1:])
		rest = rest[:i]
	}
	a := f.arts[rest]
	if a != nil && rev == 0 {
		rev = len(a.revs)
	}
	if a != nil && (rev < 1 || rev > len(a.revs)) {
		return nil, rest, 0
	}
	return a, rest, rev
}

func (f *fakeCaster) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"kind":"unauthenticated","message":"bad token cst_secret_token"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/head":
		a, _, rev := f.parse(r.URL.Query().Get("ref"))
		if a == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"state":"not_found"}`))
			return
		}
		c := a.revs[rev-1]
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": r.URL.Query().Get("ref"), "state": "resolved", "title": a.title, "mime": a.mime,
			"size": len(c), "revision": strconv.Itoa(rev), "sha256": sum(c), "floating": !strings.Contains(r.URL.Query().Get("ref"), "@")})
	case "/v1/get":
		a, _, rev := f.parse(r.URL.Query().Get("ref"))
		if a == nil {
			w.WriteHeader(404)
			return
		}
		body := a.revs[rev-1]
		if f.skew {
			body += " moved"
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	case "/v1/log":
		a := f.arts[r.URL.Query().Get("name")]
		if a == nil {
			w.WriteHeader(404)
			return
		}
		var revs []map[string]any
		for i := len(a.revs); i >= 1; i-- {
			revs = append(revs, map[string]any{"rev": i, "cas": "sha256:" + sum(a.revs[i-1]), "title": a.title + " v" + strconv.Itoa(i), "at": time.Unix(int64(1000+i), 0).UTC().Format(time.RFC3339)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": r.URL.Query().Get("name"), "revisions": revs})
	default:
		w.WriteHeader(404)
	}
}

type cenv struct {
	fenv
	fake  *fakeCaster
	c     *Caster
	token string
	fe    *frameEnv
}

func casterSetup(t *testing.T) cenv {
	t.Helper()
	fe := filesSetup(t)
	fake := newFakeCaster(t)
	fake.arts["designer/page"] = &fakeArt{mime: "text/html", title: "Page", revs: []string{
		"<!DOCTYPE html><html><body>one</body></html>", "<!DOCTYPE html><html><body>two</body></html>", "<!DOCTYPE html><html><body>three</body></html>"}}
	fake.arts["designer/notes"] = &fakeArt{mime: "text/markdown", title: "Notes", revs: []string{"# hello\n", "# hello again\n"}}
	fake.arts["designer/pic"] = &fakeArt{mime: "image/png", title: "Pic", revs: []string{"\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)}}
	fake.arts["designer/logo"] = &fakeArt{mime: "image/svg+xml", title: "Logo", revs: []string{`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`}}
	fake.arts["designer/big"] = &fakeArt{mime: "text/plain", title: "Big", revs: []string{strings.Repeat("line of log\n", 200)}}
	fake.arts["secrets/vault"] = &fakeArt{mime: "text/plain", title: "Vault", revs: []string{"hidden"}}
	tok := filepath.Join(t.TempDir(), "caster.token")
	if err := os.WriteFile(tok, []byte(fake.token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Caster{Svc: fe.f.Guard.Svc, BaseURL: fake.srv.URL, TokenFile: tok, Audit: fe.f.Audit, Principal: fe.f.Principal}
	fe.f.Caster = c
	c.Mount(fe.mux, nil)
	return cenv{fenv: fe, fake: fake, c: c, token: tok}
}

func (e cenv) resolve(ref string) (int, map[string]any) {
	w := e.do("GET", fileviewapi.Prefix+"/resolve?ref="+ref, "")
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestParseCasterRef(t *testing.T) {
	good := map[string]CasterRef{
		"caster://designer/page":      {NS: "designer", Name: "page"},
		"caster://designer/page@3":    {NS: "designer", Name: "page", Rev: 3},
		"caster://scratch/a.b_c-1@12": {NS: "scratch", Name: "a.b_c-1", Rev: 12},
	}
	for in, want := range good {
		got, ok := ParseCasterRef(in)
		if !ok || got != want || got.String() != in {
			t.Errorf("%q: got %+v ok=%v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "caster://", "caster://designer", "caster://designer/", "caster:designer/page", "http://designer/page",
		"caster://Designer/page", "caster://designer/page@0", "caster://designer/page@-1", "caster://designer/page@01", "caster://designer/page@x",
		"caster://designer/page?x=1", "caster://designer/page#f", "caster://designer/a/b", "caster://designer/../x", "caster://u:p@designer/page",
		"caster://designer/page%2e", "caster://designer/" + strings.Repeat("a", 600), "caster://designer/pa ge", "caster://designer/page\n"} {
		if _, ok := ParseCasterRef(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestCasterResolve(t *testing.T) {
	e := casterSetup(t)
	code, m := e.resolve("caster://designer/page")
	if code != 200 || m["floating"] != true || m["rev"] != 3.0 || m["latest_rev"] != 3.0 || m["kind"] != "html" || m["title"] != "Page" {
		t.Fatalf("floating: %d %v", code, m)
	}
	if m["sha256"] != sum("<!DOCTYPE html><html><body>three</body></html>") {
		t.Fatalf("sha: %v", m["sha256"])
	}
	revs := m["revisions"].([]any)
	if len(revs) != 3 || revs[0].(map[string]any)["rev"] != 3.0 {
		t.Fatalf("revisions: %v", revs)
	}
	code, m = e.resolve("caster://designer/page@1")
	if code != 200 || m["floating"] != false || m["rev"] != 1.0 || m["latest_rev"] != 3.0 || m["sha256"] != sum("<!DOCTYPE html><html><body>one</body></html>") {
		t.Fatalf("pinned: %d %v", code, m)
	}
	if code, m = e.resolve("caster://designer/notes"); code != 200 || m["kind"] != "markdown" {
		t.Fatalf("markdown: %d %v", code, m)
	}
	if code, m = e.resolve("caster://designer/pic"); code != 200 || m["kind"] != "image" || m["mime"] != "image/png" {
		t.Fatalf("image: %d %v", code, m)
	}
	for _, p := range e.fake.paths {
		if p != "/v1/head" && p != "/v1/log" {
			t.Fatalf("resolve must only use head and log, saw %s", p)
		}
	}
	for _, a := range e.fake.auths {
		if a != "Bearer "+e.fake.token {
			t.Fatalf("token not sent: %q", a)
		}
	}
}

func TestCasterResolveErrors(t *testing.T) {
	e := casterSetup(t)
	for ref, want := range map[string]int{
		"caster://designer/missing":  404,
		"caster://designer/page@9":   404,
		"caster://secrets/vault":     403,
		"nonsense":                   400,
		"caster://designer/page?x=1": 400,
		"http://designer/page":       400,
		"caster://designer/a%2Fb":    400,
		"caster://designer/page%00":  400,
		"caster://Designer/page":     400,
		"":                           400,
		"caster://designer/page@03":  400,
	} {
		if code, _ := e.resolve(ref); code != want {
			t.Errorf("%q: got %d want %d", ref, code, want)
		}
	}
	w := e.do("GET", fileviewapi.Prefix+"/resolve?ref=caster://designer/page&x=1", "")
	if w.Code != 400 {
		t.Fatalf("unknown parameter: %d", w.Code)
	}
	rows := auditRows(t, e.f.Audit)
	var denied, missing int
	for _, r := range rows {
		if r.Kind == "caster" && r.Outcome == "denied" && r.DenyReason != nil {
			switch *r.DenyReason {
			case "namespace_not_allowed":
				denied++
			case "not_found_or_hidden":
				missing++
			}
		}
	}
	if denied != 1 || missing != 2 {
		t.Fatalf("audit denials: namespace=%d missing=%d rows=%d", denied, missing, len(rows))
	}
}

func TestCasterNamespaceSetting(t *testing.T) {
	e := casterSetup(t)
	n := len(e.fake.paths)
	if code, _ := e.resolve("caster://secrets/vault"); code != 403 || len(e.fake.paths) != n {
		t.Fatalf("a namespace off the list must be refused before any daemon call: %d, calls %d->%d", code, n, len(e.fake.paths))
	}
	if code, _ := e.resolve("caster://designer/page"); code != 200 {
		t.Fatal("designer is on the default list")
	}
	body := `{"app":{"caster_namespaces":["secrets"]}}`
	r := httptest.NewRequest("PUT", "http://x"+fileviewapi.Prefix+"/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	e.f.Guard.Svc.Mount(mux, nil, nil)
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("settings put: %d %s", w.Code, w.Body)
	}
	if code, _ := e.resolve("caster://secrets/vault"); code != 200 {
		t.Fatal("secrets is now allowed")
	}
	if code, _ := e.resolve("caster://designer/page"); code != 403 {
		t.Fatal("designer is now off the list")
	}
}

func TestCasterTokenProblems(t *testing.T) {
	e := casterSetup(t)
	// wrong token: the daemon says 401; the browser gets 503 and no daemon text or token
	if err := os.WriteFile(e.token, []byte("cst_wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", fileviewapi.Prefix+"/resolve?ref=caster://designer/page", "")
	if w.Code != 503 || strings.Contains(w.Body.String(), "cst_") || strings.Contains(w.Body.String(), e.fake.srv.URL) {
		t.Fatalf("wrong token: %d %s", w.Code, w.Body)
	}
	// renewed token file is picked up without a restart
	if err := os.WriteFile(e.token, []byte(e.fake.token), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.resolve("caster://designer/page"); code != 200 {
		t.Fatalf("renewed token: %d", code)
	}
	// missing or empty file
	_ = os.Remove(e.token)
	if code, _ := e.resolve("caster://designer/page"); code != 503 {
		t.Fatalf("missing token file: %d", code)
	}
	_ = os.WriteFile(e.token, []byte("  \n"), 0o600)
	if code, _ := e.resolve("caster://designer/page"); code != 503 {
		t.Fatalf("empty token file: %d", code)
	}
	// daemon down
	_ = os.WriteFile(e.token, []byte(e.fake.token), 0o600)
	e.fake.srv.Close()
	w = e.do("GET", fileviewapi.Prefix+"/resolve?ref=caster://designer/page", "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "caster_unavailable") || strings.Contains(w.Body.String(), "127.0.0.1") {
		t.Fatalf("daemon down: %d %s", w.Code, w.Body)
	}
}

func TestCasterNoRedirectFollowed(t *testing.T) {
	e := casterSetup(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect was followed with %q", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	redir := httptest.NewServer(http.RedirectHandler(other.URL, 302))
	defer redir.Close()
	e.c.BaseURL = redir.URL
	if code, _ := e.resolve("caster://designer/page"); code != 503 {
		t.Fatalf("redirect: %d", code)
	}
}

func TestCasterContent(t *testing.T) {
	e := casterSetup(t)
	get := func(ref string, hdr ...string) *httptest.ResponseRecorder {
		return e.do("GET", fileviewapi.Prefix+"/content?caster_ref="+ref, "", hdr...)
	}
	w := get("caster://designer/page@2")
	if w.Code != 200 || w.Body.String() != "<!DOCTYPE html><html><body>two</body></html>" {
		t.Fatalf("pinned html: %d %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("ETag") != `"`+sum("<!DOCTYPE html><html><body>two</body></html>")+`"` {
		t.Fatalf("html headers: %v", h)
	}
	if w = get("caster://designer/page"); !strings.Contains(w.Body.String(), "three") {
		t.Fatalf("floating serves the latest: %s", w.Body)
	}
	etag := w.Header().Get("ETag")
	if w = get("caster://designer/page", "If-None-Match", etag); w.Code != 304 {
		t.Fatalf("if-none-match: %d", w.Code)
	}
	if w = get("caster://designer/notes"); w.Code != 200 || w.Body.String() != "# hello again\n" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("markdown: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w = get("caster://designer/big", "Range", "bytes=0-10"); w.Code != 206 || w.Body.Len() != 11 || !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes 0-10/") {
		t.Fatalf("range: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w = get("caster://designer/pic"); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image: %d %v", w.Code, w.Header())
	}
	if w = get("caster://designer/logo"); w.Code != 200 || w.Header().Get("Content-Security-Policy") != fileviewapi.SVGCSP || w.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("svg: %d %v", w.Code, w.Header())
	}
	if w = get("caster://secrets/vault"); w.Code != 403 {
		t.Fatalf("namespace: %d", w.Code)
	}
	if w = get("caster://designer/missing"); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	if w = e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/page&path=/tmp/x", ""); w.Code != 400 {
		t.Fatalf("both: %d", w.Code)
	}
}

func TestCasterContentCapAndSkew(t *testing.T) {
	e := casterSetup(t)
	e.fake.arts["designer/huge"] = &fakeArt{mime: "image/png", title: "Huge", revs: []string{"\x89PNG\r\n\x1a\n" + strings.Repeat("a", 2000)}}
	// lower the image cap through the real settings route
	mux := http.NewServeMux()
	e.f.Guard.Svc.Mount(mux, nil, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("PUT", "http://x"+fileviewapi.Prefix+"/settings", strings.NewReader(`{"app":{"caps":{"text_bytes":1024,"image_bytes":1024,"html_bytes":1024}}}`)))
	if w.Code != 200 {
		t.Fatalf("caps: %d %s", w.Code, w.Body)
	}
	if w = e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/huge", ""); w.Code != 413 {
		t.Fatalf("image over cap: %d", w.Code)
	}
	if w = e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/big", ""); w.Code != 413 {
		t.Fatalf("text over cap without Range: %d", w.Code)
	}
	if w = e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/big", "", "Range", "bytes=0-3"); w.Code != 206 {
		t.Fatalf("text over cap with Range: %d", w.Code)
	}
	e.fake.skew = true
	if w = e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/notes", ""); w.Code != 409 {
		t.Fatalf("bytes that do not match the head hash: %d", w.Code)
	}
}

func TestCasterFrames(t *testing.T) {
	e := casterSetup(t)
	fe := &frameEnv{dev: e.dev, now: time.Now()}
	fe.fr = &Frames{Guard: e.f.Guard, Audit: e.f.Audit, Principal: e.f.Principal, Caster: e.c, Now: func() time.Time { return fe.now }}
	fe.mux = http.NewServeMux()
	fe.fr.Mount(fe.mux, nil)

	w := fe.mint(`{"caster_ref":"caster://designer/page@2"}`)
	if w.Code != 201 {
		t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	var m struct {
		FrameURL string `json:"frame_url"`
		ETag     string `json:"etag"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.ETag != `"`+sum("<!DOCTYPE html><html><body>two</body></html>")+`"` {
		t.Fatalf("etag: %s", m.ETag)
	}
	g := fe.get(m.FrameURL)
	if g.Code != 200 || !strings.Contains(g.Body.String(), "two") || g.Header().Get("Content-Security-Policy") != fileviewapi.FrameCSP {
		t.Fatalf("serve: %d %v %s", g.Code, g.Header(), g.Body)
	}
	if g = fe.get(m.FrameURL); g.Code != 410 {
		t.Fatalf("single use: %d", g.Code)
	}
	// floating mint pins the rev it resolved to: a later revision does not change an open frame
	w = fe.mint(`{"caster_ref":"caster://designer/page"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	e.fake.arts["designer/page"].revs = append(e.fake.arts["designer/page"].revs, "<!DOCTYPE html><html><body>four</body></html>")
	if g = fe.get(m.FrameURL); g.Code != 200 || !strings.Contains(g.Body.String(), "three") {
		t.Fatalf("pinned at mint: %d %s", g.Code, g.Body)
	}
	// bytes that changed under the same rev
	w = fe.mint(`{"caster_ref":"caster://designer/page@1"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	e.fake.skew = true
	if g = fe.get(m.FrameURL); g.Code != 410 {
		t.Fatalf("changed bytes: %d", g.Code)
	}
	e.fake.skew = false
	// refusals
	for body, want := range map[string]int{
		`{"caster_ref":"caster://designer/notes"}`:                400, // not html
		`{"caster_ref":"caster://secrets/vault"}`:                 403,
		`{"caster_ref":"caster://designer/missing"}`:              404,
		`{"caster_ref":"nope"}`:                                   400,
		`{"caster_ref":"caster://designer/page","scripts":true}`:  403,
		`{"caster_ref":"caster://designer/page","path":"/tmp/x"}`: 400,
	} {
		if w := fe.mint(body); w.Code != want {
			t.Errorf("%s: got %d want %d (%s)", body, w.Code, want, w.Body)
		}
	}
	// namespace removed between mint and serve
	w = fe.mint(`{"caster_ref":"caster://designer/page@1"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	mux := http.NewServeMux()
	e.f.Guard.Svc.Mount(mux, nil, nil)
	pw := httptest.NewRecorder()
	mux.ServeHTTP(pw, httptest.NewRequest("PUT", "http://x"+fileviewapi.Prefix+"/settings", strings.NewReader(`{"app":{"caster_namespaces":["scratch"]}}`)))
	if g = fe.get(m.FrameURL); g.Code != 410 {
		t.Fatalf("namespace removed after mint: %d", g.Code)
	}
}

func TestCasterOffMeansUnavailable(t *testing.T) {
	e := filesSetup(t)
	if w := e.do("GET", fileviewapi.Prefix+"/content?caster_ref=caster://designer/page", ""); w.Code != 501 {
		t.Fatalf("content without a caster: %d", w.Code)
	}
	fe := frameSetup(t)
	if w := fe.mint(`{"caster_ref":"caster://designer/page"}`); w.Code != 501 {
		t.Fatalf("frame without a caster: %d", w.Code)
	}
}

func auditRows(t *testing.T, a *Auditor) []AuditEntry {
	t.Helper()
	rows, _, err := a.List(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
