package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type RuntimeRequest struct {
	ID               string `json:"id"`
	ObjectID         string `json:"object_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	FormationID      string `json:"formation_id"`
	Operation        string `json:"operation"`
	Address          string `json:"address,omitempty"`
	Body             string `json:"body,omitempty"`
	MemberID         string `json:"member_id,omitempty"`
	Slot             string `json:"slot,omitempty"`
	Cascade          bool   `json:"cascade,omitempty"`
}
type teamRequest struct {
	RunID    string `json:"run_id"`
	Address  string `json:"address,omitempty"`
	Body     string `json:"body,omitempty"`
	MemberID string `json:"member_id,omitempty"`
	Slot     string `json:"slot,omitempty"`
	Cascade  bool   `json:"cascade,omitempty"`
}

func boundedID(s string) bool {
	return len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}
func (s *Service) PrepareRuntime(ctx context.Context, in RuntimeRequest, actor string) (Receipt, error) {
	if e := actorRequired(actor); e != nil {
		return Receipt{}, e
	}
	if !validID(in.ID) || !validID(in.ObjectID) || !validID(in.FormationID) || in.ExpectedRevision < 1 {
		return Receipt{}, invalid("Action, saved object, formation UUIDs and reviewed revision are required.")
	}
	encoded, _ := json.Marshal(in)
	inputDigest := digest(append([]byte("runtime\n"), encoded...))
	if old, e := s.Get(ctx, in.ID); e == nil {
		if old.inputDigest != inputDigest {
			return Receipt{}, conflict("This action UUID belongs to a different request.")
		}
		return old, nil
	}
	if !boundedID(in.Address) || !boundedID(in.MemberID) || !boundedID(in.Slot) || len(in.Body) > 48<<10 {
		return Receipt{}, invalid("Invalid or oversized runtime arguments.")
	}
	row, e := objects.New(s.Store).Get(ctx, in.ObjectID)
	if e != nil {
		return Receipt{}, invalid("Saved object unavailable.")
	}
	verb := "teams." + in.Operation
	if row.Ownership != "tachyon" || row.Revision != in.ExpectedRevision || row.Deleted() && !cleanupVerb(verb) {
		return Receipt{}, conflict("Reload an eligible saved object before reviewing the action.")
	}
	origin, e := s.Get(ctx, in.FormationID)
	if e != nil || origin.Verb != "teams.form" || origin.State != "succeeded" {
		return Receipt{}, invalid("Select a confirmed formation receipt.")
	}
	var result struct {
		Run *teams.TeamRun `json:"run"`
	}
	if json.Unmarshal(origin.Result, &result) != nil || result.Run == nil || result.Run.ID == "" {
		return Receipt{}, invalid("Formation receipt has no confirmed run.")
	}
	bound, e := s.Bind()
	if e != nil {
		return Receipt{}, unavailable("Tether configuration or credential is unavailable.")
	}
	if bound.Target() != origin.Target || bound.Identity() != origin.identity {
		return Receipt{}, conflict("This formation belongs to a different provider target or credential. Reconcile its caller identity first.")
	}
	req := teamRequest{RunID: result.Run.ID}
	if in.Operation == "assign" {
		var a objects.Assignment
		if row.Kind != objects.AssignmentKind || json.Unmarshal(row.Definition, &a) != nil || a.TeamID != origin.ObjectID || a.AgentID != "" || a.State == "completed" || a.State == "cancelled" {
			return Receipt{}, invalid("Select an active planned assignment for this formation's saved team.")
		}
		if in.MemberID != "" || in.Slot != "" || in.Cascade || strings.TrimSpace(in.Body) == "" || in.Address == "" || in.Address == "@all" || strings.HasSuffix(in.Address, "*") {
			return Receipt{}, invalid("Assignment dispatch requires an explicit team address and briefing only.")
		}
		// Keep the target qualified; provider IDs are never treated as local IDs.
		target, _ := json.Marshal(struct {
			Local    *objects.Reference         `json:"local,omitempty"`
			External *objects.ExternalReference `json:"external,omitempty"`
		}{a.Target, a.ExternalTarget})
		req.Address = in.Address
		req.Body = fmt.Sprintf("Tachyon assignment %s (revision %d): %s\n%s\nTarget: %s\n\n%s", row.ID, row.Revision, a.Name, a.Description, target, in.Body)
		if len(req.Body) > 64<<10 {
			return Receipt{}, invalid("Assignment briefing exceeds Tether's message limit.")
		}
	} else {
		if row.Kind != objects.TeamKind || row.ID != origin.ObjectID {
			return Receipt{}, invalid("Select the saved team belonging to this formation.")
		}
		switch in.Operation {
		case "address":
			if in.Address == "" || strings.TrimSpace(in.Body) == "" || in.MemberID != "" || in.Slot != "" || in.Cascade {
				return Receipt{}, invalid("Team messaging requires an address and body only.")
			}
			req.Address = in.Address
			req.Body = in.Body
		case "member_add":
			if in.Slot == "" || in.Address != "" || in.Body != "" || in.MemberID != "" || in.Cascade {
				return Receipt{}, invalid("Adding a member requires a slot only.")
			}
			var formation struct {
				Team teams.Team `json:"team"`
			}
			if json.Unmarshal(origin.Payload, &formation) != nil {
				return Receipt{}, invalid("Invalid formation payload.")
			}
			found := false
			for _, slot := range formation.Team.Slots {
				if slot.Name == in.Slot {
					found = true
				}
			}
			if !found {
				return Receipt{}, invalid("Slot is not in the executed formation.")
			}
			req.Slot = in.Slot
		case "member_remove", "cancel":
			if in.MemberID == "" || in.Address != "" || in.Body != "" || in.Slot != "" || in.Operation == "member_remove" && in.Cascade {
				return Receipt{}, invalid("Stopping or removing requires a member ID; cascade is available only for cancel.")
			}
			req.MemberID = in.MemberID
			req.Cascade = in.Cascade
		case "dissolve":
			if in.MemberID != "" || in.Address != "" || in.Body != "" || in.Slot != "" || in.Cascade {
				return Receipt{}, invalid("Dissolve accepts only the selected run.")
			}
		default:
			return Receipt{}, invalid("Unsupported runtime operation.")
		}
	}
	payload, _ := json.Marshal(req)
	var dependencies []BindingDependency
	if in.Operation == "member_add" {
		for _, dependency := range origin.BindingDependencies {
			if dependency.Slot == in.Slot {
				dependencies = append(dependencies, dependency)
			}
		}
	}
	return s.retain(ctx, in.ID, row, verb, payload, inputDigest, origin.ID, bound, actor, dependencies...)
}

type RosterRequest struct {
	FormationID string `json:"formation_id"`
	Refresh     bool   `json:"refresh,omitempty"`
}
type Member struct {
	ID     string `json:"id"`
	Slot   string `json:"slot"`
	Actor  string `json:"actor"`
	Status string `json:"status"`
	Kind   string `json:"kind"`
}
type RosterView struct {
	Run     teams.TeamRun `json:"run"`
	Version uint64        `json:"roster_version"`
	Members []Member      `json:"members"`
}
type RosterSnapshot struct {
	Runs       []RosterView `json:"runs"`
	ObservedAt string       `json:"observed_at,omitempty"`
	Problem    string       `json:"problem,omitempty"`
}
type rosterReader interface {
	Roster(context.Context, string) ([]RosterView, error)
}

func (s *Service) Roster(ctx context.Context, in RosterRequest) (RosterSnapshot, error) {
	out := RosterSnapshot{Runs: []RosterView{}}
	origin, e := s.Get(ctx, in.FormationID)
	if e != nil || origin.Verb != "teams.form" || origin.State != "succeeded" {
		return out, invalid("Select a confirmed formation.")
	}
	var result struct {
		Run *teams.TeamRun `json:"run"`
	}
	if json.Unmarshal(origin.Result, &result) != nil || result.Run == nil || result.Run.ID == "" {
		return out, invalid("Missing run receipt.")
	}
	var raw []byte
	e = s.Store.Reader.QueryRowContext(ctx, "SELECT body,observed_at FROM provider_roster_cache WHERE identity=? AND selection=?", origin.identity, origin.ID).Scan(&raw, &out.ObservedAt)
	if e == nil {
		if json.Unmarshal(raw, &out.Runs) != nil {
			return out, unavailable("Saved roster cannot be read.")
		}
	} else if e != sql.ErrNoRows {
		return out, e
	}
	if !in.Refresh {
		return out, nil
	}
	bound, e := s.Bind()
	if e != nil {
		out.Problem = "Tether connection is not configured. Saved membership is retained."
		return out, nil
	}
	if bound.Target() != origin.Target || bound.Identity() != origin.identity {
		out.Problem = "The formation's provider identity changed. Saved membership is retained; reconcile the caller before refreshing."
		return out, nil
	}
	reader, ok := bound.(rosterReader)
	if !ok {
		return out, unavailable("Provider roster reads are not supported.")
	}
	fresh, e := reader.Roster(ctx, result.Run.ID)
	if e != nil {
		out.Problem = "Tether roster refresh failed. Saved membership is retained; connectivity and permission validation is pending."
		return out, nil
	}
	if len(fresh) != 1 || fresh[0].Run.ID != result.Run.ID || fresh[0].Run.TeamID != result.Run.TeamID || fresh[0].Run.TeamVersion != result.Run.TeamVersion {
		out.Problem = "Provider returned a mismatched roster. Saved membership is retained."
		return out, nil
	}
	out.Runs = fresh
	out.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, _ = json.Marshal(fresh)
	e = s.Store.Writer.Submit(ctx, "provider_roster_cache", func(ctx context.Context, tx *sql.Tx) error {
		var previous []byte
		var observed string
		err := tx.QueryRowContext(ctx, "SELECT body,observed_at FROM provider_roster_cache WHERE identity=? AND selection=?", bound.Identity(), origin.ID).Scan(&previous, &observed)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		var saved []RosterView
		if err == nil {
			if json.Unmarshal(previous, &saved) != nil || len(saved) != 1 {
				return unavailable("Saved roster cannot be read.")
			}
			// Concurrent refreshes must not roll membership back to an older version.
			if saved[0].Version > fresh[0].Version {
				out.Runs = saved
				out.ObservedAt = observed
				return nil
			}
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO provider_roster_cache(identity,selection,body,observed_at) VALUES(?,?,?,?) ON CONFLICT(identity,selection) DO UPDATE SET body=excluded.body,observed_at=excluded.observed_at", bound.Identity(), origin.ID, raw, out.ObservedAt)
		if e != nil {
			return e
		}
		if e = s.projectReceiptTx(ctx, tx, &origin); e != nil {
			return e
		}
		if origin.ProjectionProblem != "" {
			out.Problem = origin.ProjectionProblem
		}
		return nil
	})
	return out, e
}
