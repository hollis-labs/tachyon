package chatbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

type Agent struct {
	LaunchID string `json:"launch_id"`
	AgentID  string `json:"agent_id"`
	Channel  string `json:"channel"`
	Session  *struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		Boundary bool   `json:"at_turn_boundary"`
	} `json:"session"`
}

type Source interface {
	Agents(context.Context) ([]Agent, error)
	ChannelMessages(context.Context, string, tether.ChannelMessagesOptions) (tether.ChannelMessagesResponse, error)
	RoutingCapabilities(context.Context, string) (tether.RoutingCapabilitiesResponse, error)
	Reply(context.Context, string, string, tether.ReplyOptions) (tether.ReplyReceipt, error)
	ReplyDelivery(context.Context, string) (tether.ReplyDelivery, error)
}

type source struct {
	*tether.Client
	http *http.Client
	base string
}
type protocolTransport struct{ http.RoundTripper }

func (t protocolTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	res, err := t.RoundTripper.RoundTrip(r)
	if err == nil {
		res.Body = http.MaxBytesReader(nil, res.Body, 16<<20)
	}
	return res, err
}

// NewSource is the Tether chat source at addr (http(s)://, tcp:host:port or unix:/path). token is a
// secrets reference resolved per request; nil sends no credential.
func NewSource(addr string, token upstreamauth.Token) (Source, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("Tether address is missing") //nolint:staticcheck // a product name; operator-visible text kept
	}
	base := strings.TrimRight(addr, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	rt, err := upstreamauth.Wrap(transport, base, token)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: protocolTransport{rt}, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	api, err := tether.New(base, tether.WithToken(""), tether.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	return &source{Client: api, http: client, base: base}, nil
}

// NewNotifier returns the Tether client over the same connection settings as
// NewSource, for senders that need the messaging calls and not the chat roster.
func NewNotifier(addr string, token upstreamauth.Token) (*tether.Client, error) {
	s, err := NewSource(addr, token)
	if err != nil {
		return nil, err
	}
	return s.(*source).Client, nil
}
func (s *source) Agents(ctx context.Context) ([]Agent, error) {
	// s.base is the operator-configured Tether address, not request input.
	req, err := http.NewRequestWithContext(ctx, "GET", s.base+"/chat/agents", nil) //nolint:gosec // see above
	if err != nil {
		return nil, err
	}
	res, err := s.http.Do(req) //nolint:gosec // the configured Tether address
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return nil, &tether.APIError{StatusCode: res.StatusCode, Code: "roster_unavailable", Message: "Tether chat roster unavailable"}
	}
	var out struct {
		Agents []Agent `json:"agents"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&out)
	return out.Agents, err
}

// RosterPath is the chat roster route under the chat prefix.
const RosterPath = "/agents"

// NewRoster serves GET <chat prefix>/agents: Tether's chat roster, passed through as Tether answers it,
// as the old console plugin's proxy did. The query is dropped, so a caller cannot widen the roster to
// disabled launches. With Tether not configured (addr empty) it answers 503 configuration and dials
// nothing.
func NewRoster(addr string, token upstreamauth.Token) http.Handler {
	var src *source
	if strings.TrimSpace(addr) != "" {
		if s, err := NewSource(addr, token); err == nil {
			src = s.(*source)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if src == nil {
			fail(w, http.StatusServiceUnavailable, "configuration", "Tether chat connection or credential is not configured")
			return
		}
		// s.base is the operator-configured Tether address, not request input.
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, src.base+"/chat/agents", nil) //nolint:gosec // see above
		if err != nil {
			fail(w, http.StatusBadGateway, "upstream_unavailable", "Tether chat roster unavailable")
			return
		}
		res, err := src.http.Do(req) //nolint:gosec // the configured Tether address
		if err != nil {
			fail(w, http.StatusBadGateway, "upstream_unavailable", "Tether chat roster unavailable")
			return
		}
		defer func() { _ = res.Body.Close() }()
		if ct := res.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(res.Body, 2<<20))
	})
}
