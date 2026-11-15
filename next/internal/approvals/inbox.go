package approvals

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/approvalsapi"
	"github.com/hollis-labs/tachyon/next/internal/cards"
)

// CardRegistry holds the card kinds the queue accepts. The agent surface and
// the respond route read it too, so there is one definition of a card kind.
var CardRegistry = cards.NewRegistry()

type Inbox struct {
	Store   *Store
	Service *hitl.Service
	// ActionRun, when set, attaches the registered-action run for an item to its
	// browser view (nil result means the item has no run).
	ActionRun func(ctx context.Context, itemID string) any
	// CardReceipt, when set, attaches the effects receipt of an approved card
	// (nil result means none yet). It holds ids and outcomes, no free text from
	// the asker, so any browser session that can read the item may see it.
	CardReceipt func(ctx context.Context, itemID string) any
	// RunState, when set, says how the approved work is going (the state only; command and output stay
	// operator-token). Empty means none.
	RunState func(ctx context.Context, itemID string) string
	// Secrets stores values submitted from the secure dialog; resolve accepts only its references.
	// Without a key (tachyon.files.secret_key) the endpoint answers 503.
	Secrets *Secrets
}

// NewInbox is the inbox over store, with secrets as its secret store (nil: no key).
func NewInbox(store *Store, secrets *Secrets) *Inbox {
	service := hitl.NewService(store, hitl.Options{ValidateResponse: func(kind string, response hitl.Response) error {
		if cards.IsCard(kind) {
			return CardRegistry.ValidateResponse(kind, response)
		}
		if kind == "approval" && (response.Decision == "approved" || response.Decision == "denied") || kind == "attention" && response.Decision == "acknowledged" {
			return nil
		}
		return fmt.Errorf("%w: decision does not match the request kind", hitl.ErrInvalidRequest)
	}})
	if secrets == nil {
		secrets = NewSecrets(store.DB, nil, "tachyon.files.secret_key is not set")
	}
	return &Inbox{Store: store, Service: service, Secrets: secrets}
}

type browserItem struct {
	hitl.ItemView
	PresentedProjectionRevision int64 `json:"presented_projection_revision"`
	ActionRun                   any   `json:"action_run,omitempty"`
	CardReceipt                 any   `json:"card_receipt,omitempty"`
	// Projection adds the queue fields (risk, group, batchable, ...). Nil leaves the item as it was.
	Projection *Projection `json:"-"`
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
	if item.Projection != nil {
		for k, v := range item.Projection.fields() {
			if _, exists := fields[k]; !exists { // never shadow a stored field
				fields[k], _ = json.Marshal(v)
			}
		}
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
	now := time.Now()
	proj := Project(rec, now, i.Store.DeferredUntil(r.Context(), id, now))
	if i.RunState != nil {
		if rs := i.RunState(r.Context(), id); rs != "" {
			proj.RunState = rs
		}
	}
	out := browserItem{ItemView: result.Item, PresentedProjectionRevision: result.Item.Revision, Projection: &proj}
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
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		fail(w, 405, "method_not_allowed", "Use GET or POST.")
		return
	}
	if r.Method == http.MethodDelete && !strings.HasSuffix(path, "/defer") {
		fail(w, 405, "method_not_allowed", "Use GET or POST.")
		return
	}
	participant, ok := participant(w, r)
	if !ok {
		return
	}
	if path == "/inbox" && r.Method == http.MethodGet {
		i.list(w, r, participant.Assurance == "operator-token")
		return
	}
	if r.Method == http.MethodPost && path == "/batch/resolve" {
		i.batchResolve(w, r, participant)
		return
	}
	if r.Method == http.MethodPost && path == "/secrets" {
		i.submitSecret(w, r, participant)
		return
	}
	if r.Method == http.MethodGet && path == "/events" {
		i.events(w, r)
		return
	}
	if r.Method == http.MethodGet && path == "/history" {
		i.history(w, r, participant.PrincipalRef)
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
	if len(parts) == 3 && parts[2] == "audit" && r.Method == http.MethodGet {
		i.audit(w, r, item.ItemID, participant.PrincipalRef)
		return
	}
	if len(parts) == 3 && parts[2] == "defer" && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
		var raw []byte
		if r.Method == http.MethodPost {
			if raw, err = commandBody(w, r); err != nil {
				writeHITLError(w, err)
				return
			}
		}
		i.deferItem(w, r, item, participant, raw)
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
			// Confirmation is the typed phrase an approval of a stored-high or cerberus item carries.
			Confirmation string `json:"confirmation,omitempty"`
			// AlternativeID chooses one of the item's alternatives instead of its main effects.
			AlternativeID string `json:"alternative_id,omitempty"`
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
		// One presentation event per session and item, so reopening a card does not flood the audit.
		_ = i.Store.LogEvent(r.Context(), Event{EventID: "evt_" + item.ItemID + "_presented_" + refSuffix(participant.PrincipalRef), ItemID: item.ItemID, Kind: "presented", ActorRef: participant.PrincipalRef, Assurance: participant.Assurance, Revision: item.Revision})
		writeValue(w, item)
		return
	}
	var request hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &request)
	class := extraString(request, "class")
	rec, err := i.Store.Get(r.Context(), item.ItemID)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	// Terminal and expired items fall through to the service, which answers them as before; the gate
	// only refuses what the browser can never decide.
	gate := CanResolve(rec, time.Now())
	if gate.Reason == ReasonUnsupportedKind {
		fail(w, 403, "unsupported_class", "This request cannot be decided here.")
		return
	}
	if gate.Destructive || gate.Phrase != "" {
		need := actionResolveHigh
		if gate.Destructive {
			need = actionDestructive
		}
		if !require(w, r, need) {
			return
		}
	}
	if cmd.Response != nil && cmd.Response.AlternativeID != "" {
		if cmd.Response.Decision != "approved" || !slices.Contains(gate.Alternatives, cmd.Response.AlternativeID) {
			writeHITLError(w, fmt.Errorf("%w: alternative_id must name one of the item's alternatives and goes with an approval", hitl.ErrInvalidRequest))
			return
		}
	} else if gate.Phrase != "" && cmd.Response != nil && cmd.Response.Decision == "approved" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(cmd.Response.Confirmation)), []byte(gate.Phrase)) != 1 {
		_ = i.Store.LogEvent(r.Context(), Event{EventID: "evt_" + item.ItemID + "_confirm_failed_" + strconv.FormatInt(time.Now().UnixNano(), 36), ItemID: item.ItemID, Kind: "confirmation_failed", ActorRef: participant.PrincipalRef, Assurance: participant.Assurance, Revision: item.Revision, ReasonCode: "phrase"})
		fail(w, 422, "confirmation_required", "Type the confirmation phrase exactly.")
		return
	}
	if class == "medium" && !require(w, r, actionResolveMedium) {
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
	if cmd.Response.AlternativeID != "" {
		response.Extra["alternative_id"], _ = json.Marshal(cmd.Response.AlternativeID)
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
	refs, err := i.checkSecretRefs(r.Context(), item.ItemID, request, response)
	if err != nil {
		writeSecretError(w, err)
		return
	}
	outcome, err := i.Service.Respond(r.Context(), hitl.RespondCommand{ItemID: item.ItemID, ExpectedRevision: &cmd.ExpectedRevision, Response: response, Participant: participant})
	if err != nil {
		writeHITLError(w, err)
		return
	}
	_ = i.Secrets.Consume(r.Context(), item.ItemID, refs)
	writeValue(w, outcome)
}

// list serves the queue. The pending side comes from the derived index (filters,
// search, urgency order, counts); history uses the index only when a filter is
// given, otherwise the original newest-first page. Cursors are item ids.
func (i *Inbox) list(w http.ResponseWriter, r *http.Request, operator bool) {
	q := r.URL.Query()
	limit := 25
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
		limit = n
	}
	oneOf := func(v string, ok ...string) bool { return v == "" || slices.Contains(ok, v) }
	status := q.Get("status")
	if !oneOf(status, "pending", "history") || !oneOf(q.Get("order"), "urgency", "created") || !oneOf(q.Get("batchable"), "1") ||
		!oneOf(q.Get("group"), approvalsapi.Groups...) || !oneOf(q.Get("risk"), "low", "medium", "high") ||
		!oneOf(q.Get("history_status"), "approved", "denied", "answered", "expired", "withdrawn", "failed", "superseded") {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	now := time.Now()
	f := Filter{Group: q.Get("group"), Risk: q.Get("risk"), Q: q.Get("q"), Requester: q.Get("requester"), Status: q.Get("history_status"),
		Batchable: q.Get("batchable") == "1", Order: q.Get("order"), Limit: limit, Operator: operator}
	pendingCursor, historyCursor := q.Get("pending_before"), q.Get("history_before")
	if c := q.Get("cursor"); c != "" {
		if status == "history" {
			historyCursor = c
		} else {
			pendingCursor = c
		}
	}
	type page struct {
		Pending     []browserItem  `json:"pending"`
		History     []browserItem  `json:"history"`
		NextPending string         `json:"next_pending,omitempty"`
		NextHistory string         `json:"next_history,omitempty"`
		SyncedAt    string         `json:"synced_at"`
		ServerNowMs int64          `json:"server_now_ms"`
		Revision    string         `json:"revision"`
		Counts      map[string]any `json:"counts,omitempty"`
	}
	out := page{Pending: []browserItem{}, History: []browserItem{}, SyncedAt: now.UTC().Format(time.RFC3339Nano), ServerNowMs: now.UnixMilli()}
	collect := func(ids []string, dst *[]browserItem) bool {
		for _, id := range ids {
			item, err := i.view(r, id, operator)
			if err != nil {
				writeHITLError(w, err)
				return false
			}
			*dst = append(*dst, item)
		}
		return true
	}
	if status != "history" {
		pf := f
		pf.After = pendingCursor
		ids, next, counts, err := i.Store.Pending(r.Context(), pf, now)
		if err != nil {
			writeHITLError(w, err)
			return
		}
		out.NextPending, out.Counts = next, counts
		if !collect(ids, &out.Pending) {
			return
		}
	}
	if status != "pending" {
		filtered := f.Group != "" || f.Risk != "" || f.Q != "" || f.Requester != "" || f.Status != ""
		var ids []string
		var next string
		var err error
		if filtered {
			hf := f
			hf.After = historyCursor
			ids, next, err = i.Store.History(r.Context(), hf, now)
		} else {
			var recs []hitl.Record
			recs, next, err = i.Store.Page(r.Context(), true, historyCursor, limit)
			for _, rec := range recs {
				ids = append(ids, rec.ItemID)
			}
		}
		if err != nil {
			writeHITLError(w, err)
			return
		}
		out.NextHistory = next
		if !collect(ids, &out.History) {
			return
		}
	}
	out.Revision = out.SyncedAt
	writeValue(w, out)
}
