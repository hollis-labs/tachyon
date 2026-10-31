// Package agentscollector is the always-on Agents collector: it follows Tether's environment
// event stream and keeps a derived, rebuildable read model in Tachyon's store (design
// tesseract:01M6547NTB3FR4P49GQRP8WC4B, decision E1).
//
// Hard rule: raw session output and raw tool arguments are never persisted, logged or returned.
// A frame's payload is read in memory for allowlisted fields only and then dropped.
package agentscollector

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Frame is one environment event reduced to the fields the collector may keep.
type Frame struct {
	Seq       int64
	EventID   string // "<env>:<seq>" or "<seq>"
	Env       string
	Kind      string
	At        time.Time
	App       string // set on events a Tether app published (the bus), empty for Tether's own
	SessionID string

	// Allowlisted facts pulled from the payload.
	WorkState       string
	From            string
	SessionState    string
	Provider, Model string
	Workroot        string
	TaskRef         string
	// Identity and lineage, when the payload says (unverified wire names: agent_urn, logical_agent_id, agent_name).
	AgentURN, LogicalAgentID, AgentName, ParentSession, ReplacedBy string
	PendingApproval                                                *bool
	PendingQuestion                                                *bool
	Tool                                                           string // sanitized tool name only
	Reason                                                         string // gap or end reason
	DeliveryState                                                  string // message frames: payload delivery.state (retry_scheduled, dead_letter, expired, ...)

	Gap          bool // a gap frame: events were missed
	Synchronized bool // the catch-up is over, live frames follow
	Output       bool // session.output: counted, never stored
	// Payload is set ONLY on session.output frames. It is handed to the OutputSink in memory and then
	// dropped; no code path stores or logs it.
	Payload []byte
	// Usage is a turn's token counts from a sanitized session.usage event: input, output, cache read, cache write.
	Usage *[4]int64

	// Bus fields (app-published events): validated and bounded by NormalizeBus.
	Name, State, Version, PR, Title, BusActor string
}

type wireFrame struct {
	ID        string          `json:"id"`
	Seq       int64           `json:"seq"`
	Kind      string          `json:"kind"`
	Time      string          `json:"time"`
	At        string          `json:"at"`
	App       string          `json:"app"`
	Scope     json.RawMessage `json:"scope"` // an object on the live stream
	SessionID string          `json:"session_id"`
	Reason    string          `json:"reason"`
	Payload   json.RawMessage `json:"payload"`
	PayloadJS json.RawMessage `json:"payload_json"` // an object, or a JSON string holding one
}

type payload struct {
	To, From        string
	State           string
	Provider        string
	Model           string
	Workroot        string
	TaskID          string
	PendingApproval *bool
	PendingQuestion *bool
	Tool            string
	Reason          string
}

// ParseFrame reads one frame. Both the live stream shape (id, time, payload) and the history shape
// (seq, at, payload_json) are accepted. A frame that does not parse returns ok=false and is skipped.
func ParseFrame(raw []byte) (Frame, bool) {
	var w wireFrame
	if json.Unmarshal(raw, &w) != nil || w.Kind == "" {
		return Frame{}, false
	}
	if w.App == "tether" {
		w.App = "" // the live stream stamps Tether's own events app=tether; App is for events another app published
	}
	f := Frame{Kind: w.Kind, App: w.App, SessionID: w.SessionID, EventID: w.ID, Seq: w.Seq, Reason: w.Reason}
	if env, n, ok := strings.Cut(w.ID, ":"); ok {
		f.Env = env
		if q, err := strconv.ParseInt(n, 10, 64); err == nil && f.Seq == 0 {
			f.Seq = q
		}
	} else if q, err := strconv.ParseInt(w.ID, 10, 64); err == nil && f.Seq == 0 {
		f.Seq = q
	}
	if f.EventID == "" && f.Seq > 0 {
		f.EventID = strconv.FormatInt(f.Seq, 10)
	}
	for _, s := range []string{w.Time, w.At} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			f.At = t.UTC()
			break
		}
	}
	switch f.Kind {
	case "gap":
		f.Gap = true
	case "synchronized":
		f.Synchronized = true
	case "session.output":
		f.Output = true
		body := []byte(w.Payload)
		if len(body) == 0 {
			body = []byte(w.PayloadJS)
		}
		if len(body) <= 1<<20 {
			f.Payload = append([]byte(nil), body...)
		}
		return f, true // the collector never reads it; the OutputSink may, in memory
	}
	body := w.Payload
	if len(body) == 0 && len(w.PayloadJS) > 0 {
		body = w.PayloadJS
		var inner string
		if json.Unmarshal(body, &inner) == nil {
			body = json.RawMessage(inner)
		}
	}
	if len(body) > 0 && len(body) <= 64<<10 {
		var p struct {
			To              string `json:"to"`
			From            string `json:"from"`
			State           string `json:"state"`
			SessionState    string `json:"session_state"`
			WorkState       string `json:"work_state"`
			Provider        string `json:"provider"`
			Model           string `json:"model"`
			Workroot        string `json:"workroot"`
			WorkingDir      string `json:"working_dir"`
			TaskID          string `json:"task_id"`
			PendingApproval *bool  `json:"pending_approval"`
			PendingQuestion *bool  `json:"pending_question"`
			Tool            string `json:"tool"`
			Reason          string `json:"reason"`
			In              *int64 `json:"input"`
			Out             *int64 `json:"output"`
			CacheRead       *int64 `json:"cache_read"`
			CacheWrite      *int64 `json:"cache_write"`
			Name            string `json:"name"`
			Resource        string `json:"resource"`
			Subject         string `json:"subject"`
			Status          string `json:"status"`
			Version         string `json:"version"`
			PR              string `json:"pr"`
			PullRequest     string `json:"pull_request"`
			Title           string `json:"title"`
			Actor           string `json:"actor"`
			Task            string `json:"task"`
			AgentURN        string `json:"agent_urn"`
			LogicalAgent    string `json:"logical_agent_id"`
			AgentName       string `json:"agent_name"`
			ParentSession   string `json:"parent_session_id"`
			ReplacedBy      string `json:"replaced_by"`
			DeliveryState   string `json:"delivery_state"`
			Delivery        struct {
				State string `json:"state"`
			} `json:"delivery"`
		}
		if json.Unmarshal(body, &p) == nil {
			f.WorkState = clean(firstNonEmpty(p.To, p.WorkState), 40)
			f.From = clean(p.From, 40)
			f.SessionState = clean(firstNonEmpty(p.SessionState, p.State), 40)
			f.Provider, f.Model = clean(p.Provider, 60), clean(p.Model, 80)
			f.Workroot = clean(firstNonEmpty(p.Workroot, p.WorkingDir), 300)
			f.TaskRef = clean(firstNonEmpty(p.TaskID, p.Task), 60)
			if w.App != "" || strings.Contains(f.Kind, ".state_changed") || strings.HasPrefix(f.Kind, "alert.") || strings.HasPrefix(f.Kind, "fleet.") || strings.HasPrefix(f.Kind, "card.") || strings.HasPrefix(f.Kind, "workstream.") {
				f.Name = clean(firstNonEmpty(p.Name, firstNonEmpty(p.Resource, p.Subject)), 80)
				f.State = clean(firstNonEmpty(p.State, firstNonEmpty(p.To, p.Status)), 24)
				f.Version, f.PR = clean(p.Version, 40), clean(firstNonEmpty(p.PR, p.PullRequest), 140)
				f.Title, f.BusActor = clean(p.Title, 200), clean(p.Actor, 80)
			}
			f.AgentURN, f.LogicalAgentID, f.AgentName = clean(p.AgentURN, 200), clean(p.LogicalAgent, 120), clean(p.AgentName, 120)
			f.ParentSession, f.ReplacedBy = clean(p.ParentSession, 128), clean(p.ReplacedBy, 128)
			f.PendingApproval, f.PendingQuestion = p.PendingApproval, p.PendingQuestion
			f.Tool = clean(p.Tool, 80)
			f.DeliveryState = clean(firstNonEmpty(p.Delivery.State, p.DeliveryState), 40)
			if f.Kind == "session.usage" && (p.In != nil || p.Out != nil) {
				f.Usage = &[4]int64{val(p.In), val(p.Out), val(p.CacheRead), val(p.CacheWrite)}
			}
			if f.Reason == "" {
				f.Reason = clean(p.Reason, 80)
			}
		}
	}
	return f, true
}

func val(p *int64) int64 {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// clean bounds a value and drops control characters. It is not a redactor: only identifiers and
// enum-like fields are read this way.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Type maps a Tether kind to the stored event type; "" means the frame is state-only and gets no
// timeline row.
func Type(kind string) string {
	switch {
	case strings.HasPrefix(kind, "tool.call"):
		return "tool"
	case strings.HasPrefix(kind, "message."), kind == "session.message":
		return "message"
	case strings.Contains(kind, "approval"), strings.Contains(kind, "question"):
		return "approval"
	case kind == "session.compacted":
		return "compaction"
	case kind == "session.cleared":
		return "clear"
	case strings.HasSuffix(kind, ".error"), strings.HasSuffix(kind, ".failed"):
		return "error"
	case strings.HasPrefix(kind, "task."):
		return "task"
	case strings.HasPrefix(kind, "session."):
		return "lifecycle"
	}
	return ""
}

// Summary is built from the kind and allowlisted fields only, never from payload text.
func (f Frame) Summary() string {
	s := f.Kind
	switch {
	case f.WorkState != "":
		s += ": " + firstNonEmpty(f.From, "?") + " -> " + f.WorkState
	case f.Tool != "":
		s += ": " + f.Tool
	case f.SessionState != "":
		s += ": " + f.SessionState
	}
	return clean(s, 200)
}
