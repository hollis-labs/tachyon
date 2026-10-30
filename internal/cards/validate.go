package cards

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// FieldTypes is the closed set of form field types. A secret field's value
// never travels in an answer: the answer is a {"secret_ref": "..."} reference
// the asker redeems separately, so a form that gains a secret later does not
// change the wire shape.
var FieldTypes = map[string]bool{
	"text": true, "textarea": true, "number": true, "bool": true,
	"enum": true, "multi": true, "date": true, "secret": true,
}

type field struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	Sensitive bool   `json:"sensitive"`
	Options   []any  `json:"options"`
}

func formFields(data json.RawMessage) ([]field, error) {
	var d struct {
		Fields []field `json:"fields"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%w: data.fields is malformed", hitl.ErrInvalidRequest)
	}
	return d.Fields, nil
}

// checkFields validates a form card's field list and reports whether any
// field is a secret.
func checkFields(data json.RawMessage) (secret bool, err error) {
	fields, err := formFields(data)
	if err != nil {
		return false, err
	}
	if len(fields) == 0 || len(fields) > 64 {
		return false, fmt.Errorf("%w: a form needs 1 to 64 fields", hitl.ErrInvalidRequest)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if strings.TrimSpace(f.ID) == "" || seen[f.ID] {
			return false, fmt.Errorf("%w: field ids must be unique and non-empty", hitl.ErrInvalidRequest)
		}
		seen[f.ID] = true
		if !FieldTypes[f.Type] {
			return false, fmt.Errorf("%w: field %q has unsupported type %q", hitl.ErrInvalidRequest, f.ID, f.Type)
		}
		if (f.Type == "enum" || f.Type == "multi") && len(f.Options) == 0 {
			return false, fmt.Errorf("%w: field %q needs options", hitl.ErrInvalidRequest, f.ID)
		}
		secret = secret || f.Type == "secret"
	}
	return secret, nil
}

// responseExtras are the only members a card response may carry beyond
// kind and decision. assurance_evidence is written by the trusted path.
var responseExtras = map[string]bool{
	"note": true, "reply": true, "answers": true, "decisions": true, "payload": true, "assurance_evidence": true,
}

// ValidateResponse is the hitl.Options.ValidateResponse check for card kinds.
// It judges the response shape only; ValidateAnswers needs the request.
func (r *Registry) ValidateResponse(kind string, resp hitl.Response) error {
	t, ok := r.Lookup(kind)
	if !ok {
		return fmt.Errorf("%w: unknown card kind %q", hitl.ErrInvalidRequest, kind)
	}
	allowed := false
	for _, d := range t.Decisions {
		allowed = allowed || d == resp.Decision
	}
	if !allowed {
		return fmt.Errorf("%w: decision does not match the card kind", hitl.ErrInvalidRequest)
	}
	for name := range resp.Extra {
		if !responseExtras[name] {
			return fmt.Errorf("%w: unexpected response member %q", hitl.ErrInvalidRequest, name)
		}
	}
	if !t.Answers && len(resp.Extra["answers"]) > 0 {
		return fmt.Errorf("%w: %s does not take answers", hitl.ErrInvalidRequest, kind)
	}
	return nil
}

type answer struct {
	QuestionID string          `json:"questionId"`
	Value      json.RawMessage `json:"value"`
}

// ValidateAnswers checks the answers of a question or form response against
// the stored request. Call it where the request snapshot is at hand (the
// respond route); the service hook cannot see the request.
func (r *Registry) ValidateAnswers(req hitl.EnqueueRequest, resp hitl.Response) error {
	t, ok := r.Lookup(req.Kind)
	if !ok || !t.Answers {
		return nil
	}
	var answers []answer
	if err := json.Unmarshal(resp.Extra["answers"], &answers); err != nil || len(answers) == 0 {
		return fmt.Errorf("%w: answers are required", hitl.ErrInvalidRequest)
	}
	byID := map[string]json.RawMessage{}
	for _, a := range answers {
		if a.QuestionID == "" || len(a.Value) == 0 || string(a.Value) == "null" {
			return fmt.Errorf("%w: every answer needs a questionId and a value", hitl.ErrInvalidRequest)
		}
		if _, dup := byID[a.QuestionID]; dup {
			return fmt.Errorf("%w: duplicate answer for %q", hitl.ErrInvalidRequest, a.QuestionID)
		}
		byID[a.QuestionID] = a.Value
	}
	if !t.Form {
		return nil
	}
	fields, err := formFields(req.Extra["data"])
	if err != nil {
		return err
	}
	known := map[string]field{}
	for _, f := range fields {
		known[f.ID] = f
	}
	for id := range byID {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("%w: answer for unknown field %q", hitl.ErrInvalidRequest, id)
		}
	}
	for _, f := range fields {
		value, present := byID[f.ID]
		if !present {
			if f.Required {
				return fmt.Errorf("%w: field %q is required", hitl.ErrInvalidRequest, f.ID)
			}
			continue
		}
		if err := checkValue(f, value); err != nil {
			return err
		}
	}
	return nil
}

func checkValue(f field, raw json.RawMessage) error {
	bad := func() error {
		return fmt.Errorf("%w: answer for %q does not match its %s field", hitl.ErrInvalidRequest, f.ID, f.Type)
	}
	switch f.Type {
	case "text", "textarea", "date":
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return bad()
		}
	case "number":
		var n float64
		if json.Unmarshal(raw, &n) != nil {
			return bad()
		}
	case "bool":
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return bad()
		}
	case "enum":
		var s string
		if json.Unmarshal(raw, &s) != nil || !optionIn(f.Options, s) {
			return bad()
		}
	case "multi":
		var list []string
		if json.Unmarshal(raw, &list) != nil {
			return bad()
		}
		for _, s := range list {
			if !optionIn(f.Options, s) {
				return bad()
			}
		}
	case "secret":
		var ref struct {
			SecretRef string `json:"secret_ref"`
		}
		if json.Unmarshal(raw, &ref) != nil || strings.TrimSpace(ref.SecretRef) == "" {
			return fmt.Errorf("%w: secret field %q takes a secret_ref, never a value", hitl.ErrInvalidRequest, f.ID)
		}
	}
	return nil
}

// optionIn accepts options given as plain strings or as {value,label} objects.
func optionIn(options []any, want string) bool {
	for _, o := range options {
		switch v := o.(type) {
		case string:
			if v == want {
				return true
			}
		case map[string]any:
			if s, _ := v["value"].(string); s == want {
				return true
			}
		}
	}
	return false
}

// SecretFields lists the ids of the secret fields of a card request's form; none for other kinds.
func SecretFields(req hitl.EnqueueRequest) []string {
	if !IsCard(req.Kind) || len(req.Extra["data"]) == 0 {
		return nil
	}
	fields, err := formFields(req.Extra["data"])
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range fields {
		if f.Type == "secret" {
			out = append(out, f.ID)
		}
	}
	return out
}
