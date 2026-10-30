package chatworkspace

import (
	"context"
	"os"
	"unicode/utf8"
)

// InlineMax is the largest text item sent inline in client_context; bigger ones go by pointer.
const InlineMax = 8 << 10

// ClientContextItem is one shared workspace item as the agent sees it for a turn.
type ClientContextItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Mime    string `json:"mime,omitempty"`
	Size    int64  `json:"size_bytes,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	URL     string `json:"url,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Text    string `json:"text,omitempty"`    // text files <= 8 KiB, inline
	Pointer string `json:"pointer,omitempty"` // larger or binary files: pull through chat.workspace.get
}

// ClientContext is the client_context v1 payload for one turn: what is shared right now.
// Unsharing removes an item from the next turn; it cannot recall what an earlier turn saw.
type ClientContext struct {
	Version   int                 `json:"version"`
	Workspace []ClientContextItem `json:"workspace"`
}

// ClientContextFor builds the payload for a thread. It returns nil when nothing is shared.
func (s *Store) ClientContextFor(ctx context.Context, thread string) (*ClientContext, error) {
	items, _, err := s.List(ctx, thread)
	if err != nil {
		return nil, err
	}
	out := &ClientContext{Version: 1}
	for _, it := range items {
		if !it.Shared {
			continue
		}
		ci := ClientContextItem{ID: it.ID, Name: it.Name, Type: it.Type, Pointer: "chat.workspace.get:" + it.ID}
		if it.Mime != nil {
			ci.Mime = *it.Mime
		}
		if it.SizeBytes != nil {
			ci.Size = *it.SizeBytes
		}
		if it.SHA256 != nil {
			ci.SHA256 = *it.SHA256
		}
		if it.URL != nil {
			ci.URL, ci.Pointer = *it.URL, ""
		}
		if it.Ref != nil {
			ci.Ref, ci.Pointer = *it.Ref, ""
		}
		if it.Type == "file" && it.SizeBytes != nil && *it.SizeBytes <= InlineMax && isText(ci.Mime) {
			if b, err := os.ReadFile(s.Path(thread, it.ID)); err == nil && utf8.Valid(b) {
				ci.Text, ci.Pointer = string(b), ""
			}
		}
		out.Workspace = append(out.Workspace, ci)
	}
	if len(out.Workspace) == 0 {
		return nil, nil
	}
	return out, nil
}

func isText(mime string) bool {
	switch mime {
	case "text/plain", "text/markdown", "text/csv", "text/xml", "application/json":
		return true
	}
	return false
}
