// Package transcripts provides bounded read-only views of Fragments Engine (slice B10, ported from the
// old internal/transcripts).
//
// Every read is a GET on Fragments Engine's /v1/transcripts API with Tachyon's credential; the caller's
// query is validated and only the known parameters are forwarded. The session-list snapshot is one walk
// shared by concurrent requests, the last good one is kept in transcript_snapshot, and it is served with
// source_state "unavailable" when Fragments Engine cannot be read.
//
// Differences from the old bridge: there is no API cache in front of it (each read goes to Fragments
// Engine, the snapshot walk is still single-flight), so there is no cache identity digest and no
// X-Tachyon-Uncacheable marker; the credential is a secrets reference resolved on each request
// (upstreamauth) instead of a token file; the session cap is the old default as a constant; and the
// tachyon.features.transcripts switch gates every outbound read.
package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// Prefix is the transcript routes' path prefix.
const Prefix = "/api/plugins/console/v1/transcripts"

// MaxSessions is how many sessions a snapshot holds at most: the old build's default
// (TACHYON_TRANSCRIPT_MAX_SESSIONS), which next's settings do not carry.
const MaxSessions = 5000

var sessionPath = regexp.MustCompile(`^/sessions/[a-f0-9]{64}(/find|/errors)?$`)

const (
	findSuffix   = "/find"
	errorsSuffix = "/errors"
)

// Bridge serves the transcript reads.
type Bridge struct {
	URL         string
	MaxSessions int
	// Enabled is tachyon.features.transcripts. Off, nothing is ever sent to Fragments Engine: the snapshot
	// route serves the saved snapshot, if there is one, and every other read is 503 disabled.
	Enabled bool
	// Client carries the credential; nil when there is none or the URL may not carry one.
	Client *http.Client
	// DB, when set, keeps the last good snapshot (metadata only) so an unreadable Fragments Engine still shows data.
	DB *store.Store

	mu       sync.Mutex
	inflight *flight
}

// flight is one snapshot build shared by every request that arrives while it runs.
type flight struct {
	done chan struct{}
	body []byte
	err  error
}

// New is a bridge to Fragments Engine at addr with the credential token (nil: none configured).
func New(addr string, token upstreamauth.Token, enabled bool) *Bridge {
	b := &Bridge{URL: strings.TrimRight(strings.TrimSpace(addr), "/"), MaxSessions: MaxSessions, Enabled: enabled}
	if token == nil || b.URL == "" {
		return b
	}
	transport, err := upstreamauth.Wrap(http.DefaultTransport, b.URL, token)
	if err == nil {
		b.Client = &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return b
}

// configured is the old Identity() validation without its cache digest: an origin-only URL, a
// credential the URL may carry (upstreamauth.Guard, applied by New), and a session cap of 200 to 20000.
// The credential's value is resolved per request by the transport; a reference that does not resolve
// fails the read with 503 configuration before anything is sent.
func (b *Bridge) configured() error {
	u, err := url.Parse(b.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || b.Client == nil || b.MaxSessions < 200 || b.MaxSessions > 20000 {
		return errors.New("invalid Fragments Engine configuration")
	}
	return nil
}

type failure struct {
	status        int
	code, message string
}

func (e *failure) Error() string { return e.message }
func respondError(w http.ResponseWriter, e error) {
	f := &failure{502, "unreachable", "Could not reach Fragments Engine."}
	var known *failure
	if errors.As(e, &known) {
		f = known
	}
	w.WriteHeader(f.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": f.code, "message": f.message})
}
func (b *Bridge) read(ctx context.Context, path string, out any) error {
	// path is one of this file's fixed reads with validated, re-encoded parameters.
	req, err := http.NewRequestWithContext(ctx, "GET", b.URL+"/v1/transcripts"+path, nil) //nolint:gosec // the configured Fragments Engine address
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := b.Client.Do(req) //nolint:gosec // the configured Fragments Engine address
	if err != nil {
		if errors.Is(err, upstreamauth.ErrCredential) {
			return &failure{503, "configuration", "Fragments Engine credential is unavailable on the Tachyon host."}
		}
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		switch res.StatusCode {
		case 401, 403:
			return &failure{res.StatusCode, "authentication", "Fragments Engine denied transcript access. Check its API credential and transcript permissions."}
		case 404:
			return &failure{404, "not_found", "The transcript or transcript API is unavailable."}
		case 429:
			return &failure{429, "rate_limited", "Fragments Engine is rate limiting reads. Try again shortly."}
		default:
			return &failure{502, "remote", "Fragments Engine could not complete the transcript read."}
		}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil || len(raw) > 16<<20 || json.Unmarshal(raw, out) != nil {
		return &failure{502, "invalid_response", "Fragments Engine returned an invalid transcript response."}
	}
	return nil
}

var (
	searchModes   = []string{"text", "summary", "semantic", "hybrid"}
	detailParams  = []string{"turn_offset", "turn_limit", "turn_from_seq"}
	findParams    = []string{"q", "after_offset", "limit"}
	statsParams   = []string{"since", "until", "tool", "model", "kind", "project", "has_error", "tag"}
	searchParams  = []string{"q", "mode", "limit", "cursor", "since", "until", "tool", "model", "kind", "project", "has_error", "tag"}
	filterText    = regexp.MustCompile(`^[^\x00-\x1f]{1,200}$`)
	maxQuery      = 256
	maxCursor     = 512
	maxSearchPage = 50
)

func invalid(msg string) error { return &failure{400, "invalid_args", msg} }

// unknown returns the first query parameter that is not allowed.
func unknown(q url.Values, allowed []string) string {
	for k := range q {
		if !slices.Contains(allowed, k) {
			return k
		}
	}
	return ""
}

// ServeHTTP serves the transcript reads under Prefix: GET only, known paths only.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		respondError(w, &failure{405, "read_only", "Transcript dashboards are read-only."})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path != "/snapshot" && path != "/search" && path != "/status" && path != "/stats" && !sessionPath.MatchString(path) {
		respondError(w, &failure{404, "not_found", "Unknown transcript read."})
		return
	}
	// The switch comes before anything that could reach Fragments Engine; the saved snapshot is local.
	if !b.Enabled {
		off := &failure{503, "disabled", "Transcript reads are off (tachyon.features.transcripts)."}
		if path == "/snapshot" && b.serveSaved(w, r, off) {
			return
		}
		respondError(w, off)
		return
	}
	if err := b.configured(); err != nil {
		cfg := &failure{503, "configuration", "Configure Fragments Engine's URL and token reference (tachyon.upstreams.fragments) on the Tachyon host."}
		if path == "/snapshot" && b.serveSaved(w, r, cfg) {
			return
		}
		respondError(w, cfg)
		return
	}
	switch path {
	case "/snapshot":
		b.snapshot(w, r)
		return
	case "/search":
		b.search(w, r)
		return
	case "/status":
		b.status(w, r)
		return
	case "/stats":
		b.stats(w, r)
		return
	}
	if strings.HasSuffix(path, findSuffix) {
		b.find(w, r, path)
		return
	}
	if strings.HasSuffix(path, errorsSuffix) {
		b.sessionErrors(w, r, path)
		return
	}
	q := r.URL.Query()
	if k := unknown(q, detailParams); k != "" {
		respondError(w, invalid("Unknown parameter "+k+"."))
		return
	}
	offset, limit := 0, 50
	var err error
	if v := q.Get("turn_offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 || offset > 1000000 {
			respondError(w, invalid("Invalid transcript page."))
			return
		}
	}
	if v := q.Get("turn_from_seq"); v != "" {
		// seq is contiguous from 0, so the window that starts at a found turn starts at that offset.
		if q.Get("turn_offset") != "" {
			respondError(w, invalid("Send turn_offset or turn_from_seq, not both."))
			return
		}
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 || offset > 1000000 {
			respondError(w, invalid("Invalid transcript page."))
			return
		}
	}
	if v := q.Get("turn_limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 200 {
			respondError(w, invalid("turn_limit must be 1 to 200."))
			return
		}
	}
	var result json.RawMessage
	if err = b.read(r.Context(), path+"?turn_limit="+strconv.Itoa(limit)+"&turn_offset="+strconv.Itoa(offset), &result); err != nil {
		respondError(w, err)
		return
	}
	_, _ = w.Write(result)
}

// status is Fragments Engine's status plus capabilities: which optional routes this bridge serves. find, errors and
// stats need Fragments Engine changes (TR3 to TR5) and stay false until then.
func (b *Bridge) status(w http.ResponseWriter, r *http.Request) {
	var st map[string]json.RawMessage
	if err := b.read(r.Context(), "/status", &st); err != nil {
		respondError(w, err)
		return
	}
	// Capabilities follow what this Fragments Engine build says it serves, so nothing is promised that it cannot answer.
	var features []string
	_ = json.Unmarshal(st["features"], &features)
	st["capabilities"], _ = json.Marshal(map[string]bool{"search": true, "find": slices.Contains(features, "find"),
		"errors": slices.Contains(features, "errors"), "stats": slices.Contains(features, "stats")})
	_ = json.NewEncoder(w).Encode(st)
}

// search is the session-level content search: the query is validated, forwarded to Fragments Engine's session
// list and the page returned as it is. It is never cached and the query text is not logged.
func (b *Bridge) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if k := unknown(q, searchParams); k != "" {
		respondError(w, invalid("Unknown parameter "+k+"."))
		return
	}
	text := strings.TrimSpace(q.Get("q"))
	if text == "" || len(text) > maxQuery {
		respondError(w, invalid("q is required and at most 256 characters."))
		return
	}
	mode := q.Get("mode")
	if mode == "" {
		mode = "text"
	}
	if !slices.Contains(searchModes, mode) {
		respondError(w, invalid("mode must be text, summary, semantic or hybrid."))
		return
	}
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxSearchPage {
			respondError(w, invalid("limit must be 1 to 50."))
			return
		}
		limit = n
	}
	out := url.Values{"q": {text}, "mode": {mode}, "limit": {strconv.Itoa(limit)}}
	if c := q.Get("cursor"); c != "" {
		if len(c) > maxCursor {
			respondError(w, invalid("cursor is too long."))
			return
		}
		out.Set("cursor", c)
	}
	for _, k := range []string{"since", "until"} {
		if v := q.Get(k); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				respondError(w, invalid(k+" must be RFC 3339."))
				return
			}
			out.Set(k, v)
		}
	}
	for _, k := range []string{"tool", "model", "project", "tag"} {
		if v := q.Get(k); v != "" {
			if !filterText.MatchString(v) {
				respondError(w, invalid(k+" is not a valid filter."))
				return
			}
			out.Set(k, v)
		}
	}
	if v := q.Get("kind"); v != "" {
		if v != "main" && v != "subagent" {
			respondError(w, invalid("kind must be main or subagent."))
			return
		}
		out.Set("kind", v)
	}
	if v := q.Get("has_error"); v != "" {
		if v != "true" && v != "false" {
			respondError(w, invalid("has_error must be true or false."))
			return
		}
		out.Set("has_error", v)
	}
	// A mode Fragments Engine does not offer (no vectors, say) is refused here instead of silently falling back.
	var st struct {
		Modes []string `json:"modes"`
	}
	if err := b.read(r.Context(), "/status", &st); err != nil {
		respondError(w, err)
		return
	}
	if len(st.Modes) > 0 && !slices.Contains(st.Modes, mode) {
		respondError(w, invalid("mode "+mode+" is not available in Fragments Engine."))
		return
	}
	var page map[string]json.RawMessage
	if err := b.read(r.Context(), "/sessions?"+out.Encode(), &page); err != nil {
		respondError(w, err)
		return
	}
	if _, ok := page["mode"]; !ok {
		page["mode"], _ = json.Marshal(mode)
	}
	if _, ok := page["items"]; !ok {
		page["items"] = json.RawMessage("[]")
	}
	_ = json.NewEncoder(w).Encode(page)
}

// snapshotMeta tells the page how fresh and how complete a snapshot is.
type snapshotMeta struct {
	SourceState    string         `json:"source_state"`
	ObservedAt     int64          `json:"observed_at"`
	FetchedAt      int64          `json:"fetched_at"`
	Complete       bool           `json:"complete"`
	Limit          int            `json:"limit"`
	TotalAvailable int            `json:"total_available"`
	OmittedCount   int            `json:"omitted_count"`
	Error          map[string]any `json:"error,omitempty"`
}

// snapshot serves the session list. Concurrent requests share one build; a good build is saved, and when
// Fragments Engine cannot be read the saved snapshot is served with source_state "unavailable" and the error.
func (b *Bridge) snapshot(w http.ResponseWriter, r *http.Request) {
	body, err := b.build()
	if err != nil {
		// Port: the saved snapshot also covers an unreachable Fragments Engine. The old bridge served it only on a
		// mapped failure; on a refused connection the old API cache answered instead, and that cache is not ported.
		f := &failure{502, "unreachable", "Could not reach Fragments Engine."}
		var known *failure
		if errors.As(err, &known) {
			f = known
		}
		if b.serveSaved(w, r, f) {
			return
		}
		respondError(w, err)
		return
	}
	_, _ = w.Write(body)
}

// build runs one snapshot walk, or joins the one already running.
func (b *Bridge) build() ([]byte, error) {
	b.mu.Lock()
	if f := b.inflight; f != nil {
		b.mu.Unlock()
		<-f.done
		return f.body, f.err
	}
	f := &flight{done: make(chan struct{})}
	b.inflight = f
	b.mu.Unlock()
	// The walk outlives the request that started it: others are waiting on the result.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f.body, f.err = b.walk(ctx)
	if f.err == nil {
		b.save(ctx, f.body)
	}
	b.mu.Lock()
	b.inflight = nil
	b.mu.Unlock()
	close(f.done)
	return f.body, f.err
}

func (b *Bridge) save(ctx context.Context, body []byte) {
	if b.DB == nil {
		return
	}
	_ = b.DB.Writer.Submit(ctx, "transcript_snapshot_save", func(ctx context.Context, tx store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO transcript_snapshot(id, body, observed_ms) VALUES (1, ?, ?)
 ON CONFLICT(id) DO UPDATE SET body = excluded.body, observed_ms = excluded.observed_ms`, body, time.Now().UnixMilli())
		return err
	})
}

// serveSaved writes the saved snapshot as an unavailable-source answer. It reports false when there is none.
func (b *Bridge) serveSaved(w http.ResponseWriter, r *http.Request, f *failure) bool {
	if b.DB == nil {
		return false
	}
	var body []byte
	var observed int64
	if err := b.DB.Reader.QueryRowContext(r.Context(), `SELECT body, observed_ms FROM transcript_snapshot WHERE id = 1`).Scan(&body, &observed); err != nil {
		return false
	}
	var snap map[string]json.RawMessage
	if json.Unmarshal(body, &snap) != nil {
		return false
	}
	var meta snapshotMeta
	_ = json.Unmarshal(snap["snapshot_meta"], &meta)
	meta.SourceState, meta.FetchedAt = "unavailable", observed
	meta.Error = map[string]any{"code": f.code, "message": f.message}
	snap["snapshot_meta"], _ = json.Marshal(meta)
	_ = json.NewEncoder(w).Encode(snap)
	return true
}

func (b *Bridge) walk(ctx context.Context) ([]byte, error) {
	var status struct {
		Available bool     `json:"available"`
		Enabled   bool     `json:"enabled"`
		Semantic  bool     `json:"semantic"`
		Modes     []string `json:"modes"`
	}
	var facets struct {
		Total        int      `json:"total"`
		Enriched     int      `json:"enriched"`
		ProjectCount int      `json:"project_count"`
		Tools        []string `json:"tools"`
		Models       []string `json:"models"`
	}
	if err := b.read(ctx, "/status", &status); err != nil {
		return nil, err
	}
	if !status.Available || !status.Enabled {
		return nil, &failure{503, "configuration", "Transcript browsing is not enabled or its database is unavailable in Fragments Engine."}
	}
	if err := b.read(ctx, "/facets", &facets); err != nil {
		return nil, err
	}
	if facets.Tools == nil {
		facets.Tools = []string{}
	}
	if facets.Models == nil {
		facets.Models = []string{}
	}
	rows := []json.RawMessage{}
	cursor := ""
	seen := map[string]bool{}
	// One fixed cutoff prevents new sessions shifting the newest-first pages.
	until := time.Now().UTC().Format(time.RFC3339Nano)
	for pageNumber := 0; len(rows) < b.MaxSessions && pageNumber < (b.MaxSessions+199)/200; pageNumber++ {
		q := url.Values{"limit": {strconv.Itoa(min(200, b.MaxSessions-len(rows)))}, "sort": {"started"}, "order": {"desc"}, "until": {until}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Items []json.RawMessage `json:"items"`
			Next  string            `json:"next_cursor"`
		}
		if err := b.read(ctx, "/sessions?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(row, &identity) != nil || identity.ID == "" {
				return nil, &failure{502, "invalid_response", "Transcript metadata is missing a session identity."}
			}
			if !seen[identity.ID] && len(rows) < b.MaxSessions {
				rows = append(rows, row)
				seen[identity.ID] = true
			}
		}
		if page.Next == "" {
			cursor = ""
			break
		}
		if page.Next == cursor || len(page.Items) == 0 {
			return nil, &failure{502, "invalid_response", "Transcript pagination did not advance."}
		}
		cursor = page.Next
	}
	now := time.Now().UnixMilli()
	meta := snapshotMeta{SourceState: "live", ObservedAt: now, FetchedAt: now, Complete: cursor == "", Limit: b.MaxSessions,
		TotalAvailable: facets.Total, OmittedCount: max(0, facets.Total-len(rows))}
	return json.Marshal(map[string]any{"status": status, "facets": facets, "sessions": rows, "complete": cursor == "", "limit": b.MaxSessions, "observed_at": until, "snapshot_meta": meta})
}

// optional reads a Fragments Engine route that only newer builds have. Without the feature the answer is 501
// "unsupported" instead of a misleading not_found.
func (b *Bridge) optional(w http.ResponseWriter, r *http.Request, feature, path string) {
	var out json.RawMessage
	if err := b.read(r.Context(), path, &out); err != nil {
		var f *failure
		if errors.As(err, &f) && f.status == 404 {
			// A 404 is an unknown session when the build offers the feature, and a missing route when it does not.
			var st struct {
				Features []string `json:"features"`
			}
			if b.read(r.Context(), "/status", &st) == nil && !slices.Contains(st.Features, feature) {
				respondError(w, &failure{501, "unsupported", "This Fragments Engine does not provide that read yet."})
				return
			}
		}
		respondError(w, err)
		return
	}
	_, _ = w.Write(out)
}

// find lists the turns of one session that contain the text (Fragments Engine per-turn search).
func (b *Bridge) find(w http.ResponseWriter, r *http.Request, path string) {
	q := r.URL.Query()
	if k := unknown(q, findParams); k != "" {
		respondError(w, invalid("Unknown parameter "+k+"."))
		return
	}
	text := strings.TrimSpace(q.Get("q"))
	if text == "" || len(text) > maxQuery {
		respondError(w, invalid("q is required and at most 256 characters."))
		return
	}
	out := url.Values{"q": {text}}
	if v := q.Get("after_offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < -1 || n > 1000000 {
			respondError(w, invalid("Invalid after_offset."))
			return
		}
		out.Set("after_offset", v)
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			respondError(w, invalid("limit must be 1 to 100."))
			return
		}
		out.Set("limit", v)
	}
	b.optional(w, r, "find", path+"?"+out.Encode())
}

// sessionErrors lists the failed tool calls of one session.
func (b *Bridge) sessionErrors(w http.ResponseWriter, r *http.Request, path string) {
	if len(r.URL.Query()) > 0 {
		respondError(w, invalid("Unknown parameter "+firstKey(r.URL.Query())+"."))
		return
	}
	b.optional(w, r, "errors", path)
}

func firstKey(q url.Values) string {
	for k := range q {
		return k
	}
	return ""
}

// stats is the whole-database aggregate, with the same filters as the session list.
func (b *Bridge) stats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if k := unknown(q, statsParams); k != "" {
		respondError(w, invalid("Unknown parameter "+k+"."))
		return
	}
	out := url.Values{}
	for _, k := range []string{"since", "until"} {
		if v := q.Get(k); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				respondError(w, invalid(k+" must be RFC 3339."))
				return
			}
			out.Set(k, v)
		}
	}
	for _, k := range []string{"tool", "model", "project", "tag"} {
		if v := q.Get(k); v != "" {
			if !filterText.MatchString(v) {
				respondError(w, invalid(k+" is not a valid filter."))
				return
			}
			out.Set(k, v)
		}
	}
	if v := q.Get("kind"); v != "" {
		if v != "main" && v != "subagent" {
			respondError(w, invalid("kind must be main or subagent."))
			return
		}
		out.Set("kind", v)
	}
	if v := q.Get("has_error"); v != "" {
		if v != "true" && v != "false" {
			respondError(w, invalid("has_error must be true or false."))
			return
		}
		out.Set("has_error", v)
	}
	b.optional(w, r, "stats", "/stats?"+out.Encode())
}
