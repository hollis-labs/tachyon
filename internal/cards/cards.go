// Package cards defines the card.* request contract on top of the go-hitl
// store: which kinds exist, what a request must carry, how its risk class and
// operation digest are fixed by the server, and what a response may say. It
// holds no storage and no transport; internal/approvals wires it into the
// existing queue.
package cards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// KindPrefix marks every card kind. The queue also holds the older
// approval/attention kinds, which this package does not touch.
const KindPrefix = "card."

// Completion is how the asker intends to collect the answer.
const (
	CompletionWait   = "wait"   // bounded block with a durable resume handle
	CompletionAsync  = "async"  // handle now, durable delivery later
	CompletionNotify = "notify" // display only; nothing is collected
)

const (
	maxTitle   = 160
	maxSummary = 600
	maxData    = 64 << 10
	maxEffects = 32
	// maxAudience bounds the unenforced audience field.
	maxAudience = 16
)

// Type is one registered card kind. A kind that is not registered is refused:
// the server never guesses how risky an unknown card is.
type Type struct {
	Kind string
	// MinClass is the lowest class the server will assign; an asker may raise
	// the class but never lower it below this.
	MinClass string
	// Decisions is the closed set of response decisions. Empty means the kind
	// is display-only and cannot be answered.
	Decisions []string
	// Answers reports whether a response carries answers (question and form
	// cards) rather than only a decision.
	Answers bool
	// Form reports whether data.fields describes a form to validate.
	Form bool
}

var builtin = []Type{
	{Kind: "card.approval", MinClass: "low", Decisions: []string{"approved", "denied"}},
	{Kind: "card.question", MinClass: "low", Decisions: []string{"submitted"}, Answers: true},
	{Kind: "card.form", MinClass: "low", Decisions: []string{"submitted"}, Answers: true, Form: true},
	// card.plan asks the operator to approve a plan or ask for changes (the note says which).
	{Kind: "card.plan", MinClass: "low", Decisions: []string{"approved", "changes_requested"}},
	// card.dispatch asks to hand work to an agent; its effect is a cardfx create (torque:task/new).
	{Kind: "card.dispatch", MinClass: "low", Decisions: []string{"approved", "denied"}},
	// card.tool_approval is a runtime tool call waiting for the operator. "always" allows that tool for the chat
	// (the server only honours it for low class).
	{Kind: "card.tool_approval", MinClass: "low", Decisions: []string{"approved", "always", "denied"}},
	{Kind: "card.notice", MinClass: "low", Decisions: []string{"acknowledged"}},
	// card.info is display-only: the asker uses completion=notify.
	{Kind: "card.info", MinClass: "low"},
}

// Registry maps kinds to their types.
type Registry struct{ types map[string]Type }

// NewRegistry returns the built-in kinds.
func NewRegistry() *Registry {
	r := &Registry{types: map[string]Type{}}
	for _, t := range builtin {
		r.types[t.Kind] = t
	}
	return r
}

// Register adds or replaces a kind. It rejects kinds outside the card.
// namespace and classes the server does not know.
func (r *Registry) Register(t Type) error {
	if !strings.HasPrefix(t.Kind, KindPrefix) || len(t.Kind) == len(KindPrefix) {
		return fmt.Errorf("card kind %q must start with %q", t.Kind, KindPrefix)
	}
	if tier(t.MinClass) == 0 {
		return fmt.Errorf("card kind %q has unknown class %q", t.Kind, t.MinClass)
	}
	r.types[t.Kind] = t
	return nil
}

// Lookup returns the type for a kind.
func (r *Registry) Lookup(kind string) (Type, bool) {
	t, ok := r.types[kind]
	return t, ok
}

// Kinds lists the registered kinds in order.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.types))
	for k := range r.types {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsCard reports whether a queue kind belongs to this package.
func IsCard(kind string) bool { return strings.HasPrefix(kind, KindPrefix) }

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

func maxClass(a, b string) string {
	if tier(b) > tier(a) {
		return b
	}
	return a
}

func str(req hitl.EnqueueRequest, key string) string {
	var v string
	_ = json.Unmarshal(req.Extra[key], &v)
	return v
}

func object(raw json.RawMessage) (map[string]any, bool) {
	var v map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil || v == nil {
		return nil, false
	}
	return v, true
}

// Digest is the operation digest the server computes for a card: SHA-256 over
// the RFC 8785 canonical form of its kind, data and effects. The asker never
// supplies the authoritative value.
func Digest(kind string, data, effects json.RawMessage) (string, error) {
	if len(effects) == 0 {
		effects = json.RawMessage("null")
	}
	body, err := json.Marshal(struct {
		Kind    string          `json:"kind"`
		Data    json.RawMessage `json:"data"`
		Effects json.RawMessage `json:"effects"`
	}{kind, data, effects})
	if err != nil {
		return "", err
	}
	canon, err := jcs.Transform(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Prepare validates a card request and fixes the members the asker may not
// choose. verifiedApp is the application proven by the caller's credential;
// whatever the request asserted as its source application is overwritten.
// It mutates req, so call it before the request is enqueued and digested for
// idempotency.
func (r *Registry) Prepare(req *hitl.EnqueueRequest, verifiedApp string) error {
	t, ok := r.Lookup(req.Kind)
	if !ok {
		return fmt.Errorf("%w: unknown card kind %q", hitl.ErrInvalidRequest, req.Kind)
	}
	if strings.TrimSpace(verifiedApp) == "" {
		return fmt.Errorf("%w: card source must come from a verified credential", hitl.ErrInvalidRequest)
	}
	req.Source.ApplicationID = verifiedApp
	if req.Extra == nil {
		req.Extra = map[string]json.RawMessage{}
	}
	title := strings.TrimSpace(str(*req, "title"))
	if title == "" || len(title) > maxTitle {
		return fmt.Errorf("%w: title is required and at most %d bytes", hitl.ErrInvalidRequest, maxTitle)
	}
	if len(str(*req, "summary")) > maxSummary {
		return fmt.Errorf("%w: summary is at most %d bytes", hitl.ErrInvalidRequest, maxSummary)
	}
	data := req.Extra["data"]
	if _, ok := object(data); !ok || len(data) > maxData {
		return fmt.Errorf("%w: data must be a JSON object of at most %d bytes", hitl.ErrInvalidRequest, maxData)
	}
	completion := str(*req, "completion")
	if completion == "" {
		completion = CompletionAsync
		if len(t.Decisions) == 0 {
			completion = CompletionNotify
		}
	}
	switch completion {
	case CompletionWait, CompletionAsync:
		if len(t.Decisions) == 0 {
			return fmt.Errorf("%w: %s cannot be answered; use completion=notify", hitl.ErrInvalidRequest, t.Kind)
		}
	case CompletionNotify:
	default:
		return fmt.Errorf("%w: completion must be wait, async or notify", hitl.ErrInvalidRequest)
	}
	req.Extra["completion"], _ = json.Marshal(completion)

	effects := req.Extra["effects"]
	if len(effects) > 0 {
		var list []map[string]any
		if json.Unmarshal(effects, &list) != nil || len(list) == 0 || len(list) > maxEffects {
			return fmt.Errorf("%w: effects must be a list of 1 to %d objects", hitl.ErrInvalidRequest, maxEffects)
		}
		for _, e := range list {
			if target, _ := e["target"].(string); target == "" {
				return fmt.Errorf("%w: every effect needs a target", hitl.ErrInvalidRequest)
			}
		}
	}
	var aud []string
	if raw := req.Extra["audience"]; len(raw) > 0 && (json.Unmarshal(raw, &aud) != nil || len(aud) > maxAudience) {
		return fmt.Errorf("%w: audience must be a list of at most %d strings", hitl.ErrInvalidRequest, maxAudience)
	}

	if t.Form {
		secret, err := checkFields(data)
		if err != nil {
			return err
		}
		if secret && tier(t.MinClass) < tier("medium") {
			// A form that collects a secret is never low risk.
			t.MinClass = "medium"
		}
	}
	class := t.MinClass
	if len(effects) > 0 {
		class = maxClass(class, "medium")
	}
	if asked := str(*req, "class"); asked != "" {
		if tier(asked) == 0 {
			return fmt.Errorf("%w: class must be low, medium or high", hitl.ErrInvalidRequest)
		}
		class = maxClass(class, asked)
	}
	req.Extra["class"], _ = json.Marshal(class)

	digest, err := Digest(req.Kind, data, effects)
	if err != nil {
		return fmt.Errorf("%w: data is not canonicalizable", hitl.ErrInvalidRequest)
	}
	if given := str(*req, "op_digest"); given != "" && given != digest {
		return fmt.Errorf("%w: op_digest does not match the card contents", hitl.ErrInvalidRequest)
	}
	req.Extra["op_digest"], _ = json.Marshal(digest)
	return nil
}
