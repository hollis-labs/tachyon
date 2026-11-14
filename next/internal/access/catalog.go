// Package access is how agents act on Tachyon (port plan section 4): service identities, each
// with identity-module API keys that carry its grant. A grant is the old one, unchanged: the data
// verbs a credential may call and the object kinds it may touch. The key holds one scope per verb
// (ScopeFor); Tachyon's own table (service_credentials) maps the key to its service identity and
// records its kinds and expiry. A request is allowed a (verb, kind) only when the key holds the
// verb's scope AND the mapping grants the verb AND, for a kind, the mapping grants the kind.
package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The verb catalog, unchanged from the old build: adding support never widens an existing grant.
var (
	readVerbs           = []string{"planner.list", "objects.list", "objects.get", "objects.history", "objects.lookup_source"}
	portfolioReadVerbs  = []string{"planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema", "planner.portfolio.resolve"}
	portfolioWriteVerbs = []string{"planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate", "planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign"}
	torqueVerbs         = []string{"torque.task.get", "torque.task.comments", "torque.task.list"}
	tesseractVerbs      = []string{"tesseract.recall", "tesseract.get"}
	chatWorkspaceVerbs  = []string{"chat.workspace.list", "chat.workspace.get"}
	chatTopicVerbs      = []string{"chat.topic.add", "chat.topic.list", "chat.bookmark.list", "chat.approvals.list"}
	chatPlanVerbs       = []string{"chat.plan.get", "chat.plan.write", "chat.plan.step"}
	chatActivityVerbs   = []string{"chat.activity.append"}
	chatPanelVerbs      = []string{"chat.panel.open"}
	terminalVerbs       = []string{"terminal.machines", "terminal.propose", "terminal.run.get"}
	actionVerbs         = []string{"actions.catalog", "actions.request", "actions.await"}
	cardVerbs           = []string{"card.ask", "card.get", "card.await", "card.cancel"}

	plannerKinds   = []string{"plan", "task", "epic", "sprint", "collection", "workstream", "inbox_item", "idea", "decision", "risk", "roadmap_item", "strategic_priority"}
	portfolioKinds = []string{"strategic_priority", "workstream", "roadmap_item", "idea", "decision", "risk", "inbox_item"}

	// mutating are the verbs that change something (POST on the agent API).
	mutating = []string{
		"planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate",
		"planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign",
		"actions.request", "card.ask", "card.cancel", "chat.topic.add", "chat.plan.write", "chat.plan.step",
		"chat.activity.append", "chat.panel.open", "terminal.propose",
	}
)

// Verbs is every grantable verb, in catalog order.
func Verbs() []string {
	var out []string
	for _, g := range [][]string{readVerbs, portfolioReadVerbs, portfolioWriteVerbs, torqueVerbs, tesseractVerbs, chatWorkspaceVerbs,
		chatTopicVerbs, chatPlanVerbs, chatActivityVerbs, chatPanelVerbs, terminalVerbs, actionVerbs, cardVerbs} {
		out = append(out, g...)
	}
	return out
}

// Kinds is every object kind a grant may name.
func Kinds() []string { return slices.Clone(plannerKinds) }

// Mutates reports a verb that changes something.
func Mutates(verb string) bool { return slices.Contains(mutating, verb) }

// kindless are the verbs that name no object kind: a grant holding only these needs no kinds.
func kindless(v string) bool {
	for _, g := range [][]string{actionVerbs, cardVerbs, tesseractVerbs, chatWorkspaceVerbs, chatTopicVerbs, chatPlanVerbs, chatActivityVerbs, chatPanelVerbs, terminalVerbs} {
		if slices.Contains(g, v) {
			return true
		}
	}
	return false
}

// ScopePrefix is the prefix of every verb scope on a key.
const ScopePrefix = "tachyon."

// ScopeFor is the identity-key scope for a data verb: "tachyon." + the verb, for example
// tachyon.planner.list (port plan section 4). Route scopes (tachyon.<area>.<read|write|...>) never
// equal a verb scope: no verb is a single word.
func ScopeFor(verb string) string { return ScopePrefix + verb }

// Grants are a credential's verbs and kinds, the old token's JSON shape.
type Grants struct {
	Verbs []string `json:"verbs"`
	Kinds []string `json:"kinds"`
}

// Validate refuses an empty grant, an unknown verb or kind, a grant that names an object verb
// without kinds, and anything that could become "*" or tachyon.operator on a key.
func (g Grants) Validate() error {
	if len(g.Verbs) == 0 {
		return errors.New("a grant needs at least one verb")
	}
	needKinds := false
	for _, v := range g.Verbs {
		if v == "*" || v == "operator" || strings.ContainsAny(v, "* ") {
			return fmt.Errorf("verb %q is never granted (no wildcard, no operator)", v)
		}
		if !slices.Contains(Verbs(), v) {
			return fmt.Errorf("unsupported service verb %q", v)
		}
		needKinds = needKinds || !kindless(v)
	}
	if needKinds && len(g.Kinds) == 0 {
		return errors.New("explicit verbs and kinds are required: an object verb needs kinds")
	}
	for _, k := range g.Kinds {
		if !slices.Contains(plannerKinds, k) {
			return fmt.Errorf("unsupported service kind %q", k)
		}
	}
	return nil
}

// Scopes are the key scopes for the grant: one per verb.
func (g Grants) Scopes() []string {
	out := make([]string, 0, len(g.Verbs))
	for _, v := range g.Verbs {
		out = append(out, ScopeFor(v))
	}
	return out
}

func (g Grants) normalized() Grants {
	n := Grants{Verbs: slices.Clone(g.Verbs), Kinds: slices.Clone(g.Kinds)}
	if n.Verbs == nil {
		n.Verbs = []string{}
	}
	if n.Kinds == nil {
		n.Kinds = []string{}
	}
	return n
}

// Preset returns a named grant. The names are the old `tachyon access issue --preset` names.
func Preset(name string) (Grants, error) {
	join := func(groups ...[]string) []string {
		var out []string
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	switch name {
	case "planner-read":
		return Grants{join(readVerbs), Kinds()}, nil
	case "planner-torque-read":
		return Grants{join(readVerbs, torqueVerbs), Kinds()}, nil
	case "torque-read":
		return Grants{join(torqueVerbs), []string{"task", "plan", "epic", "sprint"}}, nil
	case "tesseract-read":
		return Grants{Verbs: join(tesseractVerbs)}, nil
	case "app-agent-read":
		return Grants{join(readVerbs, torqueVerbs, tesseractVerbs), Kinds()}, nil
	case "chat-workspace-read":
		return Grants{Verbs: join(chatWorkspaceVerbs)}, nil
	case "chat-topic-add":
		return Grants{Verbs: join(chatTopicVerbs)}, nil
	case "chat-plan":
		return Grants{Verbs: join(chatPlanVerbs)}, nil
	case "chat-activity-append":
		return Grants{Verbs: join(chatActivityVerbs)}, nil
	case "chat-panel-open":
		return Grants{Verbs: join(chatPanelVerbs)}, nil
	case "terminal-propose":
		return Grants{Verbs: join(terminalVerbs)}, nil
	case "portfolio-read":
		return Grants{join(readVerbs, portfolioReadVerbs), slices.Clone(portfolioKinds)}, nil
	case "portfolio-write":
		return Grants{join(readVerbs, portfolioReadVerbs, portfolioWriteVerbs), slices.Clone(portfolioKinds)}, nil
	case "actions-request":
		return Grants{Verbs: join(actionVerbs)}, nil
	case "card-ask":
		return Grants{Verbs: join(cardVerbs)}, nil
	}
	return Grants{}, fmt.Errorf("unknown access preset %q", name)
}

// Pair is one (verb, kind) a credential may use; Kind "" is a call that names no object kind.
type Pair struct{ Verb, Kind string }

// AllowSet is every (verb, kind) the grant allows, over the whole catalog: the verb is granted,
// and the kind is "" or granted. It is the old build's semantics, and the comparison the reissue
// proves for every token.
func (g Grants) AllowSet() map[Pair]bool {
	out := map[Pair]bool{}
	for _, v := range Verbs() {
		if !slices.Contains(g.Verbs, v) {
			continue
		}
		out[Pair{v, ""}] = true
		for _, k := range plannerKinds {
			if slices.Contains(g.Kinds, k) {
				out[Pair{v, k}] = true
			}
		}
	}
	return out
}
