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
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
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
func NewSource(addr, tokenFile string) (Source, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("Tether address is missing")
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
	rt, err := upstreamauth.Wrap(transport, base, tokenFile)
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
func NewNotifier(addr, tokenFile string) (*tether.Client, error) {
	s, err := NewSource(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	return s.(*source).Client, nil
}
func (s *source) Agents(ctx context.Context) ([]Agent, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.base+"/chat/agents", nil)
	if err != nil {
		return nil, err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, &tether.APIError{StatusCode: res.StatusCode, Code: "roster_unavailable", Message: "Tether chat roster unavailable"}
	}
	var out struct {
		Agents []Agent `json:"agents"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&out)
	return out.Agents, err
}
