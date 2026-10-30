package chatcards

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Binder ties asked cards to threads and reacts when they settle. Every hook may be nil.
type Binder struct {
	Refs *Store
	Get  func(ctx context.Context, id string) (hitl.Record, error)
	// Exists reports whether the thread is known; Bind fails when it is not.
	Exists func(ctx context.Context, thread string) error
	// Note adds the card to the thread's message list (app threads).
	Note func(ctx context.Context, thread, itemID, caption string, c Card)
	// Publish sends a stream event for the thread.
	Publish func(ctx context.Context, thread, typ string, fields map[string]any)
	// Answer tells the agent how the card was answered (app threads); the text is the operator message.
	Answer func(ctx context.Context, thread, text string)
	// Changes subscribes to the Approvals feed.
	Changes *approvals.Broker
}

// Bind is the card.ask hook.
func (b *Binder) Bind(ctx context.Context, itemID, kind, thread, message string) error {
	if _, err := chatapi.ParseRef(thread); err != nil {
		return fmt.Errorf("not a chat thread ref")
	}
	if b.Exists != nil {
		if err := b.Exists(ctx, thread); err != nil {
			return fmt.Errorf("that chat was not found")
		}
	}
	if err := b.Refs.Bind(ctx, itemID, thread, message, kind); err != nil {
		return err
	}
	rec, err := b.Get(ctx, itemID)
	if err != nil {
		return nil
	}
	ref, _ := b.Refs.Get(ctx, itemID)
	c := Project(rec, ref, b.Refs.now())
	if b.Note != nil {
		b.Note(ctx, thread, itemID, c.Caption, c)
	}
	b.publish(ctx, thread, c)
	return nil
}

func (b *Binder) publish(ctx context.Context, thread string, c Card) {
	if b.Publish != nil {
		b.Publish(ctx, thread, chatapi.EventCard, map[string]any{"item_id": c.ItemID, "kind": c.Kind, "state": c.State})
	}
}

// OpenCounts is the chatthreads CardBlocks hook.
func (b *Binder) OpenCounts(ctx context.Context, refs []string) map[string]int {
	return b.Refs.OpenCounts(ctx, refs)
}

// Follow reacts to answered and expired cards until ctx ends: a card event for the thread and, for an answer,
// a message to the agent. A reader the broker drops is re-subscribed after 1 s.
func (b *Binder) Follow(ctx context.Context) {
	for ctx.Err() == nil {
		_, ch, _ := b.Changes.Since(0, true)
	loop:
		for {
			select {
			case <-ctx.Done():
				b.Changes.Unsubscribe(ch)
				return
			case c, ok := <-ch:
				if !ok {
					break loop
				}
				if c.Type == "item.resolved" || c.Type == "item.expired" {
					b.settled(ctx, c.ItemID)
				}
			}
		}
		b.Changes.Unsubscribe(ch)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (b *Binder) settled(ctx context.Context, itemID string) {
	ref, ok := b.Refs.Get(ctx, itemID)
	if !ok {
		return
	}
	rec, err := b.Get(ctx, itemID)
	if err != nil {
		return
	}
	c := Project(rec, ref, b.Refs.now())
	b.publish(ctx, ref.ThreadRef, c)
	if c.State == "resolved" && b.Answer != nil {
		b.Answer(ctx, ref.ThreadRef, AnswerText(c))
	}
}

// AnswerText is the operator message the agent receives for an answered card.
func AnswerText(c Card) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Card answered: %q (%s).", c.Caption, c.ItemID)
	if d, _ := c.Result["decision"].(string); d != "" {
		fmt.Fprintf(&sb, " Decision: %s.", d)
	}
	if a, ok := c.Result["answers"]; ok {
		fmt.Fprintf(&sb, " Answers: %s.", compact(a))
	}
	if n, _ := c.Result["note"].(string); n != "" {
		fmt.Fprintf(&sb, " Note: %s", n)
	}
	return strings.TrimSpace(sb.String())
}

func compact(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, compact(e))
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+compact(t[k]))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}
