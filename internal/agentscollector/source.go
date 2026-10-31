package agentscollector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Snapshot is Tether's picture of the environment at a sequence number.
type Snapshot struct {
	Env       string
	HighWater int64
	Sessions  []Frame // one synthetic frame per session, Kind "session.snapshot"
}

// Source is Tether as the collector sees it. The wire shapes behind it are UNVERIFIED against a
// live Tether (design section 2); every call is read-only and needs only a read-scope token.
type Source interface {
	// Snapshot is strategy A. An error (or a slow 500, see CW-20261021-0002) selects strategy B.
	Snapshot(ctx context.Context) (Snapshot, error)
	// Sessions and Head are strategy B: the running sessions, and the newest event seq.
	Sessions(ctx context.Context) ([]Frame, error)
	Head(ctx context.Context) (int64, error)
	// History pages events newest first. before=0 starts at the head; it returns the next cursor
	// (0 when exhausted) and only events with seq > since.
	History(ctx context.Context, since, before int64, limit int) (frames []Frame, next int64, err error)
	// Stream follows live events after seq `after` until ctx ends or the connection drops. alive
	// is called for every keepalive so the caller's watchdog can tell a quiet stream from a dead one.
	Stream(ctx context.Context, after int64, on func(Frame) error, alive func()) error
}

type httpSource struct {
	base   string
	client *http.Client
}

type tetherHeader struct{ http.RoundTripper }

func (t tetherHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return t.RoundTripper.RoundTrip(r)
}

// NewHTTPSource builds the Tether client. tokenFile must hold a read-scope token; it is refused
// over plain http to a non-loopback host (upstreamauth.Guard). The client has no overall timeout
// because the event stream is long-lived; every other call gets its own deadline.
func NewHTTPSource(addr, tokenFile string) (Source, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("Tether address is missing")
	}
	if err := upstreamauth.Guard(addr, tokenFile); err != nil {
		return nil, err
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 15 * time.Second
	tr.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	rt, err := upstreamauth.Wrap(tr, base, tokenFile)
	if err != nil {
		return nil, err
	}
	return &httpSource{base: base, client: &http.Client{Transport: tetherHeader{rt}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *httpSource) get(ctx context.Context, path string, q url.Values, max int64, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := s.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Tether unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Tether answered HTTP %d for %s", res.StatusCode, path)
	}
	return json.NewDecoder(io.LimitReader(res.Body, max)).Decode(out)
}

// Unparsed counts stream frames ParseFrame rejected.
var Unparsed atomic.Int64

type wireSession struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	SessionState string          `json:"session_state"`
	State        string          `json:"state"`
	WorkState    json.RawMessage `json:"work_state"`
	PendingA     *bool           `json:"pending_approval"`
	PendingQ     *bool           `json:"pending_question"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Workroot     string          `json:"workroot"`
	WorkingDir   string          `json:"working_dir"`
	TaskID       string          `json:"task_id"`
	StartedAt    string          `json:"started_at"`
	UpdatedAt    string          `json:"updated_at"`
	// Live /environment/snapshot names (tetherclient.EnvironmentSession).
	LogicalAgentID string `json:"logical_agent_id"`
	InstanceStatus string `json:"instance_status"`
	LastActivity   string `json:"last_activity"`
}

// frame turns a listed session into a synthetic snapshot frame. work_state may be a string or an
// object {state, known}; a missing or known:false state stays unknown.
func (w wireSession) frame() (Frame, bool) {
	id := firstNonEmpty(w.ID, w.SessionID)
	if id == "" {
		return Frame{}, false
	}
	f := Frame{Kind: "session.snapshot", SessionID: id, SessionState: clean(firstNonEmpty(w.SessionState, w.State), 40),
		Provider: clean(w.Provider, 60), Model: clean(w.Model, 80), Workroot: clean(firstNonEmpty(w.Workroot, w.WorkingDir), 300),
		LogicalAgentID: clean(w.LogicalAgentID, 120), AgentName: clean(w.LogicalAgentID, 120),
		TaskRef: clean(w.TaskID, 60), PendingApproval: w.PendingA, PendingQuestion: w.PendingQ}
	var str string
	var obj struct {
		State string `json:"state"`
		Known *bool  `json:"known"`
	}
	if json.Unmarshal(w.WorkState, &str) == nil {
		f.WorkState = clean(str, 40)
	} else if json.Unmarshal(w.WorkState, &obj) == nil && (obj.Known == nil || *obj.Known) {
		f.WorkState = clean(obj.State, 40)
	}
	if f.WorkState == "unknown" {
		f.WorkState = ""
	}
	if f.WorkState == "" && w.InstanceStatus == "waiting" {
		f.WorkState = "waiting" // running/starting say nothing about busy vs idle, so they stay unknown
	}
	for _, s := range []string{w.LastActivity, w.UpdatedAt, w.StartedAt} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			f.At = t.UTC()
			break
		}
	}
	return f, true
}

func (s *httpSource) Snapshot(ctx context.Context) (Snapshot, error) {
	var w struct {
		EnvID     string        `json:"environment_id"` // the live key
		Env       string        `json:"env_id"`         // older shape
		HighWater int64         `json:"high_water_seq"`
		Sessions  []wireSession `json:"sessions"`
	}
	if err := s.get(ctx, "/environment/snapshot", nil, 8<<20, &w); err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Env: clean(firstNonEmpty(w.EnvID, w.Env), 80), HighWater: w.HighWater}
	for _, ws := range w.Sessions {
		if f, ok := ws.frame(); ok {
			out.Sessions = append(out.Sessions, f)
		}
	}
	return out, nil
}

func (s *httpSource) Sessions(ctx context.Context) ([]Frame, error) {
	var out []Frame
	cursor := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"state": {"running"}, "limit": {"200"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var w struct {
			Sessions   []wireSession   `json:"sessions"`
			NextCursor json.RawMessage `json:"next_cursor"` // an opaque string live ("v1..."); older shapes sent a number
		}
		if err := s.get(ctx, "/sessions", q, 8<<20, &w); err != nil {
			return nil, err
		}
		for _, ws := range w.Sessions {
			if f, ok := ws.frame(); ok {
				out = append(out, f)
			}
		}
		next := cursorText(w.NextCursor)
		if next == "" || next == "0" || next == cursor || len(w.Sessions) == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

func (s *httpSource) Head(ctx context.Context) (int64, error) {
	frames, _, err := s.History(ctx, 0, 0, 1)
	if err != nil || len(frames) == 0 {
		return 0, err
	}
	return frames[0].Seq, nil
}

func (s *httpSource) History(ctx context.Context, since, before int64, limit int) ([]Frame, int64, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if since > 0 {
		q.Set("since_seq", strconv.FormatInt(since, 10))
	}
	if before > 0 {
		q.Set("cursor", strconv.FormatInt(before, 10))
	}
	var w struct {
		Events     []json.RawMessage `json:"events"`
		NextCursor json.RawMessage   `json:"next_cursor"`
	}
	if err := s.get(ctx, "/events", q, 16<<20, &w); err != nil {
		return nil, 0, err
	}
	var out []Frame
	for _, raw := range w.Events {
		if f, ok := ParseFrame(raw); ok && f.Seq > since {
			out = append(out, f)
		}
	}
	// This pager walks by sequence number; an opaque cursor would end the walk, never fail it.
	next, _ := strconv.ParseInt(cursorText(w.NextCursor), 10, 64)
	return out, next, nil
}

// cursorText reads a cursor that may be an opaque string, a number, or null.
func cursorText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return strings.TrimSpace(str)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func (s *httpSource) Stream(ctx context.Context, after int64, on func(Frame) error, alive func()) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.base+"/environment/events?after_seq="+strconv.FormatInt(after, 10), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Tether unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Tether answered HTTP %d for the event stream", res.StatusCode)
	}
	alive()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20) // a long session.output frame is read, never kept
	var data strings.Builder
	var event string // the SSE event name: Tether sends a gap as "event: gap" with no kind in the data
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data.Len() > 0 {
				if f, ok := ParseFrame(withKind([]byte(data.String()), event)); ok {
					if err := on(f); err != nil {
						return err
					}
				} else {
					Unparsed.Add(1)
				}
				data.Reset()
			}
			event = ""
			alive()
		case strings.HasPrefix(line, ":"):
			alive()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if data.Len() < 8<<20 {
				data.WriteString(strings.TrimSpace(line[5:]))
			}
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("event stream lost: %w", err)
	}
	if ctx.Err() == nil {
		return errors.New("event stream closed by Tether")
	}
	return nil
}

// withKind adds the SSE event name as the frame kind when the data carries none (a gap frame). Data that
// already has a kind, or an event name that is empty or the SSE default, is returned unchanged.
func withKind(data []byte, event string) []byte {
	if event == "" || event == "message" {
		return data
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	if k, ok := m["kind"]; ok && string(k) != `""` {
		return data
	}
	m["kind"], _ = json.Marshal(event)
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}
