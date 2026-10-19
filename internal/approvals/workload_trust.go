package approvals

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

// Wire contract: Tesseract01M5A6FC2ASZEM1YQHQXBVSNNF. The initial Tether
// implementation covers basalisk-status only; tachyon-assurance target support
// and separate narrow reader commissioning are required before activation.
type WorkloadTrustConfig struct {
	Origin          string `json:"origin"`
	Issuer          string `json:"issuer"`
	ReaderTokenFile string `json:"reader_token_file"`
}
type currentWorkload struct {
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Kind      string    `json:"kind"`
	Audience  string    `json:"audience"`
	Scopes    []string  `json:"scopes"`
	TokenID   string    `json:"token_id"`
	Parent    string    `json:"parent"`
	ExpiresAt time.Time `json:"expires_at"`
}
type WorkloadTrustClient struct {
	cfg    WorkloadTrustConfig
	watch  *tokenfile.Watcher
	client *http.Client
	now    func() time.Time
}

func NewWorkloadTrustClient(cfg WorkloadTrustConfig, client *http.Client) (*WorkloadTrustClient, error) {
	if !httpsOrigin(cfg.Origin) || !profileID(cfg.Issuer) || cfg.ReaderTokenFile == "" {
		return nil, hitl.ErrInvalidRequest
	}
	watch := tokenfile.NewWatcher(cfg.ReaderTokenFile, tokenfile.Options{Validate: validParentToken}, nil)
	return &WorkloadTrustClient{cfg, watch, boundedClient(client), time.Now}, nil
}
func (c *WorkloadTrustClient) CheckCurrentWorkload(ctx context.Context, p auth.Principal) error {
	if c == nil || !p.Verified || !profileID(p.ID) || p.Parent != p.ID || (p.Kind != auth.KindDevice && p.Kind != auth.KindService) || p.SessionID != "" || !profileID(p.TokenID) || p.Audience != AssuranceAudience || len(p.Scopes) == 0 || !c.now().Before(p.ExpiresAt) {
		return auth.ErrInvalidToken
	}
	for _, scope := range p.Scopes {
		if !trustedScope(scope) {
			return auth.ErrForbidden
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	secret, err := c.watch.Token()
	if err != nil {
		return ErrCompletionUnavailable
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return ErrCompletionUnavailable
	}
	query := base64.RawURLEncoding.EncodeToString(nonce)
	target := currentWorkload{c.cfg.Issuer, p.ID, p.Kind, p.Audience, append([]string(nil), p.Scopes...), p.TokenID, p.Parent, p.ExpiresAt.UTC()}
	body, _ := json.Marshal(struct {
		QueryID  string          `json:"query_id"`
		Workload currentWorkload `json:"workload"`
	}{query, target})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Origin+"/auth/workload-trust", bytes.NewReader(body))
	if err != nil {
		return ErrCompletionUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Tether-Protocol", "1")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	resp, err := c.client.Do(r)
	if err != nil {
		return ErrCompletionUnavailable
	}
	defer resp.Body.Close()
	var reply struct {
		QueryID   string          `json:"query_id"`
		Workload  currentWorkload `json:"workload"`
		Active    bool            `json:"active"`
		CheckedAt time.Time       `json:"checked_at"`
	}
	if readBoundedJSON(resp, 16<<10, &reply) != nil {
		return ErrCompletionUnavailable
	}
	w := reply.Workload
	if ctx.Err() != nil || !c.now().Before(p.ExpiresAt) || reply.QueryID != query || !reply.Active || reply.CheckedAt.IsZero() || reply.CheckedAt.After(c.now().Add(time.Second)) || w.Issuer != target.Issuer || w.Subject != target.Subject || w.Kind != target.Kind || w.Audience != target.Audience || !slices.Equal(w.Scopes, target.Scopes) || w.TokenID != target.TokenID || w.Parent != target.Parent || !w.ExpiresAt.Equal(target.ExpiresAt) {
		return auth.ErrInvalidToken
	}
	return nil
}
