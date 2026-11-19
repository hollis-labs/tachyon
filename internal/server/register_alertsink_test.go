package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

type sinkMail struct {
	mu   sync.Mutex
	sent []string
}

func (m *sinkMail) Send(_ context.Context, to, subject, body string, _ map[string]string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject+"|"+body)
	return "m1", nil
}

func TestAlertSinkNotifiesOnlyNeedsYouOrCriticalOncePerChange(t *testing.T) {
	m := &sinkMail{}
	k := &alertSink{to: "msg://agent/x/op", origin: "https://t.example", mail: m, seen: map[string]struct{}{}}
	need := alertsapi.Alert{AlertID: "al_1", Title: "Approve deploy", Summary: "waiting", NeedsYou: true, Severity: "warning"}
	k.notify(need, 5, true)
	k.notify(need, 5, true)                                                   // same change: no second notice
	k.notify(alertsapi.Alert{AlertID: "al_2", Severity: "info"}, 6, true)     // neither needs you nor critical
	k.notify(alertsapi.Alert{AlertID: "al_3", Severity: "critical"}, 7, true) // critical
	k.notify(need, 8, false)                                                  // quiet: no toast
	time.Sleep(100 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) != 2 {
		t.Fatalf("sent %v", m.sent)
	}
}
