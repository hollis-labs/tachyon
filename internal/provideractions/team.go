// Package provideractions owns reviewed provider requests and durable receipts.
// Editing a domain object never invokes this service implicitly.
package provideractions

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

var pinDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func formation(row objects.Record, mission, actionID string) (json.RawMessage, error) {
	if row.Kind != objects.TeamKind || row.Ownership != "tachyon" || row.DeletedAt != nil {
		return nil, invalid("Select an active Tachyon-owned team.")
	}
	if strings.TrimSpace(mission) == "" || len(mission) > 8192 {
		return nil, invalid("Provide a mission of at most 8192 bytes.")
	}
	var native struct {
		Name        string             `json:"name"`
		Coordinator string             `json:"coordinator"`
		Slots       []objects.TeamSlot `json:"slots"`
		Properties  struct {
			Builder struct {
				Preview json.RawMessage `json:"preview"`
			} `json:"builder_team"`
		} `json:"properties"`
	}
	if json.Unmarshal(row.Definition, &native) != nil || len(native.Properties.Builder.Preview) > 64<<10 {
		return nil, invalid("Invalid saved Builder configuration.")
	}
	var team teams.Team
	d := json.NewDecoder(bytes.NewReader(native.Properties.Builder.Preview))
	d.DisallowUnknownFields()
	if d.Decode(&team) != nil || teams.Validate(team) != nil {
		return nil, invalid("Save a valid pinned Builder team before preparing formation.")
	}
	if team.Authority.Mode != teams.Strict || team.Name != native.Name || team.Routing.CoordinatorSlot != native.Coordinator || team.Phases[0].OwnerSlot != native.Coordinator || len(team.Slots) != len(native.Slots) {
		return nil, invalid("Saved launch configuration differs from the team. Review and save it again in Builder.")
	}
	count := 0
	for i := range team.Slots {
		slot := &team.Slots[i]
		n := native.Slots[i]
		if n.AgentID != "" {
			return nil, invalid("Local agent bindings cannot yet be published to Tether. Use a team with pinned provider definitions.")
		}
		if slot.Name != n.Name || slot.Role != n.Role || slot.Min != n.Min || slot.Max != n.Max || slot.Required != (n.Min > 0) {
			return nil, invalid("Saved slots differ from the pinned launch configuration. Review and save the team again.")
		}
		if slot.Resolution != teams.Fresh || slot.Activation != teams.Singleton || !pinDigest.MatchString(slot.Definition.Digest) || slot.Max != 1 {
			return nil, invalid("Formation currently requires fresh single seats with SHA-256 definition pins.")
		}
		if slot.Name == native.Coordinator && slot.Min != 0 {
			return nil, invalid("The coordinator seat must be optional: Tether assigns the authenticated caller as team owner.")
		}
		count += slot.Min
		if slot.Workspace == nil {
			slot.Workspace = map[string]string{}
		}
		slot.Workspace["mission"] = mission
	}
	if count == 0 {
		return nil, invalid("At least one worker seat must be required.")
	}
	// Tether definitions are immutable by ID/version. The mission belongs to
	// this formation, so use an action-specific provider ID rather than altering
	// an existing provider definition under the authored ID/version.
	team.ID = "tachyon-" + row.ID + "-" + actionID
	raw, err := json.Marshal(struct {
		Team teams.Team `json:"team"`
	}{team})
	if err != nil || len(raw) > 64<<10 {
		return nil, invalid("Formation payload exceeds 64 KiB.")
	}
	return raw, nil
}

func digest(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }
