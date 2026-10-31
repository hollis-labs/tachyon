package server

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// alertSink is AL7: a Tether notice for alerts that need the operator or are critical. Decision H4, taken as the
// design recommends: Tether notice only (no Web Push), off until TACHYON_ALERTS_NOTIFY_TO names a recipient URN,
// content limited to title, summary and a deep link, one notice per alert change (deduped by alert id and seq).
type alertSink struct {
	to     string
	origin string
	mail   interface {
		Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error)
	}
	mu   sync.Mutex
	seen map[string]struct{}
	ord  []string
}

// newAlertSink returns nil unless a recipient and a Tether message token are configured.
func (s *Server) newAlertSink() *alertSink {
	to := strings.TrimSpace(os.Getenv("TACHYON_ALERTS_NOTIFY_TO"))
	addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr)
	if to == "" || addr == "" || messageTokenFile(s) == "" {
		return nil
	}
	m := s.agentsSender()
	if m == nil {
		return nil
	}
	origin := strings.TrimRight(strings.TrimSpace(os.Getenv("TACHYON_PUBLIC_URL")), "/")
	return &alertSink{to: to, origin: origin, mail: m, seen: map[string]struct{}{}}
}

// notify sends the notice for one toast-worthy change of a needs-you or critical alert.
func (k *alertSink) notify(a alertsapi.Alert, seq int64, toast bool) {
	if k == nil || !toast || !(a.NeedsYou || a.Severity == "critical") {
		return
	}
	id := a.AlertID + "|" + itoa(seq)
	k.mu.Lock()
	if _, dup := k.seen[id]; dup {
		k.mu.Unlock()
		return
	}
	k.seen[id] = struct{}{}
	k.ord = append(k.ord, id)
	if len(k.ord) > 512 {
		delete(k.seen, k.ord[0])
		k.ord = k.ord[1:]
	}
	k.mu.Unlock()
	body := a.Summary
	if k.origin != "" {
		body += "\n" + k.origin + "/console/alerts/" + a.AlertID
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := k.mail.Send(ctx, k.to, "Alert: "+a.Title, body, map[string]string{"source": "tachyon-alerts", "alert_id": a.AlertID, "severity": a.Severity}); err != nil {
			slog.Warn("alerts: tether notice failed", "alert", a.AlertID, "err", err)
		}
	}()
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
