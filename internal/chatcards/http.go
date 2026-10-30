package chatcards

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes is mounted explicitly by the server under chatapi.Prefix.
var Routes = append([]string{"GET /threads/{ref}/cards"}, RuleRoutes...)

// Service lists a thread's cards.
type Service struct {
	Refs *Store
	Get  func(ctx context.Context, id string) (hitl.Record, error)
}

// Cards returns the thread's cards oldest first. state filters on the card state; since is a created_ns cursor.
func (s *Service) Cards(ctx context.Context, thread, state string, since int64) ([]Card, error) {
	refs, err := s.Refs.ForThread(ctx, thread, since, 200)
	if err != nil {
		return nil, err
	}
	now := s.Refs.now()
	out := []Card{}
	for _, r := range refs {
		rec, err := s.Get(ctx, r.ItemID)
		if err != nil {
			continue
		}
		c := Project(rec, r, now)
		if state == "" || c.State == state {
			out = append(out, c)
		}
	}
	return out, nil
}

// Handler serves GET chat/threads/{ref}/cards?state=&since=.
func Handler(s *Service, exists func(ctx context.Context, ref string) error) http.Handler {
	m := http.NewServeMux()
	ruleHandlers(m, chatapi.Prefix, s.Refs, exists)
	m.HandleFunc("GET "+chatapi.Prefix+"/threads/{ref}/cards", func(w http.ResponseWriter, r *http.Request) {
		ref, err := chatapi.ParseRef(r.PathValue("ref"))
		if err != nil {
			reply(w, 400, map[string]string{"code": "invalid", "message": err.Error()})
			return
		}
		if exists != nil {
			if err := exists(r.Context(), ref.String()); err != nil {
				reply(w, 404, map[string]string{"code": "not_found", "message": "That chat was not found."})
				return
			}
		}
		var since int64
		if v := r.URL.Query().Get("since"); v != "" {
			if since, err = strconv.ParseInt(v, 10, 64); err != nil {
				reply(w, 400, map[string]string{"code": "invalid", "message": "since must be a number."})
				return
			}
		}
		cards, err := s.Cards(r.Context(), ref.String(), r.URL.Query().Get("state"), since)
		if err != nil {
			reply(w, 500, map[string]string{"code": "internal", "message": "The cards could not be read."})
			return
		}
		reply(w, 200, map[string]any{"cards": cards})
	})
	return m
}

func threadOf(w http.ResponseWriter, r *http.Request, exists func(context.Context, string) error) (string, bool) {
	ref, err := chatapi.ParseRef(r.PathValue("ref"))
	if err != nil {
		reply(w, 400, map[string]string{"code": "invalid", "message": err.Error()})
		return "", false
	}
	if exists != nil {
		if err := exists(r.Context(), ref.String()); err != nil {
			reply(w, 404, map[string]string{"code": "not_found", "message": "That chat was not found."})
			return "", false
		}
	}
	return ref.String(), true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
