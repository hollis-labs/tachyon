package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

type levelProvider struct {
	assurance.Provider
	level assurance.Level
}

func (p levelProvider) Status(*http.Request) (assurance.Status, *assurance.Error) {
	if p.level == assurance.LevelNone {
		return assurance.Status{}, &assurance.Error{Status: 401, Code: "session_required", Message: "Sign in."}
	}
	return assurance.Status{Level: p.level}, nil
}

func TestTranscriptsGuardNeedsABrowserSession(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	do := func(level assurance.Level, auth string) int {
		reached = false
		r := httptest.NewRequest("GET", "/api/plugins/console/v1/transcripts/snapshot", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		transcriptsGuard(levelProvider{level: level}, next).ServeHTTP(w, r)
		return w.Code
	}
	if c := do(assurance.LevelNone, ""); c != 401 || reached {
		t.Errorf("no session: %d reached=%v", c, reached)
	}
	if c := do(assurance.LevelBrowserSession, "Bearer x"); c != 403 || reached {
		t.Errorf("agent token: %d reached=%v", c, reached)
	}
	if c := do(assurance.LevelBrowserSession, ""); c != 200 || !reached {
		t.Errorf("browser session: %d reached=%v", c, reached)
	}
}
