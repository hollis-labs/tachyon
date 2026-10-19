// Package agentclient provides remote Planner access. It never opens Tachyon's
// database; every invocation authenticates through the same API boundary.
package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
)

type Client struct {
	base      *url.URL
	tokenFile string
	http      *http.Client
}

func New(base, tokenFile string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("URL must be an origin without credentials, path, query or fragment")
	}
	loopback := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("HTTPS is required except for a loopback IP")
	}
	if tokenFile == "" {
		return nil, errors.New("a private token file is required")
	}
	return &Client{base: u, tokenFile: tokenFile, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) get(ctx context.Context, path string, input json.RawMessage) (json.RawMessage, error) {
	token, err := tokenfile.Read(c.tokenFile, tokenfile.Options{Validate: tokenfile.ValidHex})
	if err != nil {
		return nil, err
	}
	u := *c.base
	u.Path = dataverbs.AgentPrefix + path
	u.RawPath = ""
	if input != nil {
		q := url.Values{}
		q.Set("input", string(input))
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("could not create data request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Tachyon data request failed (connection or timeout)")
	}
	defer resp.Body.Close()
	const max = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, errors.New("could not read data response")
	}
	if len(raw) > max {
		return nil, errors.New("data response exceeds 32 MiB; request a smaller page")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Tachyon data request refused (HTTP %d)", resp.StatusCode)
	}
	if !json.Valid(raw) {
		return nil, errors.New("Tachyon returned invalid JSON")
	}
	return raw, nil
}
func (c *Client) Verbs(ctx context.Context) ([]dataverbs.Verb, error) {
	raw, err := c.get(ctx, "/providers", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Providers []struct {
			ID    string           `json:"id"`
			Verbs []dataverbs.Verb `json:"verbs"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	for _, p := range result.Providers {
		if p.ID == "tachyon" {
			return p.Verbs, nil
		}
	}
	return nil, errors.New("Tachyon provider not available")
}
func (c *Client) Invoke(ctx context.Context, verb string, input json.RawMessage) (json.RawMessage, error) {
	if strings.ContainsAny(verb, "/?#%") || verb == "" {
		return nil, errors.New("invalid verb")
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if !json.Valid(input) || len(input) > 16<<10 {
		return nil, errors.New("input must be valid JSON no larger than 16 KiB")
	}
	return c.get(ctx, "/providers/tachyon/verbs/"+verb, input)
}
