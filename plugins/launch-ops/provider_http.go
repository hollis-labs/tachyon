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
	"os"
	"path/filepath"
	"strings"
	"time"

	tether "github.com/hollis-labs/go-tether-client"
)

const maxProviderResponse = 4 << 20

type providerResponseError struct {
	status    int
	oversized bool
}

func (e *providerResponseError) Error() string {
	if e.oversized {
		return fmt.Sprintf("provider response exceeds %d bytes (HTTP %d)", maxProviderResponse, e.status)
	}
	return fmt.Sprintf("provider returned HTTP %d", e.status)
}

type boundedProviderTransport struct{ base http.RoundTripper }

// Bound before either adapter decodes, including the Tether client's otherwise
// unbounded readAPIError. Never pass upstream error text/code to the client.
func (t boundedProviderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponse+1))
	if len(body) > maxProviderResponse {
		return nil, &providerResponseError{status: resp.StatusCode, oversized: true}
	}
	if err != nil {
		return nil, errors.New("provider response could not be read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ = json.Marshal(map[string]any{"error": map[string]string{"code": "upstream_error", "message": (&providerResponseError{status: resp.StatusCode}).Error()}})
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	return resp, nil
}

func safeProviderError(err error) error {
	if err == nil {
		return nil
	}
	var response *providerResponseError
	if errors.As(err, &response) {
		return response
	}
	var api *tether.APIError
	if errors.As(err, &api) {
		return &providerResponseError{status: api.StatusCode}
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("provider request cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("provider request timed out")
	}
	return errors.New("provider request failed")
}

func providerHTTPClient(base http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{Transport: boundedProviderTransport{base: base}, Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

// Preserve go-tether-client's TCP/HTTP/Unix transport choices while injecting a
// bounded transport through its supported WithHTTPClient option.
func tetherHTTPClient(addr string) (*http.Client, error) {
	if addr == "" {
		addr = tether.DefaultListenAddr
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	if strings.HasPrefix(addr, "unix:") {
		socket := strings.TrimPrefix(addr, "unix:")
		if strings.HasPrefix(socket, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, errors.New("Tether socket home unavailable")
			}
			socket = filepath.Join(home, strings.TrimPrefix(socket, "~/"))
		}
		base.Proxy = nil
		base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}
	}
	return providerHTTPClient(base, 5*time.Second), nil
}
