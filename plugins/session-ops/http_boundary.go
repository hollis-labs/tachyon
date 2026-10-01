package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tether "github.com/hollis-labs/go-tether-client"
)

const maxTetherResponseBytes = 2 << 20

var errResponseTooLarge = errors.New("Tether response exceeded size limit")
var errInactiveSession = errors.New("session is not running")

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Never read provider error bodies, including structured error codes/messages.
	// The SDK's readAPIError otherwise reads the entire body and returns it verbatim.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &tether.APIError{StatusCode: resp.StatusCode, Code: "upstream_status"}
	}
	if resp.ContentLength > maxTetherResponseBytes {
		return nil, errResponseTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTetherResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxTetherResponseBytes {
		return nil, errResponseTooLarge
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

// The SDK hides its transport. Resolve its supported Unix address forms before
// injecting the boundary, retaining direct Unix dialing and normal TCP/TLS.
func sessionHTTPClient(addr string) (string, *http.Client, error) {
	if addr == "" {
		addr = tether.DefaultListenAddr
	}
	if strings.HasPrefix(addr, "unix:~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, errors.New("cannot resolve Tether socket")
		}
		addr = "unix:" + filepath.Join(home, strings.TrimPrefix(addr, "unix:~/"))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	if strings.HasPrefix(addr, "unix:") {
		socket := strings.TrimPrefix(addr, "unix:")
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, "unix", socket)
		}
	}
	return addr, &http.Client{Transport: boundedTransport{base: transport}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
