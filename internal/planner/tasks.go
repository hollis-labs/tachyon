package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

var ErrTaskSource = errors.New("execution task source unavailable")
var taskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)

type Tasks struct {
	Base, Token string
	Client      *http.Client
}
type TaskBrief struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	UpdatedAt string `json:"updated_at"`
}

func NewTasks(base, token string) *Tasks {
	return &Tasks{Base: strings.TrimRight(base, "/"), Token: token, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (t *Tasks) read(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	if t == nil {
		return nil, ErrTaskSource
	}
	u, err := url.Parse(t.Base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrTaskSource
	}
	if err := upstreamauth.Guard(t.Base, t.Token); err != nil {
		return nil, ErrTaskSource
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrTaskSource
	}
	req.Header.Set("Accept", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, ErrTaskSource
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, objects.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrTaskSource, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 || !json.Valid(b) {
		return nil, ErrTaskSource
	}
	return b, nil
}
func (t *Tasks) Titles(ctx context.Context, ids []string) (map[string]*TaskBrief, error) {
	if len(ids) > 100 {
		return nil, objects.ErrInvalid
	}
	for _, id := range ids {
		if !taskID.MatchString(id) {
			return nil, objects.ErrInvalid
		}
	}
	out := map[string]*TaskBrief{}
	var mu sync.Mutex
	var problem error
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				problem = ErrTaskSource
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			b, err := t.read(ctx, "/api/v1/tasks/"+url.PathEscape(id), nil)
			var task *TaskBrief
			if err == nil {
				task = &TaskBrief{}
				if json.Unmarshal(b, task) != nil || task.ID != id {
					err = ErrTaskSource
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil && !errors.Is(err, objects.ErrNotFound) {
				problem = err
			}
			out[id] = task
		}(id)
	}
	wg.Wait()
	return out, problem
}
func (t *Tasks) List(ctx context.Context, filters url.Values) ([]TaskBrief, error) {
	q := url.Values{"fields": {"summary"}, "limit": {"200"}, "sort_by": {"updated_at"}, "sort_dir": {"desc"}}
	for k, v := range filters {
		q[k] = v
	}
	out := []TaskBrief{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for page := 0; page < 100; page++ {
		b, err := t.read(ctx, "/api/v1/tasks", q)
		if err != nil {
			return nil, err
		}
		var result struct {
			Items []TaskBrief `json:"items"`
			Meta  struct {
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
		}
		if json.Unmarshal(b, &result) != nil || result.Items == nil {
			return nil, ErrTaskSource
		}
		for _, v := range result.Items {
			if v.ID == "" {
				return nil, ErrTaskSource
			}
			if !seen[v.ID] {
				out = append(out, v)
				seen[v.ID] = true
			}
		}
		next := result.Meta.NextCursor
		if next == "" {
			if result.Meta.HasMore {
				return nil, ErrTaskSource
			}
			return out, nil
		}
		if cursors[next] || len(result.Items) == 0 {
			return nil, ErrTaskSource
		}
		cursors[next] = true
		q.Set("cursor", next)
	}
	return nil, fmt.Errorf("%w: task scan exceeded safety bound", ErrTaskSource)
}

type WorkstreamTasks struct {
	WorkstreamID string         `json:"workstream_id"`
	ProjectIDs   []string       `json:"torque_project_ids"`
	Tag          string         `json:"torque_tag"`
	Counts       map[string]int `json:"counts"`
	Total        int            `json:"total"`
	Open         []TaskBrief    `json:"open"`
	OpenTotal    int            `json:"open_total"`
	Note         string         `json:"note,omitempty"`
}

func openTask(s string) bool {
	switch s {
	case "backlog", "todo", "queued", "doing", "review", "blocked", "paused":
		return true
	}
	return false
}

// TaskDetails only forwards read-only task/detail/comment resources. It is not
// a catch-all provider proxy and can never reach Torque's Planner endpoints.
func (h *HTTP) TaskDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		send(w, 405, Item{"code": "method_not_allowed"})
		return
	}
	path := r.PathValue("task")
	if !taskID.MatchString(path) {
		h.fail(w, r, "", "", objects.ErrInvalid)
		return
	}
	endpoint := "/api/v1/tasks/" + url.PathEscape(path)
	query := url.Values{}
	if strings.HasSuffix(r.URL.Path, "/comments") {
		endpoint += "/comments"
		for _, k := range []string{"limit", "offset", "cursor", "sort_by", "sort_dir", "include_total"} {
			if v := r.URL.Query().Get(k); v != "" {
				query.Set(k, v)
			}
		}
	}
	b, err := h.Tasks.read(r.Context(), endpoint, query)
	if err != nil {
		h.fail(w, r, "", "", err)
		return
	}
	send(w, 200, b)
}
func (t *Tasks) ForWorkstream(ctx context.Context, row Item) (WorkstreamTasks, error) {
	out := WorkstreamTasks{WorkstreamID: textField(row, "id"), ProjectIDs: []string{}, Tag: textField(row, "torque_tag"), Counts: map[string]int{}, Open: []TaskBrief{}}
	projects := map[string]bool{}
	for _, v := range array(row["torque_project_ids"]) {
		p, ok := v.(string)
		if !ok || !taskID.MatchString(p) {
			return out, objects.ErrInvalid
		}
		if !projects[p] {
			out.ProjectIDs = append(out.ProjectIDs, p)
			projects[p] = true
		}
	}
	if len(out.ProjectIDs) > 50 {
		return out, objects.ErrInvalid
	}
	if len(out.ProjectIDs) == 0 && out.Tag == "" {
		out.Note = "This workstream names no Torque project or tag."
		return out, nil
	}
	scopes := out.ProjectIDs
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	seen := map[string]bool{}
	for _, p := range scopes {
		q := url.Values{}
		if p != "" {
			q.Set("project_id", p)
		}
		if out.Tag != "" {
			q.Set("tags", out.Tag)
		}
		rows, err := t.List(ctx, q)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out.Counts[r.Status]++
			out.Total++
			if openTask(r.Status) {
				out.Open = append(out.Open, r)
				out.OpenTotal++
			}
		}
	}
	sort.Slice(out.Open, func(i, j int) bool {
		if out.Open[i].UpdatedAt == out.Open[j].UpdatedAt {
			return out.Open[i].ID < out.Open[j].ID
		}
		return out.Open[i].UpdatedAt > out.Open[j].UpdatedAt
	})
	if len(out.Open) > 25 {
		out.Open = out.Open[:25]
	}
	return out, nil
}
