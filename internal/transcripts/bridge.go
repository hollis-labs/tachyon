// Package transcripts provides bounded read-only views of Fragments Engine.
package transcripts

import (
	"context"
	"database/sql"
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

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const Prefix = "/api/plugins/console/v1/transcripts"

var sessionPath = regexp.MustCompile(`^/sessions/[a-f0-9]{64}$`)

type Bridge struct {
	URL, TokenFile string
	MaxSessions    int
	Client         *http.Client
	// DB, when set, keeps the last good snapshot (metadata only) so an unreadable Fragments Engine still shows data.
	DB *localstore.Store

	mu       sync.Mutex
	inflight *flight
}

// flight is one snapshot build shared by every request that arrives while it runs.
type flight struct {
	done chan struct{}
	body []byte
	err  error
}

func New(addr, file string, maxSessions int) *Bridge {
	b := &Bridge{URL: strings.TrimRight(addr, "/"), TokenFile: file, MaxSessions: maxSessions}
	transport, err := upstreamauth.Wrap(http.DefaultTransport, b.URL, file)
	if err == nil {
		b.Client = &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return b
}

// Identity partitions the durable cache by endpoint and current credential.
func (b *Bridge) Identity() (string, error) {
	u, err := url.Parse(b.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || b.TokenFile == "" || b.Client == nil || b.MaxSessions < 200 || b.MaxSessions > 20000 {
		return "", errors.New("invalid Fragments Engine configuration")
	}
	if err = upstreamauth.Guard(b.URL, b.TokenFile); err != nil {
		return "", err
	}
	token, err := tokenfile.NewWatcher(b.TokenFile, tokenfile.Options{}, nil).Token()
	if err != nil {
		return "", err
	}
	return apicache.Digest("transcripts\n" + b.URL + "\n" + token + "\n" + strconv.Itoa(b.MaxSessions)), nil
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
	req, err := http.NewRequestWithContext(ctx, "GET", b.URL+"/v1/transcripts"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := b.Client.Do(req)
	if err != nil {
		if errors.Is(err, upstreamauth.ErrCredential) {
			return &failure{503, "configuration", "Fragments Engine credential is unavailable on the Tachyon host."}
		}
		return err
	}
	defer res.Body.Close()
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

// Uncacheable marks a response the API cache must pass through without storing (a saved snapshot served while
// Fragments Engine is unreadable would otherwise be cached as if it were live).
const Uncacheable = "X-Tachyon-Uncacheable"

var (
	searchModes   = []string{"text", "summary", "semantic", "hybrid"}
	detailParams  = []string{"turn_offset", "turn_limit"}
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

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		respondError(w, &failure{405, "read_only", "Transcript dashboards are read-only."})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path != "/snapshot" && path != "/search" && path != "/status" && !sessionPath.MatchString(path) {
		respondError(w, &failure{404, "not_found", "Unknown transcript read."})
		return
	}
	if _, err := b.Identity(); err != nil {
		cfg := &failure{503, "configuration", "Configure Fragments Engine's URL and private token file on the Tachyon host."}
		if path == "/snapshot" && b.serveSaved(w, cfg) {
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
	st["capabilities"], _ = json.Marshal(map[string]bool{"search": true, "find": false, "errors": false, "stats": false})
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
		var f *failure
		if errors.As(err, &f) && b.serveSaved(w, f) {
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
	_ = b.DB.Writer.Submit(ctx, "transcript_snapshot_save", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO transcript_snapshot(id, body, observed_ms) VALUES (1, ?, ?)
 ON CONFLICT(id) DO UPDATE SET body = excluded.body, observed_ms = excluded.observed_ms`, body, time.Now().UnixMilli())
		return err
	})
}

// serveSaved writes the saved snapshot as an unavailable-source answer. It reports false when there is none.
func (b *Bridge) serveSaved(w http.ResponseWriter, f *failure) bool {
	if b.DB == nil {
		return false
	}
	var body []byte
	var observed int64
	if err := b.DB.Reader.QueryRow(`SELECT body, observed_ms FROM transcript_snapshot WHERE id = 1`).Scan(&body, &observed); err != nil {
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
	w.Header().Set(Uncacheable, "1")
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
