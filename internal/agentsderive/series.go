package agentsderive

import "time"

// Run is consecutive tool calls grouped for the timeline ("9 tool calls").
type Run struct {
	Start, End time.Time
	Calls      int
	Counts     map[string]int // by verb-ish tool name
	IDs        []int
}

// RunGap is the largest pause inside one group (design: 30 s).
const RunGap = 30 * time.Second

// GroupRuns groups tool events (oldest first, with their times) into runs. A run breaks when a
// non-tool event sits between two tool events or the pause exceeds RunGap. isTool[i] says whether
// event i is a tool call; names[i] is its tool name. The stored rows stay individual.
func GroupRuns(times []time.Time, isTool []bool, names []string) []Run {
	var out []Run
	var cur *Run
	for i, t := range times {
		if !isTool[i] {
			cur = nil
			continue
		}
		if cur == nil || t.Sub(cur.End) > RunGap {
			out = append(out, Run{Start: t, End: t, Counts: map[string]int{}})
			cur = &out[len(out)-1]
		}
		cur.End = t
		cur.Calls++
		cur.Counts[names[i]]++
		cur.IDs = append(cur.IDs, i)
	}
	return out
}

// Spark returns 30 per-minute activity counts ending at now, oldest first.
func Spark(events []time.Time, now time.Time) [30]int {
	var s [30]int
	end := now.Truncate(time.Minute)
	for _, t := range events {
		back := int(end.Sub(t.Truncate(time.Minute)) / time.Minute)
		if back >= 0 && back < 30 {
			s[29-back]++
		}
	}
	return s
}
