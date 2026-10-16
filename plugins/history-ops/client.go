package main

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

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const (
	// 200 turns of at most 20 KiB each is the largest page the API serves.
	maxResponseBytes = 8 << 20
	readTimeout      = 15 * time.Second
	defaultURL       = "http://127.0.0.1:8091"
)

var errResponseTooLarge = errors.New("fragments response exceeded size limit")

// upstreamError is a Fragments Engine answer with a non-2xx status. The body is
// never kept: transcript routes answer with short plain text, and error text is
// not shown to callers.
type upstreamError struct{ status int }

func (e *upstreamError) Error() string { return fmt.Sprintf("fragments returned HTTP %d", e.status) }

// fragmentsAPI is a read-only JSON client for Fragments Engine's owner-only
// transcript routes (/v1/transcripts/*).
type fragmentsAPI struct {
	baseURL string
	http    *http.Client
}

func newFragmentsAPI(baseURL, tokenFile string) (*fragmentsAPI, error) {
	if baseURL == "" {
		baseURL = defaultURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("fragments_url must be an http(s) origin without credentials, path, query or fragment")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	rt, err := upstreamauth.Wrap(transport, baseURL, tokenFile)
	if err != nil {
		return nil, err
	}
	return &fragmentsAPI{baseURL: baseURL, http: &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// get returns the raw JSON body of a 2xx answer.
func (a *fragmentsAPI) get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	target := a.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &upstreamError{status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errResponseTooLarge
	}
	if !json.Valid(data) {
		return nil, errors.New("fragments returned a malformed response")
	}
	return json.RawMessage(data), nil
}
