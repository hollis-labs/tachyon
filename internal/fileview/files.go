package fileview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// Files serves POST /stat and GET /content for host paths (V3). Every read goes through Guard.
type Files struct {
	Guard *Guard
	// Audit, when set, records opens, denials and one row per stat batch (V6).
	Audit *Auditor
	// Principal names the caller for the audit log (a hash of the browser session, never the cookie).
	Principal func(*http.Request) string
}

// Mount registers POST /stat and GET /content under fileviewapi.Prefix, one pattern each. guard wraps both
// (RequireOperatorReads); nil means none (tests).
func (f *Files) Mount(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	if guard == nil {
		guard = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("POST "+fileviewapi.Prefix+"/stat", guard(http.HandlerFunc(f.stat)))
	mux.Handle("GET "+fileviewapi.Prefix+"/content", guard(http.HandlerFunc(f.content)))
}

// Stat is one item of the POST /stat answer.
type Stat struct {
	Path       string  `json:"path"`
	RootLabel  *string `json:"root_label"`
	RelPath    *string `json:"rel_path"`
	Exists     bool    `json:"exists"`
	Type       *string `json:"type"`
	Mime       *string `json:"mime"`
	Kind       *string `json:"kind"`
	Language   *string `json:"language"`
	Size       *int64  `json:"size"`
	MTime      *int64  `json:"mtime"`
	Allowed    bool    `json:"allowed"`
	DenyReason *string `json:"deny_reason"`
	ETag       *string `json:"etag"`
}

func pointer[T any](v T) *T { return &v }

func (f *Files) principal(r *http.Request) string {
	if f.Principal != nil {
		return f.Principal(r)
	}
	return ""
}

// etagFor is a strong validator from size, mtime and inode; there is no file watcher, clients poll it.
func etagFor(fi os.FileInfo) string {
	var ino uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d", fi.Size(), fi.ModTime().UnixNano(), ino)))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// kindInfo is what the viewer needs to know about a file's content.
type kindInfo struct{ mime, kind, language string }

var codeLanguages = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".mjs": "javascript", ".ts": "typescript", ".tsx": "tsx", ".jsx": "jsx",
	".rs": "rust", ".java": "java", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp",
	".sh": "bash", ".bash": "bash", ".zsh": "bash", ".sql": "sql", ".css": "css", ".scss": "scss", ".rb": "ruby", ".php": "php",
	".lua": "lua", ".swift": "swift", ".kt": "kotlin", ".dart": "dart", ".xml": "xml", ".vue": "vue", ".svelte": "svelte",
}

// classify decides kind and mime from the name and the first bytes. HTML and images are decided by content, never
// by extension alone; SVG is an image only when the bytes contain an svg element.
func classify(name string, head []byte) kindInfo {
	ext := strings.ToLower(path.Ext(name))
	sniff := http.DetectContentType(head)
	switch {
	case strings.HasPrefix(sniff, "text/html"):
		return kindInfo{"text/html", fileviewapi.KindHTML, ""}
	case ext == ".svg" && strings.Contains(strings.ToLower(string(head)), "<svg"):
		return kindInfo{"image/svg+xml", fileviewapi.KindImage, ""}
	case strings.HasPrefix(sniff, "image/"):
		return kindInfo{sniff, fileviewapi.KindImage, ""}
	case strings.HasPrefix(sniff, "text/") && !strings.ContainsRune(string(head), 0) && utf8.Valid(head) || len(head) == 0:
		return textKind(name, ext)
	}
	if strings.ContainsRune(string(head), 0) || !utf8.Valid(head) {
		mime := sniff
		if mime == "" || strings.HasPrefix(mime, "text/") {
			mime = "application/octet-stream"
		}
		return kindInfo{mime, fileviewapi.KindBinary, ""}
	}
	return textKind(name, ext)
}

func textKind(name, ext string) kindInfo {
	switch ext {
	case ".md", ".markdown":
		return kindInfo{"text/markdown", fileviewapi.KindMarkdown, ""}
	case ".json", ".jsonl":
		return kindInfo{"application/json", fileviewapi.KindJSON, "json"}
	case ".yaml", ".yml":
		return kindInfo{"application/yaml", fileviewapi.KindYAML, "yaml"}
	case ".toml":
		return kindInfo{"application/toml", fileviewapi.KindTOML, "toml"}
	case ".csv", ".tsv":
		return kindInfo{"text/csv", fileviewapi.KindCSV, ""}
	}
	if lang, ok := codeLanguages[ext]; ok {
		return kindInfo{"text/x-" + lang, fileviewapi.KindCode, lang}
	}
	switch strings.ToLower(path.Base(name)) {
	case "makefile":
		return kindInfo{"text/x-makefile", fileviewapi.KindCode, "makefile"}
	case "dockerfile":
		return kindInfo{"text/x-dockerfile", fileviewapi.KindCode, "dockerfile"}
	}
	return kindInfo{"text/plain", fileviewapi.KindText, ""}
}

// capFor is the largest size served inline for a kind; text beyond it is available by Range only.
func capFor(c Caps, kind string) (limit int64, rangeOK bool) {
	switch kind {
	case fileviewapi.KindImage, fileviewapi.KindBinary:
		return c.Image, false
	case fileviewapi.KindHTML:
		return c.Text, true
	}
	return c.Text, true
}

func readHead(f *os.File) []byte {
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	_, _ = f.Seek(0, io.SeekStart)
	return buf[:n]
}

func jsonErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

func (f *Files) stat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in struct {
		Paths []string `json:"paths"`
	}
	if d.Decode(&in) != nil || d.More() {
		jsonErr(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return
	}
	if len(in.Paths) == 0 || len(in.Paths) > fileviewapi.MaxStatPaths {
		jsonErr(w, 400, "invalid", "Send 1 to 100 paths.")
		return
	}
	set, err := f.Guard.Svc.Load(r.Context())
	if err != nil {
		jsonErr(w, 500, "internal", "Settings are unavailable.")
		return
	}
	items := make([]Stat, 0, len(in.Paths))
	for _, p := range in.Paths {
		if _, ok := f.Guard.expand(p); !ok {
			jsonErr(w, 400, "invalid", "Every path must be absolute (or start with ~/) and contain no NUL.")
			return
		}
		items = append(items, f.statOne(r, p, set.App.Caps))
	}
	if f.Audit != nil {
		f.Audit.RecordAsync(AuditEntry{Principal: f.principal(r), Kind: "stat", Source: "host", PathOrRef: in.Paths[0], Outcome: "ok", Count: len(in.Paths)})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (f *Files) statOne(r *http.Request, p string, caps Caps) Stat {
	res, den := f.Guard.Resolve(r.Context(), p)
	if den != nil {
		f.auditDenied(r, "stat", p, den.Reason)
		seen := den.Reason == fileviewapi.DenySecretPattern || den.Reason == fileviewapi.DenySymlinkEscape
		reason := den.Reason
		return Stat{Path: p, Exists: seen, Allowed: false, DenyReason: &reason}
	}
	out := Stat{Path: p, RootLabel: pointer(res.Root.Label), RelPath: pointer(res.Rel), Exists: true, Allowed: true,
		Size: pointer(res.Info.Size()), MTime: pointer(res.Info.ModTime().UnixMilli()), ETag: pointer(etagFor(res.Info))}
	switch {
	case res.Info.IsDir():
		out.Type, out.Size = pointer(fileviewapi.TypeDir), nil
		return out
	case !res.Info.Mode().IsRegular():
		out.Type, out.Size, out.MTime, out.ETag = pointer(fileviewapi.TypeOther), nil, nil, nil
		return out
	}
	out.Type = pointer(fileviewapi.TypeFile)
	file, den := f.Guard.OpenResolved(res)
	if den != nil {
		return Stat{Path: p, Allowed: false, DenyReason: pointer(den.Reason)}
	}
	defer file.Close()
	ki := classify(res.Path, readHead(file))
	out.Mime, out.Kind = pointer(ki.mime), pointer(ki.kind)
	if ki.language != "" {
		out.Language = pointer(ki.language)
	}
	if limit, rangeOK := capFor(caps, ki.kind); !rangeOK && res.Info.Size() > limit {
		out.Allowed, out.DenyReason = false, pointer(fileviewapi.DenyTooLarge)
	}
	return out
}

func (f *Files) auditDenied(r *http.Request, kind, p, reason string) {
	if f.Audit == nil || reason == DenyInvalid || reason == DenyNotFile {
		return
	}
	_ = f.Audit.Record(r.Context(), AuditEntry{Principal: f.principal(r), Kind: kind, Source: "host", PathOrRef: p, Outcome: "denied", DenyReason: pointer(reason), Count: 1})
}

// counting writer for bytes_served.
type countWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n += int64(n)
	return n, err
}

func (f *Files) content(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if k != "path" && k != "caster_ref" {
			jsonErr(w, 400, "invalid", "Unknown parameter.")
			return
		}
	}
	p, ref := q.Get("path"), q.Get("caster_ref")
	if (p == "") == (ref == "") {
		jsonErr(w, 400, "invalid", "Send exactly one of path or caster_ref.")
		return
	}
	if ref != "" {
		jsonErr(w, 501, "caster_unavailable", "caster:// refs are not available yet.")
		return
	}
	file, res, den := f.Guard.Open(r.Context(), p)
	if den != nil {
		f.auditDenied(r, "open", p, den.Reason)
		switch den.Reason {
		case DenyInvalid, DenyNotFile:
			jsonErr(w, 400, "invalid", "path must be an absolute path to a regular file.")
		case fileviewapi.DenySecretPattern, fileviewapi.DenySymlinkEscape:
			jsonErr(w, 403, "denied", "This file is not available ("+den.Reason+").")
		default:
			jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such file, or it is not available.")
		}
		return
	}
	defer file.Close()
	set, err := f.Guard.Svc.Load(r.Context())
	if err != nil {
		jsonErr(w, 500, "internal", "Settings are unavailable.")
		return
	}
	fi, err := file.Stat()
	if err != nil {
		jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such file, or it is not available.")
		return
	}
	ki := classify(res.Path, readHead(file))
	if limit, rangeOK := capFor(set.App.Caps, ki.kind); fi.Size() > limit && (!rangeOK || r.Header.Get("Range") == "") {
		f.auditDenied(r, "open", p, fileviewapi.DenyTooLarge)
		jsonErr(w, 413, "too_large", "This file is over the viewer limit. Fetch text in ranges.")
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etagFor(fi))
	switch ki.kind {
	case fileviewapi.KindImage:
		h.Set("Content-Type", ki.mime)
		if ki.mime == "image/svg+xml" {
			h.Set("Content-Security-Policy", fileviewapi.SVGCSP)
		}
		h.Set("Content-Disposition", "inline")
	case fileviewapi.KindBinary:
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment")
	default: // the UI renders markdown, code and html source itself
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Disposition", "inline")
	}
	cw := &countWriter{ResponseWriter: w}
	http.ServeContent(cw, r, "", fi.ModTime(), file)
	if f.Audit != nil {
		root, etag := res.Root.Label, etagFor(fi)
		_ = f.Audit.Record(r.Context(), AuditEntry{Principal: f.principal(r), Kind: "open", Source: "host", PathOrRef: p, ResolvedRoot: &root, Outcome: "ok", BytesServed: &cw.n, ETag: &etag, Count: 1})
	}
}
