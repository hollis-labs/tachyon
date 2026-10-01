package contract

import "encoding/json"

// Status is the outcome of a verb invocation (D-44).
type Status string

const (
	// StatusOK indicates the verb completed successfully.
	StatusOK Status = "ok"

	// StatusError indicates the verb failed.
	StatusError Status = "error"

	// StatusAsk indicates the verb requires an operator decision
	// before proceeding (D-45). This is the only policy middle-state;
	// plugins must not use "confirm", "needs_approval", or any synonym.
	StatusAsk Status = "ask"
)

// ResultEnvelope is the structured result every verb invocation returns
// (D-44). Consumers switch on Status to determine which payload field
// is populated.
type ResultEnvelope struct {
	Status Status          `json:"status"`
	Data   json.RawMessage `json:"data,omitempty"`
	Ask    *AskDetail      `json:"ask,omitempty"`
	Error  *ErrorDetail    `json:"error,omitempty"`
}

// AskDetail describes the decision an operator must make when
// Status == StatusAsk. Maps to the HITL gate contract (D-39).
//
// The go-hitl integration fields (Kind, Impact, Correlations, ExpiresAt)
// carry enough information for the host's HITL bridge to produce a
// tangent.hitl_enqueue request without inventing its own schema (D-43).
type AskDetail struct {
	Prompt  string         `json:"prompt"`
	Options []string       `json:"options,omitempty"`
	Context map[string]any `json:"context,omitempty"`

	// go-hitl integration fields (D-43).
	// These are optional — a plugin can return a minimal AskDetail with
	// just Prompt, and the HITL bridge will fill in defaults.

	// Kind is the go-hitl interaction kind: "approval" or "attention".
	// Default is "approval" when omitted.
	Kind string `json:"kind,omitempty"`

	// Impact describes the consequences of approving or denying.
	Impact *HitlImpact `json:"impact,omitempty"`

	// Correlations links this interaction to external entities
	// (project, task, session) for traceability.
	Correlations map[string]any `json:"correlations,omitempty"`

	// ExpiresAt is an RFC3339 timestamp after which the interaction
	// expires. Expired interactions fail closed to deny (D-43).
	ExpiresAt    string `json:"expires_at,omitempty"`
	ItemID       string `json:"item_id,omitempty"`
	ItemURL      string `json:"item_url,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
	State        string `json:"state,omitempty"`
	Expiry       string `json:"expiry,omitempty"`
	Continuation string `json:"continuation,omitempty"`
	Unavailable  string `json:"unavailable,omitempty"`
}

// HitlImpact describes the consequences of approving or denying
// a HITL interaction, displayed to the operator.
type HitlImpact struct {
	Approve string `json:"approve"`
	Deny    string `json:"deny"`
}

// ErrorDetail provides structured error information when
// Status == StatusError.
type ErrorDetail struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

// OK creates a successful result envelope wrapping the given data.
func OK(data any) (ResultEnvelope, error) {
	if data == nil {
		return ResultEnvelope{Status: StatusOK}, nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ResultEnvelope{}, err
	}
	return ResultEnvelope{Status: StatusOK, Data: b}, nil
}

// Err creates an error result envelope.
func Err(code, message string) ResultEnvelope {
	return ResultEnvelope{
		Status: StatusError,
		Error:  &ErrorDetail{Code: code, Message: message},
	}
}

// Ask creates an ask result envelope requiring operator input.
func Ask(prompt string, options ...string) ResultEnvelope {
	return ResultEnvelope{
		Status: StatusAsk,
		Ask:    &AskDetail{Prompt: prompt, Options: options},
	}
}
