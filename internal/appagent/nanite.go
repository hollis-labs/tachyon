package appagent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// NaniteConfig is read from a JSON file so a reinstalled agent (a new definition
// revision gets a new host row) is picked up without a restart:
//
//	{"url":"http://127.0.0.1:8090","token_file":"/path/client.token","label":"Tachyon",
//	 "session":{"definition_ref":{...},"host_settings":{...}}}
//
// "session" is the body Nanite gives for POST /api/agent/v1/sessions; it holds
// pins, never credentials. The token file holds a Nanite client credential with
// operate+tools and is read on every request.
type NaniteConfig struct {
	URL       string          `json:"url"`
	TokenFile string          `json:"token_file"`
	Label     string          `json:"label"`
	Session   json.RawMessage `json:"session"`
}

func LoadNaniteConfig(path string) (NaniteConfig, error) {
	var c NaniteConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("%w: %v", ErrNotConfigured, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%w: invalid config", ErrNotConfigured)
	}
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	if c.URL == "" || c.TokenFile == "" || len(bytes.TrimSpace(c.Session)) < 2 {
		return c, fmt.Errorf("%w: url, token_file and session are required", ErrNotConfigured)
	}
	if err := upstreamauth.Guard(c.URL, c.TokenFile); err != nil {
		return c, fmt.Errorf("%w: %v", ErrNotConfigured, err)
	}
	return c, nil
}

// Nanite is the temporary runtime adapter over Nanite's native agent API.
type Nanite struct {
	ConfigFile string
	Client     *http.Client // no overall timeout: Observe streams
	// StallAfter is how long a stream may stay silent. Nanite sends a keepalive
	// comment every 15 seconds, so silence this long means the stream is dead.
	StallAfter time.Duration
}

func NewNanite(configFile string) *Nanite {
	return &Nanite{ConfigFile: configFile, Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, StallAfter: 60 * time.Second}
}

func (n *Nanite) token(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: credential file unreadable", ErrRuntimeDown)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("%w: credential file is empty", ErrRuntimeDown)
	}
	return t, nil
}

func (n *Nanite) do(ctx context.Context, cfg NaniteConfig, method, path string, body any, accept string, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	tok, err := n.token(cfg.TokenFile)
	if err != nil {
		return nil, nil, err
	}
	var rd io.Reader
	if body != nil {
		var raw []byte
		if r, ok := body.(json.RawMessage); ok {
			raw = r
		} else if raw, err = json.Marshal(body); err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(raw)
	}
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.URL+"/api/agent/v1"+path, rd)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := n.Client.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("%w: %s", ErrRuntimeDown, redactURL(err))
	}
	return resp, cancel, nil
}

func redactURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return "request failed"
}

// statusErr turns a Nanite refusal into an operator-readable error without
// echoing credential-bearing text.
func statusErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var e struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &e)
	detail := e.Message
	if s, ok := e.Error.(string); ok && detail == "" {
		detail = s
	}
	if m, ok := e.Error.(map[string]any); ok && detail == "" {
		detail, _ = m["message"].(string)
	}
	switch resp.StatusCode {
	case 401, 403:
		return fmt.Errorf("%w: Nanite refused the credential (HTTP %d). It may be revoked or expired", ErrRuntimeDown, resp.StatusCode)
	case 404, 410:
		return ErrSessionGone
	case 429:
		return fmt.Errorf("%w: Nanite is busy (too many queued turns)", ErrRuntimeDown)
	}
	// Nanite's text is not shown to the operator: it may echo request content. A
	// redacted, bounded copy goes to the log for diagnosis.
	if len(detail) > 300 {
		detail = detail[:300]
	}
	slog.Warn("app agent: Nanite refused a request", "status", resp.StatusCode, "detail", redact.Text(detail))
	return fmt.Errorf("%w: Nanite answered HTTP %d", ErrRuntimeDown, resp.StatusCode)
}

func (n *Nanite) config() (NaniteConfig, error) { return LoadNaniteConfig(n.ConfigFile) }

// A stored session id is "<pin>~<id>": pin fingerprints the definition and host settings the
// session was created under. A session is bound to its pins and to the tool grants of that
// host row, so after a reinstall (a new definition revision gets a new host row and the old
// authority is revoked) an old session would silently run with no tools. When the configured
// pins change, the next turn starts a new session instead.
func pinOf(session json.RawMessage) string {
	var v any
	if json.Unmarshal(session, &v) != nil {
		return ""
	}
	canon, _ := json.Marshal(v) // object keys come out sorted
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:6])
}

func splitSession(s string) (pin, id string) {
	if i := strings.Index(s, "~"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s // legacy id stored before pins were tracked
}

func joinSession(pin, id string) string { return pin + "~" + id }

func (n *Nanite) Ensure(ctx context.Context, existing string) (string, error) {
	cfg, err := n.config()
	if err != nil {
		return "", err
	}
	pin := pinOf(cfg.Session)
	if oldPin, oldID := splitSession(existing); existing != "" && oldPin == pin {
		resp, cancel, err := n.do(ctx, cfg, http.MethodGet, "/sessions/"+url.PathEscape(oldID), nil, "application/json", 15*time.Second)
		if err != nil {
			return "", err
		}
		defer cancel()
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var s struct {
				State  string `json:"state"`
				Status string `json:"status"`
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = json.Unmarshal(b, &s)
			if st := s.State + s.Status; st != "stopped" && st != "closed" && st != "ended" {
				return existing, nil
			}
		} else if resp.StatusCode != 404 && resp.StatusCode != 410 {
			return "", statusErr(resp)
		}
	}
	resp, cancel, err := n.do(ctx, cfg, http.MethodPost, "/sessions", cfg.Session, "application/json", 30*time.Second)
	if err != nil {
		return "", err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 404 {
			return "", fmt.Errorf("%w: Nanite does not know the configured definition or host settings (reinstalled? update the app agent config)", ErrRuntimeDown)
		}
		return "", statusErr(resp)
	}
	var s struct {
		ID string `json:"session_view_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&s); err != nil || s.ID == "" {
		return "", fmt.Errorf("%w: unexpected session response", ErrRuntimeDown)
	}
	return joinSession(pin, s.ID), nil
}

func (n *Nanite) Submit(ctx context.Context, session, text string) (string, error) {
	cfg, err := n.config()
	if err != nil {
		return "", err
	}
	_, session = splitSession(session)
	body := map[string]any{"content": []map[string]string{{"kind": "text", "text": text}}, "delivery": "at_idle", "delta_mode": "live"}
	if cc := ClientContextFrom(ctx); cc != nil {
		body["client_context"] = cc
	}
	resp, cancel, err := n.do(ctx, cfg, http.MethodPost, "/sessions/"+url.PathEscape(session)+"/turns", body, "application/json", 30*time.Second)
	if err != nil {
		return "", err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", statusErr(resp)
	}
	var t struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil || t.TurnID == "" {
		return "", fmt.Errorf("%w: unexpected turn response", ErrRuntimeDown)
	}
	return t.TurnID, nil
}

// frame is the subset of a chatstream/v1 event this adapter reads.
type frame struct {
	Verb   string `json:"verb"`
	PartID string `json:"part_id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
	Meta   struct {
		CallID  string `json:"call_id"`
		Name    string `json:"name"`
		Detail  string `json:"detail"`
		IsError bool   `json:"is_error"`
	} `json:"meta"`
	Error json.RawMessage `json:"error"`
	Final struct {
		Summary string `json:"summary"`
	} `json:"final"`
}

func (n *Nanite) Observe(ctx context.Context, session, turn string, emit func(TurnEvent)) error {
	cfg, err := n.config()
	if err != nil {
		return err
	}
	_, session = splitSession(session)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	// Watchdog, started before the request so a server that never answers is also
	// caught. Any line (data or keepalive comment) resets it; silence cancels ctx.
	seen := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(n.StallAfter)
		defer t.Stop()
		for {
			select {
			case <-seen:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(n.StallAfter)
			case <-t.C:
				stop()
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	resp, cancel, err := n.do(ctx, cfg, http.MethodGet, "/sessions/"+url.PathEscape(session)+"/turns/"+url.PathEscape(turn)+"/events", nil, "text/event-stream", 0)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ErrRuntimeDown) {
			return fmt.Errorf("%w: the reply stream stalled", ErrRuntimeDown)
		}
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp)
	}
	tools := map[string]string{} // call id -> tool name
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	finished := false
	for sc.Scan() {
		select {
		case seen <- struct{}{}:
		default:
		}
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var f frame
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &f) != nil {
			continue
		}
		switch {
		case f.Verb == "part.delta" && f.Text != "":
			emit(TurnEvent{Kind: EventText, Text: f.Text})
		case f.Verb == "part.start" && f.Kind == "tool_call":
			id := firstNonEmpty(f.Meta.CallID, f.PartID)
			tools[id] = f.Meta.Name
			emit(TurnEvent{Kind: EventToolStart, Tool: Tool{ID: id, Name: f.Meta.Name, Status: "running"}})
		case f.Verb == "part.start" && f.Kind == "tool_result":
			id := firstNonEmpty(f.Meta.CallID, f.PartID)
			name := firstNonEmpty(f.Meta.Name, tools[id])
			status := "ok"
			if f.Meta.IsError {
				status = "error"
			}
			emit(TurnEvent{Kind: EventToolEnd, Tool: Tool{ID: id, Name: name, Status: status, Detail: f.Meta.Detail}})
		case f.Verb == "run.finish":
			emit(TurnEvent{Kind: EventFinish, Reason: f.Reason})
			finished = true
		case strings.HasSuffix(f.Verb, ".error") || f.Verb == "error":
			emit(TurnEvent{Kind: EventError, Err: errorText(f)})
			finished = true
		}
		if finished {
			return nil
		}
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: the reply stream stalled", ErrRuntimeDown)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%w: %s", ErrRuntimeDown, err)
	}
	return fmt.Errorf("%w: the reply stream ended early", ErrRuntimeDown)
}

func errorText(f frame) string {
	var s string
	if json.Unmarshal(f.Error, &s) == nil && s != "" {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(f.Error, &o) == nil && o.Message != "" {
		return o.Message
	}
	return firstNonEmpty(f.Text, f.Reason, "the agent run failed")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (n *Nanite) Result(ctx context.Context, session, turn string) (Result, error) {
	cfg, err := n.config()
	if err != nil {
		return Result{}, err
	}
	_, session = splitSession(session)
	resp, cancel, err := n.do(ctx, cfg, http.MethodGet, "/sessions/"+url.PathEscape(session)+"/turns/"+url.PathEscape(turn), nil, "application/json", 20*time.Second)
	if err != nil {
		return Result{}, err
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, statusErr(resp)
	}
	var g struct {
		State   string `json:"state"`
		Message struct {
			Status string `json:"status"`
			Error  any    `json:"error"`
			Parts  []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Text  string `json:"text"`
				Final struct {
					Text    string `json:"text"`
					Summary string `json:"summary"`
				} `json:"final"`
				Meta struct {
					CallID  string `json:"call_id"`
					Name    string `json:"name"`
					IsError bool   `json:"is_error"`
				} `json:"meta"`
			} `json:"parts"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&g); err != nil {
		return Result{}, fmt.Errorf("%w: unexpected turn snapshot", ErrRuntimeDown)
	}
	r := Result{State: g.State, Tools: []Tool{}}
	var text []string
	calls := map[string]int{}
	for _, p := range g.Message.Parts {
		switch p.Kind {
		case "text":
			if t := firstNonEmpty(p.Final.Text, p.Text); t != "" {
				text = append(text, t)
			}
		case "tool_call":
			id := firstNonEmpty(p.Meta.CallID, p.ID)
			calls[id] = len(r.Tools)
			r.Tools = append(r.Tools, Tool{ID: id, Name: p.Meta.Name, Status: "running"})
		case "tool_result":
			id := firstNonEmpty(p.Meta.CallID, p.ID)
			status := "ok"
			if p.Meta.IsError {
				status = "error"
			}
			if i, ok := calls[id]; ok {
				r.Tools[i].Status = status
			} else {
				r.Tools = append(r.Tools, Tool{ID: id, Name: p.Meta.Name, Status: status})
			}
		}
	}
	r.Text = strings.Join(text, "\n\n")
	if s, ok := g.Message.Error.(string); ok {
		r.Error = s
	} else if m, ok := g.Message.Error.(map[string]any); ok {
		r.Error, _ = m["message"].(string)
	}
	if r.Error == "" && (r.State == "failed" || r.State == "canceled") {
		r.Error = "the agent run " + r.State
	}
	return r, nil
}
