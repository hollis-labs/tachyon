// Package accessstatus answers "which app grants is Tachyon missing?" in one place: per app it reports the
// base URL, whether a credential is configured, and a read and a write probe. Probes are harmless: reads are
// GETs, and a write probe is a request the app can only refuse or answer "no such thing" (a comment on a
// task that does not exist), so nothing is created. Authorization is inferred from HTTP 401/403 alone.
// Tokens never leave this package; only configured/missing/rejected is reported.
package accessstatus

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Path is the console route that serves Check results.
const Path = "/api/plugins/console/v1/access/apps"

const (
	CredConfigured = "configured"
	CredMissing    = "missing"
	CredRejected   = "rejected"
	ProbeOK        = "ok"
	ProbeDenied    = "denied"
	ProbeDown      = "unreachable"
	ProbeNone      = "not_probed"
)

// Probe is one request. Body non-empty makes it a POST with a JSON body.
type Probe struct {
	Method, Path, Body string
}

// Spec describes one app. Token is a literal bearer (Torque) and TokenFile a 0600 file; at most one is set.
// Write nil means the app has no harmless write probe; WriteNote says why.
type Spec struct {
	App, BaseURL, TokenFile, Token string
	Headers                        map[string]string
	Read                           *Probe
	Write                          *Probe
	WriteNote                      string
	Note                           string // shown when the app is not wired into Tachyon
}

// Result is the wire shape for one app.
type Result struct {
	App           string `json:"app"`
	BaseURL       string `json:"base_url"`
	Credential    string `json:"credential"`
	ReadProbe     string `json:"read_probe"`
	WriteProbe    string `json:"write_probe"`
	WriteNote     string `json:"write_note,omitempty"`
	Note          string `json:"note,omitempty"`
	LastCheckedMS int64  `json:"last_checked_ms"`
}

// Checker probes specs and caches the answer briefly so the Settings page cannot hammer the apps.
type Checker struct {
	Specs func() []Spec
	TTL   time.Duration
	now   func() time.Time

	mu     sync.Mutex
	at     time.Time
	cached []Result
}

func (c *Checker) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Check returns the cached results unless refresh is set or they are older than TTL.
func (c *Checker) Check(ctx context.Context, refresh bool) []Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	if !refresh && c.cached != nil && c.clock().Sub(c.at) < ttl {
		return c.cached
	}
	specs := c.Specs()
	out := make([]Result, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = run(ctx, s, c.clock())
		}()
	}
	wg.Wait()
	c.cached, c.at = out, c.clock()
	return out
}

func run(ctx context.Context, s Spec, at time.Time) Result {
	r := Result{App: s.App, BaseURL: display(s.BaseURL), Credential: CredMissing, ReadProbe: ProbeDown, WriteProbe: ProbeNone, WriteNote: s.WriteNote, Note: s.Note, LastCheckedMS: at.UnixMilli()}
	hasCred := s.Token != "" || s.TokenFile != ""
	if hasCred {
		r.Credential = CredConfigured
	}
	if s.Read == nil || s.BaseURL == "" {
		r.ReadProbe = ProbeNone
		return r
	}
	client, err := newClient(s)
	if err != nil {
		r.Note = firstNonEmpty(r.Note, "credential or address unusable: "+err.Error())
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	r.ReadProbe = fire(ctx, client, s, *s.Read)
	if s.Write != nil {
		r.WriteProbe = fire(ctx, client, s, *s.Write)
	}
	// A refused read means the token itself is wrong; a refused write alone is a missing scope (write_probe denied).
	if hasCred && r.ReadProbe == ProbeDenied {
		r.Credential = CredRejected
	}
	return r
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// display hides userinfo and query from a base URL.
func display(base string) string {
	if strings.HasPrefix(base, "unix:") {
		return base
	}
	b := strings.TrimPrefix(base, "tcp:")
	if !strings.Contains(b, "://") && b != "" {
		b = "http://" + b
	}
	u, err := url.Parse(b)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/")
}

func newClient(s Spec) (*http.Client, error) {
	base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 4 * time.Second
	if socket, ok := strings.CutPrefix(base, "unix:"); ok {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(base, "tcp:"); ok {
		base = "http://" + rest
	}
	var rt http.RoundTripper = tr
	if s.TokenFile != "" {
		w, err := upstreamauth.Wrap(rt, base, s.TokenFile)
		if err != nil {
			return nil, err
		}
		rt = w
	}
	return &http.Client{Transport: rt, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// target resolves the request URL against the (possibly rewritten) base.
func target(s Spec, p Probe) string {
	base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if strings.HasPrefix(base, "unix:") {
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(base, "tcp:"); ok {
		base = "http://" + rest
	}
	return base + p.Path
}

func fire(ctx context.Context, c *http.Client, s Spec, p Probe) string {
	method := p.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if p.Body != "" {
		body = strings.NewReader(p.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target(s, p), body)
	if err != nil {
		return ProbeDown
	}
	req.Header.Set("Accept", "application/json")
	if p.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	res, err := c.Do(req)
	if err != nil {
		return ProbeDown
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return ProbeDenied
	}
	return ProbeOK
}
