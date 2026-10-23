package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Response projection for the non-operator-gated Tether GET proxies.
//
// Everything the browser receives from /events, /binding, /sessions/{id}/health
// and /messages/{id}/trace is REBUILT from an explicit allowlist. The hub body
// is never forwarded. Free text (turn output excerpts, prompts, tool arguments
// and results, message bodies, checkpoint labels) cannot be named by an
// allowlist entry, and every allowed value must also pass a shape check, so a
// closed-code field cannot smuggle prose. Anything unparseable is an error.

type fieldKind int

const (
	fID    fieldKind = iota // opaque identifier
	fCode                   // closed code / enum-like token
	fTool                   // tool name (may contain mcp__server__tool)
	fTime                   // RFC 3339 timestamp
	fNum                    // JSON number
	fBool                   // JSON bool
	fCodes                  // short array of closed codes
	fText                   // bounded display text (operator-only projections)
)

const maxDisplayText = 600

var (
	reID   = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,128}$`)
	reCode = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
	reTool = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)
)

type fieldSet map[string]fieldKind

// eventPayloadAllowlist is the complete set of event kinds that reach the
// browser and, per kind, the only payload fields that survive.
var eventPayloadAllowlist = map[string]fieldSet{
	"tool.call.started":  {"tool": fTool, "server": fCode},
	"tool.call.finished": {"tool": fTool, "server": fCode, "ok": fBool, "error_class": fCode, "duration_ms": fNum},
	// label is free text: dropped. The UI falls back to "Reported progress".
	"progress.checkpoint":   {"checkpoint_id": fID, "state": fCode, "attempt": fNum, "turn_id": fID},
	"session.turn_output":   {"turn_id": fID, "kind": fCode, "stop_reason": fCode},
	"session.turn_routed":   {"turn_id": fID, "message_id": fID, "thread_id": fID, "state": fCode},
	"session.state_changed": {"from": fCode, "to": fCode, "exit_code": fNum, "reasons": fCodes},
	// Hub-emitted for every session (including claude-stream ones, which emit no tool events).
	"session.work_state_changed": {"from": fCode, "to": fCode, "at": fTime},
	"session.launch_failed":      {"reason": fCode, "reasons": fCodes, "runtime": fCode, "worker_id": fID, "exit_code": fNum},
	"session.stall_detected":     {"reason": fCode, "reasons": fCodes, "duration_ms": fNum},
	"session.stall_cleared":      {"reason": fCode, "reasons": fCodes, "duration_ms": fNum},
	"message.sent":               msgFields(),
	"message.queued":             msgFields(),
	"message.delivered":          msgFields(),
	"message.read":               msgFields(),
	"message.consumed":           msgFields(),
	"message.failed":             msgFields(),
	"message.dead_lettered":      msgFields(),
	"message.redriven":           msgFields(),
	"message.canceled":           msgFields(),
	"message.wake.outcome":       {"message_id": fID, "thread_id": fID, "outcome": fCode, "state": fCode, "attempt": fNum},
	"fleet.node.down":            fleetFields(),
	"fleet.node.up":              fleetFields(),
	"fleet.node.degraded":        fleetFields(),
}

func msgFields() fieldSet {
	return fieldSet{"message_id": fID, "thread_id": fID, "state": fCode, "delivery_state": fCode,
		"attempt": fNum, "stage": fCode, "error_class": fCode, "reason": fCode, "at": fTime}
}

func fleetFields() fieldSet {
	return fieldSet{"node_id": fID, "from": fCode, "to": fCode, "reason": fCode, "reasons": fCodes}
}

func allowedScalar(kind fieldKind, v any) (any, bool) {
	switch kind {
	case fID, fCode, fTool, fTime:
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		re := reID
		switch kind {
		case fCode:
			re = reCode
		case fTool:
			re = reTool
		case fTime:
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return nil, false
			}
			return s, true
		}
		return s, re.MatchString(s)
	case fText:
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		if r := []rune(s); len(r) > maxDisplayText {
			s = string(r[:maxDisplayText]) + "…"
		}
		return s, true
	case fNum:
		n, ok := v.(json.Number)
		return n, ok
	case fBool:
		b, ok := v.(bool)
		return b, ok
	case fCodes:
		arr, ok := v.([]any)
		if !ok || len(arr) > 8 {
			return nil, false
		}
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			s, ok := e.(string)
			if !ok || !reCode.MatchString(s) {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// pick copies only allowlisted, shape-valid fields.
func pick(src map[string]any, allow fieldSet) map[string]any {
	out := map[string]any{}
	for name, kind := range allow {
		if v, present := src[name]; present {
			if clean, ok := allowedScalar(kind, v); ok {
				out[name] = clean
			}
		}
	}
	return out
}

func decodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, errors.New("not a JSON object")
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	return m, nil
}

var eventTopLevel = fieldSet{"seq": fNum, "at": fTime, "kind": fCode, "session_id": fID, "environment_id": fID, "id": fID}

// projectEvents rebuilds a hub /events page. The UI consumes seq, at, kind,
// session_id, payload_json (a JSON string) and next_cursor.
func projectEvents(body []byte) ([]byte, error) {
	page, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	rows, ok := page["events"].([]any)
	if !ok {
		return nil, errors.New("events must be an array")
	}
	events := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			return nil, errors.New("event row must be an object")
		}
		kind, _ := row["kind"].(string)
		allow, known := eventPayloadAllowlist[kind]
		if !known {
			continue // unknown kinds are dropped, never forwarded empty
		}
		item := pick(row, eventTopLevel)
		if _, ok := item["seq"]; !ok {
			continue
		}
		if _, ok := item["at"]; !ok {
			continue
		}
		var payload map[string]any
		switch p := row["payload_json"].(type) {
		case string:
			if p != "" {
				if payload, err = decodeObject([]byte(p)); err != nil {
					payload = nil // unparseable payload: forward no payload
				}
			}
		case map[string]any:
			payload = p
		}
		clean, _ := json.Marshal(pick(payload, allow))
		item["payload_json"] = string(clean)
		events = append(events, item)
	}
	out := map[string]any{"events": events}
	if n, ok := allowedScalar(fNum, page["next_cursor"]); ok {
		out["next_cursor"] = n
	}
	return json.Marshal(out)
}

// projectBinding keeps the two fields the presence indicator reads.
func projectBinding(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pick(m, fieldSet{"SessionID": fID, "LeaseExpiresAt": fTime}))
}

func projectHealth(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pick(m, fieldSet{"alive": fBool, "live_state": fCode, "last_activity_at": fTime,
		"pending_approval": fBool, "pending_question": fBool}))
}

// projectTrace keeps delivery state only. attempts is the one allowed array of
// objects; each element is projected to three scalar fields.
func projectTrace(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	out := pick(m, fieldSet{"message_id": fID, "status": fCode})
	if raw, ok := m["attempts"].([]any); ok {
		attempts := []map[string]any{}
		for _, a := range raw {
			if obj, ok := a.(map[string]any); ok && len(attempts) < 50 {
				attempts = append(attempts, pick(obj, fieldSet{"stage": fCode, "turn_submitted_at": fTime, "consumed_at": fTime}))
			}
		}
		out["attempts"] = attempts
	}
	return json.Marshal(out)
}

var (
	rePlanID  = regexp.MustCompile(`^cp_[0-9a-f]{24}$`)
	reAgentID = regexp.MustCompile(`^agt_[a-z0-9]{10}$`)
)

// projectPlan keeps a catalog plan's summary and the path and action of each
// file. The diff, file hashes and caller identity never leave the hub.
func projectPlan(body []byte) ([]byte, error) {
	m, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	out := pick(m, fieldSet{"plan_id": fID, "name": fText, "task_id": fID, "state": fCode, "created_at": fTime, "expires_at": fTime,
		"applied_at": fTime, "approved_at": fTime, "validation_ok": fBool, "validation_error": fText, "rollback_of": fID})
	if raw, ok := m["files"].([]any); ok {
		files := []map[string]any{}
		for _, f := range raw {
			if obj, ok := f.(map[string]any); ok && len(files) < 100 {
				files = append(files, pick(obj, fieldSet{"path": fText, "action": fCode, "privileged": fBool}))
			}
		}
		out["files"] = files
		out["file_count"] = len(raw)
	}
	return json.Marshal(out)
}

// projectAgent finds one agent in a registry listing by its bare id (the last
// URN segment) and keeps its public profile. Callback, host address and kind
// metadata are redacted by the hub and are not named here either.
func projectAgent(id string) func([]byte) ([]byte, error) {
	return func(body []byte) ([]byte, error) {
		m, err := decodeObject(body)
		if err != nil {
			return nil, err
		}
		rows, ok := m["agents"].([]any)
		if !ok {
			return nil, errors.New("agents must be an array")
		}
		for _, r := range rows {
			row, ok := r.(map[string]any)
			if !ok {
				continue
			}
			urn, _ := row["urn"].(string)
			if !strings.HasSuffix(urn, "/"+id) {
				continue
			}
			out := pick(row, fieldSet{"urn": fText, "display_name": fText, "title": fText, "role": fText, "description": fText, "project": fText,
				"status": fCode, "health_status": fCode, "last_seen_at": fTime, "created_at": fTime, "updated_at": fTime})
			if tags, ok := row["tags"].([]any); ok {
				list := []string{}
				for _, t := range tags {
					if s, ok := allowedScalar(fText, t); ok && len(list) < 12 {
						list = append(list, s.(string))
					}
				}
				out["tags"] = list
			}
			return json.Marshal(out)
		}
		return nil, errAgentNotFound
	}
}

var errAgentNotFound = errors.New("agent not found")
