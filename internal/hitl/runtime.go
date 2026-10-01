package hitl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	core "github.com/hollis-labs/go-hitl"
	"github.com/hollis-labs/tachyon/internal/contract"
)

const PendingCapacity = 256         // Reject new correlations rather than evict existing operations.
const MaxPayload = 32 << 10         // Maximum canonical payload retained per operation.
const PendingTTL = 10 * time.Minute // Correlation never survives host restart or this TTL.
var ErrCorrelation = errors.New("HITL correlation unavailable")

type pending struct {
	request  EnqueueRequest
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
}

func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func NewRuntime(client Client, base string) (*Runtime, error) {
	var u *url.URL
	var e error
	if base != "" {
		u, e = url.Parse(base)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid approved Tangent item base")
		}
	}
	return &Runtime{epoch: randomID(), entries: map[string]*pending{}, client: client, base: u, now: time.Now}, nil
}

// Canonical preserves JSON numbers without float conversion. Empty payload means null.
func Canonical(raw []byte) ([]byte, error) {
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
	fail := func(reason string) []byte { ask.Unavailable = reason; b, _ := json.Marshal(env); return b }
	if env.Data != nil || env.Error != nil || strings.TrimSpace(ask.Prompt) == "" || len(ask.Prompt) > MaxPayload || (ask.Kind != "" && ask.Kind != "approval" && ask.Kind != "attention") {
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
	canonical, e := Canonical(payload)
	if e != nil || len(canonical) > MaxPayload {
		return fail("payload_limit")
	}
	if r.client == nil {
		return fail("not_configured")
	}
	if !current() {
		return fail("plugin_restarted")
	}
	id := randomID()
	digest := sha256.Sum256(canonical)
	gen := sha256.Sum256([]byte(generation))
	req := FromAskDetail(verb, "tachyon:"+r.epoch+":"+id+":1", ask)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Request = strings.TrimSpace(req.Request)
	if req.Impact != nil {
		req.Impact.Approve = strings.TrimSpace(req.Impact.Approve)
		req.Impact.Deny = strings.TrimSpace(req.Impact.Deny)
		if req.Impact.Approve == "" || req.Impact.Deny == "" {
			return fail("invalid_impact")
		}
	}
	req.ExpiresAt = expiry.UTC().Format(time.RFC3339Nano)
	req.Correlations = map[string]any{"host_epoch": r.epoch, "operation_id": id, "plugin_generation": hex.EncodeToString(gen[:]), "verb": verb, "payload_digest": hex.EncodeToString(digest[:])}
	// Copy plugin trace data under its own namespace so it cannot overwrite host binding.
	if ask.Correlations != nil {
		req.Correlations["plugin"] = ask.Correlations
	}
	if e := validateTrace(req.Correlations); e != nil {
		return fail("invalid_correlations")
	}
	frozen, e := json.Marshal(req)
	if e != nil || len(frozen) > MaxPayload {
		return fail("ask_limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(frozen)))
	decoder.UseNumber()
	if e = decoder.Decode(&req); e != nil {
		return fail("invalid_ask")
	}
	p := &pending{request: req, snapshot: frozen, expiry: expiry, current: current}
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
	ask.OperationID = id
	ask.Expiry = expiry.UTC().Format(time.RFC3339Nano)
	if e != nil {
		return fail("enqueue_unavailable")
	}
	if h.ContractVersion != "1.0" || h.ItemID == "" || h.Revision < 1 || !core.State(h.State).Valid() {
		return fail("invalid_handle")
	}
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
	OperationID  string       `json:"operation_id"`
	ItemID       string       `json:"item_id"`
	State        core.State   `json:"state"`
	Approved     bool         `json:"approved"`
	Continuation string       `json:"continuation"`
	Expiry       time.Time    `json:"expiry"`
	Outcome      core.Outcome `json:"terminal_outcome,omitempty"`
}

func (r *Runtime) Status(ctx context.Context, id string, wait int) (Status, error) {
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
	result, e := r.client.Retrieve(ctx, p.handle.ItemID, wait)
	if e != nil {
		return Status{}, e
	}
	snapshot, e := Canonical(result.Item.RequestSnapshot)
	expected, ee := Canonical(p.snapshot)
	if e != nil || ee != nil || string(snapshot) != string(expected) || result.Item.ItemID != p.handle.ItemID || result.Item.Revision < p.handle.Revision || result.Validate() != nil || result.ContractVersion != "1.0" || result.Item.ContractVersion != "1.0" || !result.Item.State.Valid() {
		return Status{}, errors.New("HITL outcome binding invalid")
	}
	o := result.Item.TerminalOutcome
	if o != nil {
		wire, err := json.Marshal(o)
		if err != nil {
			return Status{}, errors.New("invalid terminal outcome")
		}
		o, err = core.UnmarshalOutcome(wire)
		if err != nil {
			return Status{}, err
		}
	}
	if resolved, ok := o.(core.Resolved); ok && resolved.Resolution.InteractionRevision != result.Item.Revision {
		return Status{}, errors.New("resolution revision mismatch")
	}
	if result.Item.State.IsTerminal() != (o != nil) || (o != nil && (o.OutcomeItemID() != p.handle.ItemID || o.OutcomeRevision() != result.Item.Revision || o.OutcomeState() != result.Item.State)) {
		return Status{}, errors.New("HITL terminal identity invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[id] != original || !r.now().Before(p.expiry) || !p.current() {
		delete(r.entries, id)
		return Status{}, ErrCorrelation
	}
	s := Status{OperationID: id, ItemID: p.handle.ItemID, State: result.Item.State, Continuation: "unavailable", Expiry: p.expiry, Outcome: o}
	if resolved, ok := o.(core.Resolved); ok {
		s.Approved = p.request.Kind == "approval" && resolved.Resolution.Response.Kind == "approval" && resolved.Resolution.Response.Decision == "approved"
	}
	return s, nil
}

// Tangent rejects blank strings anywhere in a request. Reject trace values
// that its normalization would alter, keeping the frozen snapshot exact.
func validateTrace(value any) error {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return errors.New("blank trace value")
		}
	case []any:
		for _, child := range v {
			if e := validateTrace(child); e != nil {
				return e
			}
		}
	case map[string]any:
		for _, child := range v {
			if text, ok := child.(string); ok && text != strings.TrimSpace(text) {
				return errors.New("trace whitespace must be normalized")
			}
			if e := validateTrace(child); e != nil {
				return e
			}
		}
	}
	return nil
}
