package catalogindex

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

// FileRef is one listed catalog file. Fingerprint is whatever cheap change marker the listing
// carries (sha, etag, or size+mtime); empty means the listing has none.
type FileRef struct{ Path, Layer, Fingerprint string }

// Source is the Tether catalog as the index sees it. Read-only; the wire shapes behind it are
// UNVERIFIED against a live Tether (design section 2: files route + history), so parsing is tolerant.
type Source interface {
	Files(ctx context.Context) ([]FileRef, error)
	Read(ctx context.Context, path string) (string, error)
	// LastPlan is the newest plan id in the catalog history: a cheap token for "something was applied".
	LastPlan(ctx context.Context) (string, error)
}

const maxBody = 1 << 20

type httpSource struct {
	base   string
	client *http.Client
}

type tetherHeader struct{ http.RoundTripper }

func (t tetherHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return t.RoundTripper.RoundTrip(r)
}

// NewHTTPSource builds the Tether catalog client with the read-scope token. Like the Agents
// collector it refuses a credential over plain http to a non-loopback host.
func NewHTTPSource(addr, tokenFile string) (Source, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("Tether address is missing")
	}
	if err := upstreamauth.Guard(addr, tokenFile); err != nil {
		return nil, err
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 15 * time.Second
	tr.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	rt, err := upstreamauth.Wrap(tr, base, tokenFile)
	if err != nil {
		return nil, err
	}
	return &httpSource{base: base, client: &http.Client{Transport: tetherHeader{rt}, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *httpSource) get(ctx context.Context, path string, q url.Values, out any) error {
	u := s.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return errors.New("Tether unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Tether answered HTTP %d for %s", res.StatusCode, path)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return errors.New("Tether response unreadable or too large")
	}
	return json.Unmarshal(raw, out)
}

// list accepts a bare array or an object wrapping one under any of the usual keys.
func list(raw json.RawMessage, keys ...string) []json.RawMessage {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, k := range keys {
			if json.Unmarshal(obj[k], &arr) == nil && arr != nil {
				return arr
			}
		}
	}
	return nil
}

func (s *httpSource) Files(ctx context.Context) ([]FileRef, error) {
	var raw json.RawMessage
	if err := s.get(ctx, "/catalog/files", nil, &raw); err != nil {
		return nil, err
	}
	var out []FileRef
	for _, it := range list(raw, "files", "items", "entries") {
		var f struct {
			Path   string `json:"path"`
			Layer  string `json:"layer"`
			SHA    string `json:"sha256"`
			SHA2   string `json:"sha"`
			ETag   string `json:"etag"`
			Size   *int64 `json:"size"`
			MTime  string `json:"mtime"`
			ModAt  string `json:"modified_at"`
			Update string `json:"updated_at"`
		}
		if json.Unmarshal(it, &f) != nil || f.Path == "" {
			continue
		}
		fp := firstNonEmpty(f.SHA, f.SHA2, f.ETag)
		if fp == "" && f.Size != nil {
			if m := firstNonEmpty(f.MTime, f.ModAt, f.Update); m != "" {
				fp = fmt.Sprintf("%d@%s", *f.Size, m)
			}
		}
		out = append(out, FileRef{Path: f.Path, Layer: f.Layer, Fingerprint: fp})
	}
	return out, nil
}

func (s *httpSource) Read(ctx context.Context, path string) (string, error) {
	var raw json.RawMessage
	if err := s.get(ctx, "/catalog/files", url.Values{"path": {path}}, &raw); err != nil {
		return "", err
	}
	var f struct {
		Content *string `json:"content"`
		Body    *string `json:"body"`
	}
	if json.Unmarshal(raw, &f) == nil {
		if f.Content != nil {
			return *f.Content, nil
		}
		if f.Body != nil {
			return *f.Body, nil
		}
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str, nil
	}
	return "", errors.New("Tether file response has no content")
}

func (s *httpSource) LastPlan(ctx context.Context) (string, error) {
	var raw json.RawMessage
	if err := s.get(ctx, "/catalog/history", url.Values{"limit": {"1"}}, &raw); err != nil {
		return "", err
	}
	for _, it := range list(raw, "plans", "items", "entries", "history") {
		var p struct {
			PlanID string `json:"plan_id"`
			ID     string `json:"id"`
		}
		if json.Unmarshal(it, &p) == nil {
			if id := firstNonEmpty(p.PlanID, p.ID); id != "" {
				return clean(id, 80), nil
			}
		}
	}
	return "", nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
