package builderchange

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/builderparts"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Tick is how often awaiting plans are checked for approval; applyStuck is how long an applying change may sit
// before it is called failed (the process died between apply and the answer).
const (
	Tick       = 5 * time.Second
	applyStuck = 5 * time.Minute
)

// APIError is a refusal with its HTTP status.
type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Code }

func refuse(status int, code, msg string) *APIError { return &APIError{status, code, msg} }

type Target struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Path    string `json:"path"`
	BaseSHA string `json:"base_sha"`
	// NewSHA is the sha the file has once the change is applied; handoff mode detects "applied" when every file has it.
	NewSHA string `json:"new_sha,omitempty"`
}
type Session struct {
	SessionID string `json:"session_id"`
}
type Effect struct {
	RunningSessions []Session `json:"running_sessions"`
	Activation      string    `json:"activation"`
	RestartRequired []string  `json:"restart_required"`
}
type PlanInfo struct {
	PlanID         string `json:"plan_id"`
	State          string `json:"state"`
	Digest         string `json:"digest"`
	ExpiresAt      *int64 `json:"expires_at"` // nil when the plan id is only a hint (handoff mode)
	ApproveCommand string `json:"approve_command"`
}
type Result struct {
	BackupID        string   `json:"backup_id"`
	Activation      string   `json:"activation"`
	RestartRequired []string `json:"restart_required"`
	Reloaded        bool     `json:"reloaded"`
	ReloadError     *string  `json:"reload_error"`
	AppliedAt       *int64   `json:"applied_at"`
	// Detected is set in handoff mode, where the apply is seen from file shas: no backup or activation text.
	Detected     bool `json:"detected,omitempty"`
	FilesChanged int  `json:"files_changed,omitempty"`
}

// Handoff describes the bundle the operator (or a session) plans in Tether when Tachyon holds no control token.
type Handoff struct {
	Mode             string `json:"mode"`
	Name             string `json:"name"`
	BundleSHA256     string `json:"bundle_sha256"`
	Files            int    `json:"files"`
	SessionAvailable bool   `json:"session_available"`
	Reason           string `json:"reason,omitempty"`
	// Commands are built here so the UI never hard-codes CLI flags; show and approve carry <plan_id> until known.
	Commands []Command `json:"commands"`
}

// Command is one step of the hand-off. kind shell: run Command in a terminal on the host. kind session: Command
// is the text to give a Tether session that holds catalog.write (plan and apply are MCP tools; there is no CLI).
// An omitted kind reads as shell.
type Command struct {
	Step    string `json:"step"` // plan | show | approve | apply
	Label   string `json:"label"`
	Kind    string `json:"kind"` // shell | session
	Command string `json:"command"`
}
type Failure struct {
	Step    string `json:"step"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Stale struct {
	ChangedPaths []string `json:"changed_paths"`
	TheirsSHA    string   `json:"theirs_sha"`
	MineDiff     string   `json:"mine_diff"`
	TheirsDiff   string   `json:"theirs_diff"`
}

// Change is the API shape (builderapi.Change).
type Change struct {
	ChangeID      string            `json:"change_id"`
	RequestID     string            `json:"request_id"`
	State         string            `json:"state"`
	Note          string            `json:"note"`
	Operator      string            `json:"operator"`
	CreatedAt     int64             `json:"created_at"`
	UpdatedAt     int64             `json:"updated_at"`
	Targets       []Target          `json:"targets"`
	Base          map[string]string `json:"base"`
	Diff          string            `json:"diff"`
	Checks        []Check           `json:"checks"`
	Privileged    []Priv            `json:"privileged"`
	AckPrivileged bool              `json:"ack_privileged"`
	Effect        Effect            `json:"effect"`
	Plan          *PlanInfo         `json:"plan"`
	Result        *Result           `json:"result"`
	Failure       *Failure          `json:"failure"`
	Stale         *Stale            `json:"stale"`
	Handoff       *Handoff          `json:"handoff,omitempty"`
	// PartEffects is what applying does to the Library parts tables (B5); omitted when the change touches no part.
	PartEffects *builderparts.Effects `json:"part_effects,omitempty"`
}

// Request is POST /changes.
type Request struct {
	RequestID string `json:"request_id"`
	Note      string `json:"note"`
	Ops       []OpIn `json:"ops"`
}
type OpIn struct {
	Target struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		PartKind string `json:"part_kind"`
	} `json:"target"`
	ExpectedSHA string `json:"expected_sha"`
	Op          struct {
		Type        string          `json:"type"`
		Path        string          `json:"path"`
		Value       json.RawMessage `json:"value"`
		SnapshotSeq int             `json:"snapshot_seq"`
	} `json:"op"`
}

// Service runs changes. Approval is the host's `tether catalog approve`; the service only watches for it.
// Mode is builderapi.ModeHandoff when Tachyon holds no catalog.write credential (Tether is then a read-only
// client): changes stop at awaiting_handoff and "applied" is detected from file shas. Empty or ModeDirect is
// the original plan/approve/apply flow.
type Service struct {
	Mode   string
	DB     *localstore.Store
	Tether Tether
	Index  *catalogindex.Store // masked snapshots; may be nil
	Bus    *catalogindex.Broker
	Now    func() time.Time

	mu sync.Mutex
}

func (s *Service) handoff() bool { return s.Mode == builderapi.ModeHandoff }

// ModeName is the mode GET /capabilities reports.
func (s *Service) ModeName() string {
	if s.handoff() {
		return builderapi.ModeHandoff
	}
	return builderapi.ModeDirect
}

// noPlan reports whether a change has nothing to submit yet: no Tether plan (direct) or no bundle (handoff).
func (s *Service) noPlan(c *Change) bool {
	if s.handoff() {
		return c.Handoff == nil
	}
	return c.Plan == nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func shaOf(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

func pathFor(kind, id string) string {
	switch kind {
	case builderapi.TargetAgent:
		return "agents/" + id + ".yaml"
	case builderapi.TargetLaunch:
		return "launches/" + id + ".yaml"
	case builderapi.TargetProject:
		return "projects/" + id + ".yaml"
	case builderapi.TargetSkill:
		return "skills/" + id + ".md"
	}
	return ""
}

func maskFor(path, content string) string {
	if strings.HasSuffix(path, ".md") {
		return catalogindex.MaskText(content)
	}
	m, _, err := catalogindex.MaskYAML(content)
	if err != nil {
		return ""
	}
	return m
}

type group struct {
	kind, id, path, expected string
	partKind                 string // set when kind is part
	edits                    []Edit
	attach                   []attachOp
	implied                  bool // an agent recompiled because a part it carries changed
}

func (s *Service) groups(req Request) ([]group, *APIError) {
	if !requestIDPattern.MatchString(req.RequestID) || len(req.Note) > builderapi.MaxNoteChars || len(req.Ops) < 1 || len(req.Ops) > builderapi.MaxOps {
		return nil, refuse(400, "invalid", "Send request_id, an optional note up to 500 characters and 1 to 20 ops.")
	}
	var out []group
	idx := map[string]int{}
	for _, o := range req.Ops {
		k, id := o.Target.Kind, o.Target.ID
		pk := ""
		path := pathFor(k, id)
		if k == builderapi.TargetPart {
			pk = o.Target.PartKind
			if pk != builderparts.KindPrompt && pk != builderparts.KindPolicy {
				return nil, refuse(400, "invalid", "A part target needs part_kind prompt or policy.")
			}
			path = partPath(pk, id)
		}
		if path == "" || !builderapi.IDPattern.MatchString(id) {
			return nil, refuse(400, "invalid", "target needs a kind (agent, launch, project, skill, part) and a slug id.")
		}
		key := k + "/" + pk + "/" + id
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, group{kind: k, id: id, path: path, partKind: pk})
		}
		if o.ExpectedSHA != "" {
			if out[i].expected != "" && out[i].expected != o.ExpectedSHA {
				return nil, refuse(400, "invalid", "A target has two different expected_sha values.")
			}
			out[i].expected = o.ExpectedSHA
		}
		if k == builderapi.TargetAgent && isAttachPath(o.Op.Path) {
			a, aerr := parseAttach(o)
			if aerr != nil {
				return nil, aerr
			}
			out[i].attach = append(out[i].attach, a)
			continue
		}
		out[i].edits = append(out[i].edits, Edit{Type: o.Op.Type, Path: o.Op.Path, Value: o.Op.Value, SnapshotSeq: o.Op.SnapshotSeq})
	}
	return out, nil
}

type built struct {
	files      []File
	base       map[string]string
	targets    []Target
	diff       string
	privileged []Priv
	effects    *builderparts.Effects
}

// build reads the live files, applies the edits and diffs the masked result. Nothing is written.
func (s *Service) build(ctx context.Context, gs []group) (built, *APIError) {
	b := built{base: map[string]string{}}
	var diffs []string
	pp := newPass(s)
	// Parts first: an agent in the same change may attach the part's new revision.
	for _, g := range gs {
		if g.kind != builderapi.TargetPart {
			continue
		}
		d, privs, base, perr := pp.edit(ctx, g)
		if perr != nil {
			return b, perr
		}
		b.targets = append(b.targets, Target{Kind: g.kind, ID: g.id, Path: g.path, BaseSHA: base})
		if base != "" {
			b.base[g.path] = base
		}
		b.privileged = append(b.privileged, privs...)
		diffs = append(diffs, d)
	}
	agents, perr := pp.affected(ctx)
	if perr != nil {
		return b, perr
	}
	have := map[string]bool{}
	var files []group
	for _, g := range gs {
		if g.kind == builderapi.TargetPart {
			continue
		}
		if g.kind == builderapi.TargetAgent {
			have[g.id] = true
		}
		files = append(files, g)
	}
	for _, a := range agents {
		if !have[a] {
			files = append(files, group{kind: builderapi.TargetAgent, id: a, path: pathFor(builderapi.TargetAgent, a), implied: true})
		}
	}
	for _, g := range files {
		live, exists, err := s.Tether.ReadFile(ctx, g.path)
		if err != nil {
			return b, tetherErr(err)
		}
		creating := len(g.edits) > 0 && g.edits[0].Type == builderapi.OpCreate
		sha := ""
		if exists {
			sha = shaOf(live)
		}
		switch {
		case creating && exists:
			return b, refuse(409, "exists", "That id already exists.")
		case !creating && !exists:
			return b, refuse(404, "not_found", "No such file.")
		case !creating && !g.implied && g.expected == "":
			return b, refuse(400, "invalid", "expected_sha is required to change an existing file.")
		case !creating && !g.implied && g.expected != sha:
			return b, refuse(409, "stale_base", "The file changed since you opened it. Reload it.")
		}
		snapshot := func(seq int) (string, bool) { return s.snapshot(ctx, g.path, seq) }
		next, privs, oerr := live, []Priv(nil), (*OpError)(nil)
		if len(g.edits) > 0 {
			next, privs, oerr = Apply(g.kind, g.id, g.path, live, exists, g.edits, snapshot)
			if oerr != nil {
				return b, refuse(oerr.Status, oerr.Code, oerr.Msg)
			}
		}
		if g.kind == builderapi.TargetAgent && (len(g.attach) > 0 || g.implied || len(pp.changed) > 0) {
			c, pprivs, perr := pp.compile(ctx, g, g.path, next)
			if perr != nil {
				return b, perr
			}
			next, privs = c, append(privs, pprivs...)
		}
		if next == live {
			if g.implied {
				continue // the recompile left this file as it was
			}
			return b, refuse(400, "no_change", "That edit changes nothing.")
		}
		b.files = append(b.files, File{Path: g.path, Content: next})
		b.targets = append(b.targets, Target{Kind: g.kind, ID: g.id, Path: g.path, BaseSHA: sha, NewSHA: shaOf(next)})
		if exists {
			b.base[g.path] = sha
		}
		b.privileged = append(b.privileged, privs...)
		diffs = append(diffs, builderread.UnifiedDiff(g.path, maskFor(g.path, live), maskFor(g.path, next)))
	}
	if !pp.eff.Empty() {
		b.effects = pp.eff
	}
	b.diff = strings.Join(diffs, "")
	return b, nil
}

// snapshot returns the masked content of the path's seq-th snapshot (1 = oldest kept), as the read model counts.
func (s *Service) snapshot(ctx context.Context, path string, seq int) (string, bool) {
	if s.Index == nil || seq < 1 {
		return "", false
	}
	list, err := s.Index.Snapshots(ctx, path, catalogindex.SnapshotsPerPath)
	if err != nil || seq > len(list) {
		return "", false
	}
	return list[len(list)-seq].Content, true
}

func tetherErr(err error) *APIError {
	var te *TetherError
	if errors.As(err, &te) {
		return refuse(502, "tether_refused", "Tether refused: "+catalogindex.MaskText(te.Message))
	}
	return refuse(503, "tether_unavailable", "Tether is not reachable.")
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "chg_" + hex.EncodeToString(b[:])
}

// Create is POST /changes: compute, validate, plan. Idempotent by request_id.
func (s *Service) Create(ctx context.Context, operator string, req Request) (Change, bool, *APIError) {
	gs, perr := s.groups(req)
	if perr != nil {
		return Change{}, false, perr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.byRequest(ctx, req.RequestID); err != nil {
		return Change{}, false, refuse(500, "internal", "The change could not be checked.")
	} else if prev != nil {
		return *prev, false, nil
	}
	var recent int
	_ = s.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_changes WHERE operator=? AND created_ns>?`, operator, s.now().Add(-time.Minute).UnixNano()).Scan(&recent)
	if recent >= builderapi.ChangesPerMinute {
		return Change{}, false, refuse(429, "rate_limited", "Too many changes in a minute. Try again shortly.")
	}
	for _, g := range gs {
		var other string
		if s.DB.Reader.QueryRowContext(ctx, `SELECT change_id FROM catalog_change_paths WHERE path=?`, g.path).Scan(&other) == nil {
			return Change{}, false, refuse(409, "change_open", "Another change is open on "+g.path+".")
		}
	}
	b, perr := s.build(ctx, gs)
	if perr != nil {
		return Change{}, false, perr
	}
	c := Change{ChangeID: newID(), RequestID: req.RequestID, State: builderapi.ChangeDraft, Note: req.Note, Operator: operator,
		Targets: b.targets, Base: b.base, Diff: b.diff, Privileged: nonNilPriv(b.privileged), Checks: []Check{},
		Effect: Effect{RunningSessions: []Session{}, RestartRequired: []string{}}}
	c.CreatedAt = s.now().UnixMilli()
	c.PartEffects = b.effects
	if len(b.files) > 0 {
		if perr = s.validateAndPlan(ctx, &c, b.files); perr != nil {
			return Change{}, false, perr
		}
	}
	if s.handoff() && c.Handoff != nil && len(c.Privileged) == 0 {
		c.State = builderapi.ChangeAwaitingHandoff // nothing to acknowledge, so the bundle is ready
	}
	raw, _ := json.Marshal(req)
	if err := s.insert(ctx, c, string(raw)); err != nil {
		return Change{}, false, refuse(500, "internal", "The change could not be saved.")
	}
	s.publish(c)
	return c, true, nil
}

func nonNilPriv(p []Priv) []Priv {
	if p == nil {
		return []Priv{}
	}
	return p
}

// validateAndPlan fills checks and the plan. A failing validation keeps the draft (so the dialog can show why)
// but builds no plan; submit then refuses it.
func (s *Service) validateAndPlan(ctx context.Context, c *Change, files []File) *APIError {
	rep, err := s.Tether.Validate(ctx, files)
	if err != nil {
		return tetherErr(err)
	}
	c.Checks = append([]Check{}, rep.Results...)
	if !rep.OK {
		c.Plan, c.Handoff = nil, nil
		return nil
	}
	if s.handoff() {
		c.Handoff = s.handoffFor(c, files)
		return nil
	}
	plan, err := s.Tether.Plan(ctx, "builder "+c.ChangeID, files)
	if err != nil {
		return tetherErr(err)
	}
	if plan.State == "invalid" || !plan.ValidationOK && plan.ValidationError != "" {
		c.Checks = append(c.Checks, Check{Name: "tether_plan", Status: "fail", Message: clip(catalogindex.MaskText(plan.ValidationError), 300), Entity: ""})
		c.Plan = nil
		return nil
	}
	c.Plan = &PlanInfo{PlanID: plan.ID, State: plan.State, Digest: plan.Digest, ExpiresAt: pointer(plan.ExpiresAt.UnixMilli()), ApproveCommand: "tether catalog approve " + plan.ID}
	c.Effect.Activation = plan.Activation
	c.Effect.RunningSessions = []Session{}
	for _, id := range plan.RunningSessions {
		c.Effect.RunningSessions = append(c.Effect.RunningSessions, Session{SessionID: id})
	}
	for _, p := range plan.Privileged {
		if !hasPriv(c.Privileged, p) {
			c.Privileged = append(c.Privileged, Priv{Path: p, Field: ""})
		}
	}
	return nil
}

func hasPriv(l []Priv, path string) bool {
	for _, p := range l {
		if p.Path == path {
			return true
		}
	}
	return false
}

// Submit moves a draft to awaiting_approval. The operator approves on the host.
func (s *Service) Submit(ctx context.Context, id string, ack bool) (Change, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Change{}, perr
	}
	if c.State != builderapi.ChangeDraft {
		return *c, refuse(409, "bad_state", "Only a draft can be submitted.")
	}
	for _, ck := range c.Checks {
		if ck.Status == builderapi.CheckFail {
			return *c, refuse(409, "validation_failed", "Fix the failing checks first.")
		}
	}
	partsOnly := s.noPlan(c) && c.PartEffects != nil && !hasFileTarget(*c)
	if s.noPlan(c) && !partsOnly {
		return *c, refuse(409, "validation_failed", "This change has no plan.")
	}
	if len(c.Privileged) > 0 && !ack {
		return *c, refuse(400, "ack_required", "This change touches privileged fields; send ack_privileged true.")
	}
	if partsOnly {
		// No agent carries the part, so there is no Tether plan: the Library row is the whole change.
		c.AckPrivileged = ack
		s.finish(ctx, c, ApplyResult{Applied: true})
		return s.mustLoad(ctx, id), nil
	}
	if perr = s.replan(ctx, c); perr != nil {
		return *c, perr
	}
	c.AckPrivileged = ack
	c.State = builderapi.ChangeAwaitingApproval
	if s.handoff() {
		c.State = builderapi.ChangeAwaitingHandoff
	}
	return s.save(ctx, c)
}

// replan rebuilds the bundle from the stored request on the live files and plans it again, which re-arms the
// same Tether plan when nothing moved. A moved base makes the change stale.
func (s *Service) replan(ctx context.Context, c *Change) *APIError {
	req, perr := s.request(ctx, c.ChangeID)
	if perr != nil {
		return perr
	}
	gs, perr := s.groups(req)
	if perr != nil {
		return perr
	}
	for i := range gs {
		gs[i].expected = c.Base[gs[i].path]
	}
	b, perr := s.build(ctx, gs)
	if perr != nil {
		if perr.Code == "stale_base" {
			s.markStale(ctx, c)
		}
		return perr
	}
	c.Diff, c.Privileged, c.PartEffects = b.diff, nonNilPriv(b.privileged), b.effects
	if perr = s.validateAndPlan(ctx, c, b.files); perr != nil {
		return perr
	}
	if s.noPlan(c) {
		return refuse(409, "validation_failed", "Fix the failing checks first.")
	}
	return nil
}

// Rearm re-plans an awaiting or expired change.
func (s *Service) Rearm(ctx context.Context, id string) (Change, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Change{}, perr
	}
	if s.handoff() {
		return *c, refuse(409, "bad_state", "There is no Tachyon plan to re-arm; plan the bundle again in Tether.")
	}
	if c.State != builderapi.ChangeAwaitingApproval && c.State != builderapi.ChangeExpired {
		return *c, refuse(409, "bad_state", "Only an awaiting or expired change can be re-armed.")
	}
	if perr = s.replan(ctx, c); perr != nil {
		if perr.Code == "stale_base" {
			return s.mustLoad(ctx, id), perr
		}
		return *c, perr
	}
	c.State = builderapi.ChangeAwaitingApproval
	c.Failure = nil
	return s.save(ctx, c)
}

func (s *Service) Cancel(ctx context.Context, id string) (Change, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Change{}, perr
	}
	switch c.State {
	case builderapi.ChangeDraft, builderapi.ChangeAwaitingHandoff, builderapi.ChangeAwaitingApproval, builderapi.ChangeStale, builderapi.ChangeExpired:
	default:
		return *c, refuse(409, "bad_state", "This change can no longer be cancelled.")
	}
	c.State = builderapi.ChangeCancelled
	return s.save(ctx, c)
}

func (s *Service) markStale(ctx context.Context, c *Change) {
	st := &Stale{ChangedPaths: []string{}, MineDiff: c.Diff}
	var theirs []string
	for p, base := range c.Base {
		live, exists, err := s.Tether.ReadFile(ctx, p)
		if err != nil || (exists && (shaOf(live) == base || newSHA(c, p) != "" && shaOf(live) == newSHA(c, p))) {
			continue // unchanged, or already at the value this change writes (a partly applied hand-off)
		}
		st.ChangedPaths = append(st.ChangedPaths, p)
		if exists {
			st.TheirsSHA = shaOf(live)
			if old, ok := s.snapshotBySHA(ctx, p, base); ok {
				theirs = append(theirs, builderread.UnifiedDiff(p, old, maskFor(p, live)))
			}
		}
	}
	sort.Strings(st.ChangedPaths)
	st.TheirsDiff = strings.Join(theirs, "")
	c.Stale, c.State = st, builderapi.ChangeStale
	_, _ = s.save(ctx, c)
}

func (s *Service) snapshotBySHA(ctx context.Context, path, sha string) (string, bool) {
	if s.Index == nil {
		return "", false
	}
	list, err := s.Index.Snapshots(ctx, path, catalogindex.SnapshotsPerPath)
	if err != nil {
		return "", false
	}
	for _, sn := range list {
		if sn.SHA == sha {
			return sn.Content, true
		}
	}
	return "", false
}

// Run is the watcher: while changes await approval it polls their plans and applies approved ones.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("builder change poll failed", "err", err)
			}
		}
	}
}

// Poll makes one pass over the open changes that are waiting on Tether.
func (s *Service) Poll(ctx context.Context) error {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT change_id FROM catalog_changes WHERE state IN ('awaiting_handoff','awaiting_approval','approved','applying')`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		s.step(ctx, id)
	}
	return nil
}

func (s *Service) step(ctx context.Context, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return
	}
	switch c.State {
	case builderapi.ChangeApplying:
		if s.now().UnixMilli()-c.UpdatedAt > applyStuck.Milliseconds() {
			c.State, c.Failure = builderapi.ChangeFailed, &Failure{Step: builderapi.StepApply, Code: "interrupted", Message: "The apply did not report back. Check the catalog before retrying."}
			_, _ = s.save(ctx, c)
		}
		return
	case builderapi.ChangeAwaitingHandoff, builderapi.ChangeAwaitingApproval:
		if s.handoff() {
			s.observe(ctx, c)
			return
		}
		if c.State == builderapi.ChangeAwaitingHandoff {
			return
		}
	case builderapi.ChangeApproved:
	default:
		return
	}
	if c.Plan == nil {
		return
	}
	// A base that moved under an unapplied change is surfaced, never retried silently.
	if c.PartEffects != nil {
		for _, r := range c.PartEffects.Attach { // compiled from a revision that must still be current
			if s.partMoved(ctx, partPath(r.Kind, r.PartID), strconv.Itoa(r.Rev)) && !s.editsPart(c, r) {
				s.markStale(ctx, c)
				return
			}
		}
	}
	for p, base := range c.Base {
		if strings.HasPrefix(p, partPathPrefix) {
			if s.partMoved(ctx, p, base) {
				s.markStale(ctx, c)
				return
			}
			continue
		}
		if live, exists, err := s.Tether.ReadFile(ctx, p); err == nil && (!exists || shaOf(live) != base) {
			s.markStale(ctx, c)
			return
		}
	}
	plan, err := s.Tether.GetPlan(ctx, c.Plan.PlanID)
	if err != nil {
		var te *TetherError
		if errors.As(err, &te) && te.Status == 404 {
			c.State = builderapi.ChangeExpired
			_, _ = s.save(ctx, c)
		}
		return
	}
	c.Plan.State, c.Plan.ExpiresAt = plan.State, pointer(plan.ExpiresAt.UnixMilli())
	switch {
	case plan.State == "approved":
	case plan.State == "applied":
		// Applied elsewhere (the CLI): record it rather than applying twice.
		s.finish(ctx, c, ApplyResult{Applied: true})
		return
	case plan.State == "planned" && !plan.ExpiresAt.IsZero() && s.now().After(plan.ExpiresAt):
		c.State = builderapi.ChangeExpired
		_, _ = s.save(ctx, c)
		return
	default:
		_, _ = s.save(ctx, c)
		return
	}
	c.State = builderapi.ChangeApproved
	s.save(ctx, c)
	c.State = builderapi.ChangeApplying
	s.save(ctx, c)
	res, err := s.Tether.Apply(ctx, c.Plan.PlanID)
	if err != nil {
		step, code, msg := builderapi.StepApply, "apply_failed", "The apply failed."
		var te *TetherError
		if errors.As(err, &te) {
			code, msg = te.Code, catalogindex.MaskText(te.Message)
			if strings.Contains(strings.ToLower(te.Message), "reload") {
				step = builderapi.StepReload
			}
			if strings.Contains(strings.ToLower(te.Message), "changed") || strings.Contains(strings.ToLower(te.Message), "stale") {
				s.markStale(ctx, c)
				return
			}
		} else {
			msg = "Tether did not answer the apply. Check the plan before retrying."
		}
		c.State, c.Failure = builderapi.ChangeFailed, &Failure{Step: step, Code: code, Message: clip(msg, 300)}
		_, _ = s.save(ctx, c)
		return
	}
	s.finish(ctx, c, res)
}

func (s *Service) finish(ctx context.Context, c *Change, res ApplyResult) {
	at := s.now().UnixMilli()
	r := &Result{BackupID: res.BackupID, Activation: res.Activation, RestartRequired: nonNil(res.RestartRequired), Reloaded: res.Reloaded, AppliedAt: &at,
		Detected: res.detected, FilesChanged: res.filesChanged}
	if res.ReloadError != "" {
		r.ReloadError = pointer(clip(catalogindex.MaskText(res.ReloadError), 300))
	}
	c.Result = r
	if !res.RolledBack && !c.PartEffects.Empty() {
		err := s.DB.Writer.Submit(ctx, "builder_parts_commit", func(ctx context.Context, tx *sql.Tx) error {
			planID := ""
			if c.Plan != nil {
				planID = c.Plan.PlanID
			}
			return builderparts.Commit(ctx, tx, c.PartEffects, c.Operator, planID, s.now().UnixNano())
		})
		if err != nil {
			c.State, c.Failure = builderapi.ChangeFailed, &Failure{Step: builderapi.StepApply, Code: "parts_commit", Message: "The files were applied but the Library rows could not be saved."}
			_, _ = s.save(ctx, c)
			return
		}
	}
	if res.Activation != "" {
		c.Effect.Activation = res.Activation
	}
	if res.RolledBack {
		c.State = builderapi.ChangeRolledBack
	} else {
		c.State = builderapi.ChangeApplied
		s.recordSnapshots(ctx, c)
	}
	_, _ = s.save(ctx, c)
	if c.State == builderapi.ChangeApplied && s.Bus != nil {
		paths := make([]string, 0, len(c.Targets))
		for _, t := range c.Targets {
			if !strings.HasPrefix(t.Path, partPathPrefix) {
				paths = append(paths, t.Path)
			}
		}
		s.Bus.Publish("catalog", map[string]any{"observed_at": at, "changed_paths": paths, "source": "builder"})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// recordSnapshots stores a masked snapshot of each applied file (source builder) for history and restore.
func (s *Service) recordSnapshots(ctx context.Context, c *Change) {
	if s.Index == nil {
		return
	}
	var snaps []catalogindex.Snapshot
	for _, t := range c.Targets {
		if c.Plan == nil && !s.handoff() || strings.HasPrefix(t.Path, partPathPrefix) {
			continue
		}
		live, exists, err := s.Tether.ReadFile(ctx, t.Path)
		if err != nil || !exists {
			continue
		}
		m := maskFor(t.Path, live)
		if m == "" {
			continue
		}
		planID := ""
		if c.Plan != nil {
			planID = c.Plan.PlanID
		}
		snaps = append(snaps, catalogindex.Snapshot{Path: t.Path, SHA: shaOf(live), Content: m, At: s.now(), Source: "builder", PlanID: planID, Actor: c.Operator})
	}
	if len(snaps) > 0 {
		_ = s.Index.Apply(ctx, nil, nil, snaps)
	}
}

// ---- persistence

var open = map[string]bool{builderapi.ChangeDraft: true, builderapi.ChangeAwaitingHandoff: true, builderapi.ChangeAwaitingApproval: true, builderapi.ChangeApproved: true,
	builderapi.ChangeApplying: true, builderapi.ChangeStale: true}

func (s *Service) insert(ctx context.Context, c Change, request string) error {
	return s.DB.Writer.Submit(ctx, "catalog_change_insert", func(ctx context.Context, tx *sql.Tx) error {
		c.UpdatedAt = c.CreatedAt
		body, _ := json.Marshal(c)
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_changes(change_id,request_id,operator,state,request,body,created_ns,updated_ns) VALUES(?,?,?,?,?,?,?,?)`,
			c.ChangeID, c.RequestID, c.Operator, c.State, request, string(body), c.CreatedAt*1e6, c.UpdatedAt*1e6); err != nil {
			return err
		}
		for _, t := range c.Targets {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_change_paths(path,change_id) VALUES(?,?)`, t.Path, c.ChangeID); err != nil {
				return err
			}
		}
		return nil
	})
}

// save writes the change and frees its paths when it ends, then announces the state.
func (s *Service) save(ctx context.Context, c *Change) (Change, *APIError) {
	c.UpdatedAt = s.now().UnixMilli()
	body, _ := json.Marshal(c)
	err := s.DB.Writer.Submit(ctx, "catalog_change_save", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_changes SET state=?,body=?,updated_ns=? WHERE change_id=?`, c.State, string(body), c.UpdatedAt*1e6, c.ChangeID); err != nil {
			return err
		}
		if !open[c.State] {
			_, err := tx.ExecContext(ctx, `DELETE FROM catalog_change_paths WHERE change_id=?`, c.ChangeID)
			return err
		}
		return nil
	})
	if err != nil {
		return *c, refuse(500, "internal", "The change could not be saved.")
	}
	s.publish(*c)
	return *c, nil
}

func (s *Service) publish(c Change) {
	if s.Bus != nil {
		s.Bus.Publish("change", map[string]any{"change_id": c.ChangeID, "state": c.State})
	}
}

func (s *Service) load(ctx context.Context, id string) (*Change, *APIError) {
	var body string
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT body FROM catalog_changes WHERE change_id=?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(404, "not_found", "No such change.")
	}
	var c Change
	if err != nil || json.Unmarshal([]byte(body), &c) != nil {
		return nil, refuse(500, "internal", "The change could not be read.")
	}
	return &c, nil
}

func (s *Service) mustLoad(ctx context.Context, id string) Change {
	c, _ := s.load(ctx, id)
	if c == nil {
		return Change{}
	}
	return *c
}

func (s *Service) byRequest(ctx context.Context, rid string) (*Change, error) {
	var body string
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT body FROM catalog_changes WHERE request_id=?`, rid).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Change
	return &c, json.Unmarshal([]byte(body), &c)
}

func (s *Service) request(ctx context.Context, id string) (Request, *APIError) {
	var raw string
	var req Request
	if s.DB.Reader.QueryRowContext(ctx, `SELECT request FROM catalog_changes WHERE change_id=?`, id).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &req) != nil {
		return req, refuse(500, "internal", "The change could not be read.")
	}
	return req, nil
}

// Get returns one change.
func (s *Service) Get(ctx context.Context, id string) (Change, *APIError) {
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Change{}, perr
	}
	return *c, nil
}

// List returns changes newest first. state is "", "all", "open" or one state.
func (s *Service) List(ctx context.Context, state string) ([]Change, *APIError) {
	q := `SELECT body FROM catalog_changes`
	var args []any
	switch {
	case state == "" || state == "all":
	case state == "open":
		q += ` WHERE state IN ('draft','awaiting_handoff','awaiting_approval','approved','applying','stale')`
	case slicesContains(builderapi.ChangeStates, state):
		q += ` WHERE state=?`
		args = append(args, state)
	default:
		return nil, refuse(400, "invalid", "Unknown state.")
	}
	rows, err := s.DB.Reader.QueryContext(ctx, q+` ORDER BY created_ns DESC LIMIT 100`, args...)
	if err != nil {
		return nil, refuse(500, "internal", "Changes could not be listed.")
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var body string
		var c Change
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &c) == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func slicesContains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

var _ = fmt.Sprintf

// LaunchResult is the answer to the launch action.
type LaunchResult struct {
	SessionID string `json:"session_id"`
	AgentsURL string `json:"agents_url"`
}

// passThrough are Tether's launch refusals the API reports as 409 with Tether's own code.
var passThrough = map[string]bool{"agent_disabled": true, "worker_unavailable": true, "worker_not_configured": true, "idempotency_conflict": true}

// Launch starts a session from a catalog launch (create, then start). It is not a catalog change. The request_id
// is the idempotency key, so a retry after a lost answer returns the same session. No env or override reaches
// Tether from here.
func (s *Service) Launch(ctx context.Context, launchID, requestID string) (LaunchResult, *APIError) {
	if !builderapi.IDPattern.MatchString(launchID) || !requestIDPattern.MatchString(requestID) {
		return LaunchResult{}, refuse(400, "invalid", "Send a launch id slug and a request_id.")
	}
	if s.handoff() {
		return LaunchResult{}, refuse(503, "changes_unavailable", "Launching a session needs the Tether control credential, which this host does not hold.")
	}
	key := "builder-launch:" + requestID
	id, err := s.Tether.CreateSession(ctx, launchID, key)
	if err == nil {
		err = s.Tether.StartSession(ctx, id)
	}
	if err != nil {
		var te *TetherError
		if errors.As(err, &te) {
			switch {
			case passThrough[te.Code]:
				return LaunchResult{}, refuse(409, te.Code, clip(catalogindex.MaskText(te.Message), 300))
			case te.Status == 404:
				return LaunchResult{}, refuse(404, "not_found", "No such launch.")
			}
		}
		return LaunchResult{}, tetherErr(err)
	}
	return LaunchResult{SessionID: id, AgentsURL: "/agents/" + id}, nil
}

func hasFileTarget(c Change) bool {
	for _, t := range c.Targets {
		if !strings.HasPrefix(t.Path, partPathPrefix) {
			return true
		}
	}
	return false
}

// partMoved reports whether a part is no longer at the revision a change was built on.
func (s *Service) partMoved(ctx context.Context, path, base string) bool {
	kind, id := splitKey(strings.TrimPrefix(path, partPathPrefix))
	p, ok, err := builderparts.Get(ctx, s.DB.Reader, kind, id)
	if err != nil {
		return false
	}
	if base == "" { // a create: stale once the id exists
		return ok
	}
	return !ok || strconv.Itoa(p.Revision) != base
}

// editsPart reports whether the change itself moves the part the reference points at (its revision is the new one).
func (s *Service) editsPart(c *Change, r builderparts.Ref) bool {
	for _, p := range c.PartEffects.Parts {
		if p.Kind == r.Kind && p.ID == r.PartID {
			return true
		}
	}
	return false
}
