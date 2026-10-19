// Package objectsync imports provider observations into Tachyon's durable
// objects. Sources only read upstream; local edits never become remote writes.
package objectsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const maxItems = 10000
const maxSnapshotBytes = 16 << 20

type Progress struct {
	Pages      int    `json:"pages"`
	Items      int    `json:"items"`
	Checkpoint string `json:"checkpoint"`
}
type Snapshot struct {
	Observations []objects.Observation
	// Authoritative permits absence tombstones. Finishing pagination alone is
	// insufficient when the provider may hide records according to permissions.
	Authoritative bool
}
type Source interface {
	Name() string
	Scope() objects.SourceScope
	Scan(context.Context, time.Time, func(Progress) error) (Snapshot, error)
}

// RelationshipResolver enriches a complete inventory with exact local IDs
// before atomic reconciliation. Resolution never writes provider or local data.
type RelationshipResolver interface {
	Resolve(context.Context, Snapshot, *objects.Service) (Snapshot, error)
}

func sourceID(scope objects.SourceScope) string {
	b, _ := json.Marshal(scope)
	sum := sha256.Sum256(b)
	return "object-sync-" + hex.EncodeToString(sum[:16])
}

type remote struct {
	base   string
	client *http.Client
}

func newRemote(addr, tokenFile string, tether bool) (remote, error) {
	base := strings.TrimRight(strings.TrimSpace(addr), "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(base, "unix:"); ok {
		if socket == "" {
			return remote{}, errors.New("missing provider socket")
		}
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(base, "tcp:"); ok {
		base = "http://" + rest
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return remote{}, errors.New("invalid provider address")
	}
	var transport http.RoundTripper = tr
	if tether {
		transport = upstreamauth.WithHeader(transport, "Tether-Protocol", "1")
	}
	transport, err = upstreamauth.Wrap(transport, base, tokenFile)
	if err != nil {
		return remote{}, err
	}
	return remote{base: base, client: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Only fixed, non-secret diagnostics leave a provider adapter. Upstream bodies,
// request URLs and authentication headers never enter job logs or run history.
func (r remote) get(ctx context.Context, path, token string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, nil)
	if err != nil {
		return nil, errors.New("invalid provider request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, upstreamauth.ErrCredential) {
			return nil, &scanFailure{"provider credential unavailable"}
		}
		return nil, &scanFailure{"provider request failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &scanFailure{fmt.Sprintf("provider returned HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		return nil, &scanFailure{"provider response unreadable or exceeds 8 MiB"}
	}
	if err = json.Unmarshal(body, out); err != nil {
		return nil, &scanFailure{"invalid provider JSON"}
	}
	return resp.Header, nil
}

type collector struct {
	snapshot Snapshot
	scope    objects.SourceScope
	at       time.Time
	seen     map[string]bool
	bytes    int
}

func collect(scope objects.SourceScope, at time.Time) *collector {
	return &collector{scope: scope, at: at, seen: map[string]bool{}}
}
func (c *collector) add(id, version string, deleted bool, definition objects.Definition) error {
	if strings.TrimSpace(id) == "" || c.seen[id] {
		return errors.New("missing or duplicate provider identity")
	}
	b, err := json.Marshal(definition)
	if err != nil {
		return errors.New("invalid provider definition")
	}
	c.bytes += len(b)
	if len(c.snapshot.Observations) >= maxItems || c.bytes > maxSnapshotBytes {
		return errors.New("provider inventory exceeds bounded scan capacity")
	}
	c.seen[id] = true
	c.snapshot.Observations = append(c.snapshot.Observations, objects.Observation{Key: objects.SourceKey{SourceScope: c.scope, ExternalID: id}, ObservedAt: c.at, Version: version, Deleted: deleted, Definition: b})
	return nil
}
