package approvals

// Application profile adapter for contract 01M598CCGC6DNT2PDBTS551T7A.
// Basalisk's internal/assurance/profile.go is the interoperability reference;
// cryptographic primitives are consumed from the published shared library.
import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	proof "github.com/hollis-labs/libs/security/webauthn"
)

type credentialRegistration struct {
	Type                string   `json:"type"`
	Action              string   `json:"action"`
	PrincipalID         string   `json:"principal_id"`
	OwnerID             string   `json:"owner_id"`
	Purpose             string   `json:"purpose"`
	CredentialExpiresAt string   `json:"credential_expires_at"`
	OverlapSeconds      string   `json:"overlap_seconds"`
	DestinationID       string   `json:"destination_id"`
	DestinationRevision string   `json:"destination_revision"`
	Scopes              []string `json:"scopes"`
}

// exactProfile additionally rejects case aliases, missing fields and invalid
// Unicode escapes. Do not use typed round trips on extensible HITL carriers.
func exactProfile(raw []byte, limit int, out any) error {
	if err := strictProfileJSON(raw, limit, out); err != nil {
		return err
	}
	original, err := jcs.Transform(raw)
	if err != nil {
		return hitl.ErrInvalidRequest
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return hitl.ErrInvalidRequest
	}
	normalized, err := jcs.Transform(encoded)
	if err != nil || !bytes.Equal(original, normalized) {
		return hitl.ErrInvalidRequest
	}
	return nil
}

func parseAssuranceOperation(raw []byte) (operationBinding, string, error) {
	var op operationBinding
	if err := exactProfile(raw, 16<<10, &op); err != nil {
		return op, "", err
	}
	expiry, ok := decimalSeconds(op.ExpiresAt)
	if op.Profile != operationProfile || op.Class != "high" || !profileID(op.OperationID) || !positiveRevision(op.Revision) || !profileID(op.Requester.Authority) || !profileID(op.Requester.Subject) || !profileID(op.Audience) || !ok || len(op.AuthorizationDetails) != 1 {
		return op, "", hitl.ErrInvalidRequest
	}
	var reg credentialRegistration
	if err := exactProfile(op.AuthorizationDetails[0], 16<<10, &reg); err != nil {
		return op, "", err
	}
	credentialExpiry, ok := decimalSeconds(reg.CredentialExpiresAt)
	if reg.Type != "credential_registration" || reg.Action != "create" || !profileID(reg.PrincipalID) || !profileID(reg.OwnerID) || !profileID(reg.DestinationID) || !positiveRevision(reg.DestinationRevision) || reg.OverlapSeconds != "0" || !ok || credentialExpiry <= expiry || reg.Purpose == "" || len(reg.Purpose) > 1024 || strings.IndexFunc(reg.Purpose, unicode.IsControl) >= 0 || len(reg.Scopes) < 1 || len(reg.Scopes) > 32 || !sort.StringsAreSorted(reg.Scopes) {
		return op, "", hitl.ErrInvalidRequest
	}
	for i, s := range reg.Scopes {
		if !profileID(s) || len(s) > 128 || i > 0 && s == reg.Scopes[i-1] {
			return op, "", hitl.ErrInvalidRequest
		}
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return op, "", hitl.ErrInvalidRequest
	}
	sum := sha256.Sum256(canonical)
	return op, "sha256:" + hex.EncodeToString(sum[:]), nil
}
func bindingChallenge(b proofBinding) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("hollis operation proof v1\x00"), canonical...))
	return sum[:], nil
}
func credentialKeyRef(id []byte) string {
	sum := sha256.Sum256(id)
	return "key_" + hex.EncodeToString(sum[:])
}

func verifyCurrentEvidence(in CompletionLookup, req hitl.EnqueueRequest, status basaliskStatus, now time.Time, party *proof.Party) (json.RawMessage, error) {
	op, digest, err := parseAssuranceOperation(req.Extra["operation"])
	if err != nil || digest != extraString(req, "op_digest") || op.Audience != in.Audience {
		return nil, hitl.ErrUnauthorized
	}
	var ev assuranceEvidence
	if err := exactProfile(status.Completion.Evidence, 32<<10, &ev); err != nil {
		return nil, err
	}
	b := ev.Binding
	start, startOK := decimalSeconds(b.IssuedAt)
	end, endOK := decimalSeconds(b.ExpiresAt)
	opEnd, _ := decimalSeconds(op.ExpiresAt)
	if b.Profile != evidenceProfile || b.Authority != in.Authority || b.Audience != in.Audience || b.ItemID != in.ItemID || b.OperationID != op.OperationID || b.OperationRevision != op.Revision || b.OpDigest != digest || b.Decision != "approved" || !profileID(b.HumanSubject) || !profileID(b.KeyRef) || !profileID(b.ProofID) || !canonicalBase64(b.Nonce, 32) || !startOK || !endOK || end <= start || end-start > 300 || end > opEnd || now.Before(time.Unix(start, 0)) || !now.Before(time.Unix(end, 0)) || req.ExpiresAt == nil || time.Unix(end, 0).After(*req.ExpiresAt) {
		return nil, hitl.ErrUnauthorized
	}
	if op.Requester.Authority == b.Authority && op.Requester.Subject == b.HumanSubject {
		return nil, hitl.ErrUnauthorized
	}
	if status.Completion.AuthTime.Before(time.Unix(start, 0)) || !status.Completion.AuthTime.Before(time.Unix(end, 0)) || status.Completion.AuthTime.After(now) {
		return nil, hitl.ErrUnauthorized
	}
	user := status.Credential
	if string(user.ID) != b.HumanSubject || len(user.Credentials) != 1 || credentialKeyRef(user.Credentials[0].ID) != b.KeyRef || base64.RawURLEncoding.EncodeToString(user.Credentials[0].ID) != ev.Assertion.CredentialID {
		return nil, hitl.ErrUnauthorized
	}
	for _, s := range []string{ev.Assertion.CredentialID, ev.Assertion.ClientDataJSON, ev.Assertion.AuthenticatorData, ev.Assertion.Signature} {
		if !canonicalBase64(s, 0) {
			return nil, hitl.ErrUnauthorized
		}
	}
	clientData, _ := base64.RawURLEncoding.DecodeString(ev.Assertion.ClientDataJSON)
	if len(clientData) > 16<<10 || boundedCarrierJSON(clientData, 16<<10) != nil {
		return nil, hitl.ErrUnauthorized
	}
	assertion, _ := json.Marshal(map[string]any{"id": ev.Assertion.CredentialID, "rawId": ev.Assertion.CredentialID, "type": "public-key", "response": map[string]string{"clientDataJSON": ev.Assertion.ClientDataJSON, "authenticatorData": ev.Assertion.AuthenticatorData, "signature": ev.Assertion.Signature}, "clientExtensionResults": map[string]any{}})
	challenge, err := bindingChallenge(b)
	if err != nil {
		return nil, err
	}
	// Current status owns a fresh decoded public user. Re-verifying a committed
	// assertion is not another counter event and must not change Basalisk state.
	user.Credentials[0].Authenticator.SignCount = 0
	user.Credentials[0].Authenticator.CloneWarning = false
	if _, err = party.VerifyAssertion(user, assertion, challenge, status.Origin); err != nil {
		return nil, hitl.ErrUnauthorized
	}
	return append(json.RawMessage(nil), status.Completion.Evidence...), nil
}
