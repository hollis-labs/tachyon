// Package pluginkit holds helpers shared by Tachyon's first-party plugins.
package pluginkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

// MaxUpstreamBody bounds a buffered upstream response. It matches app-host's
// request bound; streams use the host's SSE relay instead.
const MaxUpstreamBody = 8 << 20

// Route maps a plugin-relative path prefix onto an upstream base URL.
// A request to /api/plugins/{id}{Prefix}{rest} is forwarded to
// {Upstream}{Target}{rest}.
type Route struct {
	Prefix   string
	Upstream string
	Target   string
	// Bearer is sent as Authorization when non-empty.
	Bearer string
	// Headers are fixed request headers added for this upstream.
	Headers map[string]string
}

// Proxy is a plugin-sdk HTTPHandler that forwards matching routes upstream.
// Credentials live in the plugin's explicit environment, never the browser.
type Proxy struct {
	PluginID string
	Routes   []Route
	Client   *http.Client
}

// NewProxy builds a proxy with a bounded client.
func NewProxy(id string, routes ...Route) *Proxy {
	return &Proxy{PluginID: id, Routes: routes, Client: &http.Client{Timeout: 60 * time.Second}}
}

// HTTPHandle implements subprocess.HTTPHandler.
func (p *Proxy) HTTPHandle(ctx context.Context, req subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
	rel, ok := strings.CutPrefix(req.Path, "/api/plugins/"+p.PluginID)
	if !ok {
		return jsonError(http.StatusNotFound, "not_found", "unknown plugin path"), nil
	}
	for _, rt := range p.Routes {
		rest, ok := strings.CutPrefix(rel, rt.Prefix)
		if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
			continue
		}
		return p.forward(ctx, rt, rest, req), nil
	}
	return jsonError(http.StatusNotFound, "not_found", "no route for "+rel), nil
}

func (p *Proxy) forward(ctx context.Context, rt Route, rest string, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	target := strings.TrimRight(rt.Upstream, "/") + rt.Target + rest
	if q := query(req); q != "" {
		target += "?" + q
	}
	out, err := http.NewRequestWithContext(ctx, req.Method, target, bytes.NewReader(req.Body))
	if err != nil {
		return jsonError(http.StatusBadRequest, "invalid", "bad upstream request")
	}
	if ct := req.Headers["Content-Type"]; ct != "" {
		out.Header.Set("Content-Type", ct)
	}
	out.Header.Set("Accept", "application/json")
	for k, v := range rt.Headers {
		out.Header.Set(k, v)
	}
	if rt.Bearer != "" {
		out.Header.Set("Authorization", "Bearer "+rt.Bearer)
	}
	resp, err := p.Client.Do(out)
	if err != nil {
		return jsonError(http.StatusBadGateway, "unavailable", "upstream unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxUpstreamBody+1))
	if err != nil || len(body) > MaxUpstreamBody {
		return jsonError(http.StatusBadGateway, "unavailable", "upstream response unreadable or oversized")
	}
	headers := map[string]string{}
	for _, h := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	return subprocess.HTTPResponse{Status: resp.StatusCode, Headers: headers, Body: body}
}

// query prefers the exact encoded query; hosts that forward only the decoded
// map lose repeated values, so the map is a fallback.
func query(req subprocess.HTTPRequest) string {
	if req.RawQuery != "" {
		return req.RawQuery
	}
	v := url.Values{}
	for k, val := range req.Query {
		v.Set(k, val)
	}
	return v.Encode()
}

func jsonError(status int, code, msg string) subprocess.HTTPResponse {
	body := fmt.Sprintf(`{"error":%q,"code":%q}`, msg, code)
	return subprocess.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(body)}
}

// Env reads a variable from the plugin's explicit environment.
func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
