package approvals

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/cards"
)

// Enqueue validation: how a producer's request becomes an inbox item (the old producer's rules,
// kept for the item kinds and classes the inbox serves; the producer route itself, /api/approvals/v1,
// is not ported).

// operationDigest is the op_digest a high-class approval must carry.
var operationDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type ProducerPolicy struct {
	ApplicationID  string   `json:"application_id"`
	MinimumClass   string   `json:"minimum_class"`
	HandoffOrigins []string `json:"handoff_origins,omitempty"`
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

func extraString(req hitl.EnqueueRequest, key string) string {
	var value string
	_ = json.Unmarshal(req.Extra[key], &value)
	return value
}

// prepareRequest validates a producer request. Card kinds go through the card
// registry, which fixes class, digest and source on the server; the older
// approval and attention kinds keep their own checks.
func prepareRequest(req *hitl.EnqueueRequest, policy ProducerPolicy) error {
	if !cards.IsCard(req.Kind) {
		return validateInboxRequest(*req, policy)
	}
	if err := CardRegistry.Prepare(req, policy.ApplicationID); err != nil {
		return err
	}
	class := extraString(*req, "class")
	// A producer policy sets a floor, and Cerberus is provider-handoff only.
	if req.Source.ApplicationID == "cerberus" {
		class = "high"
	}
	if tier(class) < tier(policy.MinimumClass) {
		class = policy.MinimumClass
	}
	req.Extra["class"], _ = json.Marshal(class)
	if class == "high" && extraString(*req, "handoff_url") == "" {
		return fmt.Errorf("%w: high tier requires a provider handoff URL", hitl.ErrInvalidRequest)
	}
	return nil
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
