package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/tachyon/next/internal/access"
)

const NativeKind = "gate_native_v1"
const NativeAssessmentTTL = 5 * time.Second

// NativeGate adds an explicitly configured profile to the existing owner. Its
// dependencies must read current authority; absence never falls back to a token
// snapshot. It owns no decision store and grants no admission authority.
type NativeGate struct {
	OwnerRef, ProducerIdentity, CallerScope string
	CurrentUser                             func(context.Context, string) (identity.PublicUser, error)
	ProducerActive                          func(context.Context) error
	Authorize                               func(context.Context, ProposedResponse) (ResponseAuthorization, error)
}

type ProposedResponse struct {
	ContractVersion   string                  `json:"contract_version"`
	OwnerRef          string                  `json:"owner_ref"`
	IdempotencyKey    string                  `json:"idempotency_key"`
	ItemID            string                  `json:"item_id"`
	RequestDigest     runtimecontract.Digest  `json:"request_digest"`
	OperationDigest   runtimecontract.Digest  `json:"operation_digest"`
	Binding           runtimecontract.Binding `json:"binding"`
	NativeRequestID   gate.NativeRequestID    `json:"native_request_id"`
	ExpectedRevision  int64                   `json:"expected_revision"`
	PresentedRevision int64                   `json:"presented_projection_revision"`
	OptionID          string                  `json:"option_id"`
	Participant       hitl.Participant        `json:"participant"`
	ActorScopes       []string                `json:"actor_scopes"`
	AssessedAt        time.Time               `json:"assessed_at"`
	ExpiresAt         time.Time               `json:"expires_at"`
}

// ResponseAuthorization echoes the complete owner-attested proposal. A positive
// observation is usable only for this immediate owner CAS, never as a grant.
type ResponseAuthorization struct {
	ProposedResponse
	Authorized bool `json:"authorized"`
}

type AssessResponseCommand struct {
	hitl.GetCommand
	ExpectedTerminalRevision int64 `json:"expected_terminal_revision"`
}

type ResponseAssessment struct {
	IdempotencyKey   string                  `json:"idempotency_key"`
	Binding          runtimecontract.Binding `json:"binding"`
	NativeRequestID  gate.NativeRequestID    `json:"native_request_id"`
	ContractVersion  string                  `json:"contract_version"`
	OwnerRef         string                  `json:"owner_ref"`
	ItemID           string                  `json:"item_id"`
	RequestDigest    runtimecontract.Digest  `json:"request_digest"`
	OperationDigest  runtimecontract.Digest  `json:"operation_digest"`
	OptionID         string                  `json:"option_id"`
	TerminalRevision int64                   `json:"terminal_revision"`
	Participant      hitl.Participant        `json:"participant"`
	AssessedAt       time.Time               `json:"assessed_at"`
	ExpiresAt        time.Time               `json:"expires_at"`
}

// NativeRequest decodes the native JSON string without a float64 round trip or
// adding legacy title/class fields to the immutable request.
func NativeRequest(req hitl.EnqueueRequest) (gate.Request, error) {
	var text string
	var native gate.Request
	if req.Kind != NativeKind || json.Unmarshal(req.Extra["gate_request"], &text) != nil || text == "" || json.Unmarshal([]byte(text), &native) != nil {
		return native, fmt.Errorf("%w: gate_request must be a serialized native request string", hitl.ErrInvalidRequest)
	}
	if err := native.Validate(); err != nil {
		return native, fmt.Errorf("%w: invalid native request", hitl.ErrInvalidRequest)
	}
	if req.Validate() != nil || req.ExpiresAt == nil || !req.ExpiresAt.Equal(native.ExpiresAt) || req.IdempotencyKey != native.IdempotencyKey || req.Source.AgentID != string(native.Binding.OperativeID) {
		return native, fmt.Errorf("%w: native request binding, key and expiry must agree", hitl.ErrInvalidRequest)
	}
	for _, o := range native.Options {
		if o.Scope.Kind != gate.Once || o.Scope.MaxUses != 1 || o.Scope.Target != native.OperationID {
			return native, fmt.Errorf("%w: only exact operation-bound Once choices are supported", hitl.ErrInvalidRequest)
		}
	}
	return native, nil
}

func nativeOption(n gate.Request, id string, now time.Time) (gate.NativeOption, error) {
	if !now.Before(n.ExpiresAt) {
		return gate.NativeOption{}, hitl.ErrUnauthorized
	}
	for _, o := range n.Options {
		if o.ID == id && now.Before(o.Scope.ExpiresAt) {
			return o, nil
		}
	}
	return gate.NativeOption{}, fmt.Errorf("%w: choose an exact unexpired native option ID", hitl.ErrInvalidRequest)
}

func (g *NativeGate) active(ctx context.Context) error {
	if g == nil || g.OwnerRef == "" || g.ProducerIdentity == "" || g.CallerScope == "" || g.CurrentUser == nil || g.ProducerActive == nil || g.Authorize == nil {
		return &hitl.CodedError{Code: "unavailable", Message: "Native gate authority is unavailable."}
	}
	err := g.ProducerActive(ctx)
	if errors.Is(err, access.ErrInactive) || errors.Is(err, access.ErrNotFound) {
		return hitl.ErrUnauthorized
	}
	return err
}

func (g *NativeGate) currentParticipant(ctx context.Context, p hitl.Participant) ([]string, error) {
	if err := g.active(ctx); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p.PrincipalRef, "user:") || p.Authority != "tachyon/identity" || p.Assurance != "operator-token" || p.Responder == nil || p.Responder.Kind != "human-session" || p.Responder.Ref != p.PrincipalRef {
		return nil, hitl.ErrUnauthorized
	}
	u, err := g.CurrentUser(ctx, strings.TrimPrefix(p.PrincipalRef, "user:"))
	if err != nil {
		return nil, &hitl.CodedError{Code: "unavailable", Message: "Current responder authority could not be checked."}
	}
	current := libauth.Principal{Scopes: u.Scopes}
	if u.Disabled || u.ID != strings.TrimPrefix(p.PrincipalRef, "user:") || !current.HasScope("tachyon.operator") || !current.HasScope("tachyon.approvals.resolve") {
		return nil, hitl.ErrUnauthorized
	}
	return []string{"tachyon.operator", "tachyon.approvals.resolve"}, nil
}

func boundedExpiry(now time.Time, n gate.Request, o gate.NativeOption) time.Time {
	until := now.Add(NativeAssessmentTTL)
	for _, t := range []time.Time{n.ExpiresAt, o.Scope.ExpiresAt} {
		if t.Before(until) {
			until = t
		}
	}
	return until
}

// AssessResponse reads only: the responder comes exclusively from the actual
// immutable resolution, then current roles/mapping/expiry are checked anew.
func (i *Inbox) AssessResponse(ctx context.Context, q AssessResponseCommand) (ResponseAssessment, error) {
	g := i.Native
	if err := g.active(ctx); err != nil {
		return ResponseAssessment{}, err
	}
	if q.Validate() != nil || q.Caller.ApplicationID != g.CallerScope || q.ExpectedTerminalRevision < 1 {
		return ResponseAssessment{}, hitl.ErrInvalidRequest
	}
	// Read the retained record directly. Unlike Get/Await, assessment must not
	// materialize expiry (or perform any other owner transition).
	rec, err := i.Store.Get(ctx, q.ItemID)
	if err != nil {
		return ResponseAssessment{}, err
	}
	if rec.CallerScope != g.CallerScope || rec.Kind != NativeKind {
		return ResponseAssessment{}, hitl.ErrNotFound
	}
	if rec.Revision != q.ExpectedTerminalRevision {
		return ResponseAssessment{}, &hitl.StaleRevisionError{Operation: "assess-response", ItemID: q.ItemID, RevisionKind: "interaction", Expected: q.ExpectedTerminalRevision, Actual: rec.Revision, CurrentState: rec.State, Outcome: rec.Outcome}
	}
	var req hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &req) != nil {
		return ResponseAssessment{}, hitl.ErrInvalidRequest
	}
	n, err := NativeRequest(req)
	if err != nil {
		return ResponseAssessment{}, err
	}
	d, err := n.Digest()
	if err != nil {
		return ResponseAssessment{}, hitl.ErrUnauthorized
	}
	resolved, ok := rec.Outcome.(hitl.Resolved)
	if !ok || rec.State != hitl.StateResolved || resolved.InteractionRevision != rec.Revision || resolved.Resolution.Response.Kind != NativeKind {
		return ResponseAssessment{}, hitl.ErrUnauthorized
	}
	now := time.Now().UTC()
	o, err := nativeOption(n, resolved.Resolution.Response.Decision, now)
	if err != nil {
		return ResponseAssessment{}, err
	}
	if !resolved.Resolution.ResolvedAt.Before(o.Scope.ExpiresAt) {
		return ResponseAssessment{}, hitl.ErrUnauthorized
	}
	if _, err = g.currentParticipant(ctx, resolved.Resolution.Participant); err != nil {
		return ResponseAssessment{}, err
	}
	// Stamp after the current-authority reads, and check expiry again so a slow
	// lookup cannot return an already expired observation.
	now = time.Now().UTC()
	if _, err = nativeOption(n, resolved.Resolution.Response.Decision, now); err != nil {
		return ResponseAssessment{}, err
	}
	return ResponseAssessment{IdempotencyKey: n.IdempotencyKey, Binding: n.Binding, NativeRequestID: n.NativeRequestID, ContractVersion: gate.ContractVersion, OwnerRef: g.OwnerRef, ItemID: q.ItemID, RequestDigest: d, OperationDigest: n.OperationDigest, OptionID: resolved.Resolution.Response.Decision, TerminalRevision: rec.Revision, Participant: resolved.Resolution.Participant, AssessedAt: now, ExpiresAt: boundedExpiry(now, n, o)}, nil
}

func (i *Inbox) resolveNative(w http.ResponseWriter, r *http.Request, item browserItem, p hitl.Participant, expected, presented int64, response hitl.Response) {
	if response.Kind != NativeKind || response.Decision == "" || response.Note != "" || response.Reply != "" || len(response.Extra) != 0 {
		writeNativeError(w, hitl.ErrInvalidRequest)
		return
	}
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &req)
	n, err := NativeRequest(req)
	if err != nil {
		writeNativeError(w, err)
		return
	}
	g := i.Native
	scopes, err := g.currentParticipant(r.Context(), p)
	if err != nil {
		writeNativeError(w, err)
		return
	}
	if req.Source.ApplicationID != g.CallerScope {
		writeNativeError(w, hitl.ErrUnauthorized)
		return
	}
	principal, _ := libauth.FromContext(r.Context())
	if !principal.HasScope("tachyon.operator") || !principal.HasScope("tachyon.approvals.resolve") {
		writeNativeError(w, hitl.ErrUnauthorized)
		return
	}
	started := time.Now()
	now := started.UTC()
	o, err := nativeOption(n, response.Decision, now)
	if err != nil {
		writeNativeError(w, err)
		return
	}
	d, _ := n.Digest()
	proposal := ProposedResponse{ContractVersion: gate.ContractVersion, OwnerRef: g.OwnerRef, IdempotencyKey: n.IdempotencyKey, ItemID: item.ItemID, RequestDigest: d, OperationDigest: n.OperationDigest, Binding: n.Binding, NativeRequestID: n.NativeRequestID, ExpectedRevision: expected, PresentedRevision: presented, OptionID: o.ID, Participant: p, ActorScopes: scopes, AssessedAt: now, ExpiresAt: boundedExpiry(now, n, o)}
	a, err := g.Authorize(r.Context(), proposal)
	if err != nil {
		writeNativeError(w, err)
		return
	}
	got, _ := json.Marshal(a.ProposedResponse)
	want, _ := json.Marshal(proposal)
	if !a.Authorized || string(got) != string(want) || !time.Now().Before(a.ExpiresAt) {
		writeNativeError(w, hitl.ErrUnauthorized)
		return
	}
	if _, err = g.currentParticipant(r.Context(), p); err != nil {
		writeNativeError(w, err)
		return
	}
	if !time.Now().Before(a.ExpiresAt) {
		writeNativeError(w, hitl.ErrUnauthorized)
		return
	}
	if _, err = nativeOption(n, response.Decision, time.Now()); err != nil {
		writeNativeError(w, err)
		return
	}
	// Carry the echoed authorization window through writer contention, retries,
	// the actual CAS and transaction commit, rather than only checking it here.
	responseCtx, cancel := context.WithDeadline(context.WithValue(r.Context(), nativeAuthorizationKey{}, nativeAuthorization{Proposal: a.ProposedResponse, BudgetDeadline: started.Add(proposal.ExpiresAt.Sub(proposal.AssessedAt))}), a.ExpiresAt)
	defer cancel()
	out, err := i.Service.Respond(responseCtx, hitl.RespondCommand{ItemID: item.ItemID, ExpectedRevision: &expected, Response: response, Participant: p})
	if err != nil {
		// Drivers can report their own cancellation error when a transaction
		// waits on a lock. Keep this native deadline refusal consistent.
		if responseCtx.Err() != nil && !errors.Is(err, ErrNativeUnconfirmed) {
			err = responseCtx.Err()
		}
		writeNativeError(w, err)
		return
	}
	writeValue(w, out)
}

func nativeItem(item browserItem) bool {
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &req)
	return req.Kind == NativeKind
}

// Native failures retain the HITL wire. Legacy browser errors stay unchanged.
func writeNativeError(w http.ResponseWriter, err error) {
	status := 503
	switch {
	case errors.Is(err, hitl.ErrInvalidRequest):
		status = 400
	case errors.Is(err, hitl.ErrNotFound):
		status = 404
	case errors.Is(err, hitl.ErrUnauthorized):
		status = 403
	case errors.Is(err, hitl.ErrStaleRevision), errors.Is(err, hitl.ErrTerminalConflict), errors.Is(err, hitl.ErrIdempotencyConflict):
		status = 409
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 408
	}
	if status == 503 {
		err = &hitl.CodedError{Code: "unavailable", Message: "Native gate authority is unavailable; refresh before deciding."}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(json.RawMessage(hitl.EncodeError(err)))
}
