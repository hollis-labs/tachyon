package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/util/svcerr"
	"github.com/hollis-labs/tachyon/internal/actionrun"
)

// Agent-facing registered-action tools. An agent names a machine id, an action
// id and parameters; Tachyon builds the argv, computes the op_digest, asks the
// owner and, once approved, runs it. The agent never supplies a command line,
// a digest or its own identity.

type actionRequest struct {
	Machine string                     `json:"machine"`
	Action  string                     `json:"action"`
	Params  map[string]json.RawMessage `json:"params,omitempty"`
	Reason  string                     `json:"reason"`
}
type actionAwait struct {
	RunID  string `json:"run_id"`
	WaitMs int    `json:"wait_ms,omitempty"`
}
type noInput struct{}

const (
	maxAwait         = 50 * time.Second
	defaultAwait     = 25 * time.Second
	agentOutputBytes = 16 << 10
)

const actionRules = "Ask for an action only when you need a privileged command run on another machine. " +
	"Use the full machine id and the exact registered action id from actions.catalog; never ask for a free-form shell command. " +
	"Never put secrets, passphrases or tokens in params or reason: the owner types any prompt answers into a terminal themselves. " +
	"Give a concrete reason. After actions.request, call actions.await with run_id (repeat while status is still pending; the owner may take minutes). " +
	"If the result is denied, expired or refused, do not retry the same request without new information from the owner."

// ActionVerbs lists the tools for a credential that holds them.
func actionVerbs() []Verb {
	return []Verb{
		{Name: "actions.catalog", Description: "List the machine ids and registered actions an approved action can name, with parameters and rules. Read this before actions.request.", InputSchema: inputSchema(reflect.TypeFor[noInput](), nil)},
		{Name: "actions.request", Mutates: true, Description: "Ask the owner to approve one registered action on a machine. Returns run_id, the exact command, op_digest (computed by Tachyon) and a link for the owner. Nothing runs until the owner approves. " + actionRules, InputSchema: inputSchema(reflect.TypeFor[actionRequest](), []string{"machine", "action", "reason"})},
		{Name: "actions.await", Description: "Wait (up to wait_ms, max 50000) for a requested action: returns its status, exit code and bounded output once finished, or the current status if it is still pending, waiting for the owner, or running.", InputSchema: inputSchema(reflect.TypeFor[actionAwait](), []string{"run_id"})},
	}
}

type catalogAction struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Machines    []string       `json:"machines"`
	Params      []catalogParam `json:"params"`
	Interactive bool           `json:"interactive"`
}
type catalogParam struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Help     string `json:"help"`
	Pattern  string `json:"pattern,omitempty"`
}

func actionCatalog() map[string]any {
	acts := []catalogAction{}
	for _, a := range actionrun.Actions() {
		item := catalogAction{ID: a.ID, Title: a.Title, Description: a.Description, Machines: a.Machines, Interactive: a.Interactive, Params: []catalogParam{}}
		for _, p := range a.Params {
			cp := catalogParam{Name: p.Name, Type: "string", Required: p.Required, Help: p.Help}
			if p.Pattern != nil {
				cp.Pattern = p.Pattern.String()
			} else {
				cp.Type = `boolean ("true" or "false")`
			}
			item.Params = append(item.Params, cp)
		}
		acts = append(acts, item)
	}
	sort.Slice(acts, func(i, j int) bool { return acts[i].ID < acts[j].ID })
	return map[string]any{"machines": actionrun.Machines(), "actions": acts, "rules": actionRules}
}

// paramStrings accepts strings and JSON booleans; anything else is invalid.
func paramStrings(in map[string]json.RawMessage) (map[string]string, error) {
	out := map[string]string{}
	for k, raw := range in {
		var s string
		var b bool
		switch {
		case json.Unmarshal(raw, &s) == nil:
			out[k] = s
		case json.Unmarshal(raw, &b) == nil:
			out[k] = fmt.Sprint(b)
		default:
			return nil, invalid(fmt.Sprintf("parameter %q must be a string or boolean", k))
		}
	}
	return out, nil
}

func actionError(err error) error {
	var verr *actionrun.ValidationError
	if errors.As(err, &verr) {
		return invalid(verr.Msg)
	}
	if errors.Is(err, actionrun.ErrNotFound) {
		return svcerr.New(svcerr.CodeNotFound, "No such action run for this credential.")
	}
	return err
}

// invokeAction runs one action verb as requester ("service:<id>").
func invokeAction(ctx context.Context, svc *actionrun.Service, name string, raw json.RawMessage, requester string) (json.RawMessage, error) {
	if svc == nil {
		return nil, svcerr.New(svcerr.CodePermission, "Registered actions are not enabled on this host.")
	}
	switch name {
	case "actions.catalog":
		return json.Marshal(actionCatalog())
	case "actions.request":
		var q actionRequest
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		params, err := paramStrings(q.Params)
		if err != nil {
			return nil, err
		}
		h, err := svc.Request(ctx, actionrun.RequestInput{Machine: q.Machine, Action: q.Action, Params: params, Reason: q.Reason, AgentID: requester})
		if err != nil {
			return nil, actionError(err)
		}
		return json.Marshal(map[string]any{
			"run_id": h.RunID, "status": h.State, "machine": h.Machine, "action": h.Action, "command": h.Command,
			"op_digest": h.OpDigest, "class": h.Class, "labels": h.Labels, "expires_at": h.Expires,
			"approval": map[string]string{"item_id": h.ItemID, "link": "[Approval: " + h.Action + " on " + h.Machine + "](tach:approval/" + h.ItemID + ")"},
			"next":     "Nothing has run. The owner must approve in Tachyon. Call actions.await with this run_id.",
		})
	case "actions.await":
		var q actionAwait
		if err := decode(raw, &q); err != nil {
			return nil, err
		}
		wait := time.Duration(q.WaitMs) * time.Millisecond
		if q.WaitMs == 0 {
			wait = defaultAwait
		}
		if wait < 0 || wait > maxAwait {
			return nil, invalid("wait_ms must be between 0 and 50000.")
		}
		// A credential sees only runs its own identity requested.
		run, err := svc.Store.Get(ctx, q.RunID)
		if err != nil || run.RequestedBy != requester {
			return nil, actionError(actionrun.ErrNotFound)
		}
		run, err = svc.Await(ctx, q.RunID, wait)
		if err != nil {
			return nil, actionError(err)
		}
		out := run.Output
		if len(out) > agentOutputBytes {
			out = "[earlier output truncated]\n" + out[len(out)-agentOutputBytes:]
		}
		view := map[string]any{
			"run_id": run.ID, "status": run.State, "finished": run.Terminal, "machine": run.Machine, "action": run.Action,
			"command": run.Command, "op_digest": run.OpDigest, "output": strings.TrimSpace(out), "error": run.Error,
			"requested_at": run.CreatedAt, "started_at": run.StartedAt, "ended_at": run.EndedAt,
		}
		if run.ExitCode != nil {
			view["exit_code"] = *run.ExitCode
		}
		if run.Note != "" {
			view["note"] = run.Note
		}
		if !run.Terminal {
			view["next"] = "Not finished. Call actions.await again; do not re-request."
		}
		return json.Marshal(view)
	}
	return nil, denied()
}
