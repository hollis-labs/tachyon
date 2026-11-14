package ceremony

import (
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

// TorqueHTTP reads Torque's task list and checkpoints (GET /api/v1/tasks, GET /api/v1/checkpoints/pending,
// GET /api/v1/checkpoints/{correlation_id}).
type TorqueHTTP struct {
	Base   string
	Token  string
	Client *http.Client
}

func (t *TorqueHTTP) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.Base, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("torque unreachable")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("torque answered %d", resp.StatusCode)
	}
	if json.Unmarshal(b, out) != nil {
		return fmt.Errorf("torque sent an unreadable answer")
	}
	return nil
}

type taskPage struct {
	Items []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		UpdatedAt string `json:"updated_at"`
	} `json:"items"`
	Meta struct {
		Total *int `json:"total"`
	} `json:"meta"`
}

// Stalled lists tasks in doing, review or blocked not updated since before, oldest first (up to limit per
// status), with the total.
func (t *TorqueHTTP) Stalled(ctx context.Context, before time.Time, limit int) ([]Task, int, error) {
	var out []Task
	total := 0
	for _, st := range []string{"doing", "review", "blocked"} {
		var p taskPage
		q := url.Values{"status": {st}, "updated_before": {before.UTC().Format(time.RFC3339)}, "sort_by": {"updated_at"}, "sort_dir": {"asc"},
			"limit": {strconv.Itoa(limit)}, "include_total": {"true"}}
		if err := t.get(ctx, "/api/v1/tasks", q, &p); err != nil {
			return nil, 0, err
		}
		for _, it := range p.Items {
			at, _ := time.Parse(time.RFC3339Nano, it.UpdatedAt)
			out = append(out, Task{ID: it.ID, Title: it.Title, Status: it.Status, UpdatedAt: at})
		}
		if p.Meta.Total != nil {
			total += *p.Meta.Total
		} else {
			total += len(p.Items)
		}
	}
	return out, total, nil
}

// DoneSince counts tasks closed done (last updated) after since.
func (t *TorqueHTTP) DoneSince(ctx context.Context, since time.Time) (int, error) {
	var p taskPage
	q := url.Values{"status": {"done"}, "updated_after": {since.UTC().Format(time.RFC3339)}, "limit": {"1"}, "include_total": {"true"}}
	if err := t.get(ctx, "/api/v1/tasks", q, &p); err != nil {
		return 0, err
	}
	if p.Meta.Total == nil {
		return 0, fmt.Errorf("torque sent no total")
	}
	return *p.Meta.Total, nil
}

// PendingCheckpoints lists every pending checkpoint, oldest first.
func (t *TorqueHTTP) PendingCheckpoints(ctx context.Context) ([]Checkpoint, error) {
	var out []Checkpoint
	cursor := ""
	for page := 0; page < 10; page++ {
		var p struct {
			Items []struct {
				CorrelationID string `json:"correlation_id"`
				TaskID        string `json:"task_id"`
				Type          string `json:"type"`
				PayloadJSON   string `json:"payload_json"`
				EmittedAt     string `json:"emitted_at"`
			} `json:"items"`
			Meta struct {
				NextCursor string `json:"next_cursor"`
				HasMore    bool   `json:"has_more"`
			} `json:"meta"`
		}
		q := url.Values{"limit": {"200"}, "sort_by": {"created_at"}, "sort_dir": {"asc"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		if err := t.get(ctx, "/api/v1/checkpoints/pending", q, &p); err != nil {
			return nil, err
		}
		for _, it := range p.Items {
			var pl struct {
				Title string `json:"title"`
			}
			_ = json.Unmarshal([]byte(it.PayloadJSON), &pl)
			at, _ := time.Parse(time.RFC3339Nano, it.EmittedAt)
			out = append(out, Checkpoint{ID: it.CorrelationID, TaskID: it.TaskID, Type: it.Type, Title: pl.Title, EmittedAt: at})
		}
		if !p.Meta.HasMore || p.Meta.NextCursor == "" {
			break
		}
		cursor = p.Meta.NextCursor
	}
	return out, nil
}

// Checkpoint reads one checkpoint's record by correlation_id (GET /api/v1/checkpoints/{correlation_id}; about 40ms).
func (t *TorqueHTTP) Checkpoint(ctx context.Context, correlationID string) (CheckpointRecord, error) {
	var r struct {
		CorrelationID string  `json:"correlation_id"`
		Type          string  `json:"type"`
		Status        string  `json:"status"`
		EmittedAt     string  `json:"emitted_at"`
		RespondedAt   *string `json:"responded_at"`
	}
	if err := t.get(ctx, "/api/v1/checkpoints/"+url.PathEscape(correlationID), url.Values{}, &r); err != nil {
		return CheckpointRecord{}, err
	}
	out := CheckpointRecord{ID: r.CorrelationID, Type: r.Type, Status: r.Status}
	out.EmittedAt, _ = time.Parse(time.RFC3339Nano, r.EmittedAt)
	if r.RespondedAt != nil {
		if at, err := time.Parse(time.RFC3339Nano, *r.RespondedAt); err == nil {
			out.RespondedAt = &at
		}
	}
	return out, nil
}
