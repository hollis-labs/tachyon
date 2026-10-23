// Package actionrun runs approved, registered actions on a chosen machine.
//
// An action is a fixed argv template with typed parameters; a request names a
// machine, an action and parameters, never a command line. The approval's
// op_digest covers the resolved request and is recomputed before anything runs.
package actionrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Machine ids. Transports are bound to these in the Manager.
const (
	MachineAgentOS = "agent-os"
	MachineHub     = "hub"
)

type paramKind int

const (
	paramPattern paramKind = iota
	paramBool
)

// Param is one typed parameter of an action.
type Param struct {
	Name     string
	Kind     paramKind
	Pattern  *regexp.Regexp // paramPattern
	Flag     string         // paramBool: argv element appended when true
	Required bool
	Help     string
}

// Action is a registry entry. Argv is the fixed prefix; Params are appended in
// declaration order, so the argv is a function of the params alone.
type Action struct {
	ID          string
	Title       string
	Description string
	Machines    []string
	Argv        []string
	Params      []Param
	// Interactive actions prompt the human (digest prefix, passphrase) and wait
	// for the owner to attach a terminal; others start on approval.
	Interactive bool
	// LabelParam names the Cerberus resource id parameter whose env/owner/admin
	// labels classify the request. Empty means no resource target.
	LabelParam string
	// Class is used when the action has no label-classified target.
	Class string
}

var (
	resourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	planID     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

func cerberusResource(verb, title string) Action {
	return Action{
		ID: "cerberus.resource." + verb, Title: title,
		Description: "cerberus resource " + verb + " <resource_id> --ack on a dev resource (env=dev owner=self admin=self). Prod, admin=owner or unlabeled targets are refused.",
		Machines:    []string{MachineAgentOS, MachineHub},
		Argv:        []string{"cerberus", "resource", verb},
		Params: []Param{
			{Name: "resource_id", Kind: paramPattern, Pattern: resourceID, Required: true, Help: "Cerberus resource id, e.g. hub-tether"},
		},
		LabelParam: "resource_id",
	}
}

// Actions returns the registry in id order.
func Actions() []Action {
	list := []Action{
		cerberusResource("apply", "Apply a Cerberus resource"),
		cerberusResource("deploy", "Deploy a Cerberus resource"),
		cerberusResource("reload", "Reload a Cerberus resource"),
		cerberusResource("remove", "Remove a Cerberus resource"),
		cerberusResource("stop", "Stop a Cerberus resource"),
		cerberusResource("sync", "Sync a Cerberus resource"),
		{
			ID: "tether.catalog.approve", Title: "Approve a Tether catalog plan",
			Description: "tether catalog approve <plan_id> on the hub. Prompts for the digest prefix and the operator passphrase, which the owner types into the terminal.",
			Machines:    []string{MachineHub},
			Argv:        []string{"tether", "catalog", "approve"},
			Params: []Param{
				{Name: "plan_id", Kind: paramPattern, Pattern: planID, Required: true, Help: "Tether catalog plan id"},
				{Name: "ack_privileged", Kind: paramBool, Flag: "--ack-privileged", Help: "acknowledge a privileged plan"},
				{Name: "allow_running", Kind: paramBool, Flag: "--allow-running", Help: "allow changes to running resources"},
			},
			Interactive: true, Class: "medium",
		},
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// ActionByID finds a registered action.
func ActionByID(id string) (Action, bool) {
	for _, a := range Actions() {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// Machines returns the known machine ids.
func Machines() []string { return []string{MachineAgentOS, MachineHub} }

// ValidationError lists what is allowed so an agent can correct its request.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func allowedHelp() string {
	ids := make([]string, 0, 8)
	for _, a := range Actions() {
		ids = append(ids, a.ID)
	}
	return "machines: " + strings.Join(Machines(), ", ") + "; actions: " + strings.Join(ids, ", ")
}

// Spec is a fully resolved request: what will run, where.
type Spec struct {
	Machine string            `json:"machine"`
	Action  string            `json:"action"`
	Params  map[string]string `json:"params"`
	Argv    []string          `json:"argv"`
}

// Resolve validates a request against the registry and builds its argv. Bool
// params are the strings "true"/"false" so the digest has one canonical form.
func Resolve(machine, actionID string, params map[string]string) (Spec, Action, error) {
	act, ok := ActionByID(actionID)
	if !ok {
		return Spec{}, Action{}, &ValidationError{"unknown action " + fmt.Sprintf("%q", actionID) + "; " + allowedHelp()}
	}
	okMachine := false
	for _, m := range act.Machines {
		okMachine = okMachine || m == machine
	}
	if !okMachine {
		return Spec{}, Action{}, &ValidationError{fmt.Sprintf("action %s runs on machines %s; got %q", act.ID, strings.Join(act.Machines, ", "), machine)}
	}
	known := map[string]Param{}
	for _, p := range act.Params {
		known[p.Name] = p
	}
	for name := range params {
		if _, ok := known[name]; !ok {
			return Spec{}, Action{}, &ValidationError{fmt.Sprintf("action %s has no parameter %q", act.ID, name)}
		}
	}
	argv := append([]string(nil), act.Argv...)
	clean := map[string]string{}
	var flags []string
	for _, p := range act.Params {
		v, present := params[p.Name]
		if !present || v == "" {
			if p.Required {
				return Spec{}, Action{}, &ValidationError{fmt.Sprintf("action %s requires parameter %s (%s)", act.ID, p.Name, p.Help)}
			}
			if p.Kind == paramBool {
				clean[p.Name] = "false"
			}
			continue
		}
		switch p.Kind {
		case paramPattern:
			if !p.Pattern.MatchString(v) {
				return Spec{}, Action{}, &ValidationError{fmt.Sprintf("parameter %s of %s must match %s", p.Name, act.ID, p.Pattern)}
			}
			clean[p.Name] = v
			argv = append(argv, v)
		case paramBool:
			if v != "true" && v != "false" {
				return Spec{}, Action{}, &ValidationError{fmt.Sprintf("parameter %s of %s must be true or false", p.Name, act.ID)}
			}
			clean[p.Name] = v
			if v == "true" {
				flags = append(flags, p.Flag)
			}
		}
	}
	argv = append(argv, flags...)
	if strings.HasPrefix(act.ID, "cerberus.resource.") {
		argv = append(argv, "--ack")
	}
	return Spec{Machine: machine, Action: act.ID, Params: clean, Argv: argv}, act, nil
}

// canonical is the JSON the digest covers: sorted keys, no whitespace.
func (s Spec) canonical() []byte {
	// encoding/json sorts map keys; struct fields are in a fixed order.
	b, _ := json.Marshal(struct {
		Action  string            `json:"action"`
		Argv    []string          `json:"argv"`
		Machine string            `json:"machine"`
		Params  map[string]string `json:"params"`
	}{s.Action, s.Argv, s.Machine, s.Params})
	return b
}

// Digest is sha256 over the canonical (machine, action, params, argv).
func (s Spec) Digest() string {
	sum := sha256.Sum256(s.canonical())
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Command renders the argv for people, quoting any element that needs it. It is
// display only; execution always uses the argv.
func (s Spec) Command() string {
	parts := make([]string, len(s.Argv))
	for i, a := range s.Argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

var safeWord = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

func shellQuote(a string) string {
	if safeWord.MatchString(a) {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}
