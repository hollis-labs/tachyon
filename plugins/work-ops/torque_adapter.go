package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TorqueAdapter maps provider-neutral operations onto Torque's HTTP API.
type TorqueAdapter struct {
	baseURL string
	client  *http.Client
}

func NewTorqueAdapter(baseURL string) *TorqueAdapter {
	return &TorqueAdapter{baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 30 * time.Second}}
}
func (*TorqueAdapter) Capabilities(context.Context) (WorkCapabilities, error) {
	return WorkCapabilities{Provider: "torque", CanCreate: true, CanRead: true, CanUpdate: true, CanList: true, CanSearch: true, CanAssign: true, CanTransition: true, CanComment: true}, nil
}

func (a *TorqueAdapter) request(ctx context.Context, method, path string, body, out any) error {
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+"/api/v1"+path, input)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("torque request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("torque %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode torque response: %w", err)
	}
	return nil
}
func taskPath(id string) string { return "/tasks/" + url.PathEscape(id) }
func (a *TorqueAdapter) CreateWorkItem(ctx context.Context, req CreateWorkRequest) (*WorkItem, error) {
	// Keep tasks manual: this control plane does not enqueue scheduler runs.
	body := struct {
		CreateWorkRequest
		Manual bool `json:"manual"`
	}{req, true}
	var item WorkItem
	err := a.request(ctx, http.MethodPost, "/tasks", body, &item)
	return &item, err
}
func (a *TorqueAdapter) GetWorkItem(ctx context.Context, id string) (*WorkItem, error) {
	var item WorkItem
	err := a.request(ctx, http.MethodGet, taskPath(id), nil, &item)
	return &item, err
}
func (a *TorqueAdapter) UpdateWorkItem(ctx context.Context, id string, req UpdateWorkRequest) (*WorkItem, error) {
	var item WorkItem
	err := a.request(ctx, http.MethodPut, taskPath(id), req, &item)
	return &item, err
}
func (a *TorqueAdapter) ListWorkItems(ctx context.Context, filters WorkFilters) (*WorkList, error) {
	q := url.Values{}
	if filters.Status != "" {
		q.Set("status", filters.Status)
	}
	if filters.ProjectID != "" {
		q.Set("project_id", filters.ProjectID)
	}
	if filters.Tags != "" {
		q.Set("tags", filters.Tags)
	}
	if filters.Limit != 0 {
		q.Set("limit", strconv.Itoa(filters.Limit))
	}
	if filters.Offset != 0 {
		q.Set("offset", strconv.Itoa(filters.Offset))
	}
	var list WorkList
	err := a.request(ctx, http.MethodGet, "/tasks?"+q.Encode(), nil, &list)
	return &list, err
}
func (a *TorqueAdapter) SearchWorkItems(ctx context.Context, query string) (*WorkList, error) {
	var list WorkList
	err := a.request(ctx, http.MethodGet, "/tasks/search?"+url.Values{"q": {query}}.Encode(), nil, &list)
	list.Total = len(list.Tasks)
	return &list, err
}
func (a *TorqueAdapter) AssignWorkItem(ctx context.Context, id, assignee string) (*WorkItem, error) {
	// Torque has no native assignee field. Preserve metadata read from the
	// provider before updating its assignee convention. This is not an atomic
	// merge: concurrent metadata edits can race until Torque exposes a patch API.
	item, err := a.GetWorkItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata["assignee"] = assignee
	return a.UpdateWorkItem(ctx, id, UpdateWorkRequest{Metadata: &item.Metadata})
}
func (a *TorqueAdapter) TransitionWorkItem(ctx context.Context, id, status string) (*WorkItem, error) {
	var item WorkItem
	err := a.request(ctx, http.MethodPost, taskPath(id)+"/transition", map[string]string{"status": status}, &item)
	return &item, err
}
func (a *TorqueAdapter) AddComment(ctx context.Context, id string, comment CommentRequest) (*WorkComment, error) {
	var result WorkComment
	err := a.request(ctx, http.MethodPost, taskPath(id)+"/comments", comment, &result)
	return &result, err
}

var _ WorkAdapter = (*TorqueAdapter)(nil)
