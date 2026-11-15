package agentscollector

import "testing"

func TestParseFrameDeliveryState(t *testing.T) {
	f, ok := ParseFrame([]byte(`{"id":"1","seq":5,"kind":"message.failed","time":"2026-10-30T10:00:00Z","payload":{"delivery":{"state":"retry_scheduled"}}}`))
	if !ok || f.DeliveryState != "retry_scheduled" {
		t.Fatalf("%v %+v", ok, f)
	}
}

func TestParseFrameLiveScopeObject(t *testing.T) {
	raw := `{"id":"env:41","seq":41,"kind":"session.output","session_id":"s1","time":"2026-10-31T02:00:00Z","scope":{"project":"x"},"source":{},"actor":{},"payload":{"line":"hi"}}`
	f, ok := ParseFrame([]byte(raw))
	if !ok || f.Seq != 41 || !f.Output {
		t.Fatalf("live frame with an object scope must parse: %+v %v", f, ok)
	}
}

func TestParseFrameTetherAppIsNative(t *testing.T) {
	f, ok := ParseFrame([]byte(`{"kind":"tool.call.started","app":"tether","id":"env:5","seq":5,"session_id":"s1","payload":{"tool":"x"}}`))
	if !ok || f.App != "" || f.SessionID != "s1" {
		t.Fatalf("%+v", f)
	}
	f, _ = ParseFrame([]byte(`{"kind":"task.state_changed","app":"torque","session_id":"s1"}`))
	if f.App != "torque" {
		t.Fatal(f.App)
	}
}

// Tether's message.failed carries "to" as an array; that must not blank the state, reason and sender.
func TestParseFrameMessageFailedWithRecipientArray(t *testing.T) {
	raw := `{"seq":5636379,"kind":"message.failed","scope":"daemon","at":"2026-11-15T00:49:01.174Z","payload_json":"{\"delivery\":{\"attempt\":1,\"state\":\"retry_scheduled\"},\"from\":\"msg://agent/agent-mux/agt_a\",\"message_id\":\"m1\",\"reason\":\"lease_expired\",\"to\":[\"msg://agent/agent-mux/agt_b\"]}"}`
	f, ok := ParseFrame([]byte(raw))
	if !ok || f.DeliveryState != "retry_scheduled" || f.Reason != "lease_expired" || f.WorkState != "" {
		t.Fatalf("%v %+v", ok, f)
	}
	if g, _ := ParseFrame([]byte(`{"seq":1,"kind":"session.work_state_changed","payload":{"from":"processing","to":"idle"}}`)); g.WorkState != "idle" || g.From != "processing" {
		t.Fatalf("a string 'to' still reads: %+v", g)
	}
}
