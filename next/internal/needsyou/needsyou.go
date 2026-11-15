// Package needsyou is the needs-you view (CW-20261114-0023c): one live list of what waits on the operator,
// @chrispian (msg://user/local/operator). It asks the sources for their current state on every read, so an item
// leaves the list as soon as its condition ends (a reply, an archive, a new status) with nothing to settle.
//
// Sources:
//   - asks: typed messages waiting on his reply (Tether GET /messages/pending-replies).
//   - mail: his mailbox. Interim asks (the ask skill's kind request, or a payload that says attention human or
//     awaiting @chrispian) wait until he replies (in_reply_to) or they are canceled or archived, as typed asks do;
//     reading does not settle them. Other mail marked for him (attention human; a typed message to him defaults to
//     it) needs him only while unread.
//   - statuses: seats that declared themselves blocked or waiting-decision with owner @chrispian (seat.status,
//     kept by the collector).
//   - chat: chat threads with open question or approval turns (where most asks happen today), one item per thread.
//   - approvals: Tachyon's approvals queue, one item for what is pending; critical when any is high risk.
//
// An approval request at urgency high is critical and sorts first. A source that cannot be read is reported, so
// a short list is never shown as complete.
package needsyou

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/next/internal/agentscollector"
)

// Operator is the address and handle needs-you is for. Operator is also the operator credential's principal.
const (
	Operator       = "msg://user/local/operator"
	OperatorHandle = "@chrispian"
	// OperatorCanonical is the canonical address tether#554 (CW-20261114-0067) gives Chrispian; the old address is
	// an alias of it, and his mail may come from either.
	OperatorCanonical = "msg://user/local/chrispian"
	envelopeType      = "application/vnd.tether.envelope.v1+json"
	mailWindow        = 100 // newest messages of his mailbox read per request
)

// Item is one thing waiting on the operator.
type Item struct {
	ID        string    `json:"id"`     // ask:<message id>, mail:<message id>, status:<agent urn>, chat:<thread ref>, approvals:queue
	Source    string    `json:"source"` // ask | mail | status | chat | approvals
	Kind      string    `json:"kind"`   // the envelope or payload kind (question, decision, approval_request, request, update...) or the status state
	Why       string    `json:"why"`    // reply | read | unblock | decide
	Title     string    `json:"title"`
	From      string    `json:"from"` // @handle when known, else the sender's address
	Ref       string    `json:"ref,omitempty"`
	Since     time.Time `json:"since"`
	Critical  bool      `json:"critical"`
	MessageID string    `json:"message_id,omitempty"`
	Detail    string    `json:"detail,omitempty"` // counts, for an item that stands for several (a chat thread, the approvals queue)
	Card      *Card     `json:"card,omitempty"`   // the ask as a card: question, decision or approval_request
	Link      string    `json:"link,omitempty"`   // where in Tachyon to act on it
}

// SourceState says whether a source was read, and whether it was cut short.
type SourceState struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Result is the view.
type Result struct {
	Items     []Item                 `json:"items"`
	Sources   map[string]SourceState `json:"sources"`
	CheckedAt time.Time              `json:"checked_at"`
}

// Tether is what needs-you reads from Tether.
type Tether interface {
	PendingReplies(ctx context.Context, as string) ([]Pending, error)
	Mailbox(ctx context.Context, owner string, limit int) ([]Message, int, error)
}

// Statuses lists seats' declared statuses (agentscollector.Store.SeatStatuses).
type Statuses interface {
	SeatStatuses(ctx context.Context, now time.Time) ([]agentscollector.SeatStatus, error)
}

// Pending is an item of GET /messages/pending-replies.
type Pending struct {
	MessageID string    `json:"message_id"`
	From      string    `json:"from"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"created_at"`
}

// Message is a mailbox message (GET /messages/list).
type Message struct {
	ID          string            `json:"id"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Kind        string            `json:"kind"`
	ContentType string            `json:"content_type"`
	Subject     string            `json:"subject"`
	Body        string            `json:"body"`
	Payload     json.RawMessage   `json:"payload"`
	Metadata    map[string]string `json:"metadata"`
	InReplyTo   string            `json:"in_reply_to"`
	CreatedAt   time.Time         `json:"created_at"`
	ReadAt      *time.Time        `json:"read_at"`
	CanceledAt  *time.Time        `json:"canceled_at"`
	ArchivedAt  *time.Time        `json:"archived_at"`
}

// fields is what needs-you reads from a payload: a typed envelope, or the ask skill's interim payload.
type fields struct {
	Kind      string `json:"kind"`
	Attention string `json:"attention"`
	Awaiting  string `json:"awaiting"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Re        string `json:"re"`
}

func payloadOf(m Message) fields {
	var f fields
	raw := []byte(m.Payload)
	var inner string
	if json.Unmarshal(raw, &inner) == nil {
		raw = []byte(inner) // a payload stored as a JSON string holding the object
	}
	_ = json.Unmarshal(raw, &f)
	return f
}

var askKinds = map[string]bool{"question": true, "decision": true, "approval_request": true}

// operatorForms are every address Chrispian's mail is sent from.
var operatorForms = map[string]bool{Operator: true, OperatorCanonical: true}

// fromOperator reports whether m was really sent by Chrispian: from one of his addresses, and stamped verified by
// Tether with the operator credential as the principal. In observe mode any caller may claim from=operator; that
// claim is stored unverified and is not his reply.
func fromOperator(m Message) bool {
	return operatorForms[m.From] && m.Metadata["from_verification"] == "verified" && m.Metadata["from_principal"] == Operator
}

// Aggregator builds the view.
type Aggregator struct {
	Tether    Tether
	Statuses  Statuses
	Chats     Chats
	Approvals Approvals
	Now       func() time.Time
}

// Read returns what waits on the operator now.
func (a *Aggregator) Read(ctx context.Context) Result {
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now()
	}
	r := Result{Items: []Item{}, Sources: map[string]SourceState{}, CheckedAt: now}
	handles := map[string]string{} // agent urn -> @handle, from declared statuses
	statuses, serr := a.statuses(ctx, now)
	for _, s := range statuses {
		if s.Handle != "" {
			handles[s.AgentURN] = s.Handle
		}
	}
	who := func(urn string) string {
		if operatorForms[urn] {
			return OperatorHandle
		}
		if h := handles[urn]; h != "" {
			return h
		}
		return urn
	}

	// Asks: typed messages waiting on his reply. Urgency comes from the mailbox copy of the same message.
	var box []Message
	var total int
	var berr error
	if a.Tether != nil {
		box, total, berr = a.Tether.Mailbox(ctx, Operator, mailWindow)
	}
	byID := map[string]Message{}
	for _, m := range box {
		byID[m.ID] = m
	}
	pendingIDs := map[string]bool{}
	if a.Tether == nil {
		r.Sources["asks"] = SourceState{Error: "Tether is not configured"}
	} else if pending, err := a.Tether.PendingReplies(ctx, Operator); err != nil {
		r.Sources["asks"] = SourceState{Error: "Tether pending replies unavailable: " + err.Error()}
	} else {
		r.Sources["asks"] = SourceState{OK: true, Truncated: len(pending) >= 20}
		for _, p := range pending {
			pendingIDs[p.MessageID] = true
			m, known := byID[p.MessageID]
			if mg, ok := a.Tether.(messageGetter); !known && ok {
				// Outside the mailbox window: read the one message, so the card and its urgency are known.
				if got, err := mg.Message(ctx, Operator, p.MessageID); err == nil && got.ID == p.MessageID {
					m, known = got, true
				}
			}
			f := payloadOf(m)
			it := Item{ID: "ask:" + p.MessageID, Source: "ask", Kind: p.Kind, Why: "reply", Title: titleOf(p.Subject, f.Body, m.Body),
				From: who(p.From), Ref: f.Re, Since: p.CreatedAt, MessageID: p.MessageID}
			it.Critical = known && critical(p.Kind, m)
			if known {
				it.Card = cardOf(m, p.Kind, it.From)
			}
			r.Items = append(r.Items, it)
		}
	}

	// Mail: interim asks and mail marked for him.
	if a.Tether == nil {
		r.Sources["mail"] = SourceState{Error: "Tether is not configured"}
	} else if berr != nil {
		r.Sources["mail"] = SourceState{Error: "Tether mailbox unavailable: " + berr.Error()}
	} else {
		state := SourceState{OK: true, Truncated: total > len(box)}
		replied := map[string]map[string]bool{} // sender -> ids the operator replied to
		for _, m := range box {
			if m.CanceledAt != nil || m.ArchivedAt != nil || pendingIDs[m.ID] || fromOperator(m) {
				continue
			}
			f := payloadOf(m)
			typed := strings.HasPrefix(m.ContentType, envelopeType)
			it := Item{ID: "mail:" + m.ID, Source: "mail", Kind: firstOf(f.Kind, m.Kind), Title: titleOf(firstOf(f.Subject, m.Subject), f.Body, m.Body),
				From: who(m.From), Ref: f.Re, Since: m.CreatedAt, MessageID: m.ID, Critical: critical(firstOf(f.Kind, m.Kind), m)}
			switch {
			case typed:
				// A typed message that waits on him is an ask (listed above); one that does not needs him while
				// unread, unless its sender marked it for an agent or for nobody.
				if m.ReadAt != nil || f.Attention == "agent" || f.Attention == "none" {
					continue
				}
				it.Why = "read"
			case m.Kind == "request" || f.Awaiting == OperatorHandle || askKinds[f.Kind]:
				// An interim ask waits until he replies to it, or it is canceled or archived.
				if replied[m.From] == nil {
					ids, err := a.repliesFrom(ctx, m.From)
					if err != nil {
						state.Error = "could not check replies to " + m.From + ": " + err.Error()
					}
					replied[m.From] = ids
				}
				if replied[m.From][m.ID] {
					continue
				}
				it.Why = "reply"
				it.Card = cardOf(m, firstOf(f.Kind, m.Kind), it.From)
			case f.Attention == "human" && m.ReadAt == nil:
				it.Why = "read"
			default:
				continue // ordinary mail: the inbox's, not needs-you's
			}
			r.Items = append(r.Items, it)
		}
		r.Sources["mail"] = state
	}

	// Statuses: seats blocked on him, or waiting on his decision.
	if serr != nil {
		r.Sources["statuses"] = SourceState{Error: "seat statuses unavailable: " + serr.Error()}
	} else {
		r.Sources["statuses"] = SourceState{OK: a.Statuses != nil, Error: map[bool]string{true: "", false: "seat statuses are not collected"}[a.Statuses != nil]}
		for _, s := range statuses {
			if s.Owner != OperatorHandle || (s.State != "blocked" && s.State != "waiting-decision") {
				continue
			}
			r.Items = append(r.Items, Item{ID: "status:" + s.AgentURN, Source: "status", Kind: s.State, Why: "unblock",
				Title: firstOf(s.Reason, strings.ReplaceAll(s.State, "-", " ")), From: who(s.AgentURN), Ref: s.Ref, Since: s.At})
		}
	}

	a.local(ctx, now, &r)

	// Critical first, then whatever has waited longest.
	sort.SliceStable(r.Items, func(i, j int) bool {
		if r.Items[i].Critical != r.Items[j].Critical {
			return r.Items[i].Critical
		}
		return r.Items[i].Since.Before(r.Items[j].Since)
	})
	return r
}

func (a *Aggregator) statuses(ctx context.Context, now time.Time) ([]agentscollector.SeatStatus, error) {
	if a.Statuses == nil {
		return nil, nil
	}
	return a.Statuses.SeatStatuses(ctx, now)
}

// repliesFrom lists the ids the operator replied to in sender's mailbox (his replies go to the asker). Only a
// verified reply from him counts: an unverified one claiming his address does not clear an ask.
func (a *Aggregator) repliesFrom(ctx context.Context, sender string) (map[string]bool, error) {
	out := map[string]bool{}
	box, _, err := a.Tether.Mailbox(ctx, sender, 200)
	for _, m := range box {
		if m.InReplyTo != "" && fromOperator(m) {
			out[m.InReplyTo] = true
		}
	}
	return out, err
}

// critical: an approval request at urgency high (docs/typed-messages.md "Urgency") from a sender Tether verified.
// Urgency is the sender's own claim; in observe mode any caller can send unverified mail, and that is listed as
// normal rather than allowed to mark itself critical.
func critical(kind string, m Message) bool {
	return kind == "approval_request" && m.Metadata["urgency"] == "high" && m.Metadata["from_verification"] == "verified"
}

// titleOf is a one-line, redacted title: the subject, else the body's first line.
func titleOf(subject string, bodies ...string) string {
	if t := oneLine(subject); t != "" {
		return t
	}
	for _, b := range bodies {
		for _, l := range strings.Split(b, "\n") {
			if t := oneLine(l); t != "" {
				return t
			}
		}
	}
	return ""
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(redact.Text(s)), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// AsksSince counts asks to the operator created since (typed questions, decisions and approval requests, and the
// ask skill's interim requests), from the newest mailbox window. The ceremony meter counts them as gates.
func (a *Aggregator) AsksSince(ctx context.Context, since time.Time) (int, error) {
	if a.Tether == nil {
		return 0, errNoTether
	}
	box, _, err := a.Tether.Mailbox(ctx, Operator, mailWindow)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range box {
		if m.CreatedAt.Before(since) || fromOperator(m) {
			continue
		}
		f := payloadOf(m)
		if askKinds[f.Kind] || (!strings.HasPrefix(m.ContentType, envelopeType) && (m.Kind == "request" || f.Awaiting == OperatorHandle)) {
			n++
		}
	}
	return n, nil
}

type nsError string

func (e nsError) Error() string { return string(e) }

const errNoTether = nsError("Tether is not configured")
