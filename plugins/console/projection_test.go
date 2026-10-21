package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

var injected = []string{"sk-live-SECRET123", "SECRET-TEXT-BODY", "SECRET-OUTPUT", "SECRET-EXCERPT", "SECRET-ARGS", "SECRET-RESULT", "SECRET-NESTED", "SECRET-URN", "SECRET-LABEL"}

// poison is merged into every payload and top-level row: free text, secrets,
// nested objects and unknown fields.
func poison() map[string]any {
	return map[string]any{
		"text": "SECRET-TEXT-BODY sk-live-SECRET123", "text_truncated": true, "output": "SECRET-OUTPUT",
		"excerpt": "SECRET-EXCERPT", "body": "SECRET-TEXT-BODY", "subject": "SECRET-TEXT-BODY",
		"args": map[string]any{"cmd": "SECRET-ARGS"}, "result": "SECRET-RESULT", "label": "SECRET-LABEL",
		"from_urn": "SECRET-URN", "to_urn": "SECRET-URN", "sender": "msg://agent/x/SECRET-URN",
		"extra": map[string]any{"deep": map[string]any{"v": "SECRET-NESTED"}}, "args_schema_fp": "SECRET-ARGS",
		"unknown_field": "sk-live-SECRET123",
	}
}

func withPoison(good map[string]any) map[string]any {
	m := poison()
	for k, v := range good {
		m[k] = v
	}
	return m
}

func hubRow(seq int, kind string, payload map[string]any) map[string]any {
	pj, _ := json.Marshal(payload)
	row := poison()
	delete(row, "label")
	row["seq"], row["at"], row["kind"], row["session_id"] = seq, "2026-10-20T10:00:00Z", kind, "s1"
	row["payload_json"] = string(pj)
	return row
}

func handleStub(t *testing.T, status int, body string, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer up.Close()
	return NewObjectHandler(up.URL, "").Handle(context.Background(), req)
}

func assertNoInjection(t *testing.T, body []byte) {
	t.Helper()
	for _, s := range append(injected, "text_truncated", "extra", "unknown_field", "args_schema_fp") {
		if strings.Contains(string(body), s) {
			t.Errorf("response leaked %q: %s", s, body)
		}
	}
}

func TestEventProjectionAllowsOnlyListedFieldsPerKind(t *testing.T) {
	good := map[string]map[string]any{
		"tool.call.started":      {"tool": "mcp__tether__send", "server": "tether"},
		"tool.call.finished":     {"tool": "Bash", "server": "x", "ok": false, "error_class": "timeout", "duration_ms": 12},
		"progress.checkpoint":    {"checkpoint_id": "c1", "state": "open", "attempt": 2, "turn_id": "t1"},
		"session.turn_output":    {"turn_id": "t1", "kind": "final", "stop_reason": "end_turn"},
		"session.turn_routed":    {"turn_id": "t1", "message_id": "m1", "thread_id": "th1", "state": "routed"},
		"session.state_changed":  {"from": "idle", "to": "busy", "exit_code": 0, "reasons": []any{"a_b", "c"}},
		"session.launch_failed":  {"reason": "no_worker", "runtime": "claude", "worker_id": "w1"},
		"session.stall_detected": {"reason": "no_output", "duration_ms": 5},
		"session.stall_cleared":  {"reason": "output", "duration_ms": 6},
		"message.wake.outcome":   {"message_id": "m1", "outcome": "woken", "attempt": 1},
		"fleet.node.down":        {"node_id": "n1", "from": "up", "to": "down", "reason": "ping"},
		"fleet.node.up":          {"node_id": "n1"},
		"fleet.node.degraded":    {"node_id": "n1", "reasons": []any{"disk"}},
	}
	for _, k := range []string{"sent", "queued", "delivered", "read", "consumed", "failed", "dead_lettered", "redriven", "canceled"} {
		good["message."+k] = map[string]any{"message_id": "m1", "thread_id": "th1", "state": k, "delivery_state": "x", "attempt": 3, "stage": "turn_submitted", "error_class": "e", "reason": "r", "at": "2026-10-20T10:00:01Z"}
	}
	if len(good) != len(eventPayloadAllowlist) {
		t.Fatalf("test covers %d kinds, allowlist has %d", len(good), len(eventPayloadAllowlist))
	}
	for kind, payload := range good {
		kind, payload := kind, payload
		t.Run(kind, func(t *testing.T) {
			res := handleStub(t, 200, mustJSON(map[string]any{"events": []any{hubRow(7, kind, withPoison(payload))}, "next_cursor": 9, "debug": "SECRET-TEXT-BODY"}),
				subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/events"})
			if res.Status != 200 {
				t.Fatalf("status %d: %s", res.Status, res.Body)
			}
			assertNoInjection(t, res.Body)
			var out struct {
				Events []map[string]any `json:"events"`
				Next   int              `json:"next_cursor"`
			}
			if err := json.Unmarshal(res.Body, &out); err != nil || len(out.Events) != 1 || out.Next != 9 {
				t.Fatalf("shape: %v %s", err, res.Body)
			}
			ev := out.Events[0]
			if ev["kind"] != kind || ev["session_id"] != "s1" || ev["seq"] != float64(7) {
				t.Errorf("top-level fields lost: %v", ev)
			}
			for k := range ev {
				switch k {
				case "seq", "at", "kind", "session_id", "payload_json", "id", "environment_id":
				default:
					t.Errorf("unexpected top-level field %q", k)
				}
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(ev["payload_json"].(string)), &got); err != nil {
				t.Fatal(err)
			}
			for k, v := range payload {
				if want, _ := json.Marshal(v); mustJSON(got[k]) != string(want) {
					t.Errorf("allowed field %s lost or altered: %v", k, got)
				}
			}
			for k := range got {
				if _, ok := payload[k]; !ok {
					t.Errorf("unexpected payload field %q", k)
				}
			}
		})
	}
}

func TestEventProjectionTurnOutputNeverCarriesText(t *testing.T) {
	res := handleStub(t, 200, mustJSON(map[string]any{"events": []any{hubRow(1, "session.turn_output", withPoison(map[string]any{"turn_id": "t1", "kind": "final", "stop_reason": "end_turn"}))}}),
		subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/events"})
	if !strings.Contains(string(res.Body), "stop_reason") {
		t.Fatal(string(res.Body))
	}
	assertNoInjection(t, res.Body)
}

func TestEventProjectionDropsUnknownKindsAndRejectsShapeViolations(t *testing.T) {
	row := hubRow(3, "session.output", map[string]any{"text": "SECRET-TEXT-BODY"})
	ok := hubRow(4, "tool.call.started", map[string]any{
		"tool": "has spaces SECRET-TEXT-BODY", "server": "UPPER SECRET-TEXT-BODY", "ok": "SECRET-TEXT-BODY"})
	bad := hubRow(5, "session.state_changed", map[string]any{"from": "idle", "to": "busy", "reasons": []any{"fine", "free text with SECRET-TEXT-BODY"}})
	res := handleStub(t, 200, mustJSON(map[string]any{"events": []any{row, ok, bad}}), subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/events"})
	var out struct{ Events []map[string]any }
	if json.Unmarshal(res.Body, &out) != nil || len(out.Events) != 2 {
		t.Fatalf("expected unknown kind dropped: %s", res.Body)
	}
	if strings.Contains(string(res.Body), "session.output") {
		t.Error("unknown kind forwarded")
	}
	assertNoInjection(t, res.Body)
	if strings.Contains(string(res.Body), "SECRET-TEXT-BODY") {
		t.Errorf("text-shaped value in a code field survived: %s", res.Body)
	}
}

func TestProjectedGETsFailClosedOnMalformedOrErrorBodies(t *testing.T) {
	reqs := []subprocess.HTTPRequest{
		{Method: "GET", Path: objectsPrefix + "/events"},
		{Method: "GET", Path: objectsPrefix + "/binding", RawQuery: "urn=msg%3A%2F%2Fagent%2Fagent-mux%2Fa1"},
		{Method: "GET", Path: objectsPrefix + "/sessions/s1/health"},
		{Method: "GET", Path: objectsPrefix + "/messages/m1/trace"},
	}
	for _, req := range reqs {
		for _, body := range []string{`not json SECRET-TEXT-BODY`, `[1,2]`, `null`, `{"events":"SECRET-TEXT-BODY"}`, `{"a":1} trailing SECRET-TEXT-BODY`} {
			if req.Path == objectsPrefix+"/events" || strings.Contains(body, "not json") || body == `[1,2]` || body == `null` || strings.Contains(body, "trailing") {
				res := handleStub(t, 200, body, req)
				if res.Status != 502 || strings.Contains(string(res.Body), "SECRET") {
					t.Errorf("%s %q: status %d body %s", req.Path, body, res.Status, res.Body)
				}
			}
		}
		res := handleStub(t, 500, `{"error":"SECRET-TEXT-BODY stack"}`, req)
		if res.Status != 500 || strings.Contains(string(res.Body), "SECRET") {
			t.Errorf("upstream error body forwarded: %d %s", res.Status, res.Body)
		}
	}
}

func TestBindingHealthTraceProjection(t *testing.T) {
	extra := `"text":"SECRET-TEXT-BODY","body":"SECRET-TEXT-BODY","nested":{"x":"SECRET-NESTED"},"token":"sk-live-SECRET123"`
	cases := []struct {
		name string
		req  subprocess.HTTPRequest
		body string
		want map[string]any
	}{
		{"binding", subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/binding", RawQuery: "urn=msg%3A%2F%2Fagent%2Fagent-mux%2Fa1"},
			`{"SessionID":"s1","LeaseExpiresAt":"2026-10-20T10:00:00Z","TargetURN":"SECRET-URN",` + extra + `}`,
			map[string]any{"SessionID": "s1", "LeaseExpiresAt": "2026-10-20T10:00:00Z"}},
		{"health", subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/sessions/s1/health"},
			`{"alive":true,"live_state":"idle","last_activity_at":"2026-10-20T10:00:00Z","pending_approval":false,"pending_question":true,"last_output":"SECRET-OUTPUT",` + extra + `}`,
			map[string]any{"alive": true, "live_state": "idle", "last_activity_at": "2026-10-20T10:00:00Z", "pending_approval": false, "pending_question": true}},
		{"trace", subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/messages/m1/trace"},
			`{"message_id":"m1","status":"failed","subject":"SECRET-TEXT-BODY","attempts":[{"stage":"consumed","turn_submitted_at":"2026-10-20T10:00:00Z","consumed_at":"2026-10-20T10:00:01Z","error":"SECRET-RESULT","body":"SECRET-TEXT-BODY"},"SECRET-NESTED"],` + extra + `}`,
			map[string]any{"message_id": "m1", "status": "failed", "attempts": []any{map[string]any{"stage": "consumed", "turn_submitted_at": "2026-10-20T10:00:00Z", "consumed_at": "2026-10-20T10:00:01Z"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := handleStub(t, 200, c.body, c.req)
			if res.Status != 200 {
				t.Fatal(res.Status, string(res.Body))
			}
			assertNoInjection(t, res.Body)
			if strings.Contains(string(res.Body), "SECRET") {
				t.Errorf("leak: %s", res.Body)
			}
			var got any
			_ = json.Unmarshal(res.Body, &got)
			if mustJSON(got) != mustJSON(c.want) {
				t.Errorf("got %s want %s", res.Body, mustJSON(c.want))
			}
		})
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
