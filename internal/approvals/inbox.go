package approvals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// CardRegistry holds the card kinds the queue accepts. The agent surface and
// the respond route read it too, so there is one definition of a card kind.
var CardRegistry = cards.NewRegistry()

type Inbox struct {
	Store    *Store
	Service  *hitl.Service
	Sessions *Bridge
	// ActionRun, when set, attaches the registered-action run for an item to its
	// browser view (nil result means the item has no run).
	ActionRun func(ctx context.Context, itemID string) any
	// CardReceipt, when set, attaches the effects receipt of an approved card
	// (nil result means none yet). It holds ids and outcomes, no free text from
	// the asker, so any browser session that can read the item may see it.
	CardReceipt func(ctx context.Context, itemID string) any
}

func NewInbox(store *Store, sessions *Bridge) *Inbox {
	service := hitl.NewService(store, hitl.Options{ValidateResponse: func(kind string, response hitl.Response) error {
		if cards.IsCard(kind) {
			return CardRegistry.ValidateResponse(kind, response)
		}
		if kind == "approval" && (response.Decision == "approved" || response.Decision == "denied") || kind == "attention" && response.Decision == "acknowledged" {
			return nil
		}
		return fmt.Errorf("%w: decision does not match the request kind", hitl.ErrInvalidRequest)
	}})
	return &Inbox{Store: store, Service: service, Sessions: sessions}
}

type sessionStatus struct {
	Elevated  bool   `json:"elevated"`
	Assurance string `json:"assurance"`
	// Tangent owns the lifetime; these only feed operator-facing copy.
	ElevationTTL       string `json:"elevation_ttl,omitempty"`
	ElevationExpiresAt string `json:"elevation_expires_at,omitempty"`
}

func sameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Host == r.Host && (origin.Scheme == "https" || origin.Scheme == "http") && origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}
func (b *Bridge) participant(w http.ResponseWriter, r *http.Request) (hitl.Participant, sessionStatus, bool) {
	// Bearer requesters cannot acquire response authority by adding a cookie.
	if r.Header.Get("Authorization") != "" {
		fail(w, 403, "requester_cannot_approve", "Use the operator browser session for inbox access.")
		return hitl.Participant{}, sessionStatus{}, false
	}
	check := r.Clone(r.Context())
	u := *r.URL
	u.Path = Prefix + "/status"
	u.RawPath = ""
	u.RawQuery = ""
	check.URL = &u
	check.Method = http.MethodGet
	check.Body = nil
	captured := &statusCapture{header: make(http.Header), status: 200}
	b.ServeHTTP(captured, check)
	if captured.status != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(captured.status)
		_, _ = w.Write([]byte(captured.body.String()))
		return hitl.Participant{}, sessionStatus{}, false
	}
	var status sessionStatus
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" || json.Unmarshal([]byte(captured.body.String()), &status) != nil || status.Assurance == "" {
		fail(w, 401, "session_required", "Connect an operator browser session.")
		return hitl.Participant{}, sessionStatus{}, false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	ref := "browser-session:" + hex.EncodeToString(hash[:])
	assurance := "browser-session"
	if status.Elevated && status.Assurance == "operator-token" {
		assurance = "operator-token"
	}
	return hitl.Participant{PrincipalRef: ref, Authority: "tachyon-inbox/tangent-session", Assurance: assurance, Responder: &hitl.Responder{Kind: "human-session", Ref: ref}}, status, true
}

type browserItem struct {
	hitl.ItemView
	PresentedProjectionRevision int64 `json:"presented_projection_revision"`
	ActionRun                   any   `json:"action_run,omitempty"`
	CardReceipt                 any   `json:"card_receipt,omitempty"`
}

// ItemView has custom marshaling; explicitly add the presentation field.
func (item browserItem) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(item.ItemView)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["presented_projection_revision"], _ = json.Marshal(item.PresentedProjectionRevision)
	if item.ActionRun != nil {
		fields["action_run"], _ = json.Marshal(item.ActionRun)
	}
	if item.CardReceipt != nil {
		fields["card_receipt"], _ = json.Marshal(item.CardReceipt)
	}
	return json.Marshal(fields)
}

// view projects one item. The action run (its command, output and exit status) is
// attached only for an elevated operator session: the same bar as the dedicated
// run-read route, never the weaker browser-session assurance of the queue itself.
func (i *Inbox) view(r *http.Request, id string, operator bool) (browserItem, error) {
	rec, err := i.Store.Get(r.Context(), id)
	if err != nil {
		return browserItem{}, err
	}
	result, err := i.Service.Get(r.Context(), hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: id, Caller: hitl.CallerAssertion{ApplicationID: rec.CallerScope}})
	if err != nil {
		return browserItem{}, err
	}
	out := browserItem{ItemView: result.Item, PresentedProjectionRevision: result.Item.Revision}
	if i.ActionRun != nil && operator {
		out.ActionRun = i.ActionRun(r.Context(), id)
	}
	if i.CardReceipt != nil {
		out.CardReceipt = i.CardReceipt(r.Context(), id)
	}
	return out, nil
}
func (i *Inbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	if path == "/session" || path == "/status" || path == "/elevate" || path == "/de-elevate" {
		i.Sessions.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "Use GET or POST.")
		return
	}
	if r.Method == http.MethodPost && !sameOrigin(r) {
		fail(w, 403, "origin_denied", "Use the Tachyon browser origin.")
		return
	}
	participant, session, ok := i.Sessions.participant(w, r)
	if !ok {
		return
	}
	if path == "/inbox" && r.Method == http.MethodGet {
		i.list(w, r, participant.Assurance == "operator-token")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "items" || !itemPath.MatchString("/items/"+parts[1]) {
		fail(w, 404, "not_found", "Unknown inbox operation.")
		return
	}
	item, err := i.view(r, parts[1], participant.Assurance == "operator-token")
	if err != nil {
		writeHITLError(w, err)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		writeValue(w, item)
		return
	}
	if len(parts) != 3 || r.Method != http.MethodPost || (parts[2] != "present" && parts[2] != "resolve") {
		fail(w, 404, "not_found", "Unknown inbox operation; external evidence stays with its provider.")
		return
	}
	raw, err := commandBody(w, r)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	var cmd struct {
		ExpectedRevision            int64  `json:"expected_revision"`
		PresentedProjectionRevision int64  `json:"presented_projection_revision"`
		ConnectionID                string `json:"connection_id,omitempty"`
		Response                    *struct {
			Kind     string `json:"kind"`
			Decision string `json:"decision"`
			Note     string `json:"note,omitempty"`
			Reply    string `json:"reply,omitempty"`
			// Answers, Decisions and Payload are card-only; a card response
			// is checked against the stored request before it is recorded.
			Answers   json.RawMessage `json:"answers,omitempty"`
			Decisions json.RawMessage `json:"decisions,omitempty"`
			Payload   json.RawMessage `json:"payload,omitempty"`
		} `json:"response,omitempty"`
	}
	if err = strictJSON(raw, &cmd); err != nil {
		writeHITLError(w, err)
		return
	}
	if cmd.ExpectedRevision != item.Revision || cmd.PresentedProjectionRevision != item.Revision {
		writeHITLError(w, &hitl.StaleRevisionError{Operation: parts[2], ItemID: item.ItemID, RevisionKind: "presented_projection", Expected: cmd.ExpectedRevision, Actual: item.Revision, CurrentState: item.State, Outcome: item.TerminalOutcome})
		return
	}
	if parts[2] == "present" {
		writeValue(w, item)
		return
	}
	var request hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &request)
	class := extraString(request, "class")
	if class == "high" || request.Source.ApplicationID == "cerberus" {
		fail(w, 403, "provider_handoff_required", "Complete this decision in the provider console.")
		return
	}
	if class != "low" && class != "medium" {
		fail(w, 403, "unsupported_class", "This request cannot be decided here.")
		return
	}
	if class == "medium" && participant.Assurance != "operator-token" {
		fail(w, 403, "assurance_insufficient", "Enter your operator key to decide this request. "+ElevationCopy(session.ElevationTTL, session.ElevationExpiresAt))
		return
	}
	if cmd.Response == nil {
		writeHITLError(w, fmt.Errorf("%w: response is required", hitl.ErrInvalidRequest))
		return
	}
	response := hitl.Response{Kind: cmd.Response.Kind, Decision: cmd.Response.Decision, Extra: map[string]json.RawMessage{}}
	if cmd.Response.Note != "" {
		response.Extra["note"], _ = json.Marshal(cmd.Response.Note)
	}
	if cmd.Response.Reply != "" {
		response.Extra["reply"], _ = json.Marshal(cmd.Response.Reply)
	}
	for name, raw := range map[string]json.RawMessage{"answers": cmd.Response.Answers, "decisions": cmd.Response.Decisions, "payload": cmd.Response.Payload} {
		if len(raw) == 0 {
			continue
		}
		if !cards.IsCard(request.Kind) {
			writeHITLError(w, fmt.Errorf("%w: %s apply only to cards", hitl.ErrInvalidRequest, name))
			return
		}
		response.Extra[name] = raw
	}
	if cards.IsCard(request.Kind) {
		if response.Kind != request.Kind {
			writeHITLError(w, fmt.Errorf("%w: response kind must be %s", hitl.ErrInvalidRequest, request.Kind))
			return
		}
		if err = CardRegistry.ValidateAnswers(request, response); err != nil {
			writeHITLError(w, err)
			return
		}
	}
	outcome, err := i.Service.Respond(r.Context(), hitl.RespondCommand{ItemID: item.ItemID, ExpectedRevision: &cmd.ExpectedRevision, Response: response, Participant: participant})
	if err != nil {
		writeHITLError(w, err)
		return
	}
	writeValue(w, outcome)
}
func (i *Inbox) list(w http.ResponseWriter, r *http.Request, operator bool) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
		limit = n
	}
	type page struct {
		Pending     []browserItem `json:"pending"`
		History     []browserItem `json:"history"`
		NextPending string        `json:"next_pending,omitempty"`
		NextHistory string        `json:"next_history,omitempty"`
		SyncedAt    string        `json:"synced_at"`
		Revision    string        `json:"revision"`
	}
	out := page{Pending: []browserItem{}, History: []browserItem{}, SyncedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, history := range []bool{false, true} {
		before := r.URL.Query().Get("pending_before")
		if history {
			before = r.URL.Query().Get("history_before")
		}
		records, next, err := i.Store.Page(r.Context(), history, before, limit)
		if err != nil {
			writeHITLError(w, err)
			return
		}
		for _, rec := range records {
			item, err := i.view(r, rec.ItemID, operator)
			if err != nil {
				writeHITLError(w, err)
				return
			}
			if history {
				out.History = append(out.History, item)
			} else {
				out.Pending = append(out.Pending, item)
			}
		}
		if history {
			out.NextHistory = next
		} else {
			out.NextPending = next
		}
	}
	out.Revision = out.SyncedAt
	writeValue(w, out)
}
