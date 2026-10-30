package builderchange

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

// Handoff mode (BH1): Tachyon holds no catalog.write credential. A change still gets typed edits, a diff,
// checks and stale detection, but ends at a bundle the operator plans in Tether (`tether catalog plan`,
// approve, apply). Applied is DETECTED: every target file's sha equals its NewSHA. No plan id or credential is
// needed. Design: Tesseract design_tachyon_builder_change_handoff_20261030.

var planIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,80}$`)

// Bundle is exactly the body of Tether's POST /catalog/plans, plus its sha256. It carries full file content
// (which may contain environment values), so it is only ever served to the operator's browser, uncached.
type Bundle struct {
	Name     string    `json:"name"`
	Note     string    `json:"note"`
	Files    []File    `json:"files"`
	SHA256   string    `json:"sha256"`
	Commands []Command `json:"commands"`
}

// commandsFor builds the three Tether commands for a bundle name. The CLI spellings are UNVERIFIED; they live
// here so the UI can show whatever this says and a correction is one edit.
func commandsFor(name, planID string) []Command {
	if planID == "" {
		planID = "<plan_id>"
	}
	return []Command{
		{Step: "plan", Label: "Plan the bundle", Command: "tether catalog plan --name " + strconv.Quote(name) + " --file bundle.json"},
		{Step: "approve", Label: "Approve the plan", Command: "tether catalog approve " + planID},
		{Step: "apply", Label: "Apply the plan", Command: "tether catalog apply " + planID},
	}
}

func bundleSHA(name, note string, files []File) string {
	raw, _ := json.Marshal(struct {
		Name  string `json:"name"`
		Note  string `json:"note"`
		Files []File `json:"files"`
	}{name, note, files})
	return shaOf(string(raw))
}

// handoffFor describes the copy-mode bundle for a validated change. Session mode (BH2) is not offered yet.
func (s *Service) handoffFor(c *Change, files []File) *Handoff {
	name := "builder " + c.ChangeID
	return &Handoff{Mode: builderapi.HandoffCopy, Name: name, BundleSHA256: bundleSHA(name, c.Note, files), Files: len(files),
		SessionAvailable: false, Reason: "not_configured", Commands: commandsFor(name, "")}
}

func newSHA(c *Change, path string) string {
	for _, t := range c.Targets {
		if t.Path == path {
			return t.NewSHA
		}
	}
	return ""
}

// Bundle rebuilds the files from the stored request on the live catalog. A moved base makes the change stale.
func (s *Service) Bundle(ctx context.Context, id string) (Bundle, *APIError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Bundle{}, perr
	}
	if !s.handoff() || c.Handoff == nil || c.State != builderapi.ChangeAwaitingHandoff && c.State != builderapi.ChangeAwaitingApproval {
		return Bundle{}, refuse(409, "bad_state", "Only a change waiting on a Tether hand-off has a bundle.")
	}
	req, perr := s.request(ctx, id)
	if perr != nil {
		return Bundle{}, perr
	}
	gs, perr := s.groups(req)
	if perr != nil {
		return Bundle{}, perr
	}
	for i := range gs {
		gs[i].expected = c.Base[gs[i].path]
	}
	b, perr := s.build(ctx, gs)
	if perr != nil {
		if perr.Code == "stale_base" {
			s.markStale(ctx, c)
		}
		return Bundle{}, perr
	}
	for _, f := range b.files { // the bundle must produce the shas the change will be recognised by
		if want := newSHA(c, f.Path); want != "" && want != shaOf(f.Content) {
			return Bundle{}, refuse(409, "changed", "The change no longer matches the live catalog. Cancel it and edit again.")
		}
	}
	name := "builder " + c.ChangeID
	planID := ""
	if c.Plan != nil {
		planID = c.Plan.PlanID
	}
	return Bundle{Name: name, Note: c.Note, Files: b.files, SHA256: bundleSHA(name, c.Note, b.files), Commands: commandsFor(name, planID)}, nil
}

// MarkPlanned records the plan id the operator got from Tether, only so the UI can say "plan X awaiting
// approval". It is a hint: the applied state still comes from file shas.
func (s *Service) MarkPlanned(ctx context.Context, id, planID string) (Change, *APIError) {
	if planID != "" && !planIDPattern.MatchString(planID) {
		return Change{}, refuse(400, "invalid", "plan_id is a Tether plan id of at most 80 characters.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, perr := s.load(ctx, id)
	if perr != nil {
		return Change{}, perr
	}
	if !s.handoff() || c.Handoff == nil || c.State != builderapi.ChangeAwaitingHandoff && c.State != builderapi.ChangeAwaitingApproval {
		return *c, refuse(409, "bad_state", "Only a change waiting on a Tether hand-off can be marked planned.")
	}
	c.Plan = nil
	if planID != "" {
		c.Plan = &PlanInfo{PlanID: planID, State: "planned", ApproveCommand: "tether catalog approve " + planID}
	}
	c.Handoff.Commands = commandsFor(c.Handoff.Name, planID)
	c.State = builderapi.ChangeAwaitingApproval
	return s.save(ctx, c)
}

// Check is "Check now": it looks at the files immediately instead of waiting for the next tick.
func (s *Service) Check(ctx context.Context, id string) (Change, *APIError) {
	if _, perr := s.load(ctx, id); perr != nil {
		return Change{}, perr
	}
	if s.handoff() {
		s.step(ctx, id)
	}
	return s.Get(ctx, id)
}

// observe decides what the live files say about a change waiting on a hand-off. Every file at its new sha is
// applied; a file at neither its base nor its new sha means something else changed it (stale); a mix of base
// and new is a partly applied bundle and keeps waiting. The caller holds s.mu.
func (s *Service) observe(ctx context.Context, c *Change) {
	if c.PartEffects != nil {
		for _, r := range c.PartEffects.Attach {
			if s.partMoved(ctx, partPath(r.Kind, r.PartID), strconv.Itoa(r.Rev)) && !s.editsPart(c, r) {
				s.markStale(ctx, c)
				return
			}
		}
	}
	for p, base := range c.Base {
		if strings.HasPrefix(p, partPathPrefix) && s.partMoved(ctx, p, base) {
			s.markStale(ctx, c)
			return
		}
	}
	applied, pending := 0, 0
	for _, t := range c.Targets {
		if strings.HasPrefix(t.Path, partPathPrefix) || t.NewSHA == "" {
			continue
		}
		live, exists, err := s.Tether.ReadFile(ctx, t.Path)
		if err != nil {
			return // Tether is unreachable; look again next tick
		}
		base := c.Base[t.Path]
		switch {
		case exists && shaOf(live) == t.NewSHA:
			applied++
		case !exists && base == "", exists && shaOf(live) == base:
			pending++
		default:
			s.markStale(ctx, c)
			return
		}
	}
	if applied > 0 && pending == 0 {
		s.finish(ctx, c, ApplyResult{Applied: true, detected: true, filesChanged: applied})
	}
}

// ReadOnly wraps a Tether client that holds only a read token. Reads work; validate is best effort (a refusal
// becomes a warning, not a failed check); every write answers 403 no_control_credential.
func ReadOnly(t Tether) Tether { return readOnly{t} }

type readOnly struct{ Tether }

var errNoControl = &TetherError{Status: 403, Code: "no_control_credential", Message: "This host holds no catalog.write credential."}

func (r readOnly) Validate(ctx context.Context, files []File) (Report, error) {
	rep, err := r.Tether.Validate(ctx, files)
	if err != nil {
		return Report{OK: true, Results: []Check{{Name: "tether_validate", Status: builderapi.CheckWarn,
			Message: "Not validated by Tether; only Tachyon's own checks ran."}}}, nil
	}
	return rep, nil
}
func (readOnly) Plan(context.Context, string, []File) (Plan, error) { return Plan{}, errNoControl }
func (readOnly) GetPlan(context.Context, string) (Plan, error)      { return Plan{}, errNoControl }
func (readOnly) Apply(context.Context, string) (ApplyResult, error) {
	return ApplyResult{}, errNoControl
}
func (readOnly) CreateSession(context.Context, string, string) (string, error) {
	return "", errNoControl
}
func (readOnly) StartSession(context.Context, string) error { return errNoControl }
