// Package cerbcontrol drives Cerberus resources directly over its daemon API
// (unix socket or http), the dev-open alternative to Approve-and-Run.
//
// Mutations always send {"acknowledged":true}: the caller is Tachyon's
// operator-guarded route, which records every call. Destructive verbs (remove)
// are flagged in the record but not blocked.
package cerbcontrol

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Control modes (TACHYON_CERBERUS_CONTROL).
const (
	ModeDirect        = "direct"
	ModeApproveAndRun = "approve-and-run"
)

// ParseMode maps the setting to a mode; empty and unknown values give direct (dev default).
func ParseMode(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), ModeApproveAndRun) {
		return ModeApproveAndRun
	}
	return ModeDirect
}

// Verb is a lifecycle verb Tachyon exposes.
type Verb struct {
	Name        string `json:"name"`
	Route       string `json:"route"` // Cerberus resource route it calls
	Destructive bool   `json:"destructive,omitempty"`
	Note        string `json:"note,omitempty"`
}

// Verbs lists what Cerberus offers. Cerberus has no separate start or restart
// route: reload kickstarts the resource through its runtime backend, which
// starts a stopped one and restarts a running one.
func Verbs() []Verb {
	return []Verb{
		{Name: "start", Route: "reload", Note: "kickstart: starts a stopped resource"},
		{Name: "restart", Route: "reload", Note: "kickstart: restart without rebuild"},
		{Name: "reload", Route: "reload", Note: "restart without rebuild"},
		{Name: "stop", Route: "stop"},
		{Name: "deploy", Route: "deploy", Note: "build then activate"},
		{Name: "apply", Route: "apply", Note: "activate an already-built resource"},
		{Name: "sync", Route: "sync"},
		{Name: "remove", Route: "remove", Destructive: true, Note: "uninstalls runtime state"},
	}
}

// VerbByName finds a verb.
func VerbByName(name string) (Verb, bool) {
	for _, v := range Verbs() {
		if v.Name == name {
			return v, true
		}
	}
	return Verb{}, false
}

// ResourceID is the accepted resource id shape (same as the Approve-and-Run registry).
var ResourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ErrInvalid marks a caller mistake (bad id or verb).
var ErrInvalid = errors.New("invalid cerberus request")

// StatusError is a non-2xx answer from Cerberus.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string { return fmt.Sprintf("cerberus returned HTTP %d", e.Status) }

// Client talks to the Cerberus daemon.
type Client struct {
	base string
	http *http.Client
}

// New builds a client for addr (unix:/path, tcp:host:port or http(s)://...). tokenFile is optional.
func New(addr, tokenFile string) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(addr), "/")
	if base == "" {
		return nil, errors.New("CERBERUS_ADDR is not set")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Minute // deploy builds
	if sock, ok := strings.CutPrefix(base, "unix:"); ok {
		if sock == "" {
			return nil, errors.New("missing cerberus socket")
		}
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(base, "tcp:"); ok {
		base = "http://" + rest
	}
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid cerberus address")
	}
	rt, err := upstreamauth.Wrap(tr, base, tokenFile)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, http: &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("cerberus unreachable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &StatusError{Status: res.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if !json.Valid(raw) {
		raw, _ = json.Marshal(string(raw))
	}
	return raw, nil
}

func checkID(id string) error {
	if !ResourceID.MatchString(id) {
		return fmt.Errorf("%w: resource id must match %s", ErrInvalid, ResourceID)
	}
	return nil
}

// List returns resources, optionally filtered by project, connector or tag.
func (c *Client) List(ctx context.Context, project, connector, tag string) (json.RawMessage, error) {
	q := url.Values{}
	for k, v := range map[string]string{"project_id": project, "connector": connector, "tag": tag} {
		if v != "" {
			q.Set(k, v)
		}
	}
	p := "/resources"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return c.do(ctx, http.MethodGet, p, nil, 20*time.Second)
}

// Read fetches a read-only view of one resource: status, inspect or doctor.
func (c *Client) Read(ctx context.Context, id, view string) (json.RawMessage, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	switch view {
	case "status", "inspect", "doctor":
	default:
		return nil, fmt.Errorf("%w: unknown view %q", ErrInvalid, view)
	}
	return c.do(ctx, http.MethodGet, "/resources/"+id+"/"+view, nil, 30*time.Second)
}

// Logs returns the tail of a resource log.
func (c *Client) Logs(ctx context.Context, id string, lines int, stream string) (json.RawMessage, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	q := url.Values{}
	if lines > 0 {
		if lines > 2000 {
			lines = 2000
		}
		q.Set("lines", strconv.Itoa(lines))
	}
	if stream != "" {
		q.Set("stream", stream)
	}
	p := "/resources/" + id + "/logs"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return c.do(ctx, http.MethodGet, p, nil, 30*time.Second)
}

// Health returns daemon health, or one resource when id is set.
func (c *Client) Health(ctx context.Context, id string) (json.RawMessage, error) {
	p := "/health"
	if id != "" {
		if err := checkID(id); err != nil {
			return nil, err
		}
		p += "?resource_id=" + url.QueryEscape(id)
	}
	return c.do(ctx, http.MethodGet, p, nil, 20*time.Second)
}

// Mutate runs a lifecycle verb with {"acknowledged":true}.
func (c *Client) Mutate(ctx context.Context, verb Verb, id string) (json.RawMessage, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/resources/"+id+"/"+verb.Route, []byte(`{"acknowledged":true}`), 10*time.Minute)
}

// Op is one recorded control call.
type Op struct {
	At          time.Time `json:"at"`
	Mode        string    `json:"mode"`
	Verb        string    `json:"verb"`
	Resource    string    `json:"resource_id"`
	Actor       string    `json:"actor,omitempty"`
	Destructive bool      `json:"destructive,omitempty"`
	Outcome     string    `json:"outcome"`
	Status      int       `json:"status,omitempty"`
}

// Journal keeps the most recent control calls in memory.
type Journal struct {
	mu  sync.Mutex
	ops []Op
}

const journalMax = 200

// Add records an op.
func (j *Journal) Add(op Op) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ops = append(j.ops, op)
	if len(j.ops) > journalMax {
		j.ops = j.ops[len(j.ops)-journalMax:]
	}
}

// Recent returns ops newest first.
func (j *Journal) Recent() []Op {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Op, len(j.ops))
	for i, op := range j.ops {
		out[len(j.ops)-1-i] = op
	}
	return out
}
