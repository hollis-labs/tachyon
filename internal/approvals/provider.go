package approvals

import (
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

// TangentProvider is the only assurance.Provider today: it reads the session the
// Tangent bridge verifies. It adds no policy of its own; the requirements table
// lives in the assurance package.
type TangentProvider struct{ Bridge *Bridge }

var _ assurance.Provider = (*TangentProvider)(nil)

func (p *TangentProvider) Status(r *http.Request) (assurance.Status, *assurance.Error) {
	part, ss, err := p.Bridge.lookup(r)
	if err != nil {
		return assurance.Status{}, err
	}
	st := assurance.Status{Level: assurance.ParseLevel(part.Assurance), TTLPolicy: ss.ElevationTTL, Ref: part.PrincipalRef}
	st.CanElevate = !st.Elevated()
	if t, e := time.Parse(time.RFC3339, ss.ElevationExpiresAt); e == nil && st.Elevated() {
		st.ExpiresAt = t
	}
	return st, nil
}

func (p *TangentProvider) Require(r *http.Request, a assurance.Action) (assurance.Status, *assurance.Error) {
	return assurance.RequireOn(p, r, a)
}

func (p *TangentProvider) Elevate(w http.ResponseWriter, r *http.Request)   { p.Bridge.ServeHTTP(w, r) }
func (p *TangentProvider) DeElevate(w http.ResponseWriter, r *http.Request) { p.Bridge.ServeHTTP(w, r) }

func (i *Inbox) access(w http.ResponseWriter, r *http.Request) {
	st, err := i.Assurance.Status(r)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeValue(w, assurance.View(st, time.Now()))
}
