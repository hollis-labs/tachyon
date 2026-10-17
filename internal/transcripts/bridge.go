// Package transcripts provides bounded read-only views of Fragments Engine.
package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const Prefix = "/api/plugins/console/v1/transcripts"

var sessionPath = regexp.MustCompile(`^/sessions/[a-f0-9]{64}$`)

type Bridge struct {
	URL, TokenFile string
	MaxSessions    int
	Client         *http.Client
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

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		respondError(w, &failure{405, "read_only", "Transcript dashboards are read-only."})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path != "/snapshot" && !sessionPath.MatchString(path) {
		respondError(w, &failure{404, "not_found", "Unknown transcript read."})
		return
	}
	if _, err := b.Identity(); err != nil {
		respondError(w, &failure{503, "configuration", "Configure Fragments Engine's URL and private token file on the Tachyon host."})
		return
	}
	if path == "/snapshot" {
		b.snapshot(w, r)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("turn_offset"))
	if r.URL.Query().Get("turn_offset") == "" {
		offset = 0
		err = nil
	}
	if err != nil || offset < 0 || offset > 1000000 {
		respondError(w, &failure{400, "invalid_args", "Invalid transcript page."})
		return
	}
	var result json.RawMessage
	if err = b.read(r.Context(), path+"?turn_limit=50&turn_offset="+strconv.Itoa(offset), &result); err != nil {
		respondError(w, err)
		return
	}
	_, _ = w.Write(result)
}

func (b *Bridge) snapshot(w http.ResponseWriter, r *http.Request) {
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
	if err := b.read(r.Context(), "/status", &status); err != nil {
		respondError(w, err)
		return
	}
	if !status.Available || !status.Enabled {
		respondError(w, &failure{503, "configuration", "Transcript browsing is not enabled or its database is unavailable in Fragments Engine."})
		return
	}
	if err := b.read(r.Context(), "/facets", &facets); err != nil {
		respondError(w, err)
		return
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
		if err := b.read(r.Context(), "/sessions?"+q.Encode(), &page); err != nil {
			respondError(w, err)
			return
		}
		for _, row := range page.Items {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(row, &identity) != nil || identity.ID == "" {
				respondError(w, &failure{502, "invalid_response", "Transcript metadata is missing a session identity."})
				return
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
			respondError(w, &failure{502, "invalid_response", "Transcript pagination did not advance."})
			return
		}
		cursor = page.Next
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "facets": facets, "sessions": rows, "complete": cursor == "", "limit": b.MaxSessions, "observed_at": until})
}
