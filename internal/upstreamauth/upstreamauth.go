// Package upstreamauth gives Tachyon's plugins one way to present a bearer
// credential to an app they call (Torque, Tether). The token lives in a
// private 0600 file read through security/secrets/tokenfile, so it is never
// part of plugin config, logs or argv, and a rotated file is picked up on the
// next request without a restart.
//
// With no token file configured nothing changes: the transport is returned
// as-is and requests go out unauthenticated, as before.
package upstreamauth

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

// TokenFile resolves a token-file path: an explicit plugin config value wins,
// then the environment variable. Empty means "no credential".
func TokenFile(config map[string]string, configKey, envKey string) string {
	if v := strings.TrimSpace(config[configKey]); v != "" {
		return expandHome(v)
	}
	return expandHome(strings.TrimSpace(os.Getenv(envKey)))
}

// Guard refuses to pair a credential with a base URL that would expose it:
// anything other than https, or http to a loopback host or unix socket. It is
// a no-op when tokenFile is empty.
func Guard(baseURL, tokenFile string) error {
	if tokenFile == "" {
		return nil
	}
	if strings.HasPrefix(baseURL, "unix:") {
		return nil
	}
	if strings.HasPrefix(baseURL, "tcp:") {
		baseURL = "http://" + strings.TrimPrefix(baseURL, "tcp:")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("upstream base URL is invalid for an authenticated client")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return errors.New("refusing to send a bearer token over plain http to a non-loopback host; use https")
	}
	return errors.New("upstream base URL must be http(s) for an authenticated client")
}

// Wrap returns a RoundTripper that adds "Authorization: Bearer <token>" to
// every request. The token is re-read from tokenFile when it changes. A file
// that is missing, not mode 0600 or otherwise untrusted fails the request
// before anything is sent. The request must target baseURL's origin, so a
// redirect can never carry the token elsewhere. Callers should still set
// CheckRedirect to http.ErrUseLastResponse.
func Wrap(base http.RoundTripper, baseURL, tokenFile string) (http.RoundTripper, error) {
	if tokenFile == "" {
		return base, nil
	}
	if err := Guard(baseURL, tokenFile); err != nil {
		return nil, err
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("upstream base URL is invalid")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{
		base:   base,
		scheme: u.Scheme,
		host:   u.Host,
		watch:  tokenfile.NewWatcher(tokenFile, tokenfile.Options{Validate: validToken}, nil),
	}, nil
}

type transport struct {
	base         http.RoundTripper
	scheme, host string
	watch        *tokenfile.Watcher
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != t.scheme || req.URL.Host != t.host {
		return nil, errors.New("upstream: authenticated request changed origin")
	}
	token, err := t.watch.Token()
	if err != nil {
		// The error names the path and the rule, never the token.
		return nil, fmt.Errorf("upstream credential unavailable: %w", err)
	}
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(out)
}

func (t *transport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// validToken accepts the printable, space-free values a bearer header can
// carry, with the 256-byte cap go-tether-client applies.
func validToken(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}
