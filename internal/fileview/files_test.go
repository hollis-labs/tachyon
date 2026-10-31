package fileview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

type fenv struct {
	f   *Files
	mux *http.ServeMux
	dev string
}

func filesSetup(t *testing.T) fenv {
	t.Helper()
	e, g, dev := corpus(t)
	f := &Files{Guard: g, Audit: &Auditor{DB: e.svc.DB}, Principal: func(*http.Request) string { return "browser-session:test" }}
	mux := http.NewServeMux()
	f.Mount(mux, nil)
	w := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dev, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	w("page.html", []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>"))
	w("disguised.txt", []byte("<!DOCTYPE html><html><body>hi</body></html>"))
	w("pic.png", png)
	w("fake.png", []byte("just text"))
	w("logo.svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`))
	w("notmarkup.svg", []byte("plain words"))
	w("data.json", []byte(`{"a":1}`))
	w("main.go", []byte("package main\n"))
	w("table.csv", []byte("a,b\n1,2\n"))
	w("cfg.yaml", []byte("a: 1\n"))
	w("blob.bin", append([]byte{0, 1, 2, 3}, bytes.Repeat([]byte{0xff}, 100)...))
	w("empty.txt", nil)
	w("big.log", bytes.Repeat([]byte("line of log\n"), 200))
	return fenv{f, mux, dev}
}

func (e fenv) do(method, target string, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://x"+target, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func (e fenv) statOne(t *testing.T, p string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"paths": []string{p}})
	w := e.do("POST", fileviewapi.Prefix+"/stat", string(b))
	var out struct{ Items []map[string]any }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 {
		t.Fatalf("stat %s: %d %s", p, w.Code, w.Body)
	}
	return out.Items[0]
}

func TestStatKindsMimeAndDenials(t *testing.T) {
	e := filesSetup(t)
	cases := []struct{ file, kind, mime, lang string }{
		{"page.html", "html", "text/html", ""},
		{"disguised.txt", "html", "text/html", ""}, // decided by content, not the extension
		{"pic.png", "image", "image/png", ""},
		{"fake.png", "text", "text/plain", ""}, // extension does not make an image
		{"logo.svg", "image", "image/svg+xml", ""},
		{"notmarkup.svg", "text", "text/plain", ""},
		{"data.json", "json", "application/json", "json"},
		{"main.go", "code", "text/x-go", "go"},
		{"table.csv", "csv", "text/csv", ""},
		{"cfg.yaml", "yaml", "application/yaml", "yaml"},
		{"readme.md", "markdown", "text/markdown", ""},
		{"blob.bin", "binary", "application/octet-stream", ""},
		{"empty.txt", "text", "text/plain", ""},
	}
	for _, c := range cases {
		s := e.statOne(t, filepath.Join(e.dev, c.file))
		if s["kind"] != c.kind || s["mime"] != c.mime || s["allowed"] != true || s["etag"] == nil || s["root_label"] == nil || s["type"] != "file" {
			t.Errorf("%s: %v", c.file, s)
		}
		if (c.lang == "") != (s["language"] == nil) || (c.lang != "" && s["language"] != c.lang) {
			t.Errorf("%s language: %v", c.file, s["language"])
		}
	}
	if s := e.statOne(t, filepath.Join(e.dev, "sub")); s["type"] != "dir" || s["kind"] != nil || s["allowed"] != true {
		t.Errorf("dir: %v", s)
	}
	for p, want := range map[string]string{
		filepath.Join(e.dev, ".env"):     "secret_pattern",
		filepath.Join(e.dev, "to-etc"):   "symlink_escape",
		"/etc/passwd":                    "not_found_or_hidden",
		filepath.Join(e.dev, "missing"):  "not_found_or_hidden",
		filepath.Join(e.dev, "to-env"):   "secret_pattern",
		filepath.Join(e.dev, "dangling"): "not_found_or_hidden",
	} {
		s := e.statOne(t, p)
		if s["allowed"] != false || s["deny_reason"] != want || s["size"] != nil || s["mime"] != nil {
			t.Errorf("%s: %v", p, s)
		}
	}
	// A different etag after the file changes.
	before := e.statOne(t, filepath.Join(e.dev, "data.json"))["etag"]
	if err := os.WriteFile(filepath.Join(e.dev, "data.json"), []byte(`{"a":22}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := e.statOne(t, filepath.Join(e.dev, "data.json"))["etag"]; after == before {
		t.Error("etag did not change")
	}
}

func TestStatBodyRules(t *testing.T) {
	e := filesSetup(t)
	for name, body := range map[string]string{
		"empty list":    `{"paths":[]}`,
		"unknown field": `{"paths":["/tmp"],"x":1}`,
		"not json":      `nope`,
		"relative":      `{"paths":["dev/x"]}`,
		"nul":           `{"paths":["/tmp/a\u0000b"]}`,
		"over 100":      `{"paths":[` + strings.TrimSuffix(strings.Repeat(`"/tmp/a",`, 101), ",") + `]}`,
	} {
		if w := e.do("POST", fileviewapi.Prefix+"/stat", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// Tilde paths work and the answer keeps the request path.
	if s := e.statOne(t, "~/dev/readme.md"); s["path"] != "~/dev/readme.md" || s["allowed"] != true {
		t.Errorf("tilde: %v", s)
	}
}

func TestContentHeadersRangeAndConditional(t *testing.T) {
	e := filesSetup(t)
	get := func(name string, hdr ...string) *httptest.ResponseRecorder {
		return e.do("GET", fileviewapi.Prefix+"/content?path="+filepath.Join(e.dev, name), "", hdr...)
	}
	w := get("main.go")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		w.Header().Get("Content-Disposition") != "inline" || w.Header().Get("ETag") == "" || w.Header().Get("Cache-Control") != "private, no-cache" || w.Body.String() != "package main\n" {
		t.Fatalf("text: %d %v %q", w.Code, w.Header(), w.Body)
	}
	// HTML is served as text, never as html.
	if w := get("page.html"); w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(w.Body.String(), "<script>") {
		t.Errorf("html: %v", w.Header())
	}
	w = get("logo.svg")
	if w.Header().Get("Content-Type") != "image/svg+xml" || w.Header().Get("Content-Security-Policy") != fileviewapi.SVGCSP || w.Header().Get("Content-Disposition") != "inline" {
		t.Errorf("svg: %v", w.Header())
	}
	if w := get("pic.png"); w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("png: %v", w.Header())
	}
	if w := get("blob.bin"); w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Disposition") != "attachment" {
		t.Errorf("binary: %v", w.Header())
	}
	etag := get("main.go").Header().Get("ETag")
	if w := get("main.go", "If-None-Match", etag); w.Code != 304 {
		t.Errorf("if-none-match: %d", w.Code)
	}
	if w := get("main.go", "Range", "bytes=0-6"); w.Code != 206 || w.Body.String() != "package" || w.Header().Get("Content-Range") == "" {
		t.Errorf("range: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w := get("main.go", "Range", "bytes=0-6", "If-Range", `"stale"`); w.Code != 200 {
		t.Errorf("if-range mismatch must send the whole file: %d", w.Code)
	}
	if w := get("main.go", "Range", "bytes=900-"); w.Code != 416 {
		t.Errorf("unsatisfiable range: %d", w.Code)
	}
	if w := get("empty.txt"); w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("empty: %d", w.Code)
	}
}

func TestContentErrorsAndCaps(t *testing.T) {
	e := filesSetup(t)
	url := func(p string) string { return fileviewapi.Prefix + "/content?path=" + p }
	for name, c := range map[string]struct {
		target string
		want   int
	}{
		"secret":        {url(filepath.Join(e.dev, ".env")), 403},
		"escape":        {url(filepath.Join(e.dev, "to-etc")), 403},
		"outside":       {url("/etc/passwd"), 404},
		"missing":       {url(filepath.Join(e.dev, "nope")), 404},
		"directory":     {url(filepath.Join(e.dev, "sub")), 400},
		"fifo":          {url(filepath.Join(e.dev, "pipe")), 400},
		"relative":      {url("dev/x"), 400},
		"neither":       {fileviewapi.Prefix + "/content", 400},
		"both":          {url("/tmp/x") + "&caster_ref=caster://a/b", 400},
		"caster":        {fileviewapi.Prefix + "/content?caster_ref=caster://designer/x", 501},
		"unknown param": {url(filepath.Join(e.dev, "main.go")) + "&x=1", 400},
	} {
		w := e.do("GET", c.target, "")
		if w.Code != c.want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
		if w.Code >= 400 && strings.Contains(w.Body.String(), e.dev) {
			t.Errorf("%s echoes the path: %s", name, w.Body)
		}
	}
	// Caps: lower the text cap and the image cap through the settings.
	set := Caps{Text: 1 << 10, Image: 1 << 10, HTML: 1 << 10}
	if _, err := e.f.Guard.Svc.Apply(context.Background(), Update{App: &struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	}{Caps: &set}}); err != nil {
		t.Fatal(err)
	}
	big := url(filepath.Join(e.dev, "big.log"))
	if w := e.do("GET", big, ""); w.Code != 413 {
		t.Errorf("text over the cap without Range: %d", w.Code)
	}
	if w := e.do("GET", big, "", "Range", "bytes=0-9"); w.Code != 206 || w.Body.Len() != 10 {
		t.Errorf("text over the cap with Range: %d", w.Code)
	}
	bigPNG := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 2000)...)
	if err := os.WriteFile(filepath.Join(e.dev, "huge.png"), bigPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", url(filepath.Join(e.dev, "huge.png")), "", "Range", "bytes=0-9"); w.Code != 413 {
		t.Errorf("image over the cap, Range does not help: %d", w.Code)
	}
	if s := e.statOne(t, filepath.Join(e.dev, "huge.png")); s["allowed"] != false || s["deny_reason"] != "too_large" || s["kind"] != "image" {
		t.Errorf("stat of a big image: %v", s)
	}
	if s := e.statOne(t, filepath.Join(e.dev, "big.log")); s["allowed"] != true {
		t.Errorf("a big text file is allowed (Range): %v", s)
	}
}

func TestAuditRowsCarryNoContentOrQuery(t *testing.T) {
	e := filesSetup(t)
	e.do("GET", fileviewapi.Prefix+"/content?path="+filepath.Join(e.dev, "main.go"), "")
	e.do("GET", fileviewapi.Prefix+"/content?path="+filepath.Join(e.dev, ".env"), "")
	e.statOne(t, filepath.Join(e.dev, "main.go"))
	var found struct{ open, denied bool }
	for i := 0; i < 50 && !(found.open && found.denied); i++ {
		rows, _, err := e.f.Audit.List(context.Background(), 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Kind == "open" && r.Outcome == "ok" && r.BytesServed != nil && *r.BytesServed == int64(len("package main\n")) && r.ETag != nil && r.Principal == "browser-session:test" {
				found.open = true
			}
			if r.Kind == "open" && r.Outcome == "denied" && r.DenyReason != nil && *r.DenyReason == "secret_pattern" {
				found.denied = true
			}
		}
	}
	if !found.open || !found.denied {
		t.Errorf("audit rows missing: %+v", found)
	}
}

func TestListOmitsSecretsAndRejectsEscapes(t *testing.T) {
	e := filesSetup(t)
	w := e.do("GET", "/api/plugins/console/v1/files/list?root=dev", "")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var out struct {
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, en := range out.Entries {
		names[en.Name] = en.Kind
	}
	if names["sub"] != "dir" || names["readme.md"] != "file" || out.Entries[0].Kind != "dir" {
		t.Fatalf("entries: %+v", out.Entries)
	}
	for _, hidden := range []string{".env", "id_rsa", "to-env", "innocent.txt", "etc-dir", "to-etc"} {
		if _, ok := names[hidden]; ok {
			t.Fatalf("%s must be omitted: %+v", hidden, out.Entries)
		}
	}
	if w := e.do("GET", "/api/plugins/console/v1/files/list?root=dev&path=sub", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "notes.txt") {
		t.Fatalf("sub: %d %s", w.Code, w.Body)
	}
	for _, q := range []string{"root=dev&path=../.ssh", "root=nope", "root=dev&path=readme.md"} {
		if w := e.do("GET", "/api/plugins/console/v1/files/list?"+q, ""); w.Code != 400 && w.Code != 404 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
}
