package cards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// Limits for the destructive-request members (approvals design, section 5).
const (
	MaxAffectedItems = 200
	MaxAlternatives  = 3
	maxShort         = 200
	maxNote          = 500
	maxSummaryText   = 300
)

var alternativeID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// destructiveOps are the effect ops the server treats as destructive whatever the asker says.
var destructiveOps = []string{"delete", "purge", "drop"}

// IsDestructiveOp reports whether an effect op is destructive (delete, purge, drop, force_*).
func IsDestructiveOp(op string) bool {
	op = strings.ToLower(op)
	for _, d := range destructiveOps {
		if op == d {
			return true
		}
	}
	return strings.HasPrefix(op, "force_")
}

// DigestOf is the operation digest over everything the operator approves: kind, data, effects and,
// when present, affected_items, destructive and alternatives. Requests without the last three digest
// exactly as before. Call it on the stored Extra, never on asker input.
func DigestOf(kind string, extra map[string]json.RawMessage) (string, error) {
	effects := extra["effects"]
	if len(effects) == 0 {
		effects = json.RawMessage("null")
	}
	opt := func(k string) json.RawMessage {
		if raw := extra[k]; len(raw) > 0 && string(raw) != "null" {
			return raw
		}
		return nil
	}
	body, err := json.Marshal(struct {
		Kind          string          `json:"kind"`
		Data          json.RawMessage `json:"data"`
		Effects       json.RawMessage `json:"effects"`
		AffectedItems json.RawMessage `json:"affected_items,omitempty"`
		Destructive   json.RawMessage `json:"destructive,omitempty"`
		Alternatives  json.RawMessage `json:"alternatives,omitempty"`
		Evidence      json.RawMessage `json:"evidence,omitempty"`
	}{kind, extra["data"], effects, opt("affected_items"), opt("destructive"), opt("alternatives"), opt("evidence")})
	if err != nil {
		return "", err
	}
	canon, err := jcs.Transform(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{hitl.ErrInvalidRequest}, a...)...)
}

// normalizeDestructive validates affected_items, alternatives and destructive and rewrites them to
// the cleaned form that is stored and digested. The asker may raise destructive but never clear it:
// a destructive effect op forces destructive.irreversible = true.
func normalizeDestructive(req *hitl.EnqueueRequest) error {
	if raw := req.Extra["affected_items"]; len(raw) > 0 && string(raw) != "null" {
		var in []map[string]any
		if json.Unmarshal(raw, &in) != nil || len(in) == 0 || len(in) > MaxAffectedItems {
			return bad("affected_items must be a list of 1 to %d objects", MaxAffectedItems)
		}
		out := make([]map[string]string, 0, len(in))
		for _, m := range in {
			ref, _ := m["ref"].(string)
			label, _ := m["label"].(string)
			kind, _ := m["kind"].(string)
			if strings.TrimSpace(ref) == "" || strings.TrimSpace(label) == "" || strings.TrimSpace(kind) == "" {
				return bad("every affected item needs ref, label and kind")
			}
			e := map[string]string{"ref": clip(ref, maxShort), "label": clip(label, maxShort), "kind": clip(kind, 40)}
			if note, _ := m["note"].(string); strings.TrimSpace(note) != "" {
				e["note"] = clip(note, maxNote)
			}
			out = append(out, e)
		}
		req.Extra["affected_items"], _ = json.Marshal(out)
	} else {
		delete(req.Extra, "affected_items")
	}

	if raw := req.Extra["alternatives"]; len(raw) > 0 && string(raw) != "null" {
		var in []struct {
			ID      string           `json:"id"`
			Label   string           `json:"label"`
			Effects []map[string]any `json:"effects"`
		}
		if json.Unmarshal(raw, &in) != nil || len(in) == 0 || len(in) > MaxAlternatives {
			return bad("alternatives must be a list of 1 to %d objects", MaxAlternatives)
		}
		seen := map[string]bool{}
		out := make([]map[string]any, 0, len(in))
		for _, a := range in {
			if !alternativeID.MatchString(a.ID) || seen[a.ID] {
				return bad("alternative ids are unique and match %s", alternativeID)
			}
			seen[a.ID] = true
			if strings.TrimSpace(a.Label) == "" || len(a.Effects) == 0 || len(a.Effects) > maxEffects {
				return bad("alternative %q needs a label and 1 to %d effects", a.ID, maxEffects)
			}
			for _, e := range a.Effects {
				if target, _ := e["target"].(string); target == "" {
					return bad("every effect of alternative %q needs a target", a.ID)
				}
			}
			out = append(out, map[string]any{"id": a.ID, "label": clip(a.Label, 80), "effects": a.Effects})
		}
		req.Extra["alternatives"], _ = json.Marshal(out)
	} else {
		delete(req.Extra, "alternatives")
	}

	raised := false
	var effects []map[string]any
	_ = json.Unmarshal(req.Extra["effects"], &effects)
	for _, e := range effects {
		if op, _ := e["op"].(string); IsDestructiveOp(op) {
			raised = true
		}
	}
	var asked struct {
		Irreversible bool   `json:"irreversible"`
		Summary      string `json:"summary"`
	}
	given := false
	if raw := req.Extra["destructive"]; len(raw) > 0 && string(raw) != "null" && string(raw) != "false" {
		if json.Unmarshal(raw, &asked) != nil {
			return bad("destructive must be an object {irreversible, summary}")
		}
		given = true
	}
	if !raised && !given {
		delete(req.Extra, "destructive")
		return nil
	}
	if strings.TrimSpace(asked.Summary) == "" {
		asked.Summary = "This permanently changes or removes data."
	}
	req.Extra["destructive"], _ = json.Marshal(map[string]any{"irreversible": asked.Irreversible || raised, "summary": clip(asked.Summary, maxSummaryText)})
	return nil
}

// MaxEvidence bounds the evidence refs on one request.
const MaxEvidence = 16

var evidenceKinds = map[string]bool{"path": true, "caster": true, "torque": true, "tesseract": true, "url": true, "chat": true}

// normalizeEvidence validates evidence [{ref,label,kind,note?}] (0..16) and rewrites it to the cleaned form
// that is stored and digested. The backend never fetches a ref; the GUI opens it in the ref drawer.
func normalizeEvidence(req *hitl.EnqueueRequest) error {
	raw := req.Extra["evidence"]
	if len(raw) == 0 || string(raw) == "null" {
		delete(req.Extra, "evidence")
		return nil
	}
	var in []map[string]any
	if json.Unmarshal(raw, &in) != nil || len(in) > MaxEvidence {
		return bad("evidence must be a list of at most %d objects", MaxEvidence)
	}
	if len(in) == 0 {
		delete(req.Extra, "evidence")
		return nil
	}
	out := make([]map[string]string, 0, len(in))
	for _, m := range in {
		ref, _ := m["ref"].(string)
		label, _ := m["label"].(string)
		kind, _ := m["kind"].(string)
		if strings.TrimSpace(ref) == "" || strings.TrimSpace(label) == "" || !evidenceKinds[kind] {
			return bad("every evidence entry needs ref, label and a kind of path, caster, torque, tesseract, url or chat")
		}
		e := map[string]string{"ref": clip(ref, 480), "label": clip(label, maxShort), "kind": kind}
		if note, _ := m["note"].(string); strings.TrimSpace(note) != "" {
			e["note"] = clip(note, maxNote)
		}
		out = append(out, e)
	}
	req.Extra["evidence"], _ = json.Marshal(out)
	return nil
}
