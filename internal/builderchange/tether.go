// Package builderchange is the Builder's write path (B3): typed edits become Tether catalog plans, and an
// approved plan is applied. Tether stays the system of record and the approval step stays on the host (the
// Approver seam is where a login-validated confirm can replace the CLI later). Environment values are never
// read into the database, a diff, a log line or an error.
package builderchange

import (
	"bytes"
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

// File is one file of a plan bundle.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Delete  bool   `json:"delete,omitempty"`
}

type PlannedFile struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	Privileged bool   `json:"privileged"`
	NewSHA256  string `json:"new_sha256"`
}

// Plan is the part of Tether's plan the service uses.
type Plan struct {
	ID              string        `json:"plan_id"`
	State           string        `json:"state"`
	Digest          string        `json:"digest"`
	ExpiresAt       time.Time     `json:"expires_at"`
	Files           []PlannedFile `json:"files"`
	ValidationOK    bool          `json:"validation_ok"`
	ValidationError string        `json:"validation_error"`
	Privileged      []string      `json:"privileged"`
	RunningSessions []string      `json:"running_sessions"`
	Activation      string        `json:"activation"`
}

type Check struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Message string  `json:"message"`
	Remedy  *string `json:"remedy"`
	Entity  string  `json:"entity"`
}

type Report struct {
	OK      bool
	Results []Check
}

type ApplyResult struct {
	Applied         bool     `json:"applied"`
	BackupID        string   `json:"backup_id"`
	Activation      string   `json:"activation"`
	RolledBack      bool     `json:"rolled_back"`
	Reloaded        bool     `json:"reloaded"`
	ReloadError     string   `json:"reload_error"`
	RestartRequired []string `json:"restart_required"`

	// Set by the service itself in handoff mode, where the apply is seen from file shas rather than reported.
	detected     bool
	filesChanged int
}

// Tether is the catalog write API. The control token behind it holds catalog.write; it never holds the
// approval passphrase and never approves.
type Tether interface {
	ReadFile(ctx context.Context, path string) (content string, exists bool, err error)
	Validate(ctx context.Context, files []File) (Report, error)
	Plan(ctx context.Context, name string, files []File) (Plan, error)
	GetPlan(ctx context.Context, id string) (Plan, error)
	Apply(ctx context.Context, id string) (ApplyResult, error)
	// CreateSession and StartSession are the two legs of starting a catalog launch (needs the sessions scope).
	// The key makes a retried create return the same session.
	CreateSession(ctx context.Context, launchID, key string) (sessionID string, err error)
	StartSession(ctx context.Context, sessionID string) error
}

// TetherError carries Tether's HTTP status and a short code; the message is Tether's own text, clipped.
type TetherError struct {
	Status  int
	Code    string
	Message string
}

func (e *TetherError) Error() string { return fmt.Sprintf("tether %d %s", e.Status, e.Code) }

var errUnreachable = errors.New("Tether unreachable")

type httpTether struct {
	base   string
	client *http.Client
}

// NewTether builds the client with the control token file (catalog.write). Like the other Tether clients it
// refuses a credential over plain http to a non-loopback host.
func NewTether(addr, tokenFile string) (Tether, error) {
	if strings.TrimSpace(addr) == "" || tokenFile == "" {
		return nil, errors.New("Tether address or control token file is missing")
	}
	if err := upstreamauth.Guard(addr, tokenFile); err != nil {
		return nil, err
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
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
	return &httpTether{base: base, client: &http.Client{Transport: header{rt}, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type header struct{ http.RoundTripper }

func (h header) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return h.RoundTripper.RoundTrip(r)
}

func (t *httpTether) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base+path, body)
	if err != nil {
		return errUnreachable
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := t.client.Do(req)
	if err != nil {
		return errUnreachable
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return errUnreachable
	}
	if res.StatusCode != 200 && res.StatusCode != 201 {
		te := &TetherError{Status: res.StatusCode, Code: "error"}
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil {
			te.Code = e.Code
			te.Message = e.Message + e.Error
		}
		if te.Code == "" {
			te.Code = "error"
		}
		te.Message = clip(te.Message, 300)
		return te
	}
	return json.Unmarshal(raw, out)
}

func (t *httpTether) ReadFile(ctx context.Context, path string) (string, bool, error) {
	var out struct {
		Content *string `json:"content"`
	}
	err := t.do(ctx, "GET", "/catalog/files?path="+url.QueryEscape(path), nil, &out)
	var te *TetherError
	if errors.As(err, &te) && te.Status == 404 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if out.Content == nil {
		return "", false, errors.New("Tether file response has no content")
	}
	return *out.Content, true, nil
}

func (t *httpTether) Validate(ctx context.Context, files []File) (Report, error) {
	var out struct {
		OK      bool `json:"ok"`
		Results []struct {
			Name, Status, Message, Remedy, Entity string
		} `json:"results"`
	}
	if err := t.do(ctx, "POST", "/catalog/validate", map[string]any{"files": files}, &out); err != nil {
		return Report{}, err
	}
	r := Report{OK: out.OK}
	for _, c := range out.Results {
		ck := Check{Name: c.Name, Status: c.Status, Message: clip(c.Message, 300), Entity: c.Entity}
		if c.Remedy != "" {
			ck.Remedy = pointer(clip(c.Remedy, 300))
		}
		r.Results = append(r.Results, ck)
	}
	return r, nil
}

func (t *httpTether) Plan(ctx context.Context, name string, files []File) (Plan, error) {
	var p Plan
	return p, t.do(ctx, "POST", "/catalog/plans", map[string]any{"name": name, "files": files}, &p)
}

func (t *httpTether) GetPlan(ctx context.Context, id string) (Plan, error) {
	var p Plan
	return p, t.do(ctx, "GET", "/catalog/plans/"+url.PathEscape(id), nil, &p)
}

func (t *httpTether) Apply(ctx context.Context, id string) (ApplyResult, error) {
	var r ApplyResult
	return r, t.do(ctx, "POST", "/catalog/plans/"+url.PathEscape(id)+"/apply", nil, &r)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func pointer[T any](v T) *T { return &v }

func (t *httpTether) CreateSession(ctx context.Context, launchID, key string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := t.do(ctx, "POST", "/sessions", map[string]any{"launch": launchID, "idempotency_key": key}, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("Tether returned no session id")
	}
	return out.ID, nil
}

func (t *httpTether) StartSession(ctx context.Context, id string) error {
	var out json.RawMessage
	return t.do(ctx, "POST", "/sessions/"+url.PathEscape(id)+"/launch", nil, &out)
}
