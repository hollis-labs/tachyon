// Package tesseractread is Tachyon's read-only view of Tesseract for agents. Two
// calls only: a search (Tesseract's read-scoped lookup route) and an item read.
// It sends a fixed, whitelisted request body, keeps a safe subset of each record,
// redacts credential-shaped text, bounds every response, and refuses namespaces
// outside an allowlist (the credential itself can read all of them). Tesseract
// owns the data: nothing here is stored or cached.
package tesseractread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

var (
	ErrUnavailable = errors.New("tesseract is unavailable")
	ErrNotFound    = errors.New("tesseract record not found")
	ErrInvalid     = errors.New("invalid tesseract read request")
	// ErrForbidden covers a namespace outside the allowlist. It never reveals whether the record exists.
	ErrForbidden = errors.New("tesseract namespace not available to the app agent")
)

const (
	// ContentTrust marks text that agents or people wrote: data, never instructions.
	ContentTrust = "untrusted_data"

	DefaultLimit  = 10
	MaxLimit      = 25
	MaxNamespaces = 5
	SummaryBytes  = 500
	BodyBytes     = 8 << 10
	ResponseBytes = 32 << 10
	upstreamBytes = 2 << 20
)

// DefaultPrefixes are the namespaces the app agent may read. `user/…` holds the
// operator's private notes and is left out until the owner widens this.
var DefaultPrefixes = []string{"project/", "system/"}

var (
	namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_./-]{0,199}$`)
	tagPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_./-]{0,63}$`)
	itemPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	cursorPattern    = regexp.MustCompile(`^[A-Za-z0-9_=-]{1,512}$`)
	domains          = map[string]bool{"memory": true, "knowledge": true, "workspace": true}
)

type Reader struct {
	Base     string
	Client   *http.Client
	Prefixes []string
}

// New returns nil, nil when no Tesseract address is configured (the feature is off).
func New(base, tokenFile string) (*Reader, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, nil
	}
	rt, err := upstreamauth.Wrap(http.DefaultTransport.(*http.Transport).Clone(), base, tokenFile)
	if err != nil {
		return nil, err
	}
	return &Reader{Base: base, Prefixes: DefaultPrefixes, Client: &http.Client{
		Transport: rt, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// validNamespace accepts lowercase path-like names and refuses empty or dot segments, so
// "project/../user/x" cannot pass a prefix check.
func validNamespace(ns string) bool {
	if !namespacePattern.MatchString(ns) {
		return false
	}
	for _, seg := range strings.Split(ns, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (r *Reader) allowed(namespace string) bool {
	if !validNamespace(namespace) {
		return false
	}
	for _, p := range r.Prefixes {
		if strings.HasPrefix(namespace, p) {
			return true
		}
	}
	return false
}

func (r *Reader) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	if r == nil || r.Client == nil {
		return nil, ErrUnavailable
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, ErrInvalid
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.Base+path, rd)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusBadRequest:
		return nil, ErrInvalid
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBytes+1))
	if err != nil || len(b) > upstreamBytes || !json.Valid(b) {
		return nil, ErrUnavailable
	}
	return b, nil
}

func cut(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// Entry is one record as the agent sees it in a search.
type Entry struct {
	ItemID           string   `json:"item_id"`
	Namespace        string   `json:"namespace"`
	Key              string   `json:"key"`
	Domain           string   `json:"domain"`
	Status           string   `json:"status,omitempty"`
	Tags             []string `json:"tags"`
	Confidence       float64  `json:"confidence,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	SummaryTruncated bool     `json:"summary_truncated,omitempty"`
	Score            float64  `json:"score,omitempty"`
}

// Item is a full read: an Entry plus its body.
type Item struct {
	Entry
	Body          string `json:"body,omitempty"`
	BodyBytes     int    `json:"body_bytes,omitempty"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
	AuthorAgent   string `json:"author_agent,omitempty"`
	ContentTrust  string `json:"content_trust"`
}

type revision struct {
	ItemID     string   `json:"item_id"`
	Namespace  string   `json:"namespace"`
	Key        string   `json:"memory_key"`
	Domain     string   `json:"domain"`
	Status     string   `json:"status"`
	Tags       []string `json:"tags"`
	Confidence float64  `json:"confidence"`
	CreatedAt  string   `json:"created_at"`
	Payload    struct {
		Summary string `json:"summary"`
		Body    string `json:"body"`
	} `json:"payload"`
	Author struct {
		AgentID string `json:"agent_id"`
	} `json:"author"`
}

func entry(v revision, score float64) Entry {
	e := Entry{ItemID: v.ItemID, Namespace: v.Namespace, Key: v.Key, Domain: v.Domain, Status: v.Status,
		Tags: []string{}, Confidence: v.Confidence, CreatedAt: v.CreatedAt, Score: score}
	for i, t := range v.Tags {
		if i == 20 {
			break
		}
		t, _ = cut(t, 64)
		e.Tags = append(e.Tags, t)
	}
	e.Summary, e.SummaryTruncated = cut(redact.Text(v.Payload.Summary), SummaryBytes)
	return e
}

type RecallRequest struct {
	Namespaces []string
	Query      string
	Tags       []string
	Domains    []string
	Limit      int
	Cursor     string
}

type RecallPage struct {
	Items        []Entry `json:"items"`
	Returned     int     `json:"returned"`
	Total        int     `json:"total"`
	HasMore      bool    `json:"has_more"`
	NextCursor   string  `json:"next_cursor,omitempty"`
	Truncated    bool    `json:"truncated,omitempty"`
	Note         string  `json:"note,omitempty"`
	ContentTrust string  `json:"content_trust"`
}

// Recall searches the given namespaces. It sends only the whitelisted fields below,
// always with payload_mode=summary, so a result never carries a body.
func (r *Reader) Recall(ctx context.Context, q RecallRequest) (RecallPage, error) {
	if len(q.Namespaces) == 0 || len(q.Namespaces) > MaxNamespaces {
		return RecallPage{}, ErrInvalid
	}
	for _, ns := range q.Namespaces {
		if !validNamespace(ns) {
			return RecallPage{}, ErrInvalid
		}
		if !r.allowed(ns) {
			return RecallPage{}, ErrForbidden
		}
	}
	q.Query = strings.TrimSpace(q.Query)
	if len(q.Query) > 300 || strings.ContainsAny(q.Query, "\x00") {
		return RecallPage{}, ErrInvalid
	}
	if len(q.Tags) > 10 {
		return RecallPage{}, ErrInvalid
	}
	for _, t := range q.Tags {
		if !tagPattern.MatchString(t) {
			return RecallPage{}, ErrInvalid
		}
	}
	for _, d := range q.Domains {
		if !domains[d] {
			return RecallPage{}, ErrInvalid
		}
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 1 || q.Limit > MaxLimit {
		return RecallPage{}, ErrInvalid
	}
	if q.Cursor != "" && !cursorPattern.MatchString(q.Cursor) {
		return RecallPage{}, ErrInvalid
	}
	body := map[string]any{"namespaces": q.Namespaces, "limit": q.Limit, "payload_mode": "summary"}
	if q.Query != "" {
		body["query"] = q.Query
	}
	if len(q.Tags) > 0 {
		body["tags"] = q.Tags
	}
	if len(q.Domains) > 0 {
		body["domains"] = q.Domains
	}
	if q.Cursor != "" {
		body["cursor"] = q.Cursor
	}
	b, err := r.do(ctx, http.MethodPost, "/v1/tesseract/lookup", body)
	if err != nil {
		return RecallPage{}, err
	}
	var in struct {
		Results []struct {
			Revision revision `json:"revision"`
			Score    float64  `json:"score"`
		} `json:"results"`
		Manifest struct {
			Total      int    `json:"results_total"`
			Truncated  bool   `json:"truncated"`
			NextCursor string `json:"next_cursor"`
		} `json:"manifest"`
	}
	if json.Unmarshal(b, &in) != nil {
		return RecallPage{}, ErrUnavailable
	}
	out := RecallPage{Items: []Entry{}, Total: in.Manifest.Total, ContentTrust: ContentTrust}
	size := 0
	for i, res := range in.Results {
		if !r.allowed(res.Revision.Namespace) {
			continue // defence in depth: never pass on a record outside the allowlist
		}
		e := entry(res.Revision, res.Score)
		raw, _ := json.Marshal(e)
		if i > 0 && size+len(raw) > ResponseBytes {
			out.Truncated = true
			out.Note = "Page cut at the size limit. Lower limit or narrow the search."
			break
		}
		size += len(raw)
		out.Items = append(out.Items, e)
	}
	out.Returned = len(out.Items)
	out.HasMore = in.Manifest.Truncated
	if !out.Truncated && in.Manifest.NextCursor != "" {
		out.NextCursor = in.Manifest.NextCursor
	}
	return out, nil
}

// Get reads one record by item id, with its body (bounded to 8 KiB).
func (r *Reader) Get(ctx context.Context, itemID string) (Item, error) {
	if !itemPattern.MatchString(itemID) {
		return Item{}, ErrInvalid
	}
	b, err := r.do(ctx, http.MethodGet, "/v1/items/"+url.PathEscape(itemID), nil)
	if err != nil {
		return Item{}, err
	}
	var v revision
	if json.Unmarshal(b, &v) != nil || v.ItemID != itemID {
		return Item{}, ErrUnavailable
	}
	if !r.allowed(v.Namespace) {
		return Item{}, ErrForbidden
	}
	out := Item{Entry: entry(v, 0), AuthorAgent: v.Author.AgentID, ContentTrust: ContentTrust}
	if v.Payload.Body != "" {
		out.BodyBytes = len(v.Payload.Body)
		out.Body, out.BodyTruncated = cut(redact.Text(v.Payload.Body), BodyBytes)
	}
	return out, nil
}
