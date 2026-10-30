package alertstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
)

// ApprovalChange is what Alerts needs from the Approvals event feed: the item and its new state.
type ApprovalChange struct {
	Type   string // item.created|updated|resolved|expired|deferred|undeferred
	ItemID string
	State  string
}

// ApprovalEvents is the in-process Approvals feed. Subscribe returns a channel (closed when the feed
// drops a slow reader) and a cancel func. The server backs it with approvals.Changes.
type ApprovalEvents interface {
	Subscribe() (<-chan ApprovalChange, func())
}

// ApprovalSubject is the subject kind an approval alert carries; its ref is the queue item id.
const ApprovalSubject = "approval"

func resolutionFor(state string) string {
	switch state {
	case "approved":
		return "Approved"
	case "denied", "rejected":
		return "Denied"
	case "expired":
		return "Expired"
	case "":
		return "Resolved"
	}
	return "Resolved (" + alertsource.Clean(state, 30) + ")"
}

// settleWhere settles every open alert chosen by pick (which returns candidate ids inside the tx).
func (s *Store) settle(ctx context.Context, name, resolution, by, label string, pick func(ctx context.Context, tx *sql.Tx) ([]string, error)) (int, error) {
	var changes []change
	err := s.DB.Writer.Submit(ctx, name, func(ctx context.Context, tx *sql.Tx) error {
		changes = nil
		ids, err := pick(ctx, tx)
		if err != nil {
			return err
		}
		now := s.nowMS()
		for _, id := range ids {
			seq, err := nextSeq(ctx, tx)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE alerts SET status='settled', read=1, resolution=?, resolved_by=?, snooze_until_ms=NULL, wake_at_ms=NULL, defer_json=NULL,
 seq=?, version=version+1, updated_ms=? WHERE alert_id=?`, resolution, by, seq, now, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_events(alert_id, dedupe_key, at_ms, kind, label) VALUES (?,?,?,?,?)`,
				id, "settle|"+id+"|"+strconv.FormatInt(seq, 10), now, "alert.settled", alertsource.Clean(label+": "+resolution, 80)); err != nil {
				return err
			}
			changes = append(changes, change{id, "state", seq})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, c := range changes {
		if s.OnChange != nil {
			s.OnChange(c.id, c.seq, c.kind)
		}
	}
	return len(changes), nil
}

func openIDs(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SettleApproval settles the open alerts about an approval item once it is decided or expired. The alerts
// never decide anything; the Approvals endpoints do.
func (s *Store) SettleApproval(ctx context.Context, itemID, state string) (int, error) {
	if itemID == "" {
		return 0, nil
	}
	return s.settle(ctx, "alert_settle_approval", resolutionFor(state), "approvals", "Approval", func(ctx context.Context, tx *sql.Tx) ([]string, error) {
		cands, err := openIDs(ctx, tx, `SELECT alert_id FROM alerts WHERE status IN ('active','snoozed','deferred') AND instr(subjects_json, ?) > 0`, itemID)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, id := range cands {
			a, err := load(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			for _, sub := range a.Subjects {
				if sub.Kind == ApprovalSubject && sub.Ref == itemID {
					out = append(out, id)
					break
				}
			}
		}
		return out, nil
	})
}

// SettleCleared settles open alerts with the coalesce key when the source says the condition cleared.
func (s *Store) SettleCleared(ctx context.Context, key string) (int, error) {
	if key == "" {
		return 0, nil
	}
	return s.settle(ctx, "alert_settle_cleared", "Cleared by source", "source", "Cleared", func(ctx context.Context, tx *sql.Tx) ([]string, error) {
		return openIDs(ctx, tx, `SELECT alert_id FROM alerts WHERE coalesce_key = ? AND status IN ('active','snoozed','deferred')`, key)
	})
}

// clearedKey is the coalesce key an alert.cleared event settles: the explicit payload key, else the
// "raised:<subject>" key the alert.raised rule uses.
func clearedKey(e alertsource.Event) string {
	if k, _ := e.Payload["coalesce_key"].(string); k != "" {
		return alertsource.Clean(k, 200)
	}
	if len(e.Subjects) > 0 && e.Subjects[0].Ref != "" {
		return "raised:" + e.Subjects[0].Ref
	}
	return ""
}

// FollowApprovals settles approval alerts from the feed until ctx ends. A feed that drops this reader is
// re-subscribed after a pause; changes during the gap are not replayed (a later change or the operator
// still settles the alert).
func (s *Store) FollowApprovals(ctx context.Context, ev ApprovalEvents, onErr func(error)) {
	for ctx.Err() == nil {
		ch, cancel := ev.Subscribe()
	loop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case c, ok := <-ch:
				if !ok {
					break loop
				}
				if c.Type != "item.resolved" && c.Type != "item.expired" {
					continue
				}
				if _, err := s.SettleApproval(ctx, c.ItemID, c.State); err != nil && ctx.Err() == nil && onErr != nil {
					onErr(err)
				}
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// ---- technical details ----

var secretKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|authorization|cookie|api[_-]?key|private|credential|bearer)`)
var secretVal = regexp.MustCompile(`(?i)(bearer\s+\S+|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})`)

const (
	maxTechFields = 12
	maxTechValue  = 200
)

// technicalFields lists the top-level scalar payload values for the detail drawer: no nested data, no
// secret-looking keys, secret-looking values replaced, values cut. Keys are sorted so the view is stable.
func technicalFields(p map[string]any) []map[string]string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []map[string]string{}
	for _, k := range keys {
		if len(out) == maxTechFields {
			break
		}
		if secretKey.MatchString(k) || len(k) > 60 {
			continue
		}
		var v string
		switch x := p[k].(type) {
		case string:
			v = x
		case float64, bool, json.Number, int, int64:
			b, _ := json.Marshal(x)
			v = string(b)
		default:
			continue
		}
		v = secretVal.ReplaceAllString(alertsource.Clean(v, maxTechValue), "[redacted]")
		out = append(out, map[string]string{"label": k, "value": v})
	}
	return out
}

func techJSON(p map[string]any) string {
	b, _ := json.Marshal(technicalFields(p))
	return string(b)
}

// approvalBlock is the detail's approval block when the alert is about a queue item.
func (s *Store) approvalBlock(ctx context.Context, a alertsapi.Alert) any {
	for _, sub := range a.Subjects {
		if sub.Kind != ApprovalSubject || sub.Ref == "" {
			continue
		}
		b := map[string]any{"item_id": sub.Ref}
		if s.ApprovalState != nil {
			if st := s.ApprovalState(ctx, sub.Ref); st != "" {
				b["state"] = st
			}
		}
		return b
	}
	return nil
}
