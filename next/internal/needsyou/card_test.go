package needsyou

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fetchingTether struct {
	*fakeTether
	byID map[string]Message
}

func (f fetchingTether) Message(_ context.Context, as, id string) (Message, error) {
	if as != Operator {
		panic("read as " + as)
	}
	return f.byID[id], nil
}

// Asks become read-only cards from what the sender wrote (typed envelope or the ask skill's interim payload);
// nothing is invented, and a pending ask outside the mailbox window is read by id.
func TestNeedsYouCardsReadTheAsk(t *testing.T) {
	decision := msg("t-decision", lead, "request", envelopeType, -time.Hour, map[string]any{
		"kind": "decision", "from": "@lead", "subject": "Keep or patch the dialog?", "body": "FormDialog ignores Escape.\n\nToken " + fakeToken() + " seen in logs.",
		"re": "task:CW-20261114-0027", "awaiting": "@chrispian", "default_option": "keep",
		"options": []map[string]any{{"id": "keep", "label": "Keep it", "recommended": true, "detail": "the reviewer agrees"}, {"id": "patch", "label": "Patch the kit", "detail": "a kit release"}, {"id": "BAD ID", "label": "dropped"}},
	}, nil)
	interim := msg("i-approval", builder, "request", "application/json", -2*time.Hour, map[string]any{
		"kind": "approval_request", "subject": "Deploy tachyon now?", "body": "Impact: 10s restart.\nRollback: redeploy the previous binary.",
		"options": []map[string]any{{"id": "go", "label": "Deploy", "recommended": true, "why": "tests are green"}, {"id": "wait", "label": "Wait for the window"}},
		"default": map[string]any{"option": "wait", "at": "2026-11-15T17:00Z"},
	}, nil)
	bare := msg("t-approval", builder, "request", envelopeType, -3*time.Hour, map[string]any{"kind": "approval_request", "from": "@builder-1", "body": "Restart the hub?"}, nil)
	ft := fetchingTether{
		fakeTether: &fakeTether{
			pending: []Pending{
				{MessageID: "t-decision", From: lead, Kind: "decision", Subject: "Keep or patch the dialog?", CreatedAt: t0.Add(-time.Hour)},
				{MessageID: "t-approval", From: builder, Kind: "approval_request", CreatedAt: t0.Add(-3 * time.Hour)},
			},
			boxes: map[string][]Message{Operator: {decision, interim}}, // t-approval is outside the window
		},
		byID: map[string]Message{"t-approval": bare},
	}
	r := (&Aggregator{Tether: ft}).Read(context.Background())
	by := map[string]Item{}
	for _, it := range r.Items {
		by[it.ID] = it
	}
	d := by["ask:t-decision"].Card
	if d == nil || d.Kind != "decision" || d.Subject != "Keep or patch the dialog?" || d.ReplyTo != "@lead" || d.DefaultOption != "keep" || d.Re != "task:CW-20261114-0027" || d.Awaiting != "@chrispian" {
		t.Fatalf("typed decision card: %+v", d)
	}
	if len(d.Options) != 2 || !d.Options[0].Recommended || d.Options[0].Detail != "the reviewer agrees" || d.Options[1].ID != "patch" {
		t.Fatalf("options (an invalid id dropped): %+v", d.Options)
	}
	if strings.Contains(d.Body, "ghp_") || !strings.Contains(d.Body, "FormDialog ignores Escape.\n\n") {
		t.Fatalf("body keeps its lines, redacted: %q", d.Body)
	}
	a := by["mail:i-approval"].Card
	if a == nil || a.Kind != "approval_request" || a.DefaultOption != "wait" || a.DefaultAt != "2026-11-15T17:00Z" || a.Options[0].Detail != "tests are green" ||
		!strings.Contains(a.Body, "Rollback: redeploy") || a.ReplyTo != "" {
		t.Fatalf("interim approval card (why as detail, default.at; no handle known): %+v", a)
	}
	b := by["ask:t-approval"]
	if b.Card == nil || len(b.Card.Options) != 2 || b.Card.Options[0].ID != "approve" || b.Card.Options[1].ID != "deny" || b.Card.ReplyTo != "@builder-1" || b.Title != "Restart the hub?" {
		t.Fatalf("an approval without options is approve/deny, read by id outside the window: %+v %+v", b, b.Card)
	}
}
