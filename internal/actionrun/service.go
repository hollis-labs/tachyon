package actionrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// ApplicationID is the approval caller scope for registered-action requests.
const ApplicationID = "tachyon-actions"

// ApprovalTTL is how long a request waits for the owner.
const ApprovalTTL = 30 * time.Minute

func extraString(req hitl.EnqueueRequest, key string) string {
	var value string
	_ = json.Unmarshal(req.Extra[key], &value)
	return value
}

// Labels are the Cerberus env/owner/admin labels of a target resource.
type Labels struct {
	Env   string `json:"env"`
	Owner string `json:"owner"`
	Admin string `json:"admin"`
}

func (l Labels) String() string {
	return fmt.Sprintf("env=%s owner=%s admin=%s", l.Env, l.Owner, l.Admin)
}

// Dev reports whether the labels mark a resource a Tachyon approval may act on.
// Prod, admin=owner and unlabeled targets are not dev and need Cerberus's
// passkey path, which Tachyon cannot satisfy.
func (l Labels) Dev() bool { return l.Env == "dev" && l.Owner == "self" && l.Admin == "self" }

// Service creates and reads action requests.
type Service struct {
	Store    *Store
	Manager  *Manager
	Approval *hitl.Service
	Clock    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

// Item implements ItemReader through the HITL service's store view.
func (s *Service) Item(ctx context.Context, itemID string) (hitl.Record, error) {
	return s.Manager.Items.Get(ctx, itemID)
}

// RequestInput is what an agent supplies.
type RequestInput struct {
	Machine string            `json:"machine"`
	Action  string            `json:"action"`
	Params  map[string]string `json:"params,omitempty"`
	Reason  string            `json:"reason"`
	// AgentID is the server-derived requester, never taken from agent JSON.
	AgentID string `json:"-"`
}

// Handle is returned to the agent.
type Handle struct {
	RunID    string   `json:"run_id"`
	ItemID   string   `json:"item_id"`
	State    string   `json:"state"`
	Machine  string   `json:"machine"`
	Action   string   `json:"action"`
	Argv     []string `json:"argv"`
	Command  string   `json:"command"`
	OpDigest string   `json:"op_digest"`
	Class    string   `json:"class"`
	Labels   string   `json:"labels,omitempty"`
	Expires  string   `json:"expires_at"`
}

func newID(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// Request validates, classifies and enqueues an approval. Nothing runs until
// the owner approves; the op_digest is computed here, never by the agent.
func (s *Service) Request(ctx context.Context, in RequestInput) (Handle, error) {
	spec, act, err := Resolve(in.Machine, in.Action, in.Params)
	if err != nil {
		return Handle{}, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 2000 {
		return Handle{}, &ValidationError{"reason is required (1-2000 characters): say why this action is needed"}
	}
	if in.AgentID == "" {
		in.AgentID = "unknown-agent"
	}
	class, labelText := act.Class, ""
	if act.LabelParam != "" {
		labels, err := s.Manager.labelsFor(ctx, in.Machine, spec.Params[act.LabelParam])
		if err != nil {
			return Handle{}, &ValidationError{err.Error()}
		}
		labelText = labels.String()
		if !labels.Dev() {
			return Handle{}, &ValidationError{fmt.Sprintf("target %s on %s is not a dev resource (%s). Prod, admin=owner and unlabeled targets need the Cerberus passkey path; Tachyon will not run them", spec.Params[act.LabelParam], in.Machine, labelText)}
		}
		class = "medium"
	}
	if class == "" {
		class = "medium"
	}
	digest := spec.Digest()
	now := s.now()
	expires := now.Add(ApprovalTTL)
	title := fmt.Sprintf("%s on %s", act.Title, in.Machine)
	summary := fmt.Sprintf("%s asks to run %s on %s. Reason: %s", in.AgentID, act.ID, in.Machine, in.Reason)
	if labelText != "" {
		summary += " Target labels: " + labelText + "."
	}
	if act.Interactive {
		summary += " The command prompts; you will type at the terminal after approving."
	}
	extra := map[string]any{
		"title": title, "summary": summary, "request": spec.Command(), "class": class, "op_digest": digest,
		"machine": spec.Machine, "action": spec.Action, "argv": spec.Argv, "params": spec.Params, "reason": in.Reason,
		"interactive": act.Interactive, "labels": labelText,
		"impact": "Runs the exact command above on " + in.Machine + " when you approve.",
	}
	req := hitl.EnqueueRequest{
		ContractVersion: hitl.ContractVersion, Kind: "approval", IdempotencyKey: newID("act_"),
		Source:    hitl.SourceAssertion{ApplicationID: ApplicationID, ApplicationLabel: "Registered actions", AgentID: in.AgentID},
		ExpiresAt: &expires, Extra: map[string]json.RawMessage{},
	}
	for k, v := range extra {
		raw, _ := json.Marshal(v)
		req.Extra[k] = raw
	}
	handle, err := s.Approval.Enqueue(ctx, req)
	if err != nil {
		return Handle{}, err
	}
	run := Run{
		ID: newID("run_"), ItemID: handle.ItemID, Machine: spec.Machine, Action: spec.Action, Params: spec.Params, Argv: spec.Argv,
		OpDigest: digest, Class: class, Labels: labelText, RequestedBy: in.AgentID, Reason: in.Reason, State: StatePending, CreatedAt: now,
	}
	if err := s.Store.Insert(ctx, run); err != nil {
		return Handle{}, err
	}
	slog.Info("action requested", "run_id", run.ID, "item_id", run.ItemID, "op_digest", digest, "machine", run.Machine, "action", run.Action, "agent", in.AgentID, "class", class)
	return Handle{RunID: run.ID, ItemID: run.ItemID, State: run.State, Machine: run.Machine, Action: run.Action, Argv: run.Argv, Command: spec.Command(), OpDigest: digest, Class: class, Labels: labelText, Expires: expires.Format(time.RFC3339)}, nil
}

// Sync advances pending runs from their approval outcomes and starts approved
// non-interactive actions. Interactive ones wait for an owner terminal.
func (s *Service) Sync(ctx context.Context) error {
	open, err := s.Store.Open(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range open {
		if r.State == StateApproved && !r.Interactive && !s.Manager.Live(r.ID) {
			// Approved but not started (the concurrency bound was reached): retry.
			if _, err := s.Manager.Start(ctx, r.ID, 120, 30); err != nil && !errors.Is(err, ErrBusy) {
				errs = append(errs, err)
			}
			continue
		}
		if r.State != StatePending {
			continue
		}
		rec, err := s.Item(ctx, r.ItemID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		lapsed := rec.ExpiresAt != nil && !s.now().Before(*rec.ExpiresAt)
		switch out := rec.Outcome.(type) {
		case hitl.Resolved:
			if out.Resolution.Response.Decision != "approved" {
				_ = s.Store.Finish(ctx, r.ID, StatePending, StateDenied, nil, "", "the owner denied the request", s.now())
				continue
			}
			next := StateApproved
			if r.Interactive {
				next = StateWaitingInput
			}
			if won, err := s.Store.Transition(ctx, r.ID, StatePending, next, nil); err != nil {
				errs = append(errs, err)
			} else if won && !r.Interactive {
				if _, err := s.Manager.Start(ctx, r.ID, 120, 30); err != nil && !errors.Is(err, ErrBusy) {
					errs = append(errs, err)
				}
			}
		case nil:
			if lapsed {
				_ = s.Store.Finish(ctx, r.ID, StatePending, StateExpired, nil, "", "the approval expired before a decision", s.now())
			}
		default:
			_ = s.Store.Finish(ctx, r.ID, StatePending, StateExpired, nil, "", "the approval ended without a decision", s.now())
		}
	}
	return errors.Join(errs...)
}

// Watch runs Sync until ctx ends.
func (s *Service) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Sync(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("action run sync", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Get returns a run after bringing it up to date with its approval.
func (s *Service) Get(ctx context.Context, runID string) (Run, error) {
	_ = s.Sync(ctx)
	return s.Store.Get(ctx, runID)
}

// ByItem is Get keyed by the approval item id.
func (s *Service) ByItem(ctx context.Context, itemID string) (Run, error) {
	_ = s.Sync(ctx)
	return s.Store.ByItem(ctx, itemID)
}

// Await waits until the run is terminal or the wait elapses.
func (s *Service) Await(ctx context.Context, runID string, wait time.Duration) (Run, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		r, err := s.Get(ctx, runID)
		if err != nil || r.Terminal || wait <= 0 {
			return r, err
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-deadline.C:
			return s.Get(ctx, runID)
		case <-tick.C:
		}
	}
}
