package hitl

import (
	"github.com/hollis-labs/tachyon/internal/contract"
)

// EnqueueRequest is the shape Tachyon sends to Tangent's hitl_enqueue
// tool. It mirrors the go-hitl wire contract (contract_version: "1.0")
// without importing go-hitl directly — Tachyon is a caller, not a store
// owner (D-41).
type EnqueueRequest struct {
	ContractVersion string          `json:"contract_version"`
	Kind            string          `json:"kind"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Title           string          `json:"title"`
	Summary         string          `json:"summary"`
	Request         string          `json:"request"`
	Source          SourceAssertion `json:"source"`
	Impact          *ImpactDetail   `json:"impact,omitempty"`
	Correlations    map[string]any  `json:"correlations,omitempty"`
	ExpiresAt       string          `json:"expires_at,omitempty"`
}

// SourceAssertion identifies the caller producing the HITL interaction.
type SourceAssertion struct {
	ApplicationID    string `json:"application_id"`
	ApplicationLabel string `json:"application_label,omitempty"`
	AgentID          string `json:"agent_id"`
	AgentLabel       string `json:"agent_label,omitempty"`
}

// ImpactDetail describes approve/deny consequences for the operator.
type ImpactDetail struct {
	Approve string `json:"approve"`
	Deny    string `json:"deny"`
}

// EnqueueHandle is the response from Tangent's hitl_enqueue.
type EnqueueHandle struct {
	ContractVersion string `json:"contract_version"`
	SurfaceID       string `json:"surface_id"`
	ItemID          string `json:"item_id"`
	State           string `json:"state"`
	Revision        int64  `json:"revision"`
	QueueSequence   int64  `json:"queue_sequence"`
	ItemURL         string `json:"item_url,omitempty"`
}

// DefaultSource is the source assertion Tachyon uses for HITL requests.
var DefaultSource = SourceAssertion{
	ApplicationID:    "tachyon",
	ApplicationLabel: "Tachyon Control Plane",
	AgentID:          "tachyon-host",
	AgentLabel:       "Tachyon Host",
}

// FromAskDetail translates a verb's AskDetail into an EnqueueRequest
// ready to send to Tangent. The verb name is used in the title and
// idempotency key.
func FromAskDetail(verb string, ask *contract.AskDetail) EnqueueRequest {
	kind := ask.Kind
	if kind == "" {
		kind = "approval"
	}

	req := EnqueueRequest{
		ContractVersion: "1.0",
		Kind:            kind,
		IdempotencyKey:  "tachyon:" + verb,
		Title:           "Tachyon: " + verb,
		Summary:         ask.Prompt,
		Request:         ask.Prompt,
		Source:          DefaultSource,
		Correlations:    ask.Correlations,
		ExpiresAt:       ask.ExpiresAt,
	}

	if ask.Impact != nil {
		req.Impact = &ImpactDetail{
			Approve: ask.Impact.Approve,
			Deny:    ask.Impact.Deny,
		}
	}

	return req
}

// AskResponse wraps a HITL interaction handle into a result the HTTP
// layer can return to the caller while the interaction is pending.
type AskResponse struct {
	ItemID        string `json:"item_id"`
	State         string `json:"state"`
	QueueSequence int64  `json:"queue_sequence"`
	ItemURL       string `json:"item_url,omitempty"`
}

// FromHandle converts an EnqueueHandle into the caller-facing response.
func FromHandle(h EnqueueHandle) AskResponse {
	return AskResponse{
		ItemID:        h.ItemID,
		State:         h.State,
		QueueSequence: h.QueueSequence,
		ItemURL:       h.ItemURL,
	}
}
