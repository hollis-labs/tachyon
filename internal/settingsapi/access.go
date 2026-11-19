package settingsapi

import (
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

// AccessFor builds the extended access body from what the provider knows. The existing assurance.Access part is
// unchanged; the added fields describe the dialog: which methods exist, the session state, the lockout and
// whether step-up is supported. stepUp comes from assurance.StepUpSupported (false for TangentProvider).
func AccessFor(st assurance.Status, now time.Time, stepUp bool) Access {
	a := Access{Access: assurance.View(st, now), Methods: assurance.Methods(), Session: Session{State: "none"}, StepUp: StepUpSupport{Supported: stepUp}}
	switch {
	case st.Level >= assurance.LevelBrowserSession:
		a.Session.State = "active"
		if !st.ExpiredAt.IsZero() && !st.Elevated() && st.ExpiredAt.Before(now) {
			a.Session = Session{State: "expired", ExpiresAt: st.ExpiredAt.UTC().Format(time.RFC3339)}
		}
	}
	if st.LockedUntil.After(now) {
		a.Lockout = Lockout{Locked: true, RetryAfterS: int(st.LockedUntil.Sub(now).Seconds()) + 1}
	}
	return a
}

func methods(hardened bool) []Method {
	return []Method{{ID: MethodOperatorToken, Available: true}, {ID: MethodPassword, Available: hardened}, {ID: MethodPasskey, Available: hardened}}
}
