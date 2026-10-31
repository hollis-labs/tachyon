package agentsserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// TetherRestarter restarts a session with Tether's control routes: POST /sessions/{id}/stop, then POST
// /logical-agents/{id}/resume. Client carries the control token (builderchange.NewControlClient).
type TetherRestarter struct {
	Base   string
	Client *http.Client
}

func (t *TetherRestarter) post(ctx context.Context, path string, body any, want ...int) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, w := range want {
		if resp.StatusCode == w {
			return b, nil
		}
	}
	return nil, fmt.Errorf("tether %s answered %d", path, resp.StatusCode)
}

func (t *TetherRestarter) Stop(ctx context.Context, sessionID string) error {
	_, err := t.post(ctx, "/sessions/"+url.PathEscape(sessionID)+"/stop", nil, 200, 202, 204)
	return err
}

func (t *TetherRestarter) Resume(ctx context.Context, logicalAgentID, key string) (string, error) {
	b, err := t.post(ctx, "/logical-agents/"+url.PathEscape(logicalAgentID)+"/resume", map[string]string{"idempotency_key": key}, 200, 201, 202)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &out) != nil || out.ID == "" {
		return "", fmt.Errorf("tether resume returned no session id")
	}
	return out.ID, nil
}
