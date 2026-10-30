package builderread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// LiveSession is a running Tether session as far as the Builder needs it. Tether has no launch or agent filter
// on /sessions (design request B5), so the list is fetched whole and grouped here.
type LiveSession struct {
	ID, LaunchID, AgentID, State string
}

// SessionSource lists running sessions. An error means "unknown", never "none": callers report null, not zero.
type SessionSource interface {
	Running(ctx context.Context) ([]LiveSession, error)
}

// cached wraps a source with a short-lived cache (5 s by default) so a page of used_by calls is one upstream call.
type cached struct {
	src SessionSource
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	at   time.Time
	list []LiveSession
	err  error
}

func newCached(src SessionSource, ttl time.Duration, now func() time.Time) *cached {
	if now == nil {
		now = time.Now
	}
	return &cached{src: src, ttl: ttl, now: now}
}

func (c *cached) Running(ctx context.Context) ([]LiveSession, error) {
	if c == nil || c.src == nil {
		return nil, errors.New("sessions are not available")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.now().Sub(c.at) < c.ttl {
		return c.list, c.err
	}
	c.list, c.err = c.src.Running(ctx)
	c.at = c.now()
	return c.list, c.err
}

type tetherSessions struct {
	base   string
	client *http.Client
}

// tetherClient builds the read-scope HTTP client for a Tether address (unix:, tcp: or http(s) URL): the same
// guards as the catalog index (credential never over plain http to a remote host, redirects refused).
func tetherClient(addr, tokenFile string) (string, *http.Client, error) {
	if strings.TrimSpace(addr) == "" {
		return "", nil, errors.New("Tether address is missing")
	}
	if err := upstreamauth.Guard(addr, tokenFile); err != nil {
		return "", nil, err
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 15 * time.Second
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
		return "", nil, err
	}
	return base, &http.Client{Transport: header{rt}, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// NewTetherSessions reads running sessions from Tether with the read-scope token. Wire names are UNVERIFIED
// (launch_id, logical_agent_id or agent_id); a session without a launch still counts for its agent.
func NewTetherSessions(addr, tokenFile string) (SessionSource, error) {
	base, client, err := tetherClient(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	return &tetherSessions{base: base, client: client}, nil
}

type header struct{ http.RoundTripper }

func (h header) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return h.RoundTripper.RoundTrip(r)
}

func (t *tetherSessions) Running(ctx context.Context) ([]LiveSession, error) {
	var out []LiveSession
	cursor := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"state": {"running"}, "limit": {"200"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", t.base+"/sessions?"+q.Encode(), nil)
		req.Header.Set("Accept", "application/json")
		res, err := t.client.Do(req)
		if err != nil {
			return nil, errors.New("Tether unreachable")
		}
		raw, rerr := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if res.StatusCode != 200 || rerr != nil {
			return nil, fmt.Errorf("Tether answered HTTP %d for /sessions", res.StatusCode)
		}
		var generic struct {
			Sessions   []map[string]any `json:"sessions"`
			NextCursor json.RawMessage  `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &generic); err != nil {
			return nil, errors.New("Tether response unreadable")
		}
		str := func(m map[string]any, keys ...string) string {
			for _, k := range keys {
				if v, ok := m[k].(string); ok && v != "" {
					return clean(v, 120)
				}
			}
			return ""
		}
		for _, m := range generic.Sessions {
			id := str(m, "session_id", "id")
			if id == "" {
				continue
			}
			out = append(out, LiveSession{ID: id, LaunchID: str(m, "launch_id", "launch"), AgentID: str(m, "logical_agent_id", "agent_id", "agent"), State: str(m, "session_state", "state")})
		}
		next := strings.Trim(string(generic.NextCursor), `"`)
		if next == "" || next == "null" || next == "0" || next == cursor || len(generic.Sessions) == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

func clean(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if len(s) > n {
		s = s[:n]
	}
	return s
}
