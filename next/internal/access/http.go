package access

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	libauth "github.com/hollis-labs/libs/security/auth"
)

type agentKey struct{}

// FromContext returns the agent RequireAgent resolved for the request.
func FromContext(ctx context.Context) (Agent, bool) {
	a, ok := ctx.Value(agentKey{}).(Agent)
	return a, ok
}

// RequireAgent serves next only to a verified API key (or a token minted from one) that maps to an
// active service identity: anything else is 403, whatever its scopes. A signed-in person is not an
// agent here (the old build ignored browser cookies on the agent API too).
func RequireAgent(s *Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, _ := libauth.FromContext(r.Context())
		agent, err := s.Resolve(r.Context(), p)
		switch {
		case errors.Is(err, ErrNotAgent):
			writeError(w, http.StatusForbidden, "agent_required", "This route answers service credentials (identity API keys) only.")
		case errors.Is(err, ErrUnmapped):
			writeError(w, http.StatusForbidden, "credential_not_mapped", "This key acts for no service identity.")
		case errors.Is(err, ErrInactive):
			writeError(w, http.StatusForbidden, "credential_inactive", "This credential, or its service identity, is revoked, disabled or expired.")
		case err != nil:
			slog.Error("access: resolve the agent", "err", err)
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Access could not be checked.")
		default:
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentKey{}, agent)))
		}
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
