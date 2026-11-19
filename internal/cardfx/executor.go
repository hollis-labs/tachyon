package cardfx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// Effect is one change a card asks to apply. Target names the thing
// (scheme:path); Op and Value say what to do to it.
type Effect struct {
	Target string
	Op     string
	Value  string
}

// Target applies effects for one scheme. The set of registered targets is the
// allowlist: a card cannot name a scheme that is not registered here.
type Target interface {
	Scheme() string
	// Validate judges an effect without side effects.
	Validate(Effect) error
	// Apply performs it and returns a short human detail.
	Apply(ctx context.Context, e Effect) (string, error)
}

// Executor applies approved cards' effects.
type Executor struct {
	Items   *approvals.Store
	Store   *Store
	Targets map[string]Target
	Now     func() time.Time
}

// New returns an executor over the given targets.
func New(items *approvals.Store, store *Store, targets ...Target) *Executor {
	e := &Executor{Items: items, Store: store, Targets: map[string]Target{}, Now: func() time.Time { return time.Now().UTC() }}
	for _, t := range targets {
		e.Targets[t.Scheme()] = t
	}
	return e
}

func (e *Executor) now() time.Time { return e.Now().UTC() }

// Recover closes runs a crash left in flight. They are not retried: an effect
// may already have applied, and replaying it is worse than reporting it.
func (e *Executor) Recover(ctx context.Context) error {
	ids, err := e.Store.Interrupted(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r := Receipt{ItemID: id, State: StateFailed, Reason: "Interrupted by a restart; the effects were not retried. Check the targets before asking again.", StartedAt: e.now()}
		if err := e.Store.Finish(ctx, r, e.now()); err != nil {
			return err
		}
	}
	return nil
}

// Watch runs the executor until ctx ends.
func (e *Executor) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 3 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := e.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("card effects pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Run makes one pass over resolved cards not yet considered.
func (e *Executor) Run(ctx context.Context) error {
	rows, err := e.Items.DB.Reader.QueryContext(ctx, "SELECT id FROM approval_items WHERE state='resolved' AND id NOT IN (SELECT item_id FROM card_effect_runs) ORDER BY created_ns LIMIT 50")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		rec, err := e.Items.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := e.Execute(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

func parseEffects(raw json.RawMessage) ([]Effect, error) {
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]Effect, 0, len(list))
	for _, m := range list {
		var e Effect
		e.Target, _ = m["target"].(string)
		e.Op, _ = m["op"].(string)
		switch v := m["value"].(type) {
		case string:
			e.Value = v
		case nil:
		default:
			return nil, fmt.Errorf("effect value must be a string")
		}
		out = append(out, e)
	}
	return out, nil
}

// Execute considers one resolved card: it runs the effects of an approved card
// whose digest still matches, and records a receipt either way.
func (e *Executor) Execute(ctx context.Context, rec hitl.Record) error {
	started := e.now()
	won, err := e.Store.Claim(ctx, rec.ItemID, started)
	if err != nil || !won {
		return err
	}
	receipt := Receipt{ItemID: rec.ItemID, State: StateNone, StartedAt: started}
	finish := func() error { return e.Store.Finish(ctx, receipt, e.now()) }

	var req hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &req) != nil || req.Kind != "card.approval" {
		return finish()
	}
	resolved, ok := rec.Outcome.(hitl.Resolved)
	if !ok {
		return finish()
	}
	res := resolved.Resolution
	receipt.Decision = res.Response.Decision
	receipt.Approver = res.Participant.PrincipalRef
	receipt.Assurance = res.Participant.Assurance
	receipt.Note = res.Response.Note
	at := res.ResolvedAt
	receipt.ApprovedAt = &at
	rawEffects := req.Extra["effects"]
	var altID string
	_ = json.Unmarshal(res.Response.Extra["alternative_id"], &altID)
	receipt.Alternative = altID
	if (len(rawEffects) == 0 && altID == "") || res.Response.Decision != "approved" {
		return finish()
	}
	refuse := func(reason string) error {
		receipt.State, receipt.Reason = StateRefused, reason
		return finish()
	}

	// The digest binds the approval to the exact change set, destructive flag, affected items and
	// alternatives; recompute it from what is stored rather than trusting the stored value.
	want, err := cards.DigestOf(req.Kind, req.Extra)
	var stored string
	_ = json.Unmarshal(req.Extra["op_digest"], &stored)
	receipt.OpDigest = stored
	if err != nil || want != stored {
		return refuse("The stored change set no longer matches the approved digest.")
	}
	if altID != "" {
		// The operator chose an alternative: run its effects, not the main set.
		var alts []struct {
			ID      string          `json:"id"`
			Effects json.RawMessage `json:"effects"`
		}
		_ = json.Unmarshal(req.Extra["alternatives"], &alts)
		rawEffects = nil
		for _, a := range alts {
			if a.ID == altID {
				rawEffects = a.Effects
			}
		}
		if len(rawEffects) == 0 {
			return refuse(fmt.Sprintf("The chosen alternative %q is not part of the approved request.", altID))
		}
	}
	effects, err := parseEffects(rawEffects)
	if err != nil {
		return refuse(err.Error())
	}
	// Validate every effect before running any: all or nothing at the gate.
	targets := make([]Target, len(effects))
	for i, ef := range effects {
		scheme, _, _ := strings.Cut(ef.Target, ":")
		t, ok := e.Targets[scheme]
		if !ok {
			return refuse(fmt.Sprintf("Effect %d names a target that is not allowed: %q.", i+1, scheme))
		}
		if err := t.Validate(ef); err != nil {
			return refuse(fmt.Sprintf("Effect %d is not valid: %v", i+1, err))
		}
		targets[i] = t
	}
	receipt.State = StateCompleted
	for i, ef := range effects {
		r := EffectResult{Index: i, Target: ef.Target, Op: ef.Op, Status: "skipped"}
		if receipt.State == StateCompleted {
			detail, err := targets[i].Apply(ctx, ef)
			if err != nil {
				r.Status, r.Error = "failed", err.Error()
				receipt.State = StateFailed
			} else {
				r.Status, r.Detail = "ok", detail
			}
		}
		receipt.Effects = append(receipt.Effects, r)
	}
	slog.Info("card effects run", "item_id", rec.ItemID, "state", receipt.State, "effects", len(effects), "approver", receipt.Approver)
	return finish()
}
