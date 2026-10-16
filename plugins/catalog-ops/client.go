package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tether "github.com/hollis-labs/substrate/mesh/tetherclient"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const (
	maxResponseBytes = 4 << 20
	maxRequestBytes  = 4 << 20
	// Upstream error text is shown to the operator (catalog validation
	// failures are the point of a builder) but capped, and only for the
	// statuses where Tether's message describes the caller's own request.
	maxUpstreamMessage = 512
	readTimeout        = 10 * time.Second
	writeTimeout       = 30 * time.Second
)

// upstreamError is a Tether answer with a non-2xx status.
type upstreamError struct {
	status  int
	code    string
	message string
}

func (e *upstreamError) Error() string { return fmt.Sprintf("tether returned HTTP %d", e.status) }

var errResponseTooLarge = errors.New("tether response exceeded size limit")

// tetherAPI is a small JSON client for Tether's catalog and registry routes.
// tetherclient carries no methods for them, so this speaks HTTP directly over
// the same address forms and the same authenticated transport.
type tetherAPI struct {
	baseURL string
	http    *http.Client
}

func newTetherAPI(addr, tokenFile string) (*tetherAPI, error) {
	if addr == "" {
		addr = tether.DefaultListenAddr
	}
	baseURL := ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	switch {
	case strings.HasPrefix(addr, "unix:"):
		socket := strings.TrimPrefix(addr, "unix:")
		if strings.HasPrefix(socket, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, errors.New("cannot resolve Tether socket")
			}
			socket = filepath.Join(home, strings.TrimPrefix(socket, "~/"))
		}
		if socket == "" {
			return nil, errors.New("unix listen addr missing path")
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		// The host is never dialed (DialContext targets the socket); using a
		// loopback name lets the credential guard and origin pin apply as usual.
		baseURL = "http://localhost"
	case strings.HasPrefix(addr, "tcp:"):
		baseURL = "http://" + strings.TrimPrefix(addr, "tcp:")
	case strings.HasPrefix(addr, "http://"), strings.HasPrefix(addr, "https://"):
		baseURL = strings.TrimRight(addr, "/")
	default:
		return nil, fmt.Errorf("unsupported Tether address %q (expect unix:/path, tcp:host:port, or http(s)://host)", addr)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid Tether address")
	}
	rt, err := upstreamauth.Wrap(transport, baseURL, tokenFile)
	if err != nil {
		return nil, err
	}
	return &tetherAPI{baseURL: baseURL, http: &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// do sends one request and returns the raw JSON body of a 2xx answer.
func (a *tetherAPI) do(ctx context.Context, method, path string, query url.Values, body any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxRequestBytes {
			return nil, errors.New("request body too large")
		}
		reader = bytes.NewReader(raw)
	}
	target := a.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errResponseTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseUpstreamError(resp.StatusCode, data)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("tether returned a malformed response")
	}
	return json.RawMessage(data), nil
}

func parseUpstreamError(status int, data []byte) *upstreamError {
	e := &upstreamError{status: status}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &env) != nil {
		return e
	}
	e.code = env.Error.Code
	switch status {
	case http.StatusBadRequest, http.StatusConflict, http.StatusForbidden, http.StatusNotFound:
		m := strings.TrimSpace(env.Error.Message)
		if len(m) > maxUpstreamMessage {
			m = m[:maxUpstreamMessage]
		}
		e.message = m
	}
	return e
}
