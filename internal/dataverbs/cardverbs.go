package dataverbs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// Agent-facing card tools. An agent names a card kind, a title and data;
// Tachyon fixes the source application (from the verified credential), the risk
// class and the operation digest, then queues the card for the operator. The
// agent never supplies its identity, a digest or a lower class.

// Cards is what the card verbs need: the queue service and the kind registry.
type Cards struct {
	Service  *hitl.Service
	Registry *cards.Registry
	// Receipt, when set, returns the effects receipt of an approved card (nil
	// when there is none). It is called with the card's item id.
	Receipt func(ctx context.Context, itemID string) any
}

type cardAsk struct {
	Kind       string           `json:"kind"`
	Title      string           `json:"title"`
	Summary    string           `json:"summary,omitempty"`
	Data       map[string]any   `json:"data,omitempty"`
	Completion string           `json:"completion,omitempty"`
	Class      string           `json:"class,omitempty"`
	Effects    []map[string]any `json:"effects,omitempty"`
	TTLSeconds int              `json:"ttl_seconds,omitempty"`
	Key        string           `json:"key,omitempty"`
	Context    string           `json:"context,omitempty"`
	Audience   []string         `json:"audience,omitempty"`
}
type cardRef struct {
	ID string `json:"id"`
}
type cardAwait struct {
	ID     string `json:"id"`
	WaitMs *int   `json:"wait_ms,omitempty"`
}
type cardCancel struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

const (
	defaultCardTTL = 24 * time.Hour
	maxCardTTL     = 7 * 24 * time.Hour
)

const cardRules = "Use a card when a person should see something, answer a question, fill a form or approve a change. " +
	"kind is card.approval, card.question, card.form, card.notice or card.info. " +
	"completion=wait means you will call card.await; async means you will check back with card.get; notify shows the card and collects nothing. " +
	"card.question data is {question, options?}; card.form data is {fields:[{id,type,label,required,options}]} with types text, textarea, number, bool, enum, multi, date, secret. " +
	"card.approval may list effects [{target, op?, summary?}] that describe what approval will do. " +
	"Never put secrets in data; a form secret field returns a secret_ref, never a value. " +
	"The risk class is set by Tachyon: you may raise it to medium, never lower it; high is reserved for provider-console handoffs and is refused here. Retry the same key to get the same card, not a duplicate. " +
	"If the card is denied, expired or canceled, do not re-ask the same thing without new information."

func cardVerbs() []Verb {
	return []Verb{
		{Name: "card.ask", Mutates: true, Description: "Show the operator a card (approval, question, form, notice or info) and get back a stable id, its expiry and a tach:card link. Nothing is applied by asking. " + cardRules, InputSchema: inputSchema(reflect.TypeFor[cardAsk](), []string{"kind", "title"})},
		{Name: "card.get", Description: "Read one card you asked: its state and, once answered, the operator's response.", InputSchema: inputSchema(reflect.TypeFor[cardRef](), []string{"id"})},
		{Name: "card.await", Description: "Wait (up to wait_ms, max 50000) for a card you asked: returns the answer once it is settled, or the current state if it is still open. Repeat while pending; the operator may take minutes.", InputSchema: inputSchema(reflect.TypeFor[cardAwait](), []string{"id"})},
		{Name: "card.cancel", Mutates: true, Description: "Withdraw a card you asked that is still open (for example because the question no longer applies).", InputSchema: inputSchema(reflect.TypeFor[cardCancel](), []string{"id"})},
	}
}

func newCardKey() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "card_" + hex.EncodeToString(b)
}

// replay returns the existing card when a retried ask names the same card.
func (c *Cards) replay(ctx context.Context, caller hitl.CallerAssertion, id string, req hitl.EnqueueRequest) (hitl.Handle, error) {
	res, err := c.Service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: id, Caller: caller})
	if err != nil {
		return hitl.Handle{}, err
	}
	var old hitl.EnqueueRequest
	if json.Unmarshal(res.Item.RequestSnapshot, &old) != nil || old.Kind != req.Kind ||
		string(old.Extra["op_digest"]) != string(req.Extra["op_digest"]) || string(old.Extra["title"]) != string(req.Extra["title"]) {
		return hitl.Handle{}, &hitl.IdempotencyConflictError{IdempotencyKey: req.IdempotencyKey, ExistingItemID: id}
	}
	return hitl.Handle{ContractVersion: hitl.ContractVersion, ItemID: id, State: res.Item.State, Revision: res.Item.Revision}, nil
}

func cardError(err error) error {
	switch {
	case errors.Is(err, hitl.ErrIdempotencyConflict):
		return svcerr.New(svcerr.CodeConflict, "That key already belongs to a different card.")
	case errors.Is(err, hitl.ErrInvalidRequest):
		return invalid(err.Error())
	case errors.Is(err, hitl.ErrNotFound):
		return svcerr.New(svcerr.CodeNotFound, "No such card for this credential.")
	}
	var term *hitl.TerminalConflictError
	if errors.As(err, &term) {
		return svcerr.New(svcerr.CodeConflict, "The card is already settled.")
	}
	return err
}

// cardView is what an agent reads back. The answer is the go-envelopes
// Response, so every app reads one shape.
func cardView(ctx context.Context, c *Cards, item hitl.ItemView, status string) map[string]any {
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &req)
	env := cards.ToEnvelope(req)
	view := map[string]any{
		"id": item.ItemID, "kind": req.Kind, "state": item.State, "finished": item.State.IsTerminal(),
		"link": "tach:card/" + item.ItemID, "title": env.Title, "class": env.Meta["class"], "completion": env.Meta["completion"],
		"op_digest": env.Meta["op_digest"], "requested_at": item.EnqueuedAt,
	}
	if exp, ok := env.Meta["expires_at"]; ok {
		view["expires_at"] = exp
	}
	if status != "" {
		view["wait_status"] = status
	}
	if item.State.IsTerminal() {
		var resp *hitl.Response
		at := item.UpdatedAt
		if r, ok := item.TerminalOutcome.(hitl.Resolved); ok {
			resp, at = &r.Resolution.Response, r.Resolution.ResolvedAt
			view["responder"] = r.Resolution.Participant.Responder
			view["assurance"] = r.Resolution.Participant.Assurance
		}
		view["response"] = cards.ResponseToEnvelope(req.IdempotencyKey, item.State, resp, at)
		if c.Receipt != nil && item.State == hitl.StateResolved {
			if receipt := c.Receipt(ctx, item.ItemID); receipt != nil {
				view["receipt"] = receipt
			} else if len(req.Extra["effects"]) > 0 {
				view["receipt_pending"] = true
				view["next"] = "Approved; the effects are still being applied. Call card.get shortly for the receipt."
			}
		}
	} else {
		view["next"] = "Not settled. Call card.await again; do not re-ask."
	}
	return view
}

// invokeCard runs one card verb as the credential's application ("service:<id>").
// The card queue scopes every item to that application, so a credential reaches
// only the cards it asked.
func invokeCard(ctx context.Context, c *Cards, name string, raw json.RawMessage, app string) (json.RawMessage, error) {
	if c == nil || c.Service == nil || c.Registry == nil {
		return nil, svcerr.New(svcerr.CodePermission, "Cards are not enabled on this host.")
	}
	caller := hitl.CallerAssertion{ApplicationID: app}
	switch name {
	case "card.ask":
		var q cardAsk
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		ttl := time.Duration(q.TTLSeconds) * time.Second
		if q.TTLSeconds == 0 {
			ttl = defaultCardTTL
		}
		if ttl < time.Minute || ttl > maxCardTTL {
			return nil, invalid("ttl_seconds must be between 60 and 604800.")
		}
		if q.Class == "high" {
			return nil, invalid("class high is reserved for provider-console handoffs; ask with low or medium.")
		}
		key := q.Key
		if key == "" {
			key = newCardKey()
		}
		expires := time.Now().UTC().Add(ttl)
		req := hitl.EnqueueRequest{
			ContractVersion: hitl.ContractVersion, Kind: q.Kind, IdempotencyKey: key,
			Source: hitl.SourceAssertion{ApplicationID: app, ApplicationLabel: app, AgentID: app}, ExpiresAt: &expires,
			Extra: map[string]json.RawMessage{},
		}
		set := func(k string, v any) {
			if raw, err := json.Marshal(v); err == nil {
				req.Extra[k] = raw
			}
		}
		set("title", q.Title)
		data := q.Data
		if data == nil {
			data = map[string]any{}
		}
		set("data", data)
		for k, v := range map[string]string{"summary": q.Summary, "completion": q.Completion, "class": q.Class, "context": q.Context} {
			if v != "" {
				set(k, v)
			}
		}
		if len(q.Effects) > 0 {
			set("effects", q.Effects)
		}
		if len(q.Audience) > 0 {
			set("audience", q.Audience)
		}
		if err := c.Registry.Prepare(&req, app); err != nil {
			return nil, cardError(err)
		}
		h, err := c.Service.Enqueue(ctx, req)
		var clash *hitl.IdempotencyConflictError
		if errors.As(err, &clash) {
			// A retry computes a new expiry, so the queue sees different content.
			// The same key naming the same card is a replay, not a conflict.
			h, err = c.replay(ctx, caller, clash.ExistingItemID, req)
		}
		if err != nil {
			return nil, cardError(err)
		}
		res, err := c.Service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: caller})
		if err != nil {
			return nil, cardError(err)
		}
		view := cardView(ctx, c, res.Item, "")
		view["expires_at"] = expires.Format(time.RFC3339)
		view["key"] = key
		if q.Completion == cards.CompletionNotify {
			view["next"] = "Shown to the operator; nothing will be collected."
		} else {
			view["next"] = "Nothing has been applied. The operator answers in Tachyon. Call card.await with this id."
		}
		return json.Marshal(view)
	case "card.get", "card.await":
		var q cardAwait
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if q.ID == "" {
			return nil, invalid("id is required.")
		}
		if name == "card.get" {
			res, err := c.Service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: q.ID, Caller: caller})
			if err != nil {
				return nil, cardError(err)
			}
			return json.Marshal(cardView(ctx, c, res.Item, ""))
		}
		wait := int(defaultAwait / time.Millisecond)
		if q.WaitMs != nil {
			wait = *q.WaitMs
		}
		if wait < 0 || wait > int(maxAwait/time.Millisecond) {
			return nil, invalid("wait_ms must be between 0 and 50000.")
		}
		res, err := c.Service.Await(ctx, hitl.AwaitCommand{ContractVersion: hitl.ContractVersion, ItemID: q.ID, Caller: caller, WaitMs: &wait})
		if err != nil {
			return nil, cardError(err)
		}
		return json.Marshal(cardView(ctx, c, res.Item, res.WaitStatus))
	case "card.cancel":
		var q cardCancel
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if q.ID == "" {
			return nil, invalid("id is required.")
		}
		if _, err := c.Service.Withdraw(ctx, hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: q.ID, Caller: caller, Reason: q.Reason}); err != nil {
			return nil, cardError(err)
		}
		res, err := c.Service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: q.ID, Caller: caller})
		if err != nil {
			return nil, cardError(err)
		}
		return json.Marshal(cardView(ctx, c, res.Item, ""))
	}
	return nil, denied()
}
