package fileview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

const (
	// DefaultCasterURL is where the caster daemon listens on the Tachyon host.
	DefaultCasterURL = "http://127.0.0.1:8991"

	casterMaxMeta  = 1 << 20  // head and log answers
	casterMaxBytes = 32 << 20 // absolute ceiling on any fetched blob, above the per-kind caps
	casterTimeout  = 10 * time.Second
	maxCasterRef   = 512

	denyNamespace = "namespace_not_allowed"
)

var (
	casterNSPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	casterNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Caster reads stored artifacts from the caster daemon with a dedicated read-scoped device token (F-V5), made
// once with `caster pair -scope read -label tachyon`. It uses only /v1/head, /v1/get and /v1/log, never the
// operator key and never caster.resolve. Caster has no per-namespace permission, so Tachyon adds the allow-list
// in the fileview.caster_namespaces setting. Bytes are untrusted and are never interpreted.
type Caster struct {
	Svc       *Service
	BaseURL   string // empty means DefaultCasterURL
	TokenFile string // read on every request, so a renewed token needs no restart
	HTTP      *http.Client
	Audit     *Auditor
	Principal func(*http.Request) string
}

// casterErr carries the HTTP answer for a failed lookup. Messages never include the token, the daemon address
// or the daemon's own error text.
type casterErr struct {
	status int
	code   string
	msg    string
	deny   string // set when the failure is an audited denial
}

func (e *casterErr) Error() string { return e.code }

var (
	errCasterDown     = &casterErr{status: 503, code: "caster_unavailable", msg: "The caster service is not available."}
	errCasterNotFound = &casterErr{status: 404, code: "not_found", msg: "No such caster ref.", deny: ""}
	errCasterChanged  = &casterErr{status: 409, code: "changed", msg: "The artifact changed while it was read. Try again."}
	errCasterTooLarge = &casterErr{status: 413, code: "too_large", msg: "This artifact is over the viewer limit."}
)

// CasterRef is a parsed caster://ns/name[@rev].
type CasterRef struct {
	NS, Name string
	Rev      int // 0 = floating
}

func (r CasterRef) String() string {
	s := "caster://" + r.NS + "/" + r.Name
	if r.Rev > 0 {
		s += "@" + strconv.Itoa(r.Rev)
	}
	return s
}

// ParseCasterRef accepts only caster://ns/name or caster://ns/name@rev: no query, fragment, userinfo or extra
// path segments.
func ParseCasterRef(s string) (CasterRef, bool) {
	rest, ok := strings.CutPrefix(s, "caster://")
	if !ok || len(s) > maxCasterRef {
		return CasterRef{}, false
	}
	ns, name, ok := strings.Cut(rest, "/")
	if !ok {
		return CasterRef{}, false
	}
	var r CasterRef
	if n, rev, pinned := strings.Cut(name, "@"); pinned {
		v, err := strconv.Atoi(rev)
		if err != nil || v < 1 || rev != strconv.Itoa(v) {
			return CasterRef{}, false
		}
		name, r.Rev = n, v
	}
	if !casterNSPattern.MatchString(ns) || !casterNamePattern.MatchString(name) {
		return CasterRef{}, false
	}
	r.NS, r.Name = ns, name
	return r, true
}

// casterMeta is the /v1/head answer.
type casterMeta struct {
	State    string `json:"state"`
	Title    string `json:"title"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
	Floating bool   `json:"floating"`
}

type casterRevision struct {
	Rev    int    `json:"rev"`
	SHA256 string `json:"sha256"`
	Size   *int64 `json:"size,omitempty"`
	At     int64  `json:"at"`
	Title  string `json:"title"`
}

// CasterDoc is a resolved artifact: the pinned ref bytes are read from, plus the metadata the viewer shows.
type CasterDoc struct {
	Ref       CasterRef // as asked
	Pinned    CasterRef // with the resolved rev
	Title     string
	Mime      string
	Size      int64
	SHA256    string
	Rev       int
	Floating  bool
	info      kindInfo
	LatestRev int
	Revisions []casterRevision
}

func (c *Caster) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: casterTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *Caster) token() (string, error) {
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", errors.New("empty token file")
	}
	return t, nil
}

// get performs one authenticated GET and returns the body, capped at max.
func (c *Caster) get(ctx context.Context, path string, q url.Values, max int64) ([]byte, int, error) {
	if c.TokenFile == "" {
		return nil, 0, errCasterDown
	}
	tok, err := c.token()
	if err != nil {
		return nil, 0, errCasterDown
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultCasterURL
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return nil, 0, errCasterDown
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, errCasterDown
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "tachyon-fileview")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, 0, errCasterDown
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, 0, errCasterDown
	}
	if int64(len(b)) > max {
		return nil, 0, errCasterTooLarge
	}
	return b, resp.StatusCode, nil
}

func (c *Caster) allowed(ctx context.Context, ns string) (bool, error) {
	set, err := c.Svc.Load(ctx)
	if err != nil {
		return false, &casterErr{status: 500, code: "internal", msg: "Settings are unavailable."}
	}
	return slices.Contains(set.App.CasterNamespaces, ns), nil
}

// Lookup parses and authorises a ref, then reads its metadata. withLog also reads the revision history.
func (c *Caster) Lookup(ctx context.Context, raw string, withLog bool) (*CasterDoc, *casterErr) {
	ref, ok := ParseCasterRef(raw)
	if !ok {
		return nil, &casterErr{status: 400, code: "invalid", msg: "Send a ref like caster://namespace/name or caster://namespace/name@3."}
	}
	if ok, err := c.allowed(ctx, ref.NS); err != nil {
		return nil, err.(*casterErr)
	} else if !ok {
		return nil, &casterErr{status: 403, code: denyNamespace, msg: "That caster namespace is not available in the viewer.", deny: denyNamespace}
	}
	body, status, err := c.get(ctx, "/v1/head", url.Values{"ref": {ref.String()}}, casterMaxMeta)
	if err != nil {
		return nil, asCasterErr(err)
	}
	var m casterMeta
	if json.Unmarshal(body, &m) != nil {
		return nil, errCasterDown
	}
	switch {
	case status == 404 || m.State == "not_found":
		return nil, &casterErr{status: 404, code: "not_found", msg: "No such caster ref.", deny: fileviewapi.DenyNotFoundHidden}
	case status != 200 || m.State != "resolved": // includes a refused token or scope: do not tell the browser which
		return nil, errCasterDown
	}
	doc := &CasterDoc{Ref: ref, Title: m.Title, Mime: m.Mime, Size: m.Size, SHA256: m.SHA256, Floating: ref.Rev == 0}
	doc.Rev, _ = strconv.Atoi(m.Revision)
	if ref.Rev > 0 {
		doc.Rev = ref.Rev
	}
	if withLog || doc.Rev == 0 {
		revs, err := c.log(ctx, ref)
		if err != nil {
			return nil, asCasterErr(err)
		}
		doc.Revisions = revs
		for _, r := range revs {
			doc.LatestRev = max(doc.LatestRev, r.Rev)
		}
		if doc.Rev == 0 {
			doc.Rev = doc.LatestRev
		}
	}
	if doc.LatestRev == 0 {
		doc.LatestRev = doc.Rev
	}
	doc.Pinned = ref
	if doc.Rev > 0 {
		doc.Pinned.Rev = doc.Rev
	}
	doc.info = casterKind(m.Mime, ref.Name)
	return doc, nil
}

func asCasterErr(err error) *casterErr {
	var ce *casterErr
	if errors.As(err, &ce) {
		return ce
	}
	return errCasterDown
}

func (c *Caster) log(ctx context.Context, ref CasterRef) ([]casterRevision, error) {
	body, status, err := c.get(ctx, "/v1/log", url.Values{"name": {ref.NS + "/" + ref.Name}}, casterMaxMeta)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, errCasterDown
	}
	var out struct {
		Revisions []struct {
			Rev   int       `json:"rev"`
			CAS   string    `json:"cas"`
			Title string    `json:"title"`
			At    time.Time `json:"at"`
		} `json:"revisions"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil, errCasterDown
	}
	revs := make([]casterRevision, 0, len(out.Revisions))
	for _, r := range out.Revisions {
		revs = append(revs, casterRevision{Rev: r.Rev, SHA256: strings.TrimPrefix(r.CAS, "sha256:"), At: r.At.UnixMilli(), Title: r.Title})
	}
	return revs, nil
}

// Bytes reads the pinned revision's bytes, at most limit, and checks they hash to what the head said, so a rev
// that moved between the two calls is a retryable conflict rather than a mixed answer.
func (c *Caster) Bytes(ctx context.Context, d *CasterDoc, limit int64) ([]byte, *casterErr) {
	if limit <= 0 || limit > casterMaxBytes {
		limit = casterMaxBytes
	}
	body, status, err := c.get(ctx, "/v1/get", url.Values{"ref": {d.Pinned.String()}, "raw": {"1"}, "max_bytes": {"-1"}}, limit)
	if err != nil {
		return nil, asCasterErr(err)
	}
	if status == 404 {
		return nil, errCasterNotFound
	}
	if status != 200 {
		return nil, errCasterDown
	}
	if d.SHA256 != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(strings.TrimPrefix(d.SHA256, "sha256:"), hex.EncodeToString(sum[:])) {
			return nil, errCasterChanged
		}
	}
	return body, nil
}

// ETag is the strong validator for a pinned revision: its content hash.
func (d *CasterDoc) ETag() string { return `"` + strings.TrimPrefix(d.SHA256, "sha256:") + `"` }

// casterKind decides the viewer kind from the stored mime, falling back to the artifact name.
func casterKind(mime, name string) kindInfo {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	ext := ""
	switch {
	case m == "text/html":
		return kindInfo{"text/html", fileviewapi.KindHTML, ""}
	case m == "image/svg+xml":
		return kindInfo{m, fileviewapi.KindImage, ""}
	case strings.HasPrefix(m, "image/"):
		return kindInfo{m, fileviewapi.KindImage, ""}
	case m == "text/markdown":
		ext = ".md"
	case m == "application/json":
		ext = ".json"
	case m == "application/yaml" || m == "application/x-yaml" || m == "text/yaml":
		ext = ".yaml"
	case m == "application/toml":
		ext = ".toml"
	case m == "text/csv":
		ext = ".csv"
	case m == "" || strings.HasPrefix(m, "text/"):
		ext = strings.ToLower(pathExt(name))
	default:
		return kindInfo{"application/octet-stream", fileviewapi.KindBinary, ""}
	}
	return textKind(name, ext)
}

func pathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

func (c *Caster) principal(r *http.Request) string {
	if c.Principal != nil {
		return c.Principal(r)
	}
	return ""
}

// auditDenied records a refused ref. A bad shape is not audited (nothing was attempted).
func (c *Caster) auditDenied(r *http.Request, kind, raw string, e *casterErr) {
	if c.Audit == nil || e.deny == "" {
		return
	}
	reason := e.deny
	_ = c.Audit.Record(r.Context(), AuditEntry{Principal: c.principal(r), Kind: kind, Source: "caster", PathOrRef: raw, Outcome: "denied", DenyReason: &reason, Count: 1})
}

func casterFail(w http.ResponseWriter, e *casterErr) { jsonErr(w, e.status, e.code, e.msg) }

// Mount registers GET /resolve under fileviewapi.Prefix behind guard (RequireOperatorReads).
func (c *Caster) Mount(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	if guard == nil {
		guard = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET "+fileviewapi.Prefix+"/resolve", guard(http.HandlerFunc(c.resolve)))
}

func (c *Caster) resolve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if k != "ref" {
			jsonErr(w, 400, "invalid", "Unknown parameter.")
			return
		}
	}
	raw := q.Get("ref")
	doc, e := c.Lookup(r.Context(), raw, true)
	if e != nil {
		c.auditDenied(r, "caster", raw, e)
		casterFail(w, e)
		return
	}
	if c.Audit != nil {
		c.Audit.RecordAsync(AuditEntry{Principal: c.principal(r), Kind: "caster", Source: "caster", PathOrRef: raw, Outcome: "ok", ETag: pointer(doc.ETag()), Count: 1})
	}
	revs := doc.Revisions
	if revs == nil {
		revs = []casterRevision{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ref": doc.Ref.String(), "namespace": doc.Ref.NS, "name": doc.Ref.Name, "kind": doc.info.kind, "mime": doc.info.mime,
		"size": doc.Size, "rev": doc.Rev, "latest_rev": doc.LatestRev, "floating": doc.Floating,
		"sha256": strings.TrimPrefix(doc.SHA256, "sha256:"), "title": doc.Title, "tags": []string{}, "revisions": revs,
	})
}

// serveContent answers GET /content?caster_ref=. It is called by Files.content.
func (c *Caster) serveContent(w http.ResponseWriter, r *http.Request, set Settings) {
	raw := r.URL.Query().Get("caster_ref")
	doc, e := c.Lookup(r.Context(), raw, false)
	if e != nil {
		c.auditDenied(r, "caster", raw, e)
		casterFail(w, e)
		return
	}
	limit, rangeOK := capFor(set.App.Caps, doc.info.kind)
	if doc.Size > limit && (!rangeOK || r.Header.Get("Range") == "") {
		c.auditDenied(r, "caster", raw, &casterErr{deny: fileviewapi.DenyTooLarge})
		jsonErr(w, 413, "too_large", "This artifact is over the viewer limit. Fetch text in ranges.")
		return
	}
	data, e := c.Bytes(r.Context(), doc, max(limit, doc.Size))
	if e != nil {
		casterFail(w, e)
		return
	}
	// The stored mime may lie: decide again from the bytes, the way a host file is.
	ki := classify(doc.Ref.Name, data[:min(len(data), 512)])
	if ki.kind == fileviewapi.KindText || ki.kind == fileviewapi.KindBinary || doc.info.kind == fileviewapi.KindMarkdown {
		ki = mergeKind(doc.info, ki)
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", doc.ETag())
	setContentHeaders(h, ki)
	cw := &countWriter{ResponseWriter: w}
	http.ServeContent(cw, r, "", time.Time{}, bytes.NewReader(data))
	if c.Audit != nil {
		_ = c.Audit.Record(r.Context(), AuditEntry{Principal: c.principal(r), Kind: "caster", Source: "caster", PathOrRef: raw, Outcome: "ok", BytesServed: &cw.n, ETag: pointer(doc.ETag()), Count: 1})
	}
}

// mergeKind keeps the stored mime's finer text kind (markdown, json) unless the bytes are binary or HTML.
func mergeKind(stored, sniffed kindInfo) kindInfo {
	if sniffed.kind == fileviewapi.KindBinary || sniffed.kind == fileviewapi.KindHTML || sniffed.kind == fileviewapi.KindImage {
		return sniffed
	}
	return stored
}
