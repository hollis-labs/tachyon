package builderread

import (
	"context"
	"fmt"
	"sort"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

// Referenced is returned when something is still in use. The change service (B3) turns it into a 409 so a skill
// or tool server is never deleted while an agent, launch or project points at it.
type Referenced struct {
	Kind, ID                   string
	Agents, Launches, Projects []string
}

func (e *Referenced) Error() string {
	return fmt.Sprintf("%s %s is still referenced by %d agent(s), %d launch(es) and %d project(s)", e.Kind, e.ID, len(e.Agents), len(e.Launches), len(e.Projects))
}

// Code is the API error code for the 409.
func (e *Referenced) Code() string { return "referenced" }

// CheckUnreferenced returns a *Referenced error when a skill or tool server is used, nil when it is free to
// delete, and a plain error when the index cannot be read (callers must then refuse: unknown is not "free").
// Boot profiles and teams-policy are not indexed, and launches are matched through their agent, so the check can
// miss a reference Tether still holds; Tether's own validation stays the backstop.
func CheckUnreferenced(ctx context.Context, r *Reader, kind, id string) error {
	if kind != builderapi.LibSkills && kind != builderapi.LibToolServers {
		return fmt.Errorf("builderread: %q has no catalog references", kind)
	}
	ix, ok := r.usageIndex(ctx)
	if !ok {
		return fmt.Errorf("builderread: the catalog index could not be read")
	}
	u := ix.of(kind, id)
	ids := func(fs []file) []string {
		out := make([]string, len(fs))
		for i, f := range fs {
			out[i] = f.ID
		}
		sort.Strings(out)
		return out
	}
	if len(u.agents)+len(u.launches)+len(u.projects) == 0 {
		return nil
	}
	return &Referenced{Kind: kind, ID: id, Agents: ids(u.agents), Launches: ids(u.launches), Projects: ids(u.projects)}
}
