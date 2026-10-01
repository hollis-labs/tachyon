package contract

import "encoding/json"

// MethodVerbInvoke is the JSON-RPC method for verb invocations.
const MethodVerbInvoke = "verb/invoke"

// VerbInvokeParams is sent by the host for method "verb/invoke".
type VerbInvokeParams struct {
	Verb    string          `json:"verb"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// VerbInvokeResult is the raw result from a verb/invoke call. The host
// wraps it in a ResultEnvelope before returning to callers.
type VerbInvokeResult = ResultEnvelope
