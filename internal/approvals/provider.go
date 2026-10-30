package approvals

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
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
	if t, e := time.Parse(time.RFC3339, ss.ElevationExpiresAt); e == nil {
		if st.Elevated() {
			st.ExpiresAt = t
		} else {
			st.ExpiredAt = t // UNVERIFIED: whether Tangent keeps reporting the expiry after the elevation ends
		}
	}
	st.LockedUntil = p.Bridge.lockedUntil()
	return st, nil
}

func (p *TangentProvider) Require(r *http.Request, a assurance.Action) (assurance.Status, *assurance.Error) {
	return assurance.RequireOn(p, r, a)
}

var _ assurance.Stepper = (*TangentProvider)(nil)

// Supported is false: Tangent has no step-up challenge, only the operator token elevation.
func (p *TangentProvider) Supported() bool { return false }

func (p *TangentProvider) Challenge(*http.Request, assurance.ChallengeRequest) (any, *assurance.Error) {
	return nil, assurance.Unsupported()
}

func (p *TangentProvider) Verify(*http.Request, assurance.VerifyRequest) (any, *assurance.Error) {
	return nil, assurance.Unsupported()
}

func (p *TangentProvider) Elevate(w http.ResponseWriter, r *http.Request)   { p.Bridge.ServeHTTP(w, r) }
func (p *TangentProvider) DeElevate(w http.ResponseWriter, r *http.Request) { p.Bridge.ServeHTTP(w, r) }

func (i *Inbox) access(w http.ResponseWriter, r *http.Request) {
	st, err := i.Assurance.Status(r)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeValue(w, settingsapi.AccessFor(st, time.Now(), assurance.StepUpSupported(i.Assurance)))
}

// stepUp serves POST /access/challenge and /access/verify through the Provider's optional Stepper. The exchange
// is specified (design section 4) but no Provider runs it yet, so the answer is 501 unsupported.
func (i *Inbox) stepUp(w http.ResponseWriter, r *http.Request, path string) {
	if _, err := i.Assurance.Status(r); err != nil {
		writeAssuranceError(w, err)
		return
	}
	st, ok := i.Assurance.(assurance.Stepper)
	if !ok || !st.Supported() {
		writeAssuranceError(w, assurance.Unsupported())
		return
	}
	body, e := commandBody(w, r)
	if e != nil {
		writeHITLError(w, e)
		return
	}
	var out any
	var aerr *assurance.Error
	if path == "/access/challenge" {
		var c assurance.ChallengeRequest
		if json.Unmarshal(body, &c) != nil || c.Action == "" {
			fail(w, 400, "invalid_request", "Send {action, ref?, digest?}.")
			return
		}
		out, aerr = st.Challenge(r, c)
	} else {
		var v assurance.VerifyRequest
		if json.Unmarshal(body, &v) != nil || v.ChallengeID == "" {
			fail(w, 400, "invalid_request", "Send {challenge_id, method, credential}.")
			return
		}
		out, aerr = st.Verify(r, v)
	}
	if aerr != nil {
		writeAssuranceError(w, aerr)
		return
	}
	writeValue(w, out)
}
