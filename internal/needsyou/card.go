package needsyou

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/libs/security/redact"
)

// Card is a typed ask rendered for the operator (CW-20261114-0023e): read-only until Tether accepts an
// operator-attested reply from Tachyon (CW-20261114-0152). It is read from a typed envelope (docs/typed-messages.md,
// envelope v1) or from the ask skill's interim payload; no field is invented. Impact and rollback of an approval
// live in its body, as the ask skill writes them.
type Card struct {
	Kind          string   `json:"kind"`
	Subject       string   `json:"subject,omitempty"`
	Body          string   `json:"body,omitempty"` // markdown, redacted, at most 4000 characters
	Options       []Option `json:"options,omitempty"`
	DefaultOption string   `json:"default_option,omitempty"`
	DefaultAt     string   `json:"default_at,omitempty"` // the interim payload's default.at: when the default applies
	Re            string   `json:"re,omitempty"`
	Awaiting      string   `json:"awaiting,omitempty"`
	ReplyTo       string   `json:"reply_to,omitempty"` // the asker's @handle, for the answer line; "" when unknown
}

// Option is one answer. An approval_request without options is answered approve or deny.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
	Detail      string `json:"detail,omitempty"` // envelope detail, or the interim payload's why
}

var (
	optionIDRE = regexp.MustCompile(`^[a-z0-9_-]{1,20}$`)
	handleRE   = regexp.MustCompile(`^@[a-z][a-z0-9_-]{0,63}$`)
)

type cardPayload struct {
	Kind          string `json:"kind"`
	From          string `json:"from"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	Re            string `json:"re"`
	Awaiting      string `json:"awaiting"`
	DefaultOption string `json:"default_option"`
	Default       *struct {
		Option string `json:"option"`
		At     string `json:"at"`
	} `json:"default"`
	Options []struct {
		ID          string `json:"id"`
		Label       string `json:"label"`
		Recommended bool   `json:"recommended"`
		Detail      string `json:"detail"`
		Why         string `json:"why"`
	} `json:"options"`
}

// cardOf reads the card of an ask (question, decision, approval_request) from m; nil for anything else.
func cardOf(m Message, kind, from string) *Card {
	raw := []byte(m.Payload)
	var inner string
	if json.Unmarshal(raw, &inner) == nil {
		raw = []byte(inner)
	}
	var p cardPayload
	_ = json.Unmarshal(raw, &p)
	kind = firstOf(p.Kind, kind)
	if !askKinds[kind] {
		return nil
	}
	c := &Card{Kind: kind, Subject: oneLine(firstOf(p.Subject, m.Subject)), Body: bodyOf(firstOf(p.Body, m.Body)), Re: oneLine(p.Re)}
	if handleRE.MatchString(p.Awaiting) {
		c.Awaiting = p.Awaiting
	}
	for _, h := range []string{p.From, from} {
		if handleRE.MatchString(h) {
			c.ReplyTo = h
			break
		}
	}
	for _, o := range p.Options {
		if !optionIDRE.MatchString(o.ID) || len(c.Options) == 6 {
			continue
		}
		c.Options = append(c.Options, Option{ID: o.ID, Label: oneLine(o.Label), Recommended: o.Recommended, Detail: oneLine(firstOf(o.Detail, o.Why))})
	}
	def := p.DefaultOption
	if p.Default != nil {
		def = firstOf(def, p.Default.Option)
		c.DefaultAt = oneLine(p.Default.At)
	}
	if optionIDRE.MatchString(def) {
		c.DefaultOption = def
	}
	if kind == "approval_request" && len(c.Options) == 0 {
		c.Options = []Option{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}}
	}
	return c
}

// bodyOf keeps the body's lines (markdown), redacted and bounded to the envelope's 4000 characters.
func bodyOf(s string) string {
	s = strings.TrimSpace(redact.Text(s))
	if utf8.RuneCountInString(s) > 4000 {
		s = string([]rune(s)[:3999]) + "…"
	}
	return s
}

// messageGetter is a Tether that can read one message, for a pending ask outside the mailbox window.
type messageGetter interface {
	Message(ctx context.Context, as, id string) (Message, error)
}

func (t *HTTPTether) Message(ctx context.Context, as, id string) (Message, error) {
	var m Message
	err := t.get(ctx, "/messages/"+url.PathEscape(id)+"?as="+url.QueryEscape(as), &m)
	return m, err
}
