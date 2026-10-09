package hitl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	core "github.com/hollis-labs/substrate/mesh/hitl"
	"github.com/hollis-labs/tachyon/internal/contract"
)

const PendingCapacity = 256         // Reject new correlations rather than evict existing operations.
const MaxPayload = 32 << 10         // Maximum canonical payload retained per operation.
const PendingTTL = 10 * time.Minute // Correlation never survives host restart or this TTL.
const MaxStatusWait = 25000         // One operation holds at most one bounded Tangent poll.
var errBindingInvalid = errors.New("HITL outcome binding invalid")
var errTerminalInvalid = errors.New("HITL terminal invalid")
var ErrStatusBusy = errors.New("HITL operation poll busy")
var ErrCorrelation = errors.New("HITL correlation unavailable")

type pending struct {
	kind     string
	poll     chan struct{}
	snapshot []byte
	handle   EnqueueHandle
	expiry   time.Time
	current  func() bool
}
type Runtime struct {
	mu      sync.Mutex
	epoch   string
	entries map[string]*pending
	client  Client
	base    *url.URL
	now     func() time.Time
	logger  *slog.Logger
}

func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func NewRuntime(client Client, base string, loggers ...*slog.Logger) (*Runtime, error) {
	var u *url.URL
	var e error
	if base != "" {
		u, e = url.Parse(base)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid approved Tangent item base")
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Runtime{logger: logger, epoch: randomID(), entries: map[string]*pending{}, client: client, base: u, now: time.Now}, nil
}

// Canonical preserves JSON numbers without float conversion. Empty payload means null.
func Canonical(raw []byte) ([]byte, error) {
	if len(raw) > MaxPayload {
		return nil, errors.New("payload limit")
	}
	if len(raw) == 0 {
		raw = []byte("null")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return nil, errors.New("multiple JSON values")
	}
	return json.Marshal(v)
}
func (r *Runtime) pruneLocked() {
	for id, p := range r.entries {
		if !r.now().Before(p.expiry) || !p.current() {
			delete(r.entries, id)
		}
	}
}

// Attach preserves asks on every failure. It never invokes or resumes a plugin.
func (r *Runtime) Attach(ctx context.Context, verb string, payload []byte, generation string, current func() bool, raw []byte) []byte {
	if r.client == nil {
		return raw
	}
	id := ""
	var header struct {
		Status contract.Status `json:"status"`
	}
	if json.Unmarshal(raw, &header) != nil || header.Status != contract.StatusAsk {
		return raw
	}
	var env contract.ResultEnvelope
	envDecoder := json.NewDecoder(strings.NewReader(string(raw)))
	envDecoder.UseNumber()
	if envDecoder.Decode(&env) != nil {
		r.logFailure(id, "invalid_ask")
		b, _ := json.Marshal(contract.ResultEnvelope{Status: contract.StatusAsk, Ask: &contract.AskDetail{State: "unavailable", Continuation: "unavailable", Unavailable: "invalid_ask"}})
		return b
	}
	if env.Ask == nil {
		env.Ask = &contract.AskDetail{}
	}
	ask := env.Ask
	// A plugin cannot supply host handles or approval evidence.
	ask.ItemID = ""
	ask.ItemURL = ""
	ask.OperationID = ""
	ask.State = "unavailable"
	ask.Expiry = ""
	ask.Continuation = "unavailable"
	ask.Unavailable = ""
	fail := func(reason string) []byte {
		ask.OperationID = ""
		ask.Expiry = ""
		ask.Unavailable = reason
		r.logFailure(id, reason)
		b, _ := json.Marshal(env)
		return b
	}
	if env.Data != nil || env.Error != nil || strings.TrimSpace(ask.Prompt) == "" || utf8.RuneCountInString(strings.TrimSpace(ask.Prompt)) > 4000 || (ask.Kind != "" && ask.Kind != "approval" && ask.Kind != "attention") {
		return fail("invalid_ask")
	}
	for _, option := range ask.Options {
		if strings.TrimSpace(option) == "" {
			return fail("invalid_options")
		}
	}
	expiry := r.now().Add(PendingTTL)
	if ask.ExpiresAt != "" {
		t, e := time.Parse(time.RFC3339, ask.ExpiresAt)
		if e != nil || !t.After(r.now()) {
			return fail("invalid_expiry")
		}
		if t.Before(expiry) {
			expiry = t
		}
	}
	if len(payload) > MaxPayload {
		return fail("payload_limit")
	}
	canonical, e := Canonical(payload)
	if e != nil || len(canonical) > MaxPayload {
		return fail("payload_limit")
	}

	if !current() {
		return fail("plugin_restarted")
	}
	id = randomID()
	digest := sha256.Sum256(canonical)
	gen := sha256.Sum256([]byte(generation))
	req := FromAskDetail(verb, "tachyon:"+r.epoch+":"+id+":1", ask)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Request = strings.TrimSpace(req.Request)
	if req.Impact != nil && req.Kind == "attention" {
		return fail("invalid_ask")
	}
	if req.Impact != nil {
		req.Impact.Approve = strings.TrimSpace(req.Impact.Approve)
		req.Impact.Deny = strings.TrimSpace(req.Impact.Deny)
		if req.Impact.Approve == "" || req.Impact.Deny == "" || utf8.RuneCountInString(req.Impact.Approve) > 2000 || utf8.RuneCountInString(req.Impact.Deny) > 2000 {
			return fail("invalid_impact")
		}
	}
	req.ExpiresAt = expiry.UTC().Format(time.RFC3339Nano)
	// Reserve five supported AdditionalCorrelationV1 references for host binding.
	// Plugin correlations are advisory and dropped, preventing spoofed host refs
	// and ensuring the schema's 16-entry limit cannot crowd out host identity.
	additional := []any{}
	for _, ref := range []string{"epoch:" + r.epoch, "op:" + id, "gen:" + hex.EncodeToString(gen[:]), "verb:" + verb, "digest:" + hex.EncodeToString(digest[:])} {
		if utf8.RuneCountInString(ref) > 512 || strings.TrimSpace(ref) != ref {
			return fail("invalid_ask")
		}
		additional = append(additional, map[string]any{"kind": "other", "authority": "tachyon", "id": ref})
	}
	req.Correlations = map[string]any{"additional": additional}
	frozen, e := json.Marshal(req)
	if e != nil || len(frozen) > MaxPayload {
		return fail("ask_limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(frozen)))
	decoder.UseNumber()
	if e = decoder.Decode(&req); e != nil {
		return fail("invalid_ask")
	}
	p := &pending{kind: req.Kind, poll: make(chan struct{}, 1), snapshot: frozen, expiry: expiry, current: current}
	r.mu.Lock()
	r.pruneLocked()
	if len(r.entries) >= PendingCapacity {
		r.mu.Unlock()
		return fail("capacity")
	}
	r.entries[id] = p
	r.mu.Unlock()
	enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*clientTimeout)
	defer cancel()
	h, e := r.client.Enqueue(enqueueCtx, req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[id] != p || !r.now().Before(expiry) || !current() {
		delete(r.entries, id)
		return fail("correlation_lost")
	}
	if e != nil {
		delete(r.entries, id)
		reason := "enqueue_unavailable"
		if errors.Is(e, errRejected) {
			reason = "enqueue_rejected"
		}
		return fail(reason)
	}
	if h.ContractVersion != "1.0" || h.ItemID == "" || h.Revision < 1 || !core.State(h.State).Valid() {
		delete(r.entries, id)
		return fail("invalid_handle")
	}
	ask.OperationID = id
	ask.Expiry = expiry.UTC().Format(time.RFC3339Nano)
	p.handle = h
	ask.ItemID = h.ItemID
	ask.State = h.State
	ask.ItemURL = r.itemLink(h.ItemURL, h.ItemID)
	b, _ := json.Marshal(env)
	return b
}
func (r *Runtime) itemLink(link, id string) string {
	if r.base == nil {
		return ""
	}
	u, e := url.Parse(link)
	if e != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return ""
	}
	u = r.base.ResolveReference(u)
	if u.Scheme != r.base.Scheme || u.Host != r.base.Host || u.Path != "/hitl/items/"+id {
		return ""
	}
	return u.String()
}

type Status struct {
	OperationID  string     `json:"operation_id"`
	ItemID       string     `json:"item_id"`
	State        core.State `json:"state"`
	Approved     bool       `json:"approved"`
	Continuation string     `json:"continuation"`
	Expiry       time.Time  `json:"expiry"`
	Decision     string     `json:"decision,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

func (r *Runtime) Status(ctx context.Context, id string, wait int) (status Status, err error) {
	defer func() {
		if err != nil {
			reason := "status_unavailable"
			if errors.Is(err, errRejected) {
				reason = "status_rejected"
			}
			if errors.Is(err, errBindingInvalid) {
				reason = "binding_invalid"
			}
			if errors.Is(err, errTerminalInvalid) {
				reason = "terminal_invalid"
			}
			if errors.Is(err, ErrCorrelation) {
				reason = "correlation_unavailable"
			}
			if errors.Is(err, ErrStatusBusy) {
				reason = "poll_busy"
			}
			r.logFailure(id, reason)
		}
	}()
	if wait < 0 || wait > MaxStatusWait {
		return Status{}, errors.New("invalid wait_ms")
	}
	r.mu.Lock()
	r.pruneLocked()
	original := r.entries[id]
	var p *pending
	if original != nil {
		copy := *original
		p = &copy
	}
	r.mu.Unlock()
	if p == nil || p.handle.ItemID == "" {
		return Status{}, ErrCorrelation
	}
	select {
	case p.poll <- struct{}{}:
		defer func() { <-p.poll }()
	default:
		return Status{}, ErrStatusBusy
	}
	result, e := r.client.Retrieve(ctx, p.handle.ItemID, wait)
	if e != nil {
		return Status{}, e
	}
	snapshot, e := Canonical(result.Item.RequestSnapshot)
	expected, ee := Canonical(p.snapshot)
	if e != nil || ee != nil || string(snapshot) != string(expected) || result.Item.ItemID != p.handle.ItemID || result.Item.Revision < p.handle.Revision || result.Validate() != nil || result.ContractVersion != "1.0" || result.Item.ContractVersion != "1.0" || !result.Item.State.Valid() {
		return Status{}, errBindingInvalid
	}
	o := result.Item.TerminalOutcome
	if o != nil {
		wire, err := json.Marshal(o)
		if err != nil {
			return Status{}, errTerminalInvalid
		}
		o, err = core.UnmarshalOutcome(wire)
		if err != nil {
			return Status{}, errTerminalInvalid
		}
	}
	if resolved, ok := o.(core.Resolved); ok && resolved.Resolution.InteractionRevision != result.Item.Revision {
		return Status{}, errTerminalInvalid
	}
	if result.Item.State.IsTerminal() != (o != nil) || (o != nil && (o.OutcomeItemID() != p.handle.ItemID || o.OutcomeRevision() != result.Item.Revision || o.OutcomeState() != result.Item.State)) {
		return Status{}, errTerminalInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[id] != original || !r.now().Before(p.expiry) || !p.current() {
		delete(r.entries, id)
		return Status{}, ErrCorrelation
	}
	s := Status{OperationID: id, ItemID: p.handle.ItemID, State: result.Item.State, Continuation: "unavailable", Expiry: p.expiry}
	if resolved, ok := o.(core.Resolved); ok {
		response := resolved.Resolution.Response
		if response.Kind != p.kind || (p.kind == "approval" && response.Decision != "approved" && response.Decision != "denied") || (p.kind == "attention" && response.Decision != "acknowledged") {
			return Status{}, errTerminalInvalid
		}
		s.Decision = resolved.Resolution.Response.Decision
		resolvedAt := resolved.Resolution.ResolvedAt
		s.ResolvedAt = &resolvedAt
		s.Approved = p.kind == "approval" && resolved.Resolution.Response.Kind == "approval" && resolved.Resolution.Response.Decision == "approved"
	}
	return s, nil
}

// Log only classes and an opaque prefix, never provider errors or request data.
func (r *Runtime) logFailure(id, reason string) {
	if _, e := hex.DecodeString(id); e != nil || len(id) != 32 {
		id = ""
	} else {
		id = id[:8]
	}
	r.logger.Warn("HITL unavailable", "reason_class", reason, "operation_prefix", id)
}
