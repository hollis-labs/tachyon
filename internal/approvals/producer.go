package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const ProducerPrefix = "/api/approvals/v1"

type ProducerPolicy struct {
	ApplicationID  string   `json:"application_id"`
	MinimumClass   string   `json:"minimum_class"`
	HandoffOrigins []string `json:"handoff_origins,omitempty"`
}
type ProducerConfig struct {
	Issuer           string `json:"issuer"`
	Audience         string `json:"audience"`
	JWKSURL          string `json:"jwks_url,omitempty"`
	BootstrapKeyFile string `json:"bootstrap_key_file,omitempty"`
	// DenyListURL is the issuer's revocation list (Tether's /auth/denylist,
	// public jti/sub/sid). With it a revoked producer token, including a
	// long-lived kind=service one, is rejected within one poll interval after a
	// successful poll; without it a producer token cannot be revoked short of
	// rotating the issuer key or removing the subject mapping. While it is
	// configured, the producer API answers 503 until the first successful poll.
	DenyListURL string                    `json:"deny_list_url,omitempty"`
	Producers   map[string]ProducerPolicy `json:"producers"` // exact JWT subject -> application
}

// producerGeneration is everything a request needs to authenticate and
// authorize a producer, immutable once built. A request loads one generation
// and uses only it, so a reload that lands mid-request can never pair one
// generation's verifier with another's subject mapping or revocation list.
type producerGeneration struct {
	cfg      ProducerConfig
	verifier auth.Verifier
	feed     *revocationFeed    // nil when no deny_list_url is configured
	stop     context.CancelFunc // ends this generation's JWKS refresh loop
}

type Producer struct {
	Service *hitl.Service
	ctx     context.Context
	mu      sync.Mutex // serialises Reload
	gen     atomic.Pointer[producerGeneration]
	// afterVerify, only set by tests, runs between token verification and the
	// subject lookup to land a reload exactly there.
	afterVerify func()
}

func (p *Producer) current() *producerGeneration { return p.gen.Load() }

// denyListInterval is how often the deny-list is polled (a variable so tests
// can shorten it).
var denyListInterval = 30 * time.Second

// revocationFeed keeps one DenyList fed from the issuer. Ready turns true after
// the first successful poll: until then the producer cannot know what has been
// revoked and refuses to serve.
type revocationFeed struct {
	url    string
	list   *auth.DenyList
	poller *auth.DenyListPoller
	ready  atomic.Bool
	stop   context.CancelFunc
}

func newRevocationFeed(rawURL string) *revocationFeed {
	list := auth.NewDenyList()
	return &revocationFeed{url: rawURL, list: list, poller: &auth.DenyListPoller{
		URL: rawURL, List: list, Interval: denyListInterval,
		Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}}
}

func (f *revocationFeed) Ready() bool { return f.ready.Load() }

// host is the only part of the deny-list URL that is ever logged: the path may
// be deployment-specific and the validated URL never carries userinfo or a
// query, but a log line should not depend on that.
func (f *revocationFeed) host() string {
	if u, err := url.Parse(f.url); err == nil {
		return u.Host
	}
	return "unparsable"
}

// run polls until ctx is done: it retries (2s doubling to 30s) until the first
// poll succeeds, marks the feed ready, then polls every interval. A failed
// poll afterwards keeps the last list in force.
func (f *revocationFeed) run(ctx context.Context) {
	wait := 2 * time.Second
	for attempt := 1; !f.ready.Load(); attempt++ {
		if err := f.poller.Poll(ctx); err == nil {
			f.ready.Store(true)
			slog.Info("approval producer revocation list ready", "host", f.host(), "attempts", attempt)
			break
		}
		// The poll error is not logged: it carries the request URL.
		slog.Warn("approval producer is refusing requests until the first revocation-list poll succeeds", "host", f.host(), "attempt", attempt, "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
	f.poller.Run(ctx)
}

func LoadProducerConfig(path string) (ProducerConfig, error) {
	var cfg ProducerConfig
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if len(raw) > 64<<10 {
		return cfg, errors.New("approval producer config too large")
	}
	if err = strictJSON(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Issuer == "" || cfg.Audience != "tachyon" || len(cfg.Producers) == 0 {
		return cfg, errors.New("approval producers require issuer, audience tachyon and exact subject mappings")
	}
	if cfg.JWKSURL != "" {
		u, err := url.Parse(cfg.JWKSURL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || upstreamauth.Guard(cfg.JWKSURL, "public-jwks") != nil {
			return cfg, errors.New("invalid approval JWKS URL")
		}
	}
	if cfg.DenyListURL != "" {
		u, err := url.Parse(cfg.DenyListURL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || upstreamauth.Guard(cfg.DenyListURL, "public-denylist") != nil {
			return cfg, errors.New("invalid approval deny-list URL")
		}
	}
	seen := map[string]bool{}
	for subject, p := range cfg.Producers {
		if strings.TrimSpace(subject) == "" || p.ApplicationID == "" || len(p.ApplicationID) > 128 || seen[p.ApplicationID] || tier(p.MinimumClass) == 0 {
			return cfg, errors.New("approval producers require distinct application IDs and valid minimum classes")
		}
		if p.ApplicationID == "cerberus" && p.MinimumClass != "high" {
			return cfg, errors.New("Cerberus requests require provider handoff until its proof verifier is available")
		}
		seen[p.ApplicationID] = true
		for _, origin := range p.HandoffOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return cfg, errors.New("handoff origins must be exact HTTPS origins")
			}
		}
	}
	return cfg, nil
}
func NewProducer(ctx context.Context, service *hitl.Service, cfg ProducerConfig) (*Producer, error) {
	p := &Producer{Service: service, ctx: ctx}
	if cfg.Issuer == "" {
		return p, nil
	} // disabled, never a permissive verifier
	g, err := p.build(cfg, nil, false)
	if err != nil {
		return nil, err
	}
	p.gen.Store(g)
	return p, nil
}

// cloneProducerConfig deep-copies what a generation serves from, so a caller
// that keeps and edits its ProducerConfig (the map, a policy's origin list)
// cannot change a running generation.
func cloneProducerConfig(c ProducerConfig) ProducerConfig {
	producers := make(map[string]ProducerPolicy, len(c.Producers))
	for subject, p := range c.Producers {
		p.HandoffOrigins = append([]string(nil), p.HandoffOrigins...)
		producers[subject] = p
	}
	c.Producers = producers
	return c
}

var errReload = errors.New("approval producer reload")

// build makes a generation. prev, when set, lets it keep the previous deny-list
// feed: while deny_list_url is unchanged the same list and poller carry on, so
// a reload never forgets a revocation. A reload also proves its trust sources
// before anything is swapped: a new deny-list endpoint must answer one poll,
// and a verifier with no bootstrap key must fetch the JWKS once. At startup
// both stay lazy (the producer API answers 503 until the first poll).
func (p *Producer) build(cfg ProducerConfig, prev *producerGeneration, reload bool) (*producerGeneration, error) {
	jcfg := auth.JWTConfig{Issuer: cfg.Issuer, Audience: cfg.Audience, JWKSURL: cfg.JWKSURL, BootstrapKeyFile: cfg.BootstrapKeyFile, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	g := &producerGeneration{cfg: cloneProducerConfig(cfg)}
	cfg = g.cfg
	var newFeed *revocationFeed
	if cfg.DenyListURL != "" {
		if prev != nil && prev.feed != nil && prev.feed.url == cfg.DenyListURL {
			g.feed = prev.feed
		} else {
			newFeed = newRevocationFeed(cfg.DenyListURL)
			g.feed = newFeed
		}
		jcfg.DenyList = g.feed.list
	}
	v, err := auth.NewJWTVerifier(jcfg)
	if err != nil {
		return nil, err
	}
	g.verifier = v
	if reload {
		ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
		defer cancel()
		if newFeed != nil {
			if err := newFeed.poller.Poll(ctx); err != nil {
				return nil, fmt.Errorf("%w: the new deny list could not be fetched", errReload)
			}
			newFeed.ready.Store(true)
		}
		if cfg.BootstrapKeyFile == "" && cfg.JWKSURL != "" {
			if err := v.Refresh(ctx); err != nil {
				return nil, fmt.Errorf("%w: the JWKS could not be fetched", errReload)
			}
		}
	}
	vctx, stop := context.WithCancel(p.ctx)
	g.stop = stop
	go v.Run(vctx)
	if newFeed != nil {
		fctx, fstop := context.WithCancel(p.ctx)
		newFeed.stop = fstop
		go newFeed.run(fctx)
	}
	return g, nil
}

// Reload swaps in a new producer configuration. It is all-or-nothing: on any
// error the previous generation keeps serving. Requests already running finish
// on the generation they started with. It cannot turn the producer API off or
// on; that is a restart.
func (p *Producer) Reload(cfg ProducerConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.gen.Load()
	if prev == nil || cfg.Issuer == "" {
		return fmt.Errorf("%w: the producer API is not enabled; restart to change that", errReload)
	}
	g, err := p.build(cfg, prev, true)
	if err != nil {
		return err
	}
	p.gen.Store(g)
	prev.stop()
	if prev.feed != nil && prev.feed != g.feed && prev.feed.stop != nil {
		prev.feed.stop() // the deny list was moved or removed on purpose
	}
	return nil
}

// Watch reloads the producer config from path when its content changes, on
// SIGHUP, or via the returned Reloader's Trigger. The startup load is the
// caller's; a bad edit is logged and the last good configuration stays.
func (p *Producer) Watch(ctx context.Context, path string, interval time.Duration, lg *slog.Logger) error {
	r := &auth.Reloader{Path: path, Interval: interval, Logger: lg, Load: func(context.Context) error {
		cfg, err := LoadProducerConfig(path)
		if err != nil {
			return err
		}
		return p.Reload(cfg)
	}}
	return r.Run(ctx)
}
func tier(class string) int {
	switch class {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return 0
}

var operationDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func extraString(req hitl.EnqueueRequest, key string) string {
	var value string
	_ = json.Unmarshal(req.Extra[key], &value)
	return value
}
func validateInboxRequest(req hitl.EnqueueRequest, policy ProducerPolicy) error {
	if req.Kind != "approval" && req.Kind != "attention" {
		return fmt.Errorf("%w: supported kinds are approval and attention", hitl.ErrInvalidRequest)
	}
	class := extraString(req, "class")
	if req.Source.ApplicationID == "cerberus" && class != "high" {
		return fmt.Errorf("%w: Cerberus requests are provider-handoff only", hitl.ErrInvalidRequest)
	}
	if tier(class) == 0 || tier(class) < tier(policy.MinimumClass) || !operationDigest.MatchString(extraString(req, "op_digest")) {
		return fmt.Errorf("%w: class must meet producer policy and op_digest must be sha256:<64 lowercase hex>", hitl.ErrInvalidRequest)
	}
	for _, key := range []string{"title", "summary", "request"} {
		value := extraString(req, key)
		if strings.TrimSpace(value) == "" || len(value) > 32<<10 {
			return fmt.Errorf("%w: bounded title, summary and request strings are required", hitl.ErrInvalidRequest)
		}
	}
	handoff := extraString(req, "handoff_url")
	if class == "high" && handoff == "" {
		return fmt.Errorf("%w: high tier requires a provider handoff URL", hitl.ErrInvalidRequest)
	}
	if handoff != "" {
		u, err := url.Parse(handoff)
		allowed := false
		if err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
			for _, origin := range policy.HandoffOrigins {
				if origin == u.Scheme+"://"+u.Host {
					allowed = true
				}
			}
		}
		if !allowed {
			return fmt.Errorf("%w: handoff URL must use an allowed provider HTTPS origin", hitl.ErrInvalidRequest)
		}
	}
	return nil
}
func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid JSON command", hitl.ErrInvalidRequest)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%w: exactly one JSON command is required", hitl.ErrInvalidRequest)
	}
	return nil
}
func commandBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return nil, fmt.Errorf("%w: use application/json", hitl.ErrInvalidRequest)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized command", hitl.ErrInvalidRequest)
	}
	return raw, nil
}
func writeValue(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func writeHITLError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, hitl.ErrInvalidRequest):
		status = 400
	case errors.Is(err, hitl.ErrNotFound):
		status = 404
	case errors.Is(err, hitl.ErrUnauthorized):
		status = 403
	case errors.Is(err, hitl.ErrStaleRevision), errors.Is(err, hitl.ErrIdempotencyConflict), errors.Is(err, hitl.ErrTerminalConflict):
		status = 409
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 408
	}
	if status == 500 {
		fail(w, status, "unavailable", "Approval storage is unavailable; fetch the retained item before retrying.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(hitl.EncodeError(err))
}
func (p *Producer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	operation := strings.TrimPrefix(r.URL.Path, ProducerPrefix+"/")
	if operation != "enqueue" && operation != "get" && operation != "await" && operation != "withdraw" {
		fail(w, 404, "not_found", "Unknown producer operation.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method_not_allowed", "Use POST.")
		return
	}
	// Producer bearer credentials are never accepted as browser decision authority.
	g := p.current()
	if g == nil {
		fail(w, 503, "producer_auth_unconfigured", "Configure the existing Tether JWT trust and producer identities.")
		return
	}
	// With a deny-list configured, the producer does not know what has been
	// revoked until the first poll succeeds: refuse rather than accept a token
	// that may already be revoked.
	if g.feed != nil && !g.feed.Ready() {
		fail(w, 503, "producer_revocation_unavailable", "The issuer's revocation list has not been fetched yet; retry shortly.")
		return
	}
	token, state := auth.ParseBearer(r.Header.Values("Authorization"))
	if state != "" {
		fail(w, 401, "unauthenticated", "A producer bearer token is required.")
		return
	}
	principal, err := g.verifier.Verify(r.Context(), token)
	if err != nil || !principal.Verified {
		status := 401
		if err != nil && !errors.Is(err, auth.ErrInvalidToken) {
			status = 503
		}
		fail(w, status, "producer_auth_failed", "Producer authentication could not be verified.")
		return
	}
	if p.afterVerify != nil {
		p.afterVerify()
	}
	policy, ok := g.cfg.Producers[principal.ID]
	scope := "tachyon.approvals_read"
	if operation == "enqueue" {
		scope = "tachyon.approvals_enqueue"
	}
	if operation == "withdraw" {
		scope = "tachyon.approvals_withdraw"
	}
	if !ok || principal.Audience != "tachyon" || !principal.HasScope(scope) {
		fail(w, 403, "forbidden", "Producer identity or required scope is not authorized.")
		return
	}
	if !principal.ExpiresAt.IsZero() {
		ctx, cancel := context.WithDeadline(r.Context(), principal.ExpiresAt)
		defer cancel()
		r = r.WithContext(ctx)
	}
	raw, err := commandBody(w, r)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	var result any
	callerOK := func(c hitl.CallerAssertion) bool {
		return c.ApplicationID == policy.ApplicationID && (c.PrincipalRef == "" || c.PrincipalRef == principal.ID)
	}
	switch operation {
	case "enqueue":
		var req hitl.EnqueueRequest
		if err = json.Unmarshal(raw, &req); err != nil {
			err = hitl.ErrInvalidRequest
		} else {
			if req.Source.ApplicationID != policy.ApplicationID {
				err = hitl.ErrUnauthorized
			} else if err = validateInboxRequest(req, policy); err == nil {
				result, err = p.Service.Enqueue(r.Context(), req)
			}
		}
	case "get":
		var cmd hitl.GetCommand
		cmd, err = hitl.DecodeGetCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Get(r.Context(), cmd)
			}
		}
	case "await":
		var cmd hitl.AwaitCommand
		cmd, err = hitl.DecodeAwaitCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Await(r.Context(), cmd)
			}
		}
	case "withdraw":
		var cmd hitl.WithdrawCommand
		cmd, err = hitl.DecodeWithdrawCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Withdraw(r.Context(), cmd)
			}
		}
	}
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeValue(w, result)
}
