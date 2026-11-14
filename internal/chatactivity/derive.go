package chatactivity

import (
	"strings"
	"sync"
	"time"
)

// Derived activity: events Tachyon works out from a Tether session's tool calls, so the Activity tab shows what
// the agent is doing without the agent writing it. Only the sanitized tool name is read, never arguments.
const (
	TypeMessageSent = "message.sent"
	TypeMessageRead = "message.received"
	TypeTaskChecked = "task.checked"
	TypeTaskUpdated = "task.updated"
	TypeReminder    = "reminder.set"
	TypeWait        = "wait"
)

// Derived is what a tool call means for the feed.
type Derived struct{ Type, Title string }

// Classify maps a tool name (any MCP prefix) to a feed entry. ok is false for noise: file reads, edits, shell, search
// and every tool that is not about messaging, Torque, reminders or waiting.
func Classify(tool string) (d Derived, ok bool) {
	t := strings.ToLower(tool)
	if i := strings.LastIndex(t, "__"); i >= 0 {
		t = t[i+2:]
	}
	switch {
	case strings.HasPrefix(t, "tether_message_"):
		switch strings.TrimPrefix(t, "tether_message_") {
		case "send", "reply", "post", "notify", "redrive":
			return Derived{TypeMessageSent, "Sent a message"}, true
		case "inbox", "consume", "list", "get", "thread", "mark_read", "trace":
			return Derived{TypeMessageRead, "Read messages"}, true
		}
	case strings.HasPrefix(t, "tether_group_"):
		switch strings.TrimPrefix(t, "tether_group_") {
		case "post":
			return Derived{TypeMessageSent, "Posted to a group"}, true
		case "read", "mentions":
			return Derived{TypeMessageRead, "Read a group"}, true
		}
	case strings.HasPrefix(t, "torque_"):
		for _, read := range []string{"_get", "_list", "_search", "_facets", "_progress", "_status"} {
			if strings.HasSuffix(t, read) {
				return Derived{TypeTaskChecked, "Checked Torque"}, true
			}
		}
		for _, w := range []string{"comment_add", "task_update", "task_transition", "task_create", "subtodo_done", "task_checkpoint_emit"} {
			if strings.Contains(t, w) {
				return Derived{TypeTaskUpdated, "Updated Torque"}, true
			}
		}
	case t == "croncreate" || t == "schedulewakeup":
		return Derived{TypeReminder, "Set a reminder"}, true
	case t == "monitor" || t == "sleep":
		return Derived{TypeWait, "Waited"}, true
	}
	return Derived{}, false
}

// Throttle drops a repeat of the same entry on a thread inside Window, so a polling loop is one row, not hundreds.
type Throttle struct {
	Window time.Duration
	mu     sync.Mutex
	seen   map[string]time.Time
}

// Allow reports whether the entry (thread, type) may be written at now.
func (t *Throttle) Allow(thread, typ string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = map[string]time.Time{}
	}
	w := t.Window
	if w <= 0 {
		w = 30 * time.Second
	}
	k := thread + "\x00" + typ
	if last, ok := t.seen[k]; ok && now.Sub(last) < w {
		return false
	}
	if len(t.seen) > 4096 {
		clear(t.seen)
	}
	t.seen[k] = now
	return true
}
