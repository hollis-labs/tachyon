package settingsapi

import (
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

// AccessFor builds the extended access body from what the provider knows. The existing assurance.Access part is
// unchanged; the added fields describe the dialog: which methods exist, the session state, the lockout and
// whether step-up is supported. Only the operator token is a method today, and no lockout or step-up is known
// without a Provider that reports them (ST4 supplies those inputs).
func AccessFor(st assurance.Status, now time.Time) Access {
	a := Access{Access: assurance.View(st, now), Methods: methods(false), Session: Session{State: "none"}}
	if st.Level >= assurance.LevelBrowserSession {
		a.Session.State = "active"
	}
	return a
}

// methods lists the sign-in methods; password and passkey share one availability flag until they differ.
func methods(hardened bool) []Method {
	return []Method{{MethodOperatorToken, true}, {MethodPassword, hardened}, {MethodPasskey, hardened}}
}

// Methods is the exported list for callers outside the package.
func Methods() []Method { return methods(false) }
