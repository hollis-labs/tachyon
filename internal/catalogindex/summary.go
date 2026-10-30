package catalogindex

import "context"

// Summary is GET builder/summary: tab badges and catalog health in one cheap call (the shape of
// builderapi's fixtures/summary.json). Counts the index cannot know yet are nil (JSON null).
type Summary struct {
	Agents struct {
		Total, Active, Disabled, Retired int
	}
	LaunchesTotal int
	Skills        int
	ToolServers   int
	Projects      int
	State         State
}

func (x *Index) Summary(ctx context.Context) (Summary, error) {
	if x.Store == nil {
		return Summary{}, errNoStore
	}
	var s Summary
	ents, err := x.Store.Entities(ctx)
	if err != nil {
		return s, err
	}
	for _, e := range ents {
		switch e.Kind {
		case KindAgent:
			s.Agents.Total++
			switch e.State {
			case "disabled":
				s.Agents.Disabled++
			case "retired":
				s.Agents.Retired++
			default:
				s.Agents.Active++
			}
		case KindLaunch:
			s.LaunchesTotal++
		case KindSkill:
			s.Skills++
		case KindMCPServer:
			s.ToolServers++
		case KindProject:
			s.Projects++
		}
	}
	s.State, err = x.Store.State(ctx)
	return s, err
}

func (s Summary) asMap() map[string]any {
	var observed any
	if !s.State.ObservedAt.IsZero() {
		observed = s.State.ObservedAt.UnixMilli()
	}
	var toolServers any = s.ToolServers
	if s.State.SourceStatus != "ok" && s.ToolServers == 0 {
		toolServers = nil
	}
	return map[string]any{
		"agents":       map[string]any{"total": s.Agents.Total, "active": s.Agents.Active, "disabled": s.Agents.Disabled, "retired": s.Agents.Retired},
		"launches":     map[string]any{"total": s.LaunchesTotal, "live": nil}, // live arrives with B2 (used_by)
		"library":      map[string]any{"prompts": 0, "skills": s.Skills, "tool_servers": toolServers, "policies": 0},
		"projects":     map[string]any{"total": s.Projects},
		"open_changes": 0, // the change service is B3
		"catalog":      map[string]any{"observed_at": observed, "source_status": s.State.SourceStatus, "last_plan_id": s.State.LastPlanID},
		"capabilities": map[string]any{
			"retired": false, "one_step_save": false, "delegated_approval": false, "tool_server_edit": false,
			"resolved_by": "tachyon_approx", "read_prompt_bodies_requires_operator": false,
		},
	}
}
