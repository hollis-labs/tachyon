package fileview

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// FrameCSPScripts is served instead of fileviewapi.FrameCSP for a frame minted with scripts on (only while
// fileview.run_scripts_allowed is true). The document still has an opaque origin: there is no allow-same-origin.
const FrameCSPScripts = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; font-src data:; sandbox allow-scripts; frame-ancestors 'self'"

const (
	maxLiveFrames = 128
	frameKeep     = time.Minute // a used or expired id answers 410 for this long, then 404
)

var frameIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type frame struct {
	path    string
	etag    string
	scripts bool
	expires time.Time
	used    bool
}

// Frames mints and serves one-time frame documents for sandboxed HTML (V4). The 256-bit id is the capability:
// the frame route has no operator guard because a sandboxed iframe carries no cookie. A frame lives five minutes,
// is served once, and is bound to the file's etag at mint time; the bytes are read again when it is served.
type Frames struct {
	Guard     *Guard
	Audit     *Auditor
	Principal func(*http.Request) string
	Now       func() time.Time

	mu     sync.Mutex
	frames map[string]*frame
}

func (f *Frames) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Mount registers POST /frame (behind mint, the operator guard) and GET /frames/{id} (unguarded).
func (f *Frames) Mount(mux *http.ServeMux, mint func(http.Handler) http.Handler) {
	if mint == nil {
		mint = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("POST "+fileviewapi.Prefix+"/frame", mint(http.HandlerFunc(f.mint)))
	mux.HandleFunc("GET "+fileviewapi.FramesPrefix+"{id}", f.serve)
}

// sweep drops frames whose keep time has passed. Callers hold f.mu.
func (f *Frames) sweep(now time.Time) (live int) {
	for id, fr := range f.frames {
		switch {
		case now.After(fr.expires.Add(frameKeep)):
			delete(f.frames, id)
		case !fr.used && now.Before(fr.expires):
			live++
		}
	}
	return live
}

func (f *Frames) mint(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in struct {
		Path      string `json:"path"`
		CasterRef string `json:"caster_ref"`
		Scripts   bool   `json:"scripts"`
	}
	if d.Decode(&in) != nil || d.More() {
		jsonErr(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return
	}
	if (in.Path == "") == (in.CasterRef == "") {
		jsonErr(w, 400, "invalid", "Send exactly one of path or caster_ref.")
		return
	}
	if in.CasterRef != "" {
		jsonErr(w, 501, "caster_unavailable", "caster:// refs are not available yet.")
		return
	}
	file, res, den := f.Guard.Open(r.Context(), in.Path)
	if den != nil {
		f.auditDenied(r, in.Path, den.Reason)
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
	fi, err := file.Stat()
	if err != nil {
		jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such file, or it is not available.")
		return
	}
	if ki := classify(res.Path, readHead(file)); ki.kind != fileviewapi.KindHTML {
		jsonErr(w, 400, "not_html", "Only HTML is rendered in a frame.")
		return
	}
	set, err := f.Guard.Svc.Load(r.Context())
	if err != nil {
		jsonErr(w, 500, "internal", "Settings are unavailable.")
		return
	}
	if fi.Size() > set.App.Caps.HTML {
		jsonErr(w, 413, "too_large", "Over the HTML limit: show the source instead.")
		return
	}
	if in.Scripts && !set.App.RunScripts {
		jsonErr(w, 403, "scripts_not_allowed", "Running scripts is turned off in Settings.")
		return
	}
	idb := make([]byte, 32)
	if _, err := rand.Read(idb); err != nil {
		jsonErr(w, 500, "internal", "Could not mint a frame.")
		return
	}
	id := hex.EncodeToString(idb)
	now := f.now()
	fr := &frame{path: res.Path, etag: etagFor(fi), scripts: in.Scripts, expires: now.Add(fileviewapi.FrameTTLSeconds * time.Second)}
	f.mu.Lock()
	if f.frames == nil {
		f.frames = map[string]*frame{}
	}
	if f.sweep(now) >= maxLiveFrames {
		f.mu.Unlock()
		jsonErr(w, 429, "too_many_frames", "Too many open frames; try again in a moment.")
		return
	}
	f.frames[id] = fr
	f.mu.Unlock()
	if f.Audit != nil {
		root, etag := res.Root.Label, fr.etag
		p := ""
		if f.Principal != nil {
			p = f.Principal(r)
		}
		_ = f.Audit.Record(r.Context(), AuditEntry{Principal: p, Kind: "frame", Source: "host", PathOrRef: in.Path, ResolvedRoot: &root, Outcome: "ok", ETag: &etag, Count: 1})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{"frame_url": fileviewapi.FramesPrefix + id, "expires_at": fr.expires.UnixMilli(), "etag": fr.etag, "scripts": fr.scripts})
}

func (f *Frames) auditDenied(r *http.Request, p, reason string) {
	if f.Audit == nil || reason == DenyInvalid || reason == DenyNotFile {
		return
	}
	who := ""
	if f.Principal != nil {
		who = f.Principal(r)
	}
	_ = f.Audit.Record(r.Context(), AuditEntry{Principal: who, Kind: "frame", Source: "host", PathOrRef: p, Outcome: "denied", DenyReason: &reason, Count: 1})
}

func (f *Frames) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	if !frameIDPattern.MatchString(id) {
		jsonErr(w, 404, "not_found", "No such frame.")
		return
	}
	now := f.now()
	f.mu.Lock()
	fr, ok := f.frames[id]
	if ok && now.After(fr.expires.Add(frameKeep)) {
		delete(f.frames, id)
		ok = false
	}
	switch {
	case !ok:
		f.mu.Unlock()
		jsonErr(w, 404, "not_found", "No such frame.")
		return
	case fr.used || !now.Before(fr.expires):
		fr.used = true
		f.mu.Unlock()
		jsonErr(w, 410, "gone", "This frame was used or has expired. Open the file again.")
		return
	}
	fr.used = true // single use: even a failed read below spends it
	f.mu.Unlock()

	file, _, den := f.Guard.Open(r.Context(), fr.path)
	if den != nil {
		jsonErr(w, 410, "gone", "This frame was used or has expired. Open the file again.")
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || etagFor(fi) != fr.etag {
		jsonErr(w, 410, "gone", "The file changed. Open it again.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", fileviewapi.FrameCSP)
	if fr.scripts {
		h.Set("Content-Security-Policy", FrameCSPScripts)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	_, _ = io.Copy(w, io.LimitReader(file, fi.Size()))
}
