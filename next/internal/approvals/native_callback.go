package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// NativeAuthorizer sends only owner-attested proposals to the configured host.
// The credential resolver must return a separately Gatecrasher-issued service
// credential. Redirects are refused so that it cannot cross a host boundary.
func NativeAuthorizer(endpoint string, loopbackHTTP bool, credential func(context.Context) (string, error)) func(context.Context, ProposedResponse) (ResponseAuthorization, error) {
	u, err := url.Parse(endpoint)
	valid := err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/api/gate/v1/authorize-response"
	if valid && u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		valid = u.Scheme == "http" && loopbackHTTP && ip != nil && ip.IsLoopback()
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, p ProposedResponse) (ResponseAuthorization, error) {
		unavailable := &hitl.CodedError{Code: "unavailable", Message: "Native response authorization is unavailable."}
		if !valid || credential == nil {
			return ResponseAuthorization{}, unavailable
		}
		token, err := credential(ctx)
		if err != nil || !strings.HasPrefix(token, "key_") || strings.ContainsAny(token, " \r\n\t") {
			return ResponseAuthorization{}, unavailable
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return ResponseAuthorization{}, unavailable
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
		if err != nil {
			return ResponseAuthorization{}, unavailable
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			return ResponseAuthorization{}, unavailable
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 {
			return ResponseAuthorization{}, unavailable
		}
		if res.StatusCode == http.StatusForbidden {
			return ResponseAuthorization{}, hitl.ErrUnauthorized
		}
		if res.StatusCode != http.StatusOK {
			return ResponseAuthorization{}, unavailable
		}
		var out ResponseAuthorization
		if err = strictJSON(body, &out); err != nil {
			return ResponseAuthorization{}, unavailable
		}
		return out, nil
	}
}
