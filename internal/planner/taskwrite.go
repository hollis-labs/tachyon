package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// TaskState is one Torque task as the lanes need it.
type TaskState struct {
	TaskBrief
	BlockedReason string `json:"blocked_reason"`
}

// Get reads one task, including its blocked reason.
func (t *Tasks) Get(ctx context.Context, id string) (TaskState, error) {
	var st TaskState
	if !taskID.MatchString(id) {
		return st, objects.ErrInvalid
	}
	b, err := t.read(ctx, "/api/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return st, err
	}
	if json.Unmarshal(b, &st) != nil || st.ID != id {
		return st, ErrTaskSource
	}
	return st, nil
}

// write POSTs to Torque with Tachyon's Torque token; the identity in Torque is the token owner.
func (t *Tasks) write(ctx context.Context, path string, body any) error {
	if t == nil {
		return ErrTaskSource
	}
	if err := upstreamauth.Guard(t.Base, t.Token); err != nil {
		return ErrTaskSource
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+path, bytes.NewReader(raw))
	if err != nil {
		return ErrTaskSource
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return ErrTaskSource
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		var detail struct {
			Error any `json:"error"`
		}
		_ = json.Unmarshal(msg, &detail)
		return fmt.Errorf("%w: Torque refused (HTTP %d)", ErrTaskSource, resp.StatusCode)
	}
	return nil
}

// Transition sets a task's status; comment is posted atomically with it. Leaving done needs force.
func (t *Tasks) Transition(ctx context.Context, id, status, comment string, force bool) error {
	body := map[string]any{"status": status}
	if comment != "" {
		body["comment"], body["comment_author"] = comment, "tachyon-planner"
	}
	if force {
		body["force"] = true
	}
	return t.write(ctx, "/api/v1/tasks/"+url.PathEscape(id)+"/transition", body)
}

// Comment posts a Torque comment. Callers treat a failure as best-effort.
func (t *Tasks) Comment(ctx context.Context, id, text string) error {
	return t.write(ctx, "/api/v1/tasks/"+url.PathEscape(id)+"/comments", map[string]any{"content": text, "author": "tachyon-planner"})
}
