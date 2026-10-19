package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
)

const TrustedPrefix = "/api/approvals/trusted/v1"

type AssuranceConfig struct {
	CurrentTrust   WorkloadTrustConfig    `json:"current_trust"`
	Authentication WorkloadAuthConfig     `json:"authentication"`
	Basalisk       BasaliskConfig         `json:"basalisk"`
	StatusExchange WorkloadExchangeConfig `json:"status_exchange"`
	Audiences      map[string]string      `json:"audiences"`
}

func LoadAssuranceConfig(path string) (*AssuranceConfig, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg AssuranceConfig
	if err = strictProfileJSON(raw, 64<<10, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type TrustedAPI struct {
	store          *Store
	decisions      *TrustedDecisions
	authentication *WorkloadAuthenticator
	now            func() time.Time
}

func NewTrustedAPI(store *Store, cfg *AssuranceConfig, current CurrentWorkloadTrust, client *http.Client) (*TrustedAPI, error) {
	if cfg == nil {
		return &TrustedAPI{}, nil
	} // known routes fail closed when disabled
	if cfg.StatusExchange.Issuer != cfg.Authentication.Issuer {
		return nil, hitl.ErrInvalidRequest
	}
	if current == nil {
		if cfg.CurrentTrust.Issuer != cfg.Authentication.Issuer {
			return nil, ErrCompletionUnavailable
		}
		var err error
		current, err = NewWorkloadTrustClient(cfg.CurrentTrust, client)
		if err != nil {
			return nil, err
		}
	}
	authentication, err := NewWorkloadAuthenticator(cfg.Authentication, current, client)
	if err != nil {
		return nil, err
	}
	tokens, err := NewWorkloadExchange(cfg.StatusExchange, client)
	if err != nil {
		return nil, err
	}
	verifier, err := NewBasaliskVerifier(cfg.Basalisk, tokens, client)
	if err != nil {
		return nil, err
	}
	decisions, err := NewTrustedDecisions(store, cfg.Basalisk.Authority, cfg.Audiences, verifier)
	if err != nil {
		return nil, err
	}
	for _, pin := range cfg.Authentication.Callers {
		for _, app := range pin.Applications {
			if _, ok := cfg.Audiences[app]; !ok {
				return nil, hitl.ErrInvalidRequest
			}
		}
	}
	return &TrustedAPI{store, decisions, authentication, time.Now}, nil
}

type trustedItem struct {
	ID        string          `json:"item_id"`
	Revision  int64           `json:"revision"`
	Status    string          `json:"status"`
	ExpiresAt time.Time       `json:"expires_at"`
	Operation json.RawMessage `json:"operation"`
	Digest    string          `json:"op_digest"`
}

func (a *TrustedAPI) item(ctx context.Context, id string, caller authenticatedCaller) (trustedItem, hitl.Record, error) {
	rec, err := a.store.Get(ctx, id)
	if err != nil {
		return trustedItem{}, rec, err
	}
	// Refuse unconfigured and out-of-scope items without leaking existence.
	if !slices.Contains(caller.Applications, rec.CallerScope) || rec.CallerScope == "cerberus" {
		return trustedItem{}, rec, hitl.ErrNotFound
	}
	var request hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &request) != nil || request.Kind != "approval" || extraString(request, "class") != "high" || rec.ExpiresAt == nil {
		return trustedItem{}, rec, hitl.ErrNotFound
	}
	op, digest, err := parseAssuranceOperation(request.Extra["operation"])
	if err != nil || digest != extraString(request, "op_digest") || op.Audience != a.decisions.audiences[rec.CallerScope] {
		return trustedItem{}, rec, hitl.ErrNotFound
	}
	state := string(rec.State)
	if rec.State == hitl.StatePresented || rec.State == hitl.StateInProgress {
		state = "pending"
		expires, _ := decimalSeconds(op.ExpiresAt)
		if !a.now().Before(*rec.ExpiresAt) || !a.now().Before(time.Unix(expires, 0)) {
			state = "expired"
		}
	}
	return trustedItem{rec.ItemID, rec.Revision, state, *rec.ExpiresAt, request.Extra["operation"], digest}, rec, nil
}
func (a *TrustedAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	operation := strings.TrimPrefix(r.URL.Path, TrustedPrefix+"/")
	scopes := map[string]string{"item": ItemReadScope, "complete": CompleteScope, "outcome": OutcomeReadScope}
	scope, ok := scopes[operation]
	if !ok {
		fail(w, 404, "not_found", "Unknown trusted approval operation.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method_not_allowed", "Use POST.")
		return
	}
	if a == nil || a.store == nil || a.decisions == nil || a.authentication == nil {
		fail(w, 503, "assurance_unconfigured", "Trusted assurance integration is not configured.")
		return
	}
	caller, err := a.authentication.authenticate(r.Context(), r, scope)
	if err != nil {
		status := 401
		if errors.Is(err, ErrCompletionUnavailable) {
			status = 503
		}
		if errors.Is(err, auth.ErrForbidden) {
			status = 403
		}
		fail(w, status, "assurance_auth_failed", "Trusted workload authentication could not be verified.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), caller.ExpiresAt)
	defer cancel()
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if err != nil {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	var cmd struct {
		ItemID           string `json:"item_id"`
		QueryID          string `json:"query_id,omitempty"`
		CompletionID     string `json:"completion_id,omitempty"`
		ExpectedRevision int64  `json:"expected_revision,omitempty"`
	}
	if exactProfile(raw, 2048, &cmd) != nil || !profileID(cmd.ItemID) {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	// No raw evidence, participant, operation snapshot or approval claim is a
	// valid delivery input. Read nonces bind recovery to a fresh lookup.
	if operation == "complete" {
		if !profileID(cmd.CompletionID) || cmd.ExpectedRevision < 1 || cmd.QueryID != "" {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
	} else if !profileID(cmd.QueryID) || cmd.CompletionID != "" || cmd.ExpectedRevision != 0 {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	item, rec, err := a.item(ctx, cmd.ItemID, caller)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	if operation == "item" {
		writeValue(w, struct {
			QueryID string      `json:"query_id"`
			Item    trustedItem `json:"item"`
		}{cmd.QueryID, item})
		return
	}
	if operation == "outcome" {
		// Lookup is independent of completion/key/proof eligibility. Transport
		// authentication remains current, but an expired proof cannot hide a commit.
		service := hitl.NewService(a.store, hitl.Options{Clock: a.now})
		result, err := service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: rec.ItemID, Caller: hitl.CallerAssertion{ApplicationID: rec.CallerScope}})
		if err != nil {
			writeHITLError(w, err)
			return
		}
		writeValue(w, struct {
			QueryID  string       `json:"query_id"`
			ItemID   string       `json:"item_id"`
			Revision int64        `json:"revision"`
			State    hitl.State   `json:"state"`
			Outcome  hitl.Outcome `json:"terminal_outcome"`
		}{cmd.QueryID, rec.ItemID, result.Item.Revision, result.Item.State, result.Item.TerminalOutcome})
		return
	}
	out, err := a.decisions.Complete(ctx, CompletionCommand{ItemID: cmd.ItemID, CompletionID: cmd.CompletionID, ExpectedRevision: cmd.ExpectedRevision})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompletionUnavailable):
			fail(w, 503, "assurance_unavailable", "Current completion verification is unavailable; use outcome lookup after an ambiguous delivery.")
		case errors.Is(err, ErrCompletionConflict):
			fail(w, 409, "completion_conflict", "Completion conflicts with the retained decision; use outcome lookup.")
		default:
			writeHITLError(w, err)
		}
		return
	}
	writeValue(w, struct {
		Outcome hitl.Outcome `json:"outcome"`
	}{out})
}
