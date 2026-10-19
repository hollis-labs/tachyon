package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const ProducerPrefix = "/api/approvals/v1"

type ProducerPolicy struct {
	ApplicationID  string   `json:"application_id"`
	MinimumClass   string   `json:"minimum_class"`
	HandoffOrigins []string `json:"handoff_origins,omitempty"`
}
type ProducerConfig struct {
	Issuer           string                    `json:"issuer"`
	Audience         string                    `json:"audience"`
	JWKSURL          string                    `json:"jwks_url,omitempty"`
	BootstrapKeyFile string                    `json:"bootstrap_key_file,omitempty"`
	Producers        map[string]ProducerPolicy `json:"producers"` // exact JWT subject -> application
}
type Producer struct {
	Service  *hitl.Service
	Verifier auth.Verifier
	Config   ProducerConfig
}

func LoadProducerConfig(path string) (ProducerConfig, error) {
	var cfg ProducerConfig
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if len(raw) > 64<<10 {
		return cfg, errors.New("approval producer config too large")
	}
	if err = strictJSON(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Issuer == "" || cfg.Audience != "tachyon" || len(cfg.Producers) == 0 {
		return cfg, errors.New("approval producers require issuer, audience tachyon and exact subject mappings")
	}
	if cfg.JWKSURL != "" {
		u, err := url.Parse(cfg.JWKSURL)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || upstreamauth.Guard(cfg.JWKSURL, "public-jwks") != nil {
			return cfg, errors.New("invalid approval JWKS URL")
		}
	}
	seen := map[string]bool{}
	for subject, p := range cfg.Producers {
		if strings.TrimSpace(subject) == "" || p.ApplicationID == "" || len(p.ApplicationID) > 128 || seen[p.ApplicationID] || tier(p.MinimumClass) == 0 {
			return cfg, errors.New("approval producers require distinct application IDs and valid minimum classes")
		}
		if p.ApplicationID == "cerberus" && p.MinimumClass != "high" {
			return cfg, errors.New("Cerberus requests require provider handoff until its proof verifier is available")
		}
		seen[p.ApplicationID] = true
		for _, origin := range p.HandoffOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return cfg, errors.New("handoff origins must be exact HTTPS origins")
			}
		}
	}
	return cfg, nil
}
func NewProducer(ctx context.Context, service *hitl.Service, cfg ProducerConfig) (*Producer, error) {
	p := &Producer{Service: service, Config: cfg}
	if cfg.Issuer == "" {
		return p, nil
	} // disabled, never a permissive verifier
	v, err := auth.NewJWTVerifier(auth.JWTConfig{Issuer: cfg.Issuer, Audience: cfg.Audience, JWKSURL: cfg.JWKSURL, BootstrapKeyFile: cfg.BootstrapKeyFile, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
	if err != nil {
		return nil, err
	}
	p.Verifier = v
	go v.Run(ctx)
	return p, nil
}
func tier(class string) int {
	switch class {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return 0
}

var operationDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func extraString(req hitl.EnqueueRequest, key string) string {
	var value string
	_ = json.Unmarshal(req.Extra[key], &value)
	return value
}
func validateInboxRequest(req hitl.EnqueueRequest, policy ProducerPolicy) error {
	if req.Kind != "approval" && req.Kind != "attention" {
		return fmt.Errorf("%w: supported kinds are approval and attention", hitl.ErrInvalidRequest)
	}
	class := extraString(req, "class")
	if req.Source.ApplicationID == "cerberus" && class != "high" {
		return fmt.Errorf("%w: Cerberus requests are provider-handoff only", hitl.ErrInvalidRequest)
	}
	if tier(class) == 0 || tier(class) < tier(policy.MinimumClass) || !operationDigest.MatchString(extraString(req, "op_digest")) {
		return fmt.Errorf("%w: class must meet producer policy and op_digest must be sha256:<64 lowercase hex>", hitl.ErrInvalidRequest)
	}
	for _, key := range []string{"title", "summary", "request"} {
		value := extraString(req, key)
		if strings.TrimSpace(value) == "" || len(value) > 32<<10 {
			return fmt.Errorf("%w: bounded title, summary and request strings are required", hitl.ErrInvalidRequest)
		}
	}
	handoff := extraString(req, "handoff_url")
	if class == "high" && handoff == "" {
		return fmt.Errorf("%w: high tier requires a provider handoff URL", hitl.ErrInvalidRequest)
	}
	if handoff != "" {
		u, err := url.Parse(handoff)
		allowed := false
		if err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
			for _, origin := range policy.HandoffOrigins {
				if origin == u.Scheme+"://"+u.Host {
					allowed = true
				}
			}
		}
		if !allowed {
			return fmt.Errorf("%w: handoff URL must use an allowed provider HTTPS origin", hitl.ErrInvalidRequest)
		}
	}
	return nil
}
func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid JSON command", hitl.ErrInvalidRequest)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%w: exactly one JSON command is required", hitl.ErrInvalidRequest)
	}
	return nil
}
func commandBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return nil, fmt.Errorf("%w: use application/json", hitl.ErrInvalidRequest)
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized command", hitl.ErrInvalidRequest)
	}
	return raw, nil
}
func writeValue(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func writeHITLError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, hitl.ErrInvalidRequest):
		status = 400
	case errors.Is(err, hitl.ErrNotFound):
		status = 404
	case errors.Is(err, hitl.ErrUnauthorized):
		status = 403
	case errors.Is(err, hitl.ErrStaleRevision), errors.Is(err, hitl.ErrIdempotencyConflict), errors.Is(err, hitl.ErrTerminalConflict):
		status = 409
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 408
	}
	if status == 500 {
		fail(w, status, "unavailable", "Approval storage is unavailable; fetch the retained item before retrying.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(hitl.EncodeError(err))
}
func (p *Producer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	operation := strings.TrimPrefix(r.URL.Path, ProducerPrefix+"/")
	if operation != "enqueue" && operation != "get" && operation != "await" && operation != "withdraw" {
		fail(w, 404, "not_found", "Unknown producer operation.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method_not_allowed", "Use POST.")
		return
	}
	// Producer bearer credentials are never accepted as browser decision authority.
	if p.Verifier == nil {
		fail(w, 503, "producer_auth_unconfigured", "Configure the existing Tether JWT trust and producer identities.")
		return
	}
	token, state := auth.ParseBearer(r.Header.Values("Authorization"))
	if state != "" {
		fail(w, 401, "unauthenticated", "A producer bearer token is required.")
		return
	}
	principal, err := p.Verifier.Verify(r.Context(), token)
	if err != nil || !principal.Verified {
		status := 401
		if err != nil && !errors.Is(err, auth.ErrInvalidToken) {
			status = 503
		}
		fail(w, status, "producer_auth_failed", "Producer authentication could not be verified.")
		return
	}
	policy, ok := p.Config.Producers[principal.ID]
	scope := "tachyon.approvals_read"
	if operation == "enqueue" {
		scope = "tachyon.approvals_enqueue"
	}
	if operation == "withdraw" {
		scope = "tachyon.approvals_withdraw"
	}
	if !ok || principal.Audience != "tachyon" || !principal.HasScope(scope) {
		fail(w, 403, "forbidden", "Producer identity or required scope is not authorized.")
		return
	}
	if !principal.ExpiresAt.IsZero() {
		ctx, cancel := context.WithDeadline(r.Context(), principal.ExpiresAt)
		defer cancel()
		r = r.WithContext(ctx)
	}
	raw, err := commandBody(w, r)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	var result any
	callerOK := func(c hitl.CallerAssertion) bool {
		return c.ApplicationID == policy.ApplicationID && (c.PrincipalRef == "" || c.PrincipalRef == principal.ID)
	}
	switch operation {
	case "enqueue":
		var req hitl.EnqueueRequest
		if err = json.Unmarshal(raw, &req); err != nil {
			err = hitl.ErrInvalidRequest
		} else {
			if req.Source.ApplicationID != policy.ApplicationID {
				err = hitl.ErrUnauthorized
			} else if err = validateInboxRequest(req, policy); err == nil {
				result, err = p.Service.Enqueue(r.Context(), req)
			}
		}
	case "get":
		var cmd hitl.GetCommand
		cmd, err = hitl.DecodeGetCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Get(r.Context(), cmd)
			}
		}
	case "await":
		var cmd hitl.AwaitCommand
		cmd, err = hitl.DecodeAwaitCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Await(r.Context(), cmd)
			}
		}
	case "withdraw":
		var cmd hitl.WithdrawCommand
		cmd, err = hitl.DecodeWithdrawCommand(raw)
		if err == nil {
			if !callerOK(cmd.Caller) {
				err = hitl.ErrUnauthorized
			} else {
				result, err = p.Service.Withdraw(r.Context(), cmd)
			}
		}
	}
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeValue(w, result)
}
