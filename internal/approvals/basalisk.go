package approvals

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	proof "github.com/hollis-labs/libs/security/webauthn"
)

// WorkloadTokenSource obtains short-lived workload transport JWTs through the
// supported issuer lifecycle. Completion references and human proofs are never
// used as transport credentials.
type WorkloadTokenSource interface {
	Token(context.Context) (string, error)
}

type BasaliskConfig struct {
	Authority string `json:"authority"`
	Origin    string `json:"origin"`
	RPID      string `json:"rp_id"`
}

type basaliskStatus struct {
	QueryID    string    `json:"query_id"`
	Authority  string    `json:"authority"`
	RPID       string    `json:"rp_id"`
	Origin     string    `json:"origin"`
	CheckedAt  time.Time `json:"checked_at"`
	Completion struct {
		ID               string          `json:"completion_id"`
		ItemID           string          `json:"item_id"`
		ExpectedRevision int64           `json:"expected_revision"`
		AuthTime         time.Time       `json:"auth_time"`
		Evidence         json.RawMessage `json:"evidence"`
	} `json:"completion"`
	Credential proof.User `json:"credential"`
}

type BasaliskVerifier struct {
	config BasaliskConfig
	client *http.Client
	tokens WorkloadTokenSource
	party  *proof.Party
	now    func() time.Time
}

func httpsOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.String() == origin
}
func boundedClient(client *http.Client) *http.Client {
	c := http.Client{}
	if client != nil {
		c = *client
	}
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	return &c
}
func NewBasaliskVerifier(cfg BasaliskConfig, tokens WorkloadTokenSource, client *http.Client) (*BasaliskVerifier, error) {
	if !profileID(cfg.Authority) || !httpsOrigin(cfg.Origin) || !profileID(cfg.RPID) || tokens == nil {
		return nil, hitl.ErrInvalidRequest
	}
	u, _ := url.Parse(cfg.Origin)
	if cfg.RPID != u.Hostname() {
		return nil, hitl.ErrInvalidRequest
	}
	party, err := proof.NewParty(cfg.RPID, "Basalisk", []string{cfg.Origin})
	if err != nil {
		return nil, err
	}
	return &BasaliskVerifier{cfg, boundedClient(client), tokens, party, time.Now}, nil
}

func boundedCarrierJSON(raw []byte, limit int) error {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return hitl.ErrInvalidRequest
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	depth := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return hitl.ErrInvalidRequest
		}
		if v, ok := tok.(json.Delim); ok {
			if v == '{' || v == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 16 {
				return hitl.ErrInvalidRequest
			}
		}
	}
	if depth != 0 {
		return hitl.ErrInvalidRequest
	}
	if _, err := jcs.Transform(raw); err != nil {
		return hitl.ErrInvalidRequest
	}
	return nil
}
func readBoundedJSON(resp *http.Response, limit int, out any) error {
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		return ErrCompletionUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit+1)))
	if err != nil || boundedCarrierJSON(raw, limit) != nil {
		return ErrCompletionUnavailable
	}
	if err := strictJSON(raw, out); err != nil {
		return err
	}
	// Closed status/exchange contracts reject case aliases and missing fields,
	// including nested public-credential fields, without rejecting permitted nulls.
	encoded, err := json.Marshal(out)
	if err != nil {
		return ErrCompletionUnavailable
	}
	original, err := jcs.Transform(raw)
	if err != nil {
		return ErrCompletionUnavailable
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil || !bytes.Equal(original, canonical) {
		return ErrCompletionUnavailable
	}
	return nil
}
func (v *BasaliskVerifier) VerifyCompletion(ctx context.Context, in CompletionLookup) (json.RawMessage, error) {
	if v == nil || in.Authority != v.config.Authority || !profileID(in.ItemID) || !profileID(in.CompletionID) || !profileID(in.Audience) || in.ExpectedRevision < 1 {
		return nil, hitl.ErrUnauthorized
	}
	if boundedCarrierJSON(in.RequestJSON, 256<<10) != nil {
		return nil, hitl.ErrInvalidRequest
	}
	var req hitl.EnqueueRequest
	if json.Unmarshal(in.RequestJSON, &req) != nil || req.Kind != "approval" || extraString(req, "class") != "high" {
		return nil, hitl.ErrInvalidRequest
	}
	// Validate operation before contacting the authority, then check all evidence
	// against the same original bytes after the current status read.
	if _, digest, err := parseAssuranceOperation(req.Extra["operation"]); err != nil || digest != extraString(req, "op_digest") {
		return nil, hitl.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, err := v.tokens.Token(ctx)
	if err != nil || !validWorkloadToken(token) {
		return nil, ErrCompletionUnavailable
	}
	query := rand.Text()
	body, _ := json.Marshal(map[string]string{"completion_id": in.CompletionID, "query_id": query})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, v.config.Origin+"/assurance/v1/status", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	resp, err := v.client.Do(r)
	if err != nil {
		return nil, ErrCompletionUnavailable
	}
	defer resp.Body.Close()
	var status basaliskStatus
	if err = readBoundedJSON(resp, 64<<10, &status); err != nil {
		return nil, err
	}
	now := v.now()
	if status.QueryID != query || status.Authority != v.config.Authority || status.RPID != v.config.RPID || status.Origin != v.config.Origin || status.Completion.ID != in.CompletionID || status.Completion.ItemID != in.ItemID || status.Completion.ExpectedRevision != in.ExpectedRevision || status.CheckedAt.IsZero() || status.CheckedAt.After(now.Add(time.Second)) {
		return nil, hitl.ErrUnauthorized
	}
	// checked_at is diagnostic, never a lease: every invocation performs a new
	// nonce-bound authenticated read; no cached-good result survives an outage.
	return verifyCurrentEvidence(in, req, status, now, v.party)
}
