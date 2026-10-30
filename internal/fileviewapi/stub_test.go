package fileviewapi

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const home = "/home/operator"

func do(t *testing.T, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	if !strings.HasPrefix(path, "/api/") {
		path = Prefix + path
	}
	r := httptest.NewRequest(method, "http://x"+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func statOf(t *testing.T, paths ...string) []map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"paths": paths})
	w := do(t, "POST", "/stat", string(b))
	var d struct {
		Items []map[string]any `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &d) != nil {
		t.Fatalf("stat: %d %s", w.Code, w.Body)
	}
	return d.Items
}

func TestFixturesAreWellFormedAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	err := fs.WalkDir(assets, "fixtures", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		if strings.HasSuffix(p, ".json") {
			var v any
			if json.Unmarshal(b, &v) != nil {
				t.Errorf("%s is not valid JSON", p)
			}
		}
		if m := secret.Find(b); m != nil {
			t.Errorf("%s looks like it holds a secret: %q", p, m)
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatal("no fixtures", err)
	}
}

func TestStatCoversKindsAndEveryDenyReasonWithoutProbing(t *testing.T) {
	var all []string
	for _, f := range files() {
		all = append(all, f["path"].(string))
	}
	kinds, denies := map[string]bool{}, map[string]bool{}
	for _, it := range statOf(t, all...) {
		if _, leaked := it["content"]; leaked {
			t.Errorf("%v: stub-only content pointer leaked", it["path"])
		}
		if _, leaked := it["large"]; leaked {
			t.Errorf("%v: stub-only large flag leaked", it["path"])
		}
		if k, _ := it["kind"].(string); k != "" {
			kinds[k] = true
		}
		if d, _ := it["deny_reason"].(string); d != "" {
			denies[d] = true
			if it["allowed"] != false {
				t.Errorf("%v: deny_reason with allowed", it["path"])
			}
		} else if it["allowed"] != true {
			t.Errorf("%v: not allowed without a reason", it["path"])
		}
		if it["allowed"] == true && it["type"] == TypeFile && (it["etag"] == nil || it["size"] == nil) {
			t.Errorf("%v: an allowed file needs etag and size", it["path"])
		}
	}
	for _, k := range []string{KindHTML, KindMarkdown, KindText, KindJSON, KindYAML, KindCSV, KindCode, KindImage} {
		if !kinds[k] {
			t.Errorf("no %s fixture", k)
		}
	}
	for _, d := range []string{DenySecretPattern, DenySymlinkEscape, DenyTooLarge} {
		if !denies[d] {
			t.Errorf("no %s fixture", d)
		}
	}
	// Outside the roots and unknown are indistinguishable.
	for _, it := range statOf(t, "/etc/passwd", home+"/nope/at/all", "~/dev/never") {
		if it["exists"] != false || it["allowed"] != false || it["deny_reason"] != DenyNotFoundHidden || it["size"] != nil {
			t.Errorf("%v must look hidden: %v", it["path"], it)
		}
	}
	items := statOf(t, home+"/dev")
	if items[0]["type"] != TypeDir {
		t.Errorf("a directory is reported as dir: %v", items[0])
	}
}

func TestStatValidatesTheBatch(t *testing.T) {
	long := `{"paths":["` + strings.Repeat("/a", MaxStatPaths) + `"]}`
	over := `{"paths":[` + strings.TrimSuffix(strings.Repeat(`"/a",`, MaxStatPaths+1), ",") + `]}`
	for name, body := range map[string]string{
		"empty": `{"paths":[]}`, "too many": over, "relative": `{"paths":["dev/x"]}`, "NUL": `{"paths":["/a\u0000b"]}`,
		"unknown field": `{"paths":["/a"],"follow":true}`, "not json": `x`, "too long": `{"paths":["/` + strings.Repeat("a", MaxPathBytes) + `"]}`,
	} {
		if w := do(t, "POST", "/stat", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	_ = long
}

func TestContentHeadersRangeAndCaps(t *testing.T) {
	w := do(t, "GET", "/content?path="+home+"/dev/README.md", "")
	h := w.Header()
	if w.Code != 200 || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Type"), "text/plain") ||
		h.Get("ETag") == "" || h.Get("Accept-Ranges") != "bytes" || h.Get("Last-Modified") == "" || !strings.Contains(h.Get("Cache-Control"), "no-cache") {
		t.Fatalf("markdown: %d %v", w.Code, h)
	}
	if !strings.HasPrefix(w.Body.String(), "# Project notes") {
		t.Error("wrong body")
	}
	// HTML is served as text, never as html.
	if ct := do(t, "GET", "/content?path="+home+"/dev/site/report.html", "").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("html content type %q", ct)
	}
	// Conditional and range requests.
	if w2 := do(t, "GET", "/content?path="+home+"/dev/README.md", "", "If-None-Match", h.Get("ETag")); w2.Code != 304 {
		t.Errorf("If-None-Match: %d", w2.Code)
	}
	if w2 := do(t, "GET", "/content?path="+home+"/dev/README.md", "", "Range", "bytes=0-6"); w2.Code != 206 || w2.Body.String() != "# Proje" {
		t.Errorf("Range: %d %q", w2.Code, w2.Body)
	}
	// A big file is refused inline and served by range.
	log := "/content?path=" + home + "/Downloads/build.log"
	if w2 := do(t, "GET", log, ""); w2.Code != 413 || !strings.Contains(w2.Body.String(), "too_large") {
		t.Errorf("large inline: %d", w2.Code)
	}
	if w2 := do(t, "GET", log, "", "Range", "bytes=0-9"); w2.Code != 206 {
		t.Errorf("large range: %d", w2.Code)
	}
	// Images and SVG.
	if w2 := do(t, "GET", "/content?path="+home+"/Downloads/chart.png", ""); w2.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w2.Body.String(), "\x89PNG") {
		t.Errorf("png: %v", w2.Header())
	}
	svg := do(t, "GET", "/content?path="+home+"/Downloads/logo.svg", "")
	if svg.Header().Get("Content-Type") != "image/svg+xml" || svg.Header().Get("Content-Security-Policy") != SVGCSP {
		t.Errorf("svg must carry the sandbox CSP: %v", svg.Header())
	}
	// Refusals.
	for name, c := range map[string]struct {
		path string
		want int
		code string
	}{
		"secret":        {"?path=" + home + "/.ssh/id_ed25519", 403, "denied"},
		"symlink":       {"?path=" + home + "/dev/link-to-etc", 403, "denied"},
		"too large":     {"?path=" + home + "/Downloads/dump.bin", 413, "too_large"},
		"outside":       {"?path=/etc/passwd", 404, DenyNotFoundHidden},
		"dir":           {"?path=" + home + "/dev", 400, "invalid"},
		"both":          {"?path=/a&caster_ref=caster://x/y", 400, "invalid"},
		"neither":       {"", 400, "invalid"},
		"relative":      {"?path=dev/x", 404, DenyNotFoundHidden},
		"caster denied": {"?caster_ref=caster://private/notes", 403, "namespace_not_allowed"},
		"caster gone":   {"?caster_ref=caster://designer/missing", 404, "not_found"},
		"caster bad":    {"?caster_ref=http://designer/x", 400, "invalid"},
	} {
		w := do(t, "GET", "/content"+c.path, "")
		if w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "ssh") && c.code == "denied" {
			t.Errorf("%s: error echoes the path", name)
		}
	}
}

func TestCasterRefsResolveWithRevisions(t *testing.T) {
	base := "caster://designer/tachyon-builder-golden-page"
	var floating, pinned map[string]any
	json.Unmarshal(do(t, "GET", "/resolve?ref="+base, "").Body.Bytes(), &floating)
	json.Unmarshal(do(t, "GET", "/resolve?ref="+base+"@2", "").Body.Bytes(), &pinned)
	if floating["floating"] != true || floating["rev"] != float64(3) || floating["latest_rev"] != float64(3) {
		t.Errorf("floating ref: %v", floating)
	}
	if pinned["floating"] != false || pinned["rev"] != float64(2) || len(pinned["revisions"].([]any)) != 3 {
		t.Errorf("pinned ref: %v", pinned)
	}
	if _, leaked := pinned["content"]; leaked {
		t.Error("stub content pointer leaked")
	}
	w := do(t, "GET", "/content?caster_ref="+base+"@2", "")
	if w.Code != 200 || w.Header().Get("ETag") != `"`+pinned["sha256"].(string)+`"` || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("caster content: %d %v", w.Code, w.Header())
	}
	for ref, want := range map[string]int{"caster://private/x": 403, "caster://designer/missing": 404, "": 400, "file:///etc/passwd": 400} {
		if w := do(t, "GET", "/resolve?ref="+ref, ""); w.Code != want {
			t.Errorf("resolve %q: %d, want %d", ref, w.Code, want)
		}
	}
}

func TestFrameMintAndServe(t *testing.T) {
	w := do(t, "POST", "/frame", `{"path":"`+home+`/dev/site/report.html"}`)
	var f struct {
		FrameURL  string `json:"frame_url"`
		ExpiresAt int64  `json:"expires_at"`
		Etag      string
		Scripts   bool
	}
	if json.Unmarshal(w.Body.Bytes(), &f) != nil || w.Code != 201 || !strings.HasPrefix(f.FrameURL, FramesPrefix) || f.Scripts || f.ExpiresAt == 0 || f.Etag == "" {
		t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	if !regexp.MustCompile(`/[0-9a-f]{64}$`).MatchString(f.FrameURL) {
		t.Errorf("frame id is not a 256-bit hex id: %s", f.FrameURL)
	}
	doc := do(t, "GET", f.FrameURL, "")
	h := doc.Header()
	if doc.Code != 200 || h.Get("Content-Security-Policy") != FrameCSP || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("frame doc: %d %v", doc.Code, h)
	}
	if strings.Contains(FrameCSP, "allow-same-origin") || !strings.Contains(FrameCSP, "sandbox") || strings.Contains(FrameCSP, "script-src") {
		t.Error("the frame CSP must sandbox without same-origin and allow no scripts")
	}
	if w := do(t, "GET", FramesPrefix+StubGoneFrameID, ""); w.Code != 410 {
		t.Errorf("used frame: %d", w.Code)
	}
	if w := do(t, "GET", FramesPrefix+strings.Repeat("0", 64), ""); w.Code != 404 {
		t.Errorf("unknown frame: %d", w.Code)
	}
	if w := do(t, "POST", "/frame", `{"caster_ref":"caster://designer/tachyon-builder-golden-page@1"}`); w.Code != 201 {
		t.Errorf("caster frame: %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]struct {
		body string
		want int
		code string
	}{
		"markdown":      {`{"path":"` + home + `/dev/README.md"}`, 400, "not_html"},
		"secret":        {`{"path":"` + home + `/.ssh/id_ed25519"}`, 403, "denied"},
		"outside":       {`{"path":"/etc/hosts"}`, 404, DenyNotFoundHidden},
		"both":          {`{"path":"/a","caster_ref":"caster://designer/x"}`, 400, "invalid"},
		"neither":       {`{}`, 400, "invalid"},
		"scripts off":   {`{"path":"` + home + `/dev/site/report.html","scripts":true}`, 403, "scripts_not_allowed"},
		"caster denied": {`{"caster_ref":"caster://private/x"}`, 403, "namespace_not_allowed"},
		"unknown field": {`{"path":"/a","sandbox":"off"}`, 400, "invalid"},
	} {
		if w := do(t, "POST", "/frame", c.body); w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestFrameHandlerServesOnlyFrames(t *testing.T) {
	r := httptest.NewRequest("GET", "http://x"+FramesPrefix+StubFrameID, nil)
	w := httptest.NewRecorder()
	Stub{}.FrameHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("frame: %d", w.Code)
	}
	for _, p := range []string{Prefix + "/roots", Prefix + "/settings", Prefix + "/content?path=" + home + "/dev/README.md"} {
		w = httptest.NewRecorder()
		Stub{}.FrameHandler().ServeHTTP(w, httptest.NewRequest("GET", "http://x"+p, nil))
		if w.Code != 404 {
			t.Errorf("the unguarded handler serves %s (%d)", p, w.Code)
		}
	}
}

func TestRootsSettingsAndAudit(t *testing.T) {
	for _, p := range []string{"/roots", "/settings", "/audit", "/openapi.yaml"} {
		if w := do(t, "GET", p, ""); w.Code != 200 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	var roots struct {
		Roots []map[string]any
	}
	json.Unmarshal(do(t, "GET", "/roots", "").Body.Bytes(), &roots)
	for _, r := range roots.Roots {
		if r["writable"] != false {
			t.Errorf("root %v is writable: the viewer is read-only", r["id"])
		}
	}
	ok := `{"app":{"roots":["` + home + `/notes"],"caster_namespaces":["designer"],"caps":{"text_bytes":2097152,"image_bytes":1048576,"html_bytes":2097152}}}`
	if w := do(t, "PUT", "/settings", ok); w.Code != 200 {
		t.Errorf("put: %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]struct {
		body string
		want int
		code string
	}{
		"root /":           {`{"app":{"roots":["/"]}}`, 400, "invalid_root"},
		"home itself":      {`{"app":{"roots":["` + home + `"]}}`, 400, "invalid_root"},
		"relative":         {`{"app":{"roots":["notes"]}}`, 400, "invalid_root"},
		"ssh":              {`{"app":{"roots":["` + home + `/.ssh"]}}`, 400, "invalid_root"},
		"inside ssh":       {`{"app":{"roots":["` + home + `/.ssh/keys"]}}`, 400, "invalid_root"},
		"dotdot":           {`{"app":{"roots":["` + home + `/dev/../.ssh"]}}`, 400, "invalid_root"},
		"caps too big":     {`{"app":{"caps":{"text_bytes":999999999999,"image_bytes":1,"html_bytes":1}}}`, 400, "invalid"},
		"bad namespace":    {`{"app":{"caster_namespaces":["De Signer"]}}`, 400, "invalid"},
		"retention":        {`{"app":{"audit_retention_days":0}}`, 400, "invalid"},
		"unknown":          {`{"app":{"allow_everything":true}}`, 400, "invalid"},
		"empty":            {`{}`, 400, "invalid"},
		"user roots off":   {`{"user":{"roots":["` + home + `/mine"]}}`, 403, "user_roots_not_allowed"},
		"user roots empty": {`{"user":{"roots":[]}}`, 200, "roots"},
	} {
		if w := do(t, "PUT", "/settings", c.body); w.Code != c.want || !strings.Contains(w.Body.String(), c.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := do(t, "PUT", "/settings", `{"app":{"allow_user_roots":true},"user":{"roots":["`+home+`/mine"]}}`); w.Code != 403 {
		t.Errorf("the app flag in the same request must not unlock user roots: %d", w.Code)
	}
	var a struct {
		Entries []map[string]any
	}
	json.Unmarshal(do(t, "GET", "/audit", "").Body.Bytes(), &a)
	denied := false
	for _, e := range a.Entries {
		denied = denied || e["outcome"] == "denied"
		if _, has := e["content"]; has {
			t.Error("the audit log must not hold content")
		}
	}
	if !denied {
		t.Error("no denied audit fixture")
	}
	json.Unmarshal(do(t, "GET", "/audit?limit=2", "").Body.Bytes(), &a)
	if len(a.Entries) != 2 {
		t.Errorf("limit=2 gave %d", len(a.Entries))
	}
	for _, p := range []string{"/audit?limit=0", "/audit?limit=x", "/audit?since=-1"} {
		if w := do(t, "GET", p, ""); w.Code != 400 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
}

func TestForcedStatusAndUnknownPaths(t *testing.T) {
	if do(t, "GET", "/roots", "", "X-Stub-Status", "503").Code != 503 {
		t.Error("X-Stub-Status not honoured")
	}
	if do(t, "GET", "/nope", "").Code != 404 {
		t.Error("unknown path must be 404")
	}
}

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	doc := string(OpenAPI())
	for _, p := range []string{"/stat:", "/content:", "/frame:", "/frames/{id}:", "/resolve:", "/roots:", "/settings:", "/audit:"} {
		if !strings.Contains(doc, "\n  "+p) {
			t.Errorf("openapi.yaml does not document %s", p)
		}
	}
}
