package contract

// Effect classifies the side effects of a verb invocation, derived from
// go-permission's Behavior taxonomy (D-46). Tachyon defines its own Effect
// type rather than importing go-mcp/server.Behavior — Tachyon is not an MCP
// server and should not depend on go-mcp — but follows the same taxonomy
// to avoid inventing a third (which D-46 explicitly prohibits).
type Effect string

const (
	// EffectReads indicates the verb only reads state. Safe to retry,
	// safe to call without confirmation.
	EffectReads Effect = "reads"

	// EffectWrites indicates the verb creates or modifies state.
	EffectWrites Effect = "writes"

	// EffectDestroys indicates the verb permanently removes state.
	EffectDestroys Effect = "destroys"

	// EffectOpenWorld indicates the verb causes effects outside
	// Tachyon's boundary (launches sessions, calls external APIs,
	// triggers deployments). Implies writes.
	EffectOpenWorld Effect = "open_world"
)

var validEffects = map[Effect]bool{
	EffectReads:     true,
	EffectWrites:    true,
	EffectDestroys:  true,
	EffectOpenWorld: true,
}

// Valid reports whether e is a recognized effect value.
func (e Effect) Valid() bool {
	return validEffects[e]
}
