package approvals

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

const operationProfile = "privileged-operation-v1-draft"
const evidenceProfile = "operation-webauthn-v1-draft"

var ErrCompletionUnavailable = errors.New("trusted approval completion verifier unavailable")
var ErrCompletionConflict = errors.New("trusted approval completion conflicts with retained decision")

// CompletionLookup is built from the authoritative local item, never a browser's
// operation snapshot. RequestJSON is an isolated copy for the verifier to parse.
type CompletionLookup struct {
	Authority, Audience, CompletionID, ItemID string
	ExpectedRevision                          int64
	RequestJSON                               []byte
}

// CompletionVerifier is a SECURITY boundary, not an assertion decoder. Its
// implementation must authenticate lookup of the durable Basalisk completion,
// check current ceremony/key status, verify the shared operation profile/JCS
// digest and WebAuthn assertion, and enforce trusted human/key ownership. It must
// match the supplied authoritative item, operation and configured audience.
// Basalisk has already spent the ceremony and retained the exact verified result;
// retries revalidate that completion, never spend a new ceremony or mint proof.
// Return the original {binding,assertion} evidence only after ALL checks pass.
// BasaliskVerifier implements this using the released shared WebAuthn module;
// activation additionally requires current workload trust and enrolled callers.
type CompletionVerifier interface {
	VerifyCompletion(context.Context, CompletionLookup) (json.RawMessage, error)
}

type CompletionCommand struct {
	ItemID, CompletionID string
	ExpectedRevision     int64
}

// TrustedDecisions is an internal adapter, deliberately not an HTTP handler.
// Only a host-owned, authenticated ceremony integration may call it. Ordinary
// browser sessions and producer JWTs have no path to this authority.
type TrustedDecisions struct {
	store     *Store
	authority string
	audiences map[string]string // producer application -> executing audience
	verifier  CompletionVerifier
	now       func() time.Time
}

func NewTrustedDecisions(store *Store, authority string, audiences map[string]string, verifier CompletionVerifier) (*TrustedDecisions, error) {
	if store == nil || store.DB == nil || verifier == nil || !profileID(authority) || len(audiences) == 0 {
		return nil, ErrCompletionUnavailable
	}
	pinned := make(map[string]string, len(audiences))
	for application, audience := range audiences {
		// Cerberus remains on its existing app-specific passkey/handoff path.
		if !profileID(application) || !profileID(audience) || application == "cerberus" {
			return nil, fmt.Errorf("%w: unsupported trusted producer mapping", hitl.ErrInvalidRequest)
		}
		pinned[application] = audience
	}
	return &TrustedDecisions{store: store, authority: authority, audiences: pinned, verifier: verifier, now: time.Now}, nil
}

type operationBinding struct {
	Profile     string `json:"profile"`
	OperationID string `json:"operation_id"`
	Revision    string `json:"revision"`
	Requester   struct {
		Authority string `json:"authority"`
		Subject   string `json:"subject"`
	} `json:"requester"`
	Audience             string            `json:"audience"`
	Class                string            `json:"class"`
	ExpiresAt            string            `json:"expires_at"`
	AuthorizationDetails []json.RawMessage `json:"authorization_details"`
}

type proofBinding struct {
	Profile           string `json:"profile"`
	Authority         string `json:"authority"`
	HumanSubject      string `json:"human_subject"`
	KeyRef            string `json:"key_ref"`
	Audience          string `json:"audience"`
	OpDigest          string `json:"op_digest"`
	OperationID       string `json:"operation_id"`
	OperationRevision string `json:"operation_revision"`
	ItemID            string `json:"item_id"`
	Decision          string `json:"decision"`
	ProofID           string `json:"proof_id"`
	Nonce             string `json:"nonce"`
	IssuedAt          string `json:"issued_at"`
	ExpiresAt         string `json:"expires_at"`
}

type assuranceEvidence struct {
	Binding   proofBinding `json:"binding"`
	Assertion struct {
		CredentialID      string `json:"credential_id"`
		ClientDataJSON    string `json:"client_data_json"`
		AuthenticatorData string `json:"authenticator_data"`
		Signature         string `json:"signature"`
	} `json:"assertion"`
}

func profileID(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
func decimalSeconds(value string) (int64, bool) {
	if value == "" || len(value) > 12 || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n <= 253402300799 // through year 9999
}
func positiveRevision(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0
}
func canonicalBase64(value string, exact int) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) > 0 && (exact == 0 || len(raw) == exact) && base64.RawURLEncoding.EncodeToString(raw) == value
}

// strictProfileJSON rejects duplicate names and nulls before typed decoding.
// It is NOT a canonicalizer or cryptographic verifier; those belong to the
// shared profile verifier. The bound also applies before recursion/allocation.
func strictProfileJSON(raw []byte, max int, out any) error {
	if len(raw) == 0 || len(raw) > max || !utf8.Valid(raw) {
		return hitl.ErrInvalidRequest
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return hitl.ErrInvalidRequest
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return hitl.ErrInvalidRequest
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return hitl.ErrInvalidRequest
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return hitl.ErrInvalidRequest
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return hitl.ErrInvalidRequest
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return hitl.ErrInvalidRequest
	}
	return strictJSON(raw, out)
}

// Complete records an APPROVED high-tier completion. Denial remains a separate
// integration policy; no unsigned denial is promoted into this proof path.
func (a *TrustedDecisions) Complete(ctx context.Context, cmd CompletionCommand) (hitl.Outcome, error) {
	if a == nil || a.verifier == nil {
		return nil, ErrCompletionUnavailable
	}
	if !profileID(cmd.ItemID) || !profileID(cmd.CompletionID) || cmd.ExpectedRevision < 1 {
		return nil, hitl.ErrInvalidRequest
	}
	rec, err := a.store.Get(ctx, cmd.ItemID)
	if err != nil {
		return nil, err
	}
	audience, ok := a.audiences[rec.CallerScope]
	if !ok || rec.CallerScope == "cerberus" {
		return nil, hitl.ErrUnauthorized
	}
	var request hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &request) != nil || rec.Kind != "approval" || extraString(request, "class") != "high" || !operationDigest.MatchString(extraString(request, "op_digest")) {
		return nil, hitl.ErrInvalidRequest
	}
	var operation operationBinding
	if err := strictProfileJSON(request.Extra["operation"], 16<<10, &operation); err != nil {
		return nil, err
	}
	expiry, validExpiry := decimalSeconds(operation.ExpiresAt)
	if operation.Profile != operationProfile || operation.Class != "high" || operation.Audience != audience || !profileID(operation.OperationID) || !positiveRevision(operation.Revision) || !profileID(operation.Requester.Authority) || !profileID(operation.Requester.Subject) || !validExpiry || len(operation.AuthorizationDetails) != 1 {
		return nil, hitl.ErrInvalidRequest
	}
	// Check an immutable snapshot after verification too: the verifier receives
	// only copied bytes and cannot alter the record about to be resolved.
	evidence, err := a.verifier.VerifyCompletion(ctx, CompletionLookup{Authority: a.authority, Audience: audience, CompletionID: cmd.CompletionID, ItemID: cmd.ItemID, ExpectedRevision: cmd.ExpectedRevision, RequestJSON: append([]byte(nil), rec.Request...)})
	if err != nil {
		if errors.Is(err, hitl.ErrUnauthorized) || errors.Is(err, hitl.ErrInvalidRequest) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: completion could not be verified", ErrCompletionUnavailable)
	}
	evidence = append(json.RawMessage(nil), evidence...)
	var verified assuranceEvidence
	if err := strictProfileJSON(evidence, 32<<10, &verified); err != nil {
		return nil, err
	}
	b := verified.Binding
	issued, validIssued := decimalSeconds(b.IssuedAt)
	until, validUntil := decimalSeconds(b.ExpiresAt)
	if b.Profile != evidenceProfile || b.Authority != a.authority || b.Audience != audience || b.ItemID != rec.ItemID || b.OpDigest != extraString(request, "op_digest") || b.OperationID != operation.OperationID || b.OperationRevision != operation.Revision || b.Decision != "approved" || !profileID(b.HumanSubject) || !profileID(b.KeyRef) || !profileID(b.ProofID) || !canonicalBase64(b.Nonce, 32) || !validIssued || !validUntil || until <= issued || until-issued > 300 || until > expiry {
		return nil, hitl.ErrUnauthorized
	}
	if operation.Requester.Authority == b.Authority && operation.Requester.Subject == b.HumanSubject {
		return nil, hitl.ErrUnauthorized
	}
	if rec.ExpiresAt != nil && time.Unix(until, 0).After(*rec.ExpiresAt) {
		return nil, hitl.ErrUnauthorized
	}
	for _, v := range []string{verified.Assertion.CredentialID, verified.Assertion.ClientDataJSON, verified.Assertion.AuthenticatorData, verified.Assertion.Signature} {
		if !canonicalBase64(v, 0) {
			return nil, hitl.ErrUnauthorized
		}
	}
	response := hitl.Response{Kind: "approval", Decision: "approved", Extra: map[string]json.RawMessage{"assurance_evidence": evidence}}
	participant := hitl.Participant{Authority: a.authority, PrincipalRef: b.HumanSubject, Assurance: "webauthn-uv", Proof: &hitl.Proof{Scheme: evidenceProfile, KeyRef: b.KeyRef, Binds: []hitl.ProofBind{{Name: "op_digest", Value: b.OpDigest}, {Name: "item_id", Value: b.ItemID}, {Name: "proof_id", Value: b.ProofID}, {Name: "audience", Value: b.Audience}}}}
	// A local delivery fingerprint, NOT the signed operation's JCS digest.
	delivery, err := json.Marshal(struct {
		Response    hitl.Response
		Participant hitl.Participant
	}{response, participant})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(delivery)
	fingerprint := hex.EncodeToString(digest[:])
	var outcome hitl.Outcome
	err = a.store.DB.Writer.Submit(ctx, "approval_verified_completion", func(ctx context.Context, tx *sql.Tx) error {
		now := a.now()
		if err := ctx.Err(); err != nil {
			return err
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		if now.Before(time.Unix(issued, 0)) || !now.Before(time.Unix(until, 0)) {
			return hitl.ErrUnauthorized
		}
		current, err := readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", rec.ItemID))
		if err != nil {
			return err
		}
		if current.Digest != rec.Digest {
			return ErrCompletionConflict
		}
		var receiptItem, receiptCompletion, receiptDigest string
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT item_id,completion_id,expected_revision,completion_digest FROM approval_proof_receipts WHERE authority=? AND proof_id=?`, a.authority, b.ProofID).Scan(&receiptItem, &receiptCompletion, &revision, &receiptDigest)
		if err == nil {
			if receiptItem != cmd.ItemID || receiptCompletion != cmd.CompletionID || revision != cmd.ExpectedRevision || receiptDigest != fingerprint || current.State != hitl.StateResolved {
				return ErrCompletionConflict
			}
			outcome = current.Outcome
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM approval_proof_receipts WHERE item_id=? OR (authority=? AND completion_id=?))`, cmd.ItemID, a.authority, cmd.CompletionID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrCompletionConflict
		}
		// Reuse shared HITL lifecycle validation/CAS inside this same transaction.
		service := hitl.NewService(&completionTx{Store: a.store, tx: tx}, hitl.Options{Clock: func() time.Time { return now }})
		outcome, err = service.Respond(ctx, hitl.RespondCommand{ItemID: cmd.ItemID, ExpectedRevision: &cmd.ExpectedRevision, Response: response, Participant: participant})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO approval_proof_receipts(authority,proof_id,completion_id,item_id,expected_revision,completion_digest,recorded_at) VALUES(?,?,?,?,?,?,?)`, a.authority, b.ProofID, cmd.CompletionID, cmd.ItemID, cmd.ExpectedRevision, fingerprint, now.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return nil, err
	}
	return outcome, nil
}

// Only Respond is used on this private transactional adapter. Its Get and Swap
// never recurse into the serial write queue. Other services observe via Await's
// bounded store polling, as they do for changes from another process.
type completionTx struct {
	*Store
	tx *sql.Tx
}

func (s *completionTx) Get(ctx context.Context, id string) (hitl.Record, error) {
	return readRecord(s.tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id))
}
func (s *completionTx) Swap(ctx context.Context, id string, expected int64, next hitl.Record) error {
	return swapRecord(ctx, s.tx, id, expected, next)
}
