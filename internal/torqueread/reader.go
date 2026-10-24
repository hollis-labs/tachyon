// Package torqueread is Tachyon's read-only view of Torque for agents. It
// issues GET requests to three fixed Torque resources, keeps only a safe subset
// of each record (never execution settings, environments, prompts, tools, files
// or credentials), redacts credential-shaped text and bounds every response.
// Torque owns tasks: nothing here is stored, cached or imported.
package torqueread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

var (
	// ErrUnavailable covers every upstream failure; callers never see Torque's body or status text.
	ErrUnavailable = errors.New("torque is unavailable")
	ErrNotFound    = errors.New("torque record not found")
	ErrInvalid     = errors.New("invalid torque read request")
)

const (
	// ContentTrust marks free text (titles, descriptions, comments) that people or other agents wrote.
	ContentTrust = "untrusted_data"

	DefaultList     = 20
	MaxList         = 50
	DefaultComments = 10
	MaxComments     = 50

	GetDescriptionBytes = 8 << 10
	ListDescription     = 200
	CommentBytes        = 4 << 10
	ResponseBytes       = 32 << 10
	upstreamBodyBytes   = 2 << 20
)

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)
	filterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.,:-]{0,199}$`)
	cursorPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]{1,512}$`)
	filterKeys    = []string{"project_id", "status", "tags", "epic_id", "sprint_id", "kind"}
)

type Reader struct {
	Base, Token string
	Client      *http.Client
}

func New(base, token string) *Reader {
	return &Reader{Base: strings.TrimRight(strings.TrimSpace(base), "/"), Token: token, Client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// get is the only network call. Callers build the path from validated parts.
func (r *Reader) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if r == nil || r.Base == "" {
		return nil, ErrUnavailable
	}
	u, err := url.Parse(r.Base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrUnavailable
	}
	if err := upstreamauth.Guard(r.Base, r.Token); err != nil {
		return nil, ErrUnavailable
	}
	target := r.Base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBodyBytes+1))
	if err != nil || len(b) > upstreamBodyBytes || !json.Valid(b) {
		return nil, ErrUnavailable
	}
	return b, nil
}

// tags accepts Torque's tag shapes: plain strings or objects carrying a name.
type tags []string

func (t *tags) UnmarshalJSON(b []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return nil
	}
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			*t = append(*t, s)
			continue
		}
		var o struct {
			Name  string `json:"name"`
			Name2 string `json:"Name"`
		}
		if json.Unmarshal(it, &o) == nil {
			if o.Name != "" {
				*t = append(*t, o.Name)
			} else if o.Name2 != "" {
				*t = append(*t, o.Name2)
			}
		}
	}
	return nil
}

// rawTask is every Torque field this package is willing to read. Anything not
// named here is dropped by the decoder.
type rawTask struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	Priority      int      `json:"priority"`
	Tags          tags     `json:"tags"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ProjectID     string   `json:"project_id"`
	ParentID      string   `json:"parent_id"`
	EpicID        string   `json:"epic_id"`
	SprintID      string   `json:"sprint_id"`
	CollectionID  string   `json:"collection_id"`
	DependsOn     []string `json:"depends_on"`
	BlockedReason string   `json:"blocked_reason"`
}

// Task is the agent-facing record. Free-text fields are redacted and bounded.
type Task struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Title                string   `json:"title"`
	Status               string   `json:"status"`
	Priority             int      `json:"priority"`
	Tags                 []string `json:"tags"`
	CreatedAt            string   `json:"created_at,omitempty"`
	UpdatedAt            string   `json:"updated_at"`
	ProjectID            string   `json:"project_id,omitempty"`
	ParentID             string   `json:"parent_id,omitempty"`
	EpicID               string   `json:"epic_id,omitempty"`
	SprintID             string   `json:"sprint_id,omitempty"`
	CollectionID         string   `json:"collection_id,omitempty"`
	DependsOn            []string `json:"depends_on,omitempty"`
	BlockedReason        string   `json:"blocked_reason,omitempty"`
	Description          string   `json:"description,omitempty"`
	DescriptionBytes     int      `json:"description_bytes,omitempty"`
	DescriptionTruncated bool     `json:"description_truncated,omitempty"`
	ContentTrust         string   `json:"content_trust"`
}

// Tachyon kind that governs access to a Torque row: plans are task rows with kind=plan.
func AccessKind(torqueKind string) string {
	if torqueKind == "plan" {
		return "plan"
	}
	return "task"
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

func shape(t rawTask, descLimit int, withDetail bool) Task {
	out := Task{ID: t.ID, Kind: t.Kind, Status: t.Status, Priority: t.Priority, UpdatedAt: t.UpdatedAt,
		ProjectID: t.ProjectID, ParentID: t.ParentID, EpicID: t.EpicID, SprintID: t.SprintID, CollectionID: t.CollectionID,
		Tags: []string{}, ContentTrust: ContentTrust}
	title, _ := cut(redact.Text(t.Title), 300)
	out.Title = title
	for i, g := range t.Tags {
		if i == 20 {
			break
		}
		g, _ = cut(g, 64)
		out.Tags = append(out.Tags, g)
	}
	if withDetail {
		out.CreatedAt = t.CreatedAt
		for i, d := range t.DependsOn {
			if i == 50 {
				break
			}
			out.DependsOn = append(out.DependsOn, d)
		}
		out.BlockedReason, _ = cut(redact.Text(t.BlockedReason), 500)
	}
	if t.Description != "" {
		out.DescriptionBytes = len(t.Description)
		out.Description, out.DescriptionTruncated = cut(redact.Text(t.Description), descLimit)
	}
	return out
}

// GetTask returns one task or plan with its description (bounded to 8 KiB).
func (r *Reader) GetTask(ctx context.Context, id string) (Task, error) {
	if !idPattern.MatchString(id) {
		return Task{}, ErrInvalid
	}
	b, err := r.get(ctx, "/api/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return Task{}, err
	}
	var t rawTask
	if json.Unmarshal(b, &t) != nil || t.ID != id {
		return Task{}, ErrUnavailable
	}
	return shape(t, GetDescriptionBytes, true), nil
}

type Comment struct {
	ID               string `json:"id"`
	Author           string `json:"author"`
	Content          string `json:"content"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
	CreatedAt        string `json:"created_at"`
}
type CommentPage struct {
	TaskID       string    `json:"task_id"`
	Items        []Comment `json:"items"`
	Returned     int       `json:"returned"`
	HasMore      bool      `json:"has_more"`
	NextCursor   string    `json:"next_cursor,omitempty"`
	ContentTrust string    `json:"content_trust"`
}

// Comments returns a page of a task's comments, oldest first. The cursor is an
// opaque offset. A page is cut at a comment boundary once it reaches 32 KiB.
func (r *Reader) Comments(ctx context.Context, id string, limit int, cursor string) (CommentPage, error) {
	if !idPattern.MatchString(id) {
		return CommentPage{}, ErrInvalid
	}
	if limit == 0 {
		limit = DefaultComments
	}
	if limit < 1 || limit > MaxComments {
		return CommentPage{}, ErrInvalid
	}
	offset := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 || n > 1_000_000 {
			return CommentPage{}, ErrInvalid
		}
		offset = n
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}, "sort_by": {"created_at"}, "sort_dir": {"asc"}}
	b, err := r.get(ctx, "/api/v1/tasks/"+url.PathEscape(id)+"/comments", q)
	if err != nil {
		return CommentPage{}, err
	}
	var in struct {
		Items []struct {
			ID        json.Number `json:"id"`
			Author    string      `json:"author"`
			Content   string      `json:"content"`
			CreatedAt string      `json:"created_at"`
		} `json:"items"`
		Meta struct {
			HasMore bool `json:"has_more"`
		} `json:"meta"`
	}
	if json.Unmarshal(b, &in) != nil || in.Items == nil {
		return CommentPage{}, ErrUnavailable
	}
	out := CommentPage{TaskID: id, Items: []Comment{}, ContentTrust: ContentTrust}
	size := 0
	for i, c := range in.Items {
		item := Comment{ID: c.ID.String(), CreatedAt: c.CreatedAt}
		item.Author, _ = cut(redact.Text(c.Author), 120)
		item.Content, item.ContentTruncated = cut(redact.Text(c.Content), CommentBytes)
		raw, _ := json.Marshal(item)
		if i > 0 && size+len(raw) > ResponseBytes {
			out.HasMore = true
			break
		}
		size += len(raw)
		out.Items = append(out.Items, item)
	}
	out.Returned = len(out.Items)
	if len(out.Items) < len(in.Items) {
		out.HasMore = true
	} else if in.Meta.HasMore {
		out.HasMore = true
	}
	if out.HasMore {
		out.NextCursor = strconv.Itoa(offset + len(out.Items))
	}
	return out, nil
}

type ListFilter struct {
	Filters map[string]string
	Limit   int
	Cursor  string
}
type TaskPage struct {
	Items        []Task `json:"items"`
	Returned     int    `json:"returned"`
	HasMore      bool   `json:"has_more"`
	NextCursor   string `json:"next_cursor,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
	Note         string `json:"note,omitempty"`
	ContentTrust string `json:"content_trust"`
}

// ListTasks returns brief records, newest update first, with Torque's own cursor.
func (r *Reader) ListTasks(ctx context.Context, f ListFilter) (TaskPage, error) {
	limit := f.Limit
	if limit == 0 {
		limit = DefaultList
	}
	if limit < 1 || limit > MaxList {
		return TaskPage{}, ErrInvalid
	}
	q := url.Values{"fields": {"summary"}, "limit": {strconv.Itoa(limit)}, "sort_by": {"updated_at"}, "sort_dir": {"desc"}}
	for _, k := range filterKeys {
		if v, ok := f.Filters[k]; ok && v != "" {
			if !filterPattern.MatchString(v) {
				return TaskPage{}, ErrInvalid
			}
			q.Set(k, v)
		}
	}
	for k := range f.Filters {
		known := false
		for _, a := range filterKeys {
			known = known || a == k
		}
		if !known {
			return TaskPage{}, ErrInvalid
		}
	}
	if f.Cursor != "" {
		if !cursorPattern.MatchString(f.Cursor) {
			return TaskPage{}, ErrInvalid
		}
		q.Set("cursor", f.Cursor)
	}
	b, err := r.get(ctx, "/api/v1/tasks", q)
	if err != nil {
		return TaskPage{}, err
	}
	var in struct {
		Items []rawTask `json:"items"`
		Meta  struct {
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if json.Unmarshal(b, &in) != nil || in.Items == nil {
		return TaskPage{}, ErrUnavailable
	}
	out := TaskPage{Items: []Task{}, ContentTrust: ContentTrust}
	size := 0
	for i, t := range in.Items {
		item := shape(t, ListDescription, false)
		raw, _ := json.Marshal(item)
		if i > 0 && size+len(raw) > ResponseBytes {
			out.Truncated = true
			out.Note = "Page cut at the size limit. Narrow the filters or lower limit."
			break
		}
		size += len(raw)
		out.Items = append(out.Items, item)
	}
	out.Returned = len(out.Items)
	out.HasMore = in.Meta.HasMore
	if !out.Truncated && in.Meta.NextCursor != "" {
		out.NextCursor = in.Meta.NextCursor
	}
	return out, nil
}
