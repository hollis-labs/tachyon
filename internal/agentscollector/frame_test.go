package agentscollector

import "testing"

func TestParseFrameDeliveryState(t *testing.T) {
	f, ok := ParseFrame([]byte(`{"id":"1","seq":5,"kind":"message.failed","time":"2026-10-30T10:00:00Z","payload":{"delivery":{"state":"retry_scheduled"}}}`))
	if !ok || f.DeliveryState != "retry_scheduled" {
		t.Fatalf("%v %+v", ok, f)
	}
}
