// Package upstreamauth gives Tachyon one way to present a bearer credential to an app it calls
// (Tether, Cerberus, GitHub). The credential is a secrets reference in the configuration
// (tachyon.upstreams.*.token), resolved by the secrets module on every request, so a rotated
// secret is picked up without a restart and the value is never part of config, logs or argv.
//
// With no credential configured nothing changes: the transport is returned as-is and requests go
// out unauthenticated.
package upstreamauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
)

// Token returns the current credential value. It never logs it.
type Token func(context.Context) (string, error)

// FromRef is a Token that resolves a secrets reference through r on every call; nil for an empty
// reference (no credential).
func FromRef(r *libsecrets.Resolver, ref string) Token {
	if strings.TrimSpace(ref) == "" {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		if r == nil {
			return "", errors.New("the secrets module is not started")
		}
		res, err := r.Resolve(ctx, ref)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(res.Secret.Reveal()), nil
	}
}

// Guard refuses to pair a credential with a base URL that would expose it: anything other than
// https, or http to a loopback host or unix socket. It is a no-op without a credential.
func Guard(baseURL string, authenticated bool) error {
	if !authenticated {
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

// Wrap returns a RoundTripper that adds "Authorization: Bearer <token>" to every request. A token
// that cannot be resolved, or is not a printable bearer value, fails the request before anything is
// sent. The request must target baseURL's origin, so a redirect can never carry the token
// elsewhere. Callers should still set CheckRedirect to http.ErrUseLastResponse.
func Wrap(base http.RoundTripper, baseURL string, token Token) (http.RoundTripper, error) {
	if token == nil {
		return base, nil
	}
	if err := Guard(baseURL, true); err != nil {
		return nil, err
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("upstream base URL is invalid")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, scheme: u.Scheme, host: u.Host, token: token}, nil
}

// ErrCredential identifies a local credential failure without exposing its value.
var ErrCredential = errors.New("upstream credential unavailable")

type transport struct {
	base         http.RoundTripper
	scheme, host string
	token        Token
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != t.scheme || req.URL.Host != t.host {
		return nil, errors.New("upstream: authenticated request changed origin")
	}
	token, err := t.token(req.Context())
	if err != nil {
		// The error names the reference's failure, never the value.
		return nil, fmt.Errorf("%w: %v", ErrCredential, err) //nolint:errorlint // the cause is described, not wrapped, so a value can never surface through errors.As
	}
	if !validToken(token) {
		return nil, fmt.Errorf("%w: the credential is not a printable bearer value", ErrCredential)
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

// validToken accepts the printable, space-free values a bearer header can carry.
func validToken(s string) bool {
	if s == "" || len(s) > 4096 {
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

// WithHeader returns a RoundTripper that sets one header on every request, for protocol markers an
// upstream requires (Tether's remote listener wants "Tether-Protocol: 1"). Place it under Wrap so it
// never sees the credential.
func WithHeader(base http.RoundTripper, key, value string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return headerTransport{base: base, key: key, value: value}
}

type headerTransport struct {
	base       http.RoundTripper
	key, value string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Header.Set(t.key, t.value)
	return t.base.RoundTrip(out)
}

func (t headerTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
