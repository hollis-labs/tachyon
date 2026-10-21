package planner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// ErrOperatorRequired is returned when an agent credential attempts a theme
// change that only the operator may make: approving, leaving approval, or
// changing the sequence. It is enforced here, in the service, so a plain patch
// or reorder cannot bypass the dedicated actions.
var ErrOperatorRequired = errors.New("operator authorization required")

// isOperator reports whether the transport established an operator actor.
// Agent credentials arrive as "service:<principal>".
func isOperator(actor string) bool {
	return actor == "operator" || strings.HasPrefix(actor, "operator:")
}

func themeApproved(row Item) bool { return textField(row, "status") == "approved" }

// themeActions are the lifecycle actions on a theme. Each takes only ids and
// small arguments and handles the revision check internally.
var themeActions = map[string]bool{"ready": true, "approve": true, "park": true, "unpark": true, "finish": true, "archive": true, "resequence": true, "append_state": true, "verify": true}

// IsThemeAction reports whether an action name belongs to the theme lifecycle.
func IsThemeAction(action string) bool { return themeActions[action] }

// guardTheme enforces who may change a theme's approval and sequence and stamps
// the approval fields. before is nil on create.
func guardTheme(before, after Item, actor string) error {
	// The earlier priority statuses stay readable on existing records but cannot
	// be written; an unchanged status passes so other edits to a legacy row work.
	if now := textField(after, "status"); legacyThemeStatus(now) && (before == nil || textField(before, "status") != now) {
		return fmt.Errorf("%w: status %q is retired for themes; use proposed, ready, approved, paused, done or archived", objects.ErrInvalid, now)
	}
	wasApproved := before != nil && themeApproved(before)
	nowApproved := themeApproved(after)
	sequenceChanged := after["order"] != nil
	if before != nil {
		sequenceChanged = rankDiffers(before["order"], after["order"])
	}
	// Parking is the one way out of approval an agent holding the write grant may
	// take; finishing, archiving and un-approving by other means stay with the
	// operator. Leaving approval releases the place in the sequence below.
	parking := wasApproved && !nowApproved && textField(after, "status") == "paused"
	if ((wasApproved != nowApproved && !parking) || sequenceChanged) && !isOperator(actor) {
		return fmt.Errorf("%w: approving, sequencing, finishing and archiving themes is operator-only; agents may park an approved theme", ErrOperatorRequired)
	}
	if nowApproved && !wasApproved {
		after["approved_by"], after["approved_at"] = actor, time.Now().UTC().Format(time.RFC3339Nano)
	}
	if wasApproved && !nowApproved {
		// Leaving approval releases the place in the sequence.
		delete(after, "order")
	}
	return nil
}

func legacyThemeStatus(s string) bool { return s == "draft" || s == "active" || s == "achieved" }

func rankDiffers(a, b any) bool {
	if a == nil || b == nil {
		return a != b
	}
	return number(a) != number(b)
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// themeAction mutates row for a lifecycle action. It runs inside change(), so
// guardTheme and the revision check apply on top of it.
func (s *Owned) themeAction(ctx context.Context, row Item, action string, args Item, actor string) error {
	status := textField(row, "status")
	audit := func(format string, a ...any) error {
		return appendComment(row, fmt.Sprintf(format, a...), "comment", actor)
	}
	switch action {
	case "ready":
		if err := needReason(args, actor); err != nil {
			return err
		}
		if status != "proposed" && status != "draft" {
			return fmt.Errorf("%w: only a proposed theme can be marked ready", objects.ErrInvalid)
		}
		row["status"] = "ready"
		return audit("ready: %s → ready%s", status, reason(args))
	case "approve":
		switch status {
		case "proposed", "ready", "paused", "draft", "active":
		default:
			return fmt.Errorf("%w: cannot approve a theme that is %s", objects.ErrInvalid, status)
		}
		row["status"] = "approved"
		order, err := s.sequenceFor(ctx, textField(row, "id"), args)
		if err != nil {
			return err
		}
		row["order"] = order
		return audit("approved: %s → approved at order %v%s", status, order, reason(args))
	case "park":
		if err := needReason(args, actor); err != nil {
			return err
		}
		if status == "paused" || status == "done" || status == "archived" {
			return fmt.Errorf("%w: theme is already %s", objects.ErrInvalid, status)
		}
		row["status"] = "paused"
		return audit("parked: %s → paused%s. Its Torque tasks are untouched.", status, reason(args))
	case "unpark":
		if err := needReason(args, actor); err != nil {
			return err
		}
		if status != "paused" {
			return fmt.Errorf("%w: only a paused theme can be unparked", objects.ErrInvalid)
		}
		row["status"] = "ready"
		return audit("unparked: paused → ready%s", reason(args))
	case "archive":
		// Declining a theme the operator does not want, with a reason.
		if !isOperator(actor) {
			return fmt.Errorf("%w: archiving a theme is operator-only", ErrOperatorRequired)
		}
		if status == "archived" {
			return fmt.Errorf("%w: theme is already archived", objects.ErrInvalid)
		}
		row["status"] = "archived"
		return audit("archived: %s → archived%s", status, reason(args))
	case "finish":
		if status == "done" || status == "archived" {
			return fmt.Errorf("%w: theme is already %s", objects.ErrInvalid, status)
		}
		row["status"] = "done"
		return audit("finished: %s → done%s", status, reason(args))
	case "append_state":
		text := strings.TrimSpace(textField(args, "text"))
		if text == "" {
			return objects.ErrInvalid
		}
		line := time.Now().UTC().Format("2006-01-02") + " " + actor + ": " + text
		if prev := strings.TrimSpace(textField(row, "state")); prev != "" {
			line = prev + "\n" + line
		}
		row["state"], row["last_verified"] = line, stamp()
		return nil
	case "verify":
		row["last_verified"] = stamp()
		if note := strings.TrimSpace(textField(args, "note")); note != "" {
			return audit("verified: %s", note)
		}
		return nil
	}
	return objects.ErrInvalid
}

// needReason makes an agent say why it moved a theme, so the audit trail
// explains the move. The operator may leave it out.
func needReason(args Item, actor string) error {
	if isOperator(actor) || strings.TrimSpace(textField(args, "reason")) != "" {
		return nil
	}
	return fmt.Errorf("%w: a reason is required", objects.ErrInvalid)
}

func reason(args Item) string {
	if r := strings.TrimSpace(textField(args, "reason")); r != "" {
		return " (" + r + ")"
	}
	return ""
}

// approvedThemes returns the approved themes in sequence, excluding exceptID.
func (s *Owned) approvedThemes(ctx context.Context, exceptID string) ([]objects.Record, error) {
	rows, err := s.records(ctx, "priorities")
	if err != nil {
		return nil, err
	}
	out := []objects.Record{}
	for _, r := range rows {
		if r.ID == exceptID || r.Deleted() {
			continue
		}
		if themeApproved(View(r)) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := View(out[i]), View(out[j])
		if number(a["order"]) != number(b["order"]) {
			return number(a["order"]) < number(b["order"])
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// sequenceFor picks the order value for a theme being approved: the end of the
// sequence, or just before the theme currently at args.position (1-based).
func (s *Owned) sequenceFor(ctx context.Context, id string, args Item) (float64, error) {
	approved, err := s.approvedThemes(ctx, id)
	if err != nil {
		return 0, err
	}
	pos := int(number(args["position"]))
	if pos < 1 || pos > len(approved) {
		last := 0.0
		if len(approved) > 0 {
			last = number(View(approved[len(approved)-1])["order"])
		}
		return last + 10, nil
	}
	next := number(View(approved[pos-1])["order"])
	prev := 0.0
	if pos > 1 {
		prev = number(View(approved[pos-2])["order"])
	}
	if next-prev > 1 {
		return prev + (next-prev)/2, nil
	}
	return next, fmt.Errorf("%w: no room before position %d; resequence first", objects.ErrConflict, pos)
}

// Resequence moves one approved theme to a 1-based position and renumbers the
// approved sequence in a single atomic write. Operator-only.
func (s *Owned) Resequence(ctx context.Context, id string, rev int64, position int, actor string) ([]Item, error) {
	if !isOperator(actor) {
		return nil, fmt.Errorf("%w: sequencing themes is operator-only", ErrOperatorRequired)
	}
	target, err := s.Resolve(ctx, "priorities", id)
	if err != nil {
		return nil, err
	}
	if rev < 1 || target.Revision != rev {
		return nil, objects.ErrConflict
	}
	if !themeApproved(View(target)) {
		return nil, fmt.Errorf("%w: only an approved theme has a place in the sequence", objects.ErrInvalid)
	}
	others, err := s.approvedThemes(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	if position < 1 || position > len(others)+1 {
		return nil, fmt.Errorf("%w: position must be 1..%d", objects.ErrInvalid, len(others)+1)
	}
	ordered := append([]objects.Record{}, others[:position-1]...)
	ordered = append(ordered, target)
	ordered = append(ordered, others[position-1:]...)
	changes := []objects.Replacement{}
	for i, r := range ordered {
		row := View(r)
		want := float64((i + 1) * 10)
		if r.ID != target.ID && number(row["order"]) == want {
			continue
		}
		row["order"] = want
		if r.ID == target.ID {
			if err := appendComment(row, fmt.Sprintf("resequenced: moved to position %d of %d", position, len(ordered)), "comment", actor); err != nil {
				return nil, err
			}
		}
		b, err := definition("priorities", row, r.Definition)
		if err != nil {
			return nil, err
		}
		changes = append(changes, objects.Replacement{ID: r.ID, ExpectedRevision: r.Revision, Definition: b})
	}
	rows, err := s.Objects.ReplaceDefinitions(ctx, changes, actor)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range rows {
		out = append(out, View(r))
	}
	return out, nil
}
