package approvals

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvalsapi"
)

// decidedKinds are what the History timeline shows by default.
var decidedKinds = []string{"resolved", "batch_resolved", "expired", "withdrawn"}

func actorView(ref, assurance, session string) map[string]any {
	label := "Operator session"
	switch {
	case ref == "":
		label = "System"
	case ref == session:
		label = "You"
	}
	return map[string]any{"ref": ref, "label": label, "assurance": assurance}
}

func eventView(r TimelineRow, session string) map[string]any {
	m := map[string]any{"event_id": r.EventID, "item_id": r.ItemID, "kind": r.Kind, "at": r.TS.Format(time.RFC3339),
		"actor": actorView(r.ActorRef, r.Assurance, session), "revision": r.Revision}
	opt := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	opt("decision", r.Decision)
	opt("status", r.Status)
	opt("note", r.Note)
	opt("reason", r.ReasonCode)
	opt("batch_id", r.BatchID)
	for k, v := range r.Extra {
		m[k] = v
	}
	return m
}

// history serves GET /history: the decision timeline with reasons, newest first.
// Query: status, requester, q, from, to (RFC 3339), kind (decided kinds by default),
// limit (1..100), cursor (opaque, from next_cursor). Every event carries requester{id,label,kind} (null when the
// item is not indexed) and the page carries facets{requesters, statuses} counted over the filter without the
// facet's own part.
func (i *Inbox) history(w http.ResponseWriter, r *http.Request, session string) {
	q := r.URL.Query()
	f := EventFilter{Status: q.Get("status"), Requester: q.Get("requester"), Q: q.Get("q"), Limit: 25, Kinds: decidedKinds}
	if f.Status != "" && !slices.Contains([]string{"approved", "denied", "answered", "expired", "withdrawn"}, f.Status) {
		writeHITLError(w, hitl.ErrInvalidRequest)
		return
	}
	if k := q.Get("kind"); k != "" {
		if !slices.Contains(approvalsapi.EventKinds, k) {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
		f.Kinds = []string{k}
	}
	for name, dst := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeHITLError(w, hitl.ErrInvalidRequest)
				return
			}
			*dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeHITLError(w, hitl.ErrInvalidRequest)
			return
		}
		f.After = n
	}
	rows, next, err := i.Store.Timeline(r.Context(), f)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	events := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := eventView(row, session)
		m["mini"] = map[string]any{"title": row.Title, "risk": row.Risk, "group": row.Group}
		m["requester"] = nil
		if row.ReqID != "" {
			m["requester"] = map[string]any{"id": row.ReqID, "label": row.ReqLabel, "kind": row.ReqKind}
		}
		events = append(events, m)
	}
	reqs, stats, err := i.Store.TimelineFacets(r.Context(), f)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	facets := map[string]any{"requesters": make([]map[string]any, 0, len(reqs)), "statuses": make([]map[string]any, 0, len(stats))}
	for _, fc := range reqs {
		facets["requesters"] = append(facets["requesters"].([]map[string]any), map[string]any{"id": fc.ID, "label": fc.Label, "count": fc.Count})
	}
	for _, fc := range stats {
		facets["statuses"] = append(facets["statuses"].([]map[string]any), map[string]any{"status": fc.Status, "count": fc.Count})
	}
	out := map[string]any{"events": events, "facets": facets}
	if next > 0 {
		out["next_cursor"] = strconv.FormatInt(next, 10)
	}
	writeValue(w, out)
}

// audit serves GET /items/{id}/audit: every logged event of one item, oldest first.
func (i *Inbox) audit(w http.ResponseWriter, r *http.Request, id, session string) {
	rows, err := i.Store.ItemEvents(r.Context(), id)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	events := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		events = append(events, eventView(row, session))
	}
	writeValue(w, map[string]any{"item_id": id, "events": events})
}

// deferItem serves POST and DELETE /items/{id}/defer. A snooze needs only a
// browser session (it decides nothing), is clamped to the item's expiry and never
// extends it, and is audited.
func (i *Inbox) deferItem(w http.ResponseWriter, r *http.Request, item browserItem, part hitl.Participant, raw []byte) {
	staleness := func(err error) {
		state := item.State
		writeHITLError(w, &hitl.StaleRevisionError{Operation: "defer", ItemID: item.ItemID, RevisionKind: "interaction", Expected: item.Revision, Actual: item.Revision, CurrentState: state, Outcome: item.TerminalOutcome})
	}
	if r.Method == http.MethodDelete {
		if _, err := i.Store.Undefer(r.Context(), item.ItemID, part.PrincipalRef, part.Assurance); err != nil {
			writeHITLError(w, err)
			return
		}
		writeValue(w, map[string]any{"item_id": item.ItemID, "deferred_until": nil, "revision": item.Revision})
		return
	}
	var cmd struct {
		ExpectedRevision int64  `json:"expected_revision"`
		DurationS        *int64 `json:"duration_s,omitempty"`
		UntilMs          *int64 `json:"until_ms,omitempty"`
		Reason           string `json:"reason,omitempty"`
	}
	if err := strictJSON(raw, &cmd); err != nil {
		writeHITLError(w, err)
		return
	}
	now := time.Now()
	secs := int64(approvalsapi.DefaultDeferS)
	switch {
	case cmd.DurationS != nil && cmd.UntilMs != nil, cmd.ExpectedRevision < 1, utf8.RuneCountInString(cmd.Reason) > 280:
		writeHITLError(w, fmt.Errorf("%w: send expected_revision and one of duration_s or until_ms; reason is at most 280 characters", hitl.ErrInvalidRequest))
		return
	case cmd.DurationS != nil:
		secs = *cmd.DurationS
	case cmd.UntilMs != nil:
		secs = (*cmd.UntilMs - now.UnixMilli()) / 1000
	}
	if secs < approvalsapi.MinDeferS || secs > approvalsapi.MaxDeferS {
		writeHITLError(w, fmt.Errorf("%w: the snooze must be between %d seconds and 7 days", hitl.ErrInvalidRequest, approvalsapi.MinDeferS))
		return
	}
	if item.State.IsTerminal() || cmd.ExpectedRevision != item.Revision {
		staleness(nil)
		return
	}
	until, clamped := now.Add(time.Duration(secs)*time.Second), false
	if exp := item.Projection.ExpiresAt; exp != nil && exp.Before(until) {
		until, clamped = *exp, true
	}
	rec, err := i.Store.Defer(r.Context(), item.ItemID, cmd.ExpectedRevision, until, cmd.Reason, part.PrincipalRef, part.Assurance)
	switch {
	case errors.Is(err, ErrDeferStale), errors.Is(err, ErrDeferTerminal):
		staleness(err)
		return
	case err != nil:
		writeHITLError(w, err)
		return
	}
	writeValue(w, map[string]any{"item_id": item.ItemID, "deferred_until": until.UTC().Format(time.RFC3339), "expires_before_snooze_ends": clamped, "revision": rec.Revision})
}
