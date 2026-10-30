package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/chatactivity"
	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// ChatActivity is what chat.activity.append and chat.panel.open need.
type ChatActivity struct {
	Rec    *chatactivity.Recorder
	Thread func(ctx context.Context, ref string) error // nil error = the thread exists
}

type chatActivityAppend struct {
	ThreadRef string              `json:"thread_ref"`
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Summary   string              `json:"summary,omitempty"`
	Cite      []chatactivity.Cite `json:"cite,omitempty"`
	ReviewRef *string             `json:"review_ref,omitempty"`
}
type chatPanelOpen struct {
	ThreadRef string `json:"thread_ref"`
	Panel     string `json:"panel"`
	Ref       string `json:"ref,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// agentActivityTypes are the only types an agent may write. The rest are written by Tachyon itself,
// so a feed entry such as card.answered or approval.decided can be trusted to be Tachyon's own.
var agentActivityTypes = map[string]bool{"task.closed": true, "note": true}

func chatActivityVerbs() []Verb {
	return []Verb{
		{Name: "chat.activity.append", Mutates: true, Description: "Add an entry to an app chat thread's activity feed (thread_ref looks like app:<id>). type is task.closed or note. title (up to 120 characters) and summary (up to 200) are shown to the operator; cite points at tasks, pull requests or files as {kind: task|pr|file, ref, label}; review_ref names what to review. The entry is attributed to you: you cannot write as the operator or as Tachyon.",
			InputSchema: inputSchema(reflect.TypeFor[chatActivityAppend](), []string{"thread_ref", "type", "title"})},
		{Name: "chat.panel.open", Mutates: true, Description: "Ask the operator's chat screen to open a side panel (thread_ref looks like app:<id>). panel is one of plan, topics, bookmarks, work, activity, approvals, files, doc; ref optionally names the item. This is a hint the screen may ignore, for example when the operator locked their layout. At most 6 per minute per thread.",
			InputSchema: inputSchema(reflect.TypeFor[chatPanelOpen](), []string{"thread_ref", "panel"})},
	}
}

func invokeChatActivity(ctx context.Context, c *ChatActivity, name string, raw json.RawMessage, principal auth.Principal) (json.RawMessage, error) {
	if c == nil || c.Rec == nil {
		return nil, svcerr.New(svcerr.CodeUnavailable, "Chat activity is not enabled on this host.")
	}
	var threadRef string
	var run func(t chatapi.ThreadRef) (any, error)
	actor := "agent:" + principal.ID
	switch name {
	case "chat.activity.append":
		var q chatActivityAppend
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		if !agentActivityTypes[q.Type] {
			return nil, invalid("type must be task.closed or note.")
		}
		if strings.TrimSpace(q.Title) == "" {
			return nil, invalid("An entry needs a title.")
		}
		threadRef = q.ThreadRef
		run = func(t chatapi.ThreadRef) (any, error) {
			if !c.Rec.Allow("append:"+t.String(), 30) {
				return nil, svcerr.New(svcerr.CodeConflict, "Too many activity entries for this chat; try again in a minute.")
			}
			e, err := c.Rec.Record(ctx, chatactivity.Entry{ThreadRef: t.String(), Type: q.Type, Actor: actor, Source: actor, Cite: q.Cite,
				Payload: chatactivity.Card(q.Title, q.Summary, q.ReviewRef)})
			if errors.Is(err, chatactivity.ErrInvalid) {
				return nil, invalid("An entry needs a title and at most 8 cites of kind task, pr or file.")
			}
			return e, err
		}
	case "chat.panel.open":
		var q chatPanelOpen
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		threadRef = q.ThreadRef
		run = func(t chatapi.ThreadRef) (any, error) {
			err := c.Rec.OpenPanel(ctx, t.String(), q.Panel, q.Ref, q.Reason, actor, actor)
			switch {
			case errors.Is(err, chatactivity.ErrRateLimited):
				return nil, svcerr.New(svcerr.CodeConflict, "That chat already had 6 panel requests this minute.")
			case errors.Is(err, chatactivity.ErrInvalid):
				return nil, invalid("panel must be plan, topics, bookmarks, work, activity, approvals, files or doc.")
			}
			return map[string]bool{"ok": err == nil}, err
		}
	default:
		return nil, denied()
	}
	t, err := chatapi.ParseRef(threadRef)
	if err != nil || t.Source != chatapi.SourceApp {
		return nil, invalid("thread_ref must be an app chat thread such as app:<id>.")
	}
	if c.Thread != nil && c.Thread(ctx, t.String()) != nil {
		return nil, svcerr.New(svcerr.CodeNotFound, "No such chat thread.")
	}
	out, err := run(t)
	if err != nil {
		var se *svcerr.Error
		if errors.As(err, &se) {
			return nil, err
		}
		return nil, svcerr.New(svcerr.CodeUnavailable, "Chat activity is unavailable.")
	}
	slog.Info("chat activity verb", "principal", principal.ID, "verb", name, "thread", t.String())
	return json.Marshal(out)
}
