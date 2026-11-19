package fileviewapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed fixtures openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Stub serves the fixtures. Handlers are mounted by pattern, so an unknown path
// is a 404 here and never falls through to the unguarded /api/plugins/
// catch-all. Writes validate their bodies like the real routes will, but
// change nothing.
type Stub struct{}

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	h := func(pattern string, f http.HandlerFunc) {
		method, p, _ := strings.Cut(pattern, " ")
		m.HandleFunc(method+" "+Prefix+p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Tachyon-Fileview-Stub", "1")
			// Error-shape tests: X-Stub-Status: 400|403|404|409|413|429|503.
			if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 {
				fail(w, s, errCode(s), "Forced by X-Stub-Status.")
				return
			}
			f(w, r)
		})
	}
	h("POST /stat", stat)
	h("GET /content", content)
	h("POST /frame", frame)
	h("GET /frames/{id}", frameDoc)
	h("GET /resolve", resolve)
	h("GET /roots", func(w http.ResponseWriter, _ *http.Request) { fixture(w, 200, "roots") })
	h("GET /settings", func(w http.ResponseWriter, _ *http.Request) { fixture(w, 200, "settings") })
	h("PUT /settings", putSettings)
	h("GET /audit", audit)
	m.HandleFunc("GET "+Prefix+"/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(OpenAPI())
	})
	return m
}

// FrameHandler serves only GET <FramesPrefix>{id}; the server mounts it without
// the operator guard.
func (Stub) FrameHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET "+FramesPrefix+"{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tachyon-Fileview-Stub", "1")
		frameDoc(w, r)
	})
	return m
}

func load(name string, v any) bool {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	return err == nil && json.Unmarshal(b, v) == nil
}

func fixture(w http.ResponseWriter, status int, name string) {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		fail(w, 500, "internal", "missing fixture "+name)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func errCode(s int) string {
	switch s {
	case 400:
		return "invalid"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 413:
		return "too_large"
	case 429:
		return "rate_limited"
	case 503:
		return "not_configured"
	}
	return "internal"
}

func readBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		fail(w, 400, "invalid", "Body must be a JSON object.")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		fail(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return false
	}
	return true
}

// file is a fixture entry: the public stat fields plus the stub-only content
// pointer. Those two are never sent.
type file map[string]any

func files() []file {
	var d struct {
		Items []file `json:"items"`
	}
	load("files", &d)
	return d.Items
}

func publicStat(f file) file {
	out := file{}
	for k, v := range f {
		if k != "content" && k != "large" {
			out[k] = v
		}
	}
	return out
}

func findFile(p string) (file, bool) {
	for _, f := range files() {
		if f["path"] == p {
			return f, true
		}
	}
	return nil, false
}

func validPath(p string) bool {
	return p != "" && len(p) <= MaxPathBytes && !strings.ContainsRune(p, 0) && utf8.ValidString(p)
}

// stat answers a batch like the ref resolvers expect. Anything the fixtures do
// not know, and anything outside the roots, is the same not_found_or_hidden item.
func stat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths []string `json:"paths"`
	}
	if !readBody(w, r, 64<<10, &in) {
		return
	}
	if len(in.Paths) == 0 || len(in.Paths) > MaxStatPaths {
		fail(w, 400, "invalid", "Send 1 to 100 paths.")
		return
	}
	out := make([]file, 0, len(in.Paths))
	for _, p := range in.Paths {
		if !validPath(p) || !path.IsAbs(p) && !strings.HasPrefix(p, "~/") {
			fail(w, 400, "invalid", "Every path must be absolute (or start with ~/) and contain no NUL.")
			return
		}
		if f, ok := findFile(p); ok {
			out = append(out, publicStat(f))
			continue
		}
		out = append(out, file{"path": p, "root_label": nil, "rel_path": nil, "exists": false, "type": nil, "mime": nil, "kind": nil,
			"language": nil, "size": nil, "mtime": nil, "allowed": false, "deny_reason": DenyNotFoundHidden, "etag": nil})
	}
	write(w, 200, map[string]any{"items": out})
}

func contentFile(name string) []byte {
	b, _ := assets.ReadFile("fixtures/content/" + name)
	return b
}

// content serves the bytes of a host path or a caster ref with the real
// headers: ETag, Last-Modified, Range (206), If-None-Match (304), nosniff.
func content(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ref := q.Get("path"), q.Get("caster_ref")
	if (p == "") == (ref == "") {
		fail(w, 400, "invalid", "Send exactly one of path or caster_ref.")
		return
	}
	var name, mime, kind, etag string
	var mtime int64
	if ref != "" {
		c, status, code := casterRef(ref)
		if c == nil {
			fail(w, status, code, "That caster ref cannot be opened.")
			return
		}
		name, mime, kind, etag = c["content"].(string), c["mime"].(string), c["kind"].(string), `"`+c["sha256"].(string)+`"`
	} else {
		if !validPath(p) {
			fail(w, 400, "invalid", "path must be absolute and contain no NUL.")
			return
		}
		f, ok := findFile(p)
		if !ok || f["deny_reason"] == DenyNotFoundHidden {
			fail(w, 404, DenyNotFoundHidden, "No such file, or it is not available.")
			return
		}
		switch f["deny_reason"] {
		case nil:
		case DenyTooLarge:
			fail(w, 413, "too_large", "This file is over the viewer limit. Fetch it in ranges.")
			return
		default:
			fail(w, 403, "denied", fmt.Sprint("This file is not available (", f["deny_reason"], ")."))
			return
		}
		if f["type"] != TypeFile || f["content"] == nil {
			fail(w, 400, "invalid", "Not a regular file.")
			return
		}
		name, mime, kind, etag = f["content"].(string), f["mime"].(string), f["kind"].(string), f["etag"].(string)
		mtime = int64(f["mtime"].(float64))
		if f["large"] == true && r.Header.Get("Range") == "" {
			fail(w, 413, "too_large", "Over the 2 MiB inline limit. Fetch it in ranges.")
			return
		}
	}
	data := contentFile(name)
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	h.Set("Accept-Ranges", "bytes")
	switch kind {
	case KindImage:
		h.Set("Content-Type", mime)
		if mime == "image/svg+xml" {
			h.Set("Content-Security-Policy", SVGCSP)
		}
		h.Set("Content-Disposition", "inline")
	case KindBinary:
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment")
	default: // the UI renders markdown, code and html source itself
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Disposition", "inline")
	}
	t := time.UnixMilli(int64(mtime))
	if mtime == 0 {
		t = time.UnixMilli(1793318400000)
	}
	http.ServeContent(w, r, "", t, bytes.NewReader(data))
}

// casterRef finds a fixture ref; the status and code say why it was refused.
func casterRef(ref string) (map[string]any, int, string) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "caster" || u.Host == "" || len(ref) > 512 {
		return nil, 400, "invalid"
	}
	var d struct {
		Refs   []map[string]any `json:"refs"`
		Denied []string         `json:"denied_namespaces"`
	}
	load("caster", &d)
	for _, ns := range d.Denied {
		if u.Host == ns {
			return nil, 403, "namespace_not_allowed"
		}
	}
	for _, c := range d.Refs {
		if c["ref"] == ref {
			return c, 200, ""
		}
	}
	return nil, 404, "not_found"
}

func resolve(w http.ResponseWriter, r *http.Request) {
	c, status, code := casterRef(r.URL.Query().Get("ref"))
	if c == nil {
		fail(w, status, code, "That caster ref cannot be resolved.")
		return
	}
	out := map[string]any{}
	for k, v := range c {
		if k != "content" && k != "allowed" {
			out[k] = v
		}
	}
	write(w, 200, out)
}

// frame mints a one-time frame document URL for an HTML file or caster ref.
func frame(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		CasterRef string `json:"caster_ref"`
		Scripts   bool   `json:"scripts"`
	}
	if !readBody(w, r, 8<<10, &in) {
		return
	}
	if (in.Path == "") == (in.CasterRef == "") {
		fail(w, 400, "invalid", "Send exactly one of path or caster_ref.")
		return
	}
	var etag string
	if in.CasterRef != "" {
		c, status, code := casterRef(in.CasterRef)
		if c == nil {
			fail(w, status, code, "That caster ref cannot be opened.")
			return
		}
		if c["kind"] != KindHTML {
			fail(w, 400, "not_html", "Only HTML is rendered in a frame.")
			return
		}
		etag = `"` + c["sha256"].(string) + `"`
	} else {
		f, ok := findFile(in.Path)
		if !validPath(in.Path) || !ok || f["deny_reason"] == DenyNotFoundHidden {
			fail(w, 404, DenyNotFoundHidden, "No such file, or it is not available.")
			return
		}
		if f["deny_reason"] != nil {
			fail(w, 403, "denied", fmt.Sprint("This file is not available (", f["deny_reason"], ")."))
			return
		}
		if f["kind"] != KindHTML {
			fail(w, 400, "not_html", "Only HTML is rendered in a frame.")
			return
		}
		if f["size"].(float64) > HTMLBytes {
			fail(w, 413, "too_large", "Over 2 MiB: show the source instead.")
			return
		}
		etag = f["etag"].(string)
	}
	if in.Scripts { // fileview.run_scripts_allowed is false in the settings fixture
		fail(w, 403, "scripts_not_allowed", "Running scripts is turned off in Settings.")
		return
	}
	write(w, 201, map[string]any{"frame_url": FramesPrefix + StubFrameID, "expires_at": time.Now().UnixMilli() + FrameTTLSeconds*1000, "etag": etag, "scripts": false})
}

// frameDoc serves a frame document. The id is the only capability: no cookie, no
// Origin check. Isolation comes from the sandbox CSP, not from a second origin.
func frameDoc(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("id") {
	case StubFrameID:
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", FrameCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(contentFile("report.html"))
	case StubGoneFrameID:
		w.Header().Set("Cache-Control", "no-store")
		fail(w, 410, "gone", "This frame was used or has expired. Open the file again.")
	default:
		w.Header().Set("Cache-Control", "no-store")
		fail(w, 404, "not_found", "No such frame.")
	}
}

// putSettings validates the way the real route will. App-level fields change
// who can read what, so they are checked hard; nothing is stored.
func putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		App *struct {
			Roots            *[]string `json:"roots"`
			DenyExtra        *[]string `json:"deny_extra"`
			Caps             *Caps     `json:"caps"`
			CasterNamespaces *[]string `json:"caster_namespaces"`
			RunScripts       *bool     `json:"run_scripts_allowed"`
			AllowUserRoots   *bool     `json:"allow_user_roots"`
			AuditRetention   *int      `json:"audit_retention_days"`
		} `json:"app"`
		User *struct {
			Roots *[]string `json:"roots"`
		} `json:"user"`
	}
	if !readBody(w, r, MaxSettingsBody, &in) {
		return
	}
	if in.App == nil && in.User == nil {
		fail(w, 400, "invalid", "Send app and/or user settings.")
		return
	}
	var s struct {
		App  map[string]any `json:"app"`
		User map[string]any `json:"user"`
	}
	load("settings", &s)
	allowUser := s.App["allow_user_roots"] == true // the stored value: a request cannot unlock itself
	if a := in.App; a != nil {
		if a.Roots != nil {
			if code, msg := checkRoots(*a.Roots); code != "" {
				fail(w, 400, code, msg)
				return
			}
			s.App["roots"] = *a.Roots
		}
		if a.DenyExtra != nil {
			if len(*a.DenyExtra) > 64 || anyBad(*a.DenyExtra, 128) {
				fail(w, 400, "invalid", "deny_extra: up to 64 patterns of at most 128 characters.")
				return
			}
			s.App["deny_extra"] = *a.DenyExtra
		}
		if a.Caps != nil {
			if a.Caps.Text < 1<<10 || a.Caps.Text > 64<<20 || a.Caps.Image < 1<<10 || a.Caps.Image > 256<<20 || a.Caps.HTML < 1<<10 || a.Caps.HTML > 16<<20 {
				fail(w, 400, "invalid", "caps are out of range.")
				return
			}
			s.App["caps"] = a.Caps
		}
		if a.CasterNamespaces != nil {
			for _, ns := range *a.CasterNamespaces {
				if !nsRE(ns) {
					fail(w, 400, "invalid", "caster_namespaces must be lower-case slugs.")
					return
				}
			}
			s.App["caster_namespaces"] = *a.CasterNamespaces
		}
		if a.RunScripts != nil {
			s.App["run_scripts_allowed"] = *a.RunScripts
		}
		if a.AllowUserRoots != nil {
			s.App["allow_user_roots"] = *a.AllowUserRoots
		}
		if a.AuditRetention != nil {
			if *a.AuditRetention < 1 || *a.AuditRetention > 365 {
				fail(w, 400, "invalid", "audit_retention_days is 1 to 365.")
				return
			}
			s.App["audit_retention_days"] = *a.AuditRetention
		}
	}
	if u := in.User; u != nil && u.Roots != nil {
		if !allowUser && len(*u.Roots) > 0 {
			fail(w, 403, "user_roots_not_allowed", "The app settings do not allow personal roots.")
			return
		}
		if code, msg := checkRoots(*u.Roots); code != "" {
			fail(w, 400, code, msg)
			return
		}
		s.User["roots"] = *u.Roots
	}
	write(w, 200, s)
}

// Caps are the inline size limits in bytes.
type Caps struct {
	Text  int64 `json:"text_bytes"`
	Image int64 `json:"image_bytes"`
	HTML  int64 `json:"html_bytes"`
}

func anyBad(a []string, max int) bool {
	for _, s := range a {
		if s == "" || len(s) > max || strings.ContainsRune(s, 0) {
			return true
		}
	}
	return false
}

func nsRE(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_' || c == '.')) {
			return false
		}
	}
	return true
}

// checkRoots enforces what the real route will: absolute, no NUL, never / or the
// home directory itself, never a secret directory or a parent of one.
func checkRoots(roots []string) (string, string) {
	if len(roots) > MaxRoots || anyBad(roots, MaxPathBytes) {
		return "invalid", "roots: up to 32 absolute paths."
	}
	for _, r := range roots {
		c := path.Clean(r)
		if !path.IsAbs(c) || c == "/" || c == "/home/operator" || c == "/home" {
			return "invalid_root", "A root must be an absolute path below the home directory, not / or home itself."
		}
		for _, seg := range strings.Split(c, "/") {
			switch seg {
			case ".ssh", ".gnupg", ".aws", ".kube", "..":
				return "invalid_root", "A root cannot be or contain a secret directory."
			}
		}
	}
	return "", ""
}

func audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := DefaultAuditMax
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(w, 400, "invalid", "limit is 1 to 1000.")
			return
		}
		limit = n
	}
	var since int64
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "since must be a non-negative unix-ms time.")
			return
		}
		since = n
	}
	var d struct {
		Entries   []map[string]any `json:"entries"`
		NextSince *int64           `json:"next_since"`
	}
	load("audit", &d)
	out := d.Entries[:0:0]
	for _, e := range d.Entries {
		if int64(e["ts_ms"].(float64)) > since && len(out) < limit {
			out = append(out, e)
		}
	}
	d.Entries = out
	write(w, 200, d)
}
