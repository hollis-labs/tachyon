package agentsderive

import "time"

// Windows is the fallback model -> context window table, used when a result frame did not carry
// contextWindow (or was cut). Extend it in config as models change.
var Windows = map[string]int64{
	"claude-sonnet-5-5": 1_000_000, "claude-opus-5-5": 1_000_000, "claude-haiku-5-5": 200_000, "claude-fable-5-1": 1_000_000,
}

// Marker is a compaction or clear. Inferred markers come from the heuristic, not from an event.
type Marker struct {
	At       time.Time `json:"at"`
	Before   int64     `json:"before,omitempty"`
	After    int64     `json:"after,omitempty"`
	Inferred bool      `json:"inferred"`
}

// Meter is one session's context state. Zero value is ready. Source is "stream" once a usage figure
// was seen and "unknown" otherwise: unmetered sessions show n/a, never 0.
type Meter struct {
	Used, Window int64
	Model        string
	Seen         bool
	Compactions  []Marker
	LastClear    *Marker
}

// Pct is the used share of the window, or -1 when either is unknown.
func (m Meter) Pct() int {
	if !m.Seen || m.Window <= 0 {
		return -1
	}
	return int(m.Used * 100 / m.Window)
}

func (m Meter) Source() string {
	if m.Seen {
		return "stream"
	}
	return "unknown"
}

// Observe folds one extraction in. used = input + cache read + cache creation of the LAST call.
// A drop of more than 40% with no new session is recorded as an inferred compaction, unless the
// frame carried a real compact_boundary (then it is recorded as not inferred). Falling under 5% of
// the window from a materially larger figure is an inferred clear.
func (m *Meter) Observe(e Extracted, at time.Time) {
	if e.Window > 0 {
		m.Window = e.Window
	}
	if e.Model != "" {
		m.Model = e.Model
		if m.Window == 0 {
			m.Window = Windows[e.Model]
		}
	}
	if e.Compact != nil {
		m.Compactions = append(m.Compactions, Marker{At: at, Before: *e.Compact, After: m.Used})
	}
	if e.Usage == nil {
		return
	}
	used := e.Usage.ContextUsed()
	if used <= 0 {
		return
	}
	if m.Seen && e.Compact == nil && m.Used > 0 {
		switch {
		case m.Window > 0 && used*20 < m.Window && m.Used*20 >= m.Window:
			m.LastClear = &Marker{At: at, Before: m.Used, After: used, Inferred: true}
		case used*10 < m.Used*6:
			m.Compactions = append(m.Compactions, Marker{At: at, Before: m.Used, After: used, Inferred: true})
		}
	}
	m.Used, m.Seen = used, true
	if len(m.Compactions) > 50 {
		m.Compactions = m.Compactions[len(m.Compactions)-50:]
	}
}
