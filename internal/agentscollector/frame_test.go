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
