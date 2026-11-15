package cards

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes"
)

// FromEnvelope maps a go-envelopes Envelope onto a queue request. The
// envelope type is the card kind. The source application is deliberately left
// empty: Prepare sets it from the verified credential.
//
// Meta members the asker may set: summary, completion, class, effects,
// audience, source_ref. Prepare validates them.
func FromEnvelope(env envelopes.Envelope) (hitl.EnqueueRequest, error) {
	if env.ID == "" || env.Type == "" {
		return hitl.EnqueueRequest{}, fmt.Errorf("%w: envelope id and type are required", hitl.ErrInvalidRequest)
	}
	req := hitl.EnqueueRequest{
		ContractVersion: hitl.ContractVersion,
		Kind:            env.Type,
		IdempotencyKey:  env.ID,
		Extra:           map[string]json.RawMessage{},
	}
	if env.Trace != nil {
		req.Source.AgentID = env.Trace.AgentID
	}
	set := func(key string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("%w: %s is not encodable", hitl.ErrInvalidRequest, key)
		}
		req.Extra[key] = raw
		return nil
	}
	data := env.Data
	if data == nil {
		data = map[string]any{}
	}
	for key, v := range map[string]any{"title": env.Title, "data": data} {
		if err := set(key, v); err != nil {
			return hitl.EnqueueRequest{}, err
		}
	}
	if env.Context != "" {
		if err := set("context", env.Context); err != nil {
			return hitl.EnqueueRequest{}, err
		}
	}
	if env.TypeVersion != "" {
		if err := set("type_version", env.TypeVersion); err != nil {
			return hitl.EnqueueRequest{}, err
		}
	}
	if env.Presentation != "" {
		if err := set("presentation", string(env.Presentation)); err != nil {
			return hitl.EnqueueRequest{}, err
		}
	}
	for _, key := range []string{"summary", "completion", "class", "effects", "audience", "source_ref", "fallback"} {
		if v, ok := env.Meta[key]; ok {
			if err := set(key, v); err != nil {
				return hitl.EnqueueRequest{}, err
			}
		}
	}
	if raw, ok := env.Meta["expires_at"].(string); ok && raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return hitl.EnqueueRequest{}, fmt.Errorf("%w: expires_at must be RFC 3339", hitl.ErrInvalidRequest)
		}
		req.ExpiresAt = &t
	}
	return req, nil
}

// ToEnvelope rebuilds the envelope a stored request came from, for a renderer.
func ToEnvelope(req hitl.EnqueueRequest) envelopes.Envelope {
	env := envelopes.Envelope{V: envelopes.ProtocolVersion, ID: req.IdempotencyKey, Type: req.Kind, Meta: map[string]any{}}
	var s string
	if json.Unmarshal(req.Extra["title"], &s) == nil {
		env.Title = s
	}
	if json.Unmarshal(req.Extra["context"], &s) == nil {
		env.Context = s
	}
	if json.Unmarshal(req.Extra["type_version"], &s) == nil {
		env.TypeVersion = s
	}
	if json.Unmarshal(req.Extra["presentation"], &s) == nil {
		env.Presentation = envelopes.Presentation(s)
	}
	_ = json.Unmarshal(req.Extra["data"], &env.Data)
	for _, key := range []string{"summary", "completion", "class", "op_digest", "effects", "affected_items", "destructive", "alternatives", "audience", "source_ref", "fallback"} {
		if raw, ok := req.Extra[key]; ok {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				env.Meta[key] = v
			}
		}
	}
	if req.ExpiresAt != nil {
		env.Meta["expires_at"] = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if req.Source.AgentID != "" {
		env.Trace = &envelopes.Trace{AgentID: req.Source.AgentID}
	}
	return env
}

// ResponseFromEnvelope maps a submitted envelope Response onto a queue
// response. Only status=submitted answers a card: canceled is never a
// decision, and partial has no home in the queue (drafts stay in the host).
func ResponseFromEnvelope(kind string, resp envelopes.Response) (hitl.Response, error) {
	if resp.Status != envelopes.ResponseStatusSubmitted {
		return hitl.Response{}, fmt.Errorf("%w: only a submitted response answers a card", hitl.ErrInvalidRequest)
	}
	out := hitl.Response{Kind: kind, Extra: map[string]json.RawMessage{}}
	switch {
	case len(resp.Decisions) == 1:
		out.Decision = resp.Decisions[0].Action
		out.Note = resp.Decisions[0].Note
	case len(resp.Decisions) > 1:
		out.Decision = "submitted"
		out.Extra["decisions"], _ = json.Marshal(resp.Decisions)
	default:
		out.Decision = "submitted"
	}
	if len(resp.Answers) > 0 {
		out.Extra["answers"], _ = json.Marshal(resp.Answers)
	}
	if resp.Payload != nil {
		out.Extra["payload"], _ = json.Marshal(resp.Payload)
	}
	return out, nil
}

// ResponseToEnvelope reports a card's terminal outcome to its asker as an
// envelope Response. A nil resp means the card ended without an answer.
func ResponseToEnvelope(envelopeID string, state hitl.State, resp *hitl.Response, at time.Time) envelopes.Response {
	out := envelopes.Response{V: envelopes.ProtocolVersion, EnvelopeID: envelopeID, CompletedAt: at.UTC().Format(time.RFC3339)}
	switch state {
	case hitl.StateResolved:
		out.Kind, out.Status = envelopes.ResponseKindData, envelopes.ResponseStatusSubmitted
		if resp == nil {
			return out
		}
		var answers []envelopes.Answer
		if json.Unmarshal(resp.Extra["answers"], &answers) == nil {
			out.Answers = answers
		}
		var decisions []envelopes.Decision
		if json.Unmarshal(resp.Extra["decisions"], &decisions) == nil && len(decisions) > 0 {
			out.Decisions = decisions
		} else if resp.Decision != "submitted" {
			d := envelopes.Decision{ItemID: envelopeID, Action: resp.Decision}
			d.Note = resp.Note
			out.Decisions = []envelopes.Decision{d}
		}
		if raw, ok := resp.Extra["payload"]; ok {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				out.Payload = v
			}
		}
	case hitl.StateCanceled:
		out.Kind, out.Status = envelopes.ResponseKindData, envelopes.ResponseStatusCanceled
	case hitl.StateExpired:
		out.Kind, out.Status = envelopes.ResponseKindError, envelopes.ResponseStatusError
		out.Error = &envelopes.ResponseError{Code: envelopes.ErrorCodeTimeout, Message: "The card expired before it was answered."}
	default:
		out.Kind, out.Status = envelopes.ResponseKindError, envelopes.ResponseStatusError
		out.Error = &envelopes.ResponseError{Code: envelopes.ErrorCodeHostError, Message: "The card ended as " + string(state) + "."}
	}
	return out
}
