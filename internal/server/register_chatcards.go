package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatcards"
)

func init() { registerServerMount(mountChatCards) }

// mountChatCards serves GET chat/threads/{ref}/cards (T5). More specific than the chat stub's prefix, so it
// wins unless TACHYON_CHAT_STUBS=1 keeps the stub in charge.
func mountChatCards(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil || s.chatCards == nil || s.chatThreads == nil || os.Getenv("TACHYON_CHAT_STUBS") == "1" {
		return
	}
	svc := &chatcards.Service{Refs: s.chatCards.Refs, Get: s.chatCards.Get}
	exists := func(ctx context.Context, ref string) error { _, err := s.chatThreads.Agg.Get(ctx, ref); return err }
	h := operator.RequireOperator(chatcards.Handler(svc, exists))
	for _, route := range chatcards.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+chatapi.Prefix+path, h)
	}
}
