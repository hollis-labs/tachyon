package fileview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

type frameEnv struct {
	fr  *Frames
	mux *http.ServeMux
	dev string
	now time.Time
}

func frameSetup(t *testing.T) *frameEnv {
	t.Helper()
	e := filesSetup(t)
	fe := &frameEnv{dev: e.dev, now: time.Now()}
	fe.fr = &Frames{Guard: e.f.Guard, Audit: e.f.Audit, Principal: e.f.Principal, Now: func() time.Time { return fe.now }}
	fe.mux = http.NewServeMux()
	fe.fr.Mount(fe.mux, nil)
	return fe
}

func (e *frameEnv) mint(body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://x"+fileviewapi.Prefix+"/frame", strings.NewReader(body))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e *frameEnv) get(url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("GET", "http://x"+url, nil))
	return w
}

func (e *frameEnv) minted(t *testing.T, name string) string {
	t.Helper()
	w := e.mint(`{"path":"` + filepath.Join(e.dev, name) + `"}`)
	var m struct {
		FrameURL  string `json:"frame_url"`
		ExpiresAt int64  `json:"expires_at"`
		ETag      string `json:"etag"`
		Scripts   bool   `json:"scripts"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil || !strings.HasPrefix(m.FrameURL, fileviewapi.FramesPrefix) || m.Scripts || m.ETag == "" {
		t.Fatalf("mint %s: %d %s", name, w.Code, w.Body)
	}
	if m.ExpiresAt != e.now.Add(5*time.Minute).UnixMilli() {
		t.Errorf("expires_at %d", m.ExpiresAt)
	}
	return m.FrameURL
}

func TestFrameIsServedOnceWithTheSandboxHeaders(t *testing.T) {
	e := frameSetup(t)
	url := e.minted(t, "page.html")
	if id := strings.TrimPrefix(url, fileviewapi.FramesPrefix); !frameIDPattern.MatchString(id) {
		t.Fatalf("id %q is not 256 bits of hex", id)
	}
	w := e.get(url)
	h := w.Header()
	if w.Code != 200 || h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Content-Security-Policy") != fileviewapi.FrameCSP ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(w.Body.String(), "<body>") {
		t.Fatalf("%d %v %q", w.Code, h, w.Body)
	}
	if strings.Contains(h.Get("Content-Security-Policy"), "allow-same-origin") || strings.Contains(h.Get("Content-Security-Policy"), "allow-scripts") {
		t.Error("the frame can gain an origin or scripts")
	}
	if w := e.get(url); w.Code != 410 {
		t.Errorf("second use: %d", w.Code)
	}
}

func TestFrameExpiresAndBindsToTheFile(t *testing.T) {
	e := frameSetup(t)
	url := e.minted(t, "page.html")
	e.now = e.now.Add(5*time.Minute + time.Second)
	if w := e.get(url); w.Code != 410 {
		t.Errorf("expired: %d", w.Code)
	}
	e.now = e.now.Add(2 * time.Minute)
	if w := e.get(url); w.Code != 404 {
		t.Errorf("forgotten: %d", w.Code)
	}
	url = e.minted(t, "page.html")
	if err := os.WriteFile(filepath.Join(e.dev, "page.html"), []byte("<!DOCTYPE html><html><body>changed</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := e.get(url); w.Code != 410 || strings.Contains(w.Body.String(), "<body>changed") {
		t.Errorf("file changed after mint: %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"nope", strings.Repeat("0", 64), strings.Repeat("A", 64), strings.Repeat("0", 63)} {
		if w := e.get(fileviewapi.FramesPrefix + id); w.Code != 404 {
			t.Errorf("%s: %d", id, w.Code)
		}
	}
}

func TestMintRules(t *testing.T) {
	e := frameSetup(t)
	in := func(n string) string { return filepath.Join(e.dev, n) }
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"not html":                             {`{"path":"` + in("main.go") + `"}`, 400},
		"png":                                  {`{"path":"` + in("pic.png") + `"}`, 400},
		"directory":                            {`{"path":"` + in("sub") + `"}`, 400},
		"secret":                               {`{"path":"` + in(".env") + `"}`, 403},
		"escape":                               {`{"path":"` + in("to-etc") + `"}`, 403},
		"outside":                              {`{"path":"/etc/passwd"}`, 404},
		"missing":                              {`{"path":"` + in("nope.html") + `"}`, 404},
		"relative":                             {`{"path":"x.html"}`, 400},
		"neither":                              {`{}`, 400},
		"both":                                 {`{"path":"/tmp/x","caster_ref":"caster://a/b"}`, 400},
		"unknown field":                        {`{"path":"` + in("page.html") + `","x":1}`, 400},
		"not json":                             {`nope`, 400},
		"caster":                               {`{"caster_ref":"caster://designer/page"}`, 501},
		"scripts off":                          {`{"path":"` + in("page.html") + `","scripts":true}`, 403},
		"html by content even with a txt name": {`{"path":"` + in("disguised.txt") + `"}`, 201},
	} {
		w := e.mint(c.body)
		if w.Code != c.want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
		if w.Code >= 400 && strings.Contains(w.Body.String(), e.dev) {
			t.Errorf("%s echoes the path", name)
		}
	}
}

func TestMintSizeLimitAndScriptsOptIn(t *testing.T) {
	e := frameSetup(t)
	ctx := context.Background()
	apply := func(u Update) {
		if _, err := e.fr.Guard.Svc.Apply(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	on := true
	apply(Update{App: &struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	}{RunScripts: &on}})
	w := e.mint(`{"path":"` + filepath.Join(e.dev, "page.html") + `","scripts":true}`)
	var m struct {
		FrameURL string `json:"frame_url"`
		Scripts  bool   `json:"scripts"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil || !m.Scripts {
		t.Fatalf("scripts mint: %d %s", w.Code, w.Body)
	}
	got := e.get(m.FrameURL).Header().Get("Content-Security-Policy")
	if got != FrameCSPScripts || strings.Contains(got, "allow-same-origin") || !strings.Contains(got, "allow-scripts") {
		t.Errorf("csp with scripts: %q", got)
	}
	small := Caps{Text: 1 << 10, Image: 1 << 10, HTML: 16 << 10}
	apply(Update{App: &struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	}{Caps: &small}})
	big := "<!DOCTYPE html><html><body>" + strings.Repeat("x", 20<<10) + "</body></html>"
	if err := os.WriteFile(filepath.Join(e.dev, "big.html"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := e.mint(`{"path":"` + filepath.Join(e.dev, "big.html") + `"}`); w.Code != 413 {
		t.Errorf("over the HTML cap: %d", w.Code)
	}
}

func TestLiveFrameCapAndAudit(t *testing.T) {
	e := frameSetup(t)
	for i := 0; i < maxLiveFrames; i++ {
		e.minted(t, "page.html")
	}
	if w := e.mint(`{"path":"` + filepath.Join(e.dev, "page.html") + `"}`); w.Code != 429 {
		t.Errorf("past the cap: %d", w.Code)
	}
	e.now = e.now.Add(6 * time.Minute)
	e.minted(t, "page.html") // expired frames free their slots
	rows, _, err := e.fr.Audit.List(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Kind == "frame" && r.Outcome == "ok" && r.ETag != nil && r.Principal == "browser-session:test" {
			n++
		}
	}
	if n != maxLiveFrames+1 {
		t.Errorf("%d frame audit rows", n)
	}
}
