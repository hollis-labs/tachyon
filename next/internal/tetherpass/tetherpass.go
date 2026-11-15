// Package tetherpass forwards a reviewed set of operations to Tether, as the old console plugin's
// Tether proxy did (plugins/console objects.go and catalog.go). The caller's request picks an
// operation; the upstream path, query and allowed body fields come from the operation, never from the
// request, and browser credentials are never forwarded.
package tetherpass

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// Proxy is the Tether connection the operations share.
type Proxy struct {
	base   string
	client *http.Client
	err    error // the configuration problem, when there is one
}

// New is the proxy for the Tether at addr (https, http to a loopback host, unix:/path or tcp:host:port)
// with token. With no address every operation answers 503 configuration and nothing is dialed.
func New(addr string, token upstreamauth.Token) *Proxy {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return &Proxy{err: errors.New("not configured")}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxResponseHeaderBytes = 64 << 10
	base := strings.TrimRight(addr, "/")
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return &Proxy{err: errors.New("expected a Tether HTTP(S), TCP or unix address")}
	}
	rt, err := upstreamauth.Wrap(tr, base, token)
	if err != nil {
		return &Proxy{err: err}
	}
	return &Proxy{base: base, client: &http.Client{Transport: rt, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// ErrUnknown is a Target error for a request no reviewed operation matches (a malformed id, for
// example): 404 not_found, as the old proxy answered when no route matched.
var ErrUnknown = errors.New("unknown operation")

// Op is one reviewed operation.
type Op struct {
	// Target maps the request to the upstream path and query. An error is a 400 invalid_request with its
	// text as the message.
	Target func(r *http.Request) (path string, query url.Values, err error)
	// Fields are the JSON body fields the operation accepts, space separated. Empty means no body is
	// forwarded; Empty (a single space) means an empty JSON object only.
	Fields string
	// Timeout overrides the 15 s default (the old chat send used 25 s).
	Timeout time.Duration
}

// Empty is Op.Fields for an operation whose body must be an empty JSON object.
const Empty = " "

// Handler serves op.
func (p *Proxy) Handler(op Op) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		path, query, err := op.Target(r)
		if errors.Is(err, ErrUnknown) {
			fail(w, http.StatusNotFound, "not_found", "Unknown project or workstream operation")
			return
		}
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if p.err != nil {
			fail(w, http.StatusServiceUnavailable, "configuration", "Tether connection or credential is not configured")
			return
		}
		var body io.Reader
		if op.Fields != "" {
			raw, ok := checkBody(w, r, op.Fields)
			if !ok {
				return
			}
			body = bytes.NewReader(raw)
		}
		ctx := r.Context()
		if op.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, op.Timeout)
			defer cancel()
		}
		target := p.base + path
		if len(query) > 0 {
			target += "?" + query.Encode()
		}
		// p.base is the operator-configured Tether address; path comes from the reviewed operation.
		req, err := http.NewRequestWithContext(ctx, r.Method, target, body) //nolint:gosec // see above
		if err != nil {
			fail(w, http.StatusBadGateway, "upstream_unavailable", "Tether request failed")
			return
		}
		req.Header.Set("Tether-Protocol", "1")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		client := p.client
		if op.Timeout > 0 {
			c := *p.client
			c.Timeout = op.Timeout
			client = &c
		}
		res, err := client.Do(req) //nolint:gosec // the configured Tether address
		if err != nil {
			// The old plugin proxy's answer when the upstream cannot be reached.
			fail(w, http.StatusBadGateway, "unreachable", "Could not reach the remote service; check its availability and network connection.")
			return
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			fail(w, http.StatusBadGateway, "upstream_redirect", "Tether returned an unexpected redirect")
			return
		}
		if ct := res.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(res.Body, 4<<20))
	})
}

// checkBody reads a bounded JSON object holding only fields, none null, and returns it re-encoded.
func checkBody(w http.ResponseWriter, r *http.Request, fields string) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 || !json.Valid(raw) {
		fail(w, http.StatusBadRequest, "invalid_request", "A bounded JSON object is required")
		return nil, false
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil || in == nil {
		fail(w, http.StatusBadRequest, "invalid_request", "A JSON object is required")
		return nil, false
	}
	for k, v := range in {
		if !strings.Contains(" "+fields+" ", " "+k+" ") || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			fail(w, http.StatusBadRequest, "invalid_request", "Unsupported or null field: "+k)
			return nil, false
		}
	}
	out, _ := json.Marshal(in)
	return out, true
}

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
