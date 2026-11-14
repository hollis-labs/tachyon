package agentsserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrResumeRefused is Tether refusing a logical-agent resume (409): a worker-placed seat while
// catalog.defaults.worker_resume is off, a detached session, an agent never launched. The restart relaunches.
var ErrResumeRefused = errors.New("tether refused the resume")

// TetherRestarter restarts a session with Tether's control routes: GET /sessions/{id} (its launch and placement,
// read before anything stops), POST /sessions/{id}/stop, then POST /sessions {launch, continuity: relaunch} and
// POST /sessions/{id}/launch. Only a hub-placed session is resumed instead (POST /logical-agents/{id}/resume),
// and relaunched if Tether refuses. Client carries the control token (builderchange.NewControlClient).
type TetherRestarter struct {
	Base   string
	Client *http.Client
}

func (t *TetherRestarter) post(ctx context.Context, path string, body any, want ...int) ([]byte, error) {
	return t.do(ctx, http.MethodPost, path, body, want...)
}

// tetherError is a refused Tether call: the route, the status and Tether's error code. Tether's message is not
// kept: it may name paths or workers, and the control status is shown in the browser.
type tetherError struct {
	path   string
	status int
	code   string
}

func (e *tetherError) Error() string {
	if e.code != "" {
		return fmt.Sprintf("tether %s answered %d (%s)", e.path, e.status, e.code)
	}
	return fmt.Sprintf("tether %s answered %d", e.path, e.status)
}

func (t *TetherRestarter) do(ctx context.Context, method, path string, body any, want ...int) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.Base+path, rd)
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
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	route := path
	if i := strings.IndexByte(route, '?'); i >= 0 {
		route = route[:i]
	}
	return nil, &tetherError{path: route, status: resp.StatusCode, code: strings.TrimSpace(e.Error.Code)}
}

// Placement reads the session's launch (what a relaunch starts from) and the worker it runs on ("" on the hub). It
// is read before anything is stopped.
func (t *TetherRestarter) Placement(ctx context.Context, sessionID string) (Placement, error) {
	b, err := t.do(ctx, http.MethodGet, "/sessions/"+url.PathEscape(sessionID), nil, 200)
	if err != nil {
		return Placement{}, err
	}
	var out struct {
		LaunchID string `json:"launch_id"`
		WorkerID string `json:"worker_id"`
	}
	if json.Unmarshal(b, &out) != nil || out.LaunchID == "" {
		return Placement{}, fmt.Errorf("tether names no launch for this session")
	}
	return Placement{Launch: out.LaunchID, Worker: out.WorkerID}, nil
}

func (t *TetherRestarter) Stop(ctx context.Context, sessionID string) error {
	_, err := t.post(ctx, "/sessions/"+url.PathEscape(sessionID)+"/stop", nil, 200, 202, 204)
	return err
}

func (t *TetherRestarter) Resume(ctx context.Context, logicalAgentID, key string) (string, error) {
	b, err := t.post(ctx, "/logical-agents/"+url.PathEscape(logicalAgentID)+"/resume", map[string]string{"idempotency_key": key}, 200, 201, 202)
	var te *tetherError
	if errors.As(err, &te) && te.status == http.StatusConflict {
		return "", fmt.Errorf("%w: %v", ErrResumeRefused, err)
	}
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

// Relaunch starts a new session of the launch as a continuation of the seat: POST /sessions {launch, continuity:
// relaunch} creates it (lineage only: no provider conversation is resumed), POST /sessions/{id}/launch starts it.
// When the create succeeds but the launch fails, it returns the created session id with the error.
// Called right after the stop: Tether gives the relaunch the previous session's grant once that session has ended
// or has a stop requested (tether#553); otherwise the grant is bounded by Tachyon's credential (ScopesDropped).
func (t *TetherRestarter) Relaunch(ctx context.Context, launchID, key string) (string, error) {
	b, err := t.post(ctx, "/sessions", map[string]string{"launch": launchID, "continuity": "relaunch", "idempotency_key": key}, 200, 201)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &out) != nil || out.ID == "" {
		return "", fmt.Errorf("tether relaunch returned no session id")
	}
	if _, err := t.post(ctx, "/sessions/"+url.PathEscape(out.ID)+"/launch", nil, 200, 201, 202); err != nil {
		return out.ID, err // created but not started: the caller names it
	}
	return out.ID, nil
}

// ScopesDropped reports the scopes Tether dropped when it launched sessionID (session.scopes_narrowed, published
// during the launch); nil when it dropped none.
func (t *TetherRestarter) ScopesDropped(ctx context.Context, sessionID string) ([]string, error) {
	b, err := t.do(ctx, http.MethodGet, "/events?kind=session.scopes_narrowed&order=desc&limit=50", nil, 200)
	if err != nil {
		return nil, err
	}
	var page struct {
		Events []struct {
			SessionID string          `json:"session_id"`
			Payload   json.RawMessage `json:"payload_json"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return nil, err
	}
	for _, e := range page.Events {
		if e.SessionID != sessionID {
			continue
		}
		raw := []byte(e.Payload)
		var inner string
		if json.Unmarshal(raw, &inner) == nil {
			raw = []byte(inner) // payload_json is an object, or a string holding one
		}
		var p struct {
			Dropped []string `json:"dropped"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p.Dropped, nil
	}
	return nil, nil
}
