package builderchange

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/builderparts"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// Library parts (B5). A part is changed through a change whose target is {kind:"part", part_kind, id}; an agent
// takes a part with append/remove on prompt.parts (value {"kind":"prompt","id":...}) or set/remove on policy.part
// (value the policy id). Part rows and references change only when the change applies; the compiled text goes into
// the agent files through the same Tether plan as any other edit.

const (
	pathPromptParts = "prompt.parts"
	pathPolicyPart  = "policy.part"
	partPathPrefix  = "parts/"
)

type attachOp struct{ typ, path, kind, id string }

func partPath(kind, id string) string { return partPathPrefix + kind + "/" + id }

func isAttachPath(p string) bool { return p == pathPromptParts || p == pathPolicyPart }

// parseAttach reads an attach op on an agent target.
func parseAttach(o OpIn) (attachOp, *APIError) {
	a := attachOp{typ: o.Op.Type, path: o.Op.Path}
	okType := o.Op.Path == pathPromptParts && (a.typ == builderapi.OpAppend || a.typ == builderapi.OpRemove) ||
		o.Op.Path == pathPolicyPart && (a.typ == builderapi.OpSet || a.typ == builderapi.OpRemove)
	if !okType {
		return a, refuse(400, "invalid", "prompt.parts takes append or remove; policy.part takes set or remove.")
	}
	if o.Op.Path == pathPolicyPart {
		a.kind = builderparts.KindPolicy
		if a.typ == builderapi.OpSet {
			var id string
			if json.Unmarshal(o.Op.Value, &id) != nil || !builderapi.IDPattern.MatchString(id) {
				return a, refuse(400, "invalid", "policy.part takes a policy id.")
			}
			a.id = id
		}
		return a, nil
	}
	var v struct{ Kind, ID string }
	if json.Unmarshal(o.Op.Value, &v) != nil || (v.Kind != "" && v.Kind != builderparts.KindPrompt) || !builderapi.IDPattern.MatchString(v.ID) {
		return a, refuse(400, "invalid", "prompt.parts takes {\"kind\":\"prompt\",\"id\":...}.")
	}
	a.kind, a.id = builderparts.KindPrompt, v.ID
	return a, nil
}

// ---- editing a part itself

func applyPart(p builderparts.Part, creating bool, edits []Edit) (builderparts.Part, []Priv, *APIError) {
	var privs []Priv
	for _, e := range edits {
		if err := checkValue(e.Value); err != nil {
			return p, nil, refuse(err.Status, err.Code, err.Msg)
		}
		switch e.Type {
		case builderapi.OpCreate:
			if !creating || p.Revision != 0 {
				return p, nil, refuse(409, "exists", "That id already exists.")
			}
			if len(e.Value) > 0 {
				var v struct {
					Name, Summary, Body string
					Bundle              map[string]any
				}
				if json.Unmarshal(e.Value, &v) != nil {
					return p, nil, refuse(400, "invalid", "create takes {name, summary, body}.")
				}
				p.Name, p.Summary = v.Name, v.Summary
				if p.Kind == builderparts.KindPrompt {
					p.Text = v.Body
				} else {
					p.Bundle = v.Bundle
				}
			}
		case builderapi.OpArchive:
			p.Archived = true
		case builderapi.OpSet, builderapi.OpRemove:
			if err := setPartField(&p, e, &privs); err != nil {
				return p, nil, err
			}
		default:
			return p, nil, refuse(400, "invalid", "That operation does not apply to a part.")
		}
	}
	if len(p.Text) > builderapi.MaxPromptBytes {
		return p, nil, refuse(413, "too_large", "Text is over 64 KiB.")
	}
	if p.Kind == builderparts.KindPolicy {
		for k := range p.Bundle {
			if !isPolicyField(k) && k != builderparts.RulesKey {
				return p, nil, refuse(400, "invalid", k+" is not a policy field.")
			}
		}
	}
	return p, privs, nil
}

func isPolicyField(k string) bool {
	for _, f := range builderparts.PolicyFields {
		if f == k {
			return true
		}
	}
	return false
}

func setPartField(p *builderparts.Part, e Edit, privs *[]Priv) *APIError {
	var s string
	switch e.Path {
	case "name", "summary":
		if e.Type != builderapi.OpSet || json.Unmarshal(e.Value, &s) != nil || len(s) > 200 {
			return refuse(400, "invalid", e.Path+" takes a short string.")
		}
		if e.Path == "name" {
			p.Name = s
		} else {
			p.Summary = s
		}
	case "body":
		if p.Kind != builderparts.KindPrompt || e.Type != builderapi.OpSet || json.Unmarshal(e.Value, &s) != nil {
			return refuse(400, "invalid", "body is the text of a prompt.")
		}
		p.Text = s
	case builderparts.RulesKey:
		if p.Kind != builderparts.KindPolicy {
			return refuse(400, "invalid", "rules belong to a policy.")
		}
		next := map[string]any{}
		for k, v := range p.Bundle {
			next[k] = v
		}
		if e.Type == builderapi.OpRemove {
			delete(next, e.Path)
		} else {
			var rules []string
			if e.Type != builderapi.OpSet || json.Unmarshal(e.Value, &rules) != nil || len(rules) > 50 {
				return refuse(400, "invalid", "rules is a list of up to 50 plain-language rules.")
			}
			out := make([]any, 0, len(rules))
			for _, r := range rules {
				if r = strings.TrimSpace(r); r == "" || len([]rune(r)) > 500 {
					return refuse(400, "invalid", "Each rule is 1 to 500 characters.")
				}
				out = append(out, r)
			}
			next[e.Path] = out
		}
		p.Bundle = next
	default:
		if p.Kind != builderparts.KindPolicy || !isPolicyField(e.Path) {
			return refuse(400, "invalid", "That field cannot be edited.")
		}
		next := map[string]any{}
		for k, v := range p.Bundle {
			next[k] = v
		}
		if e.Type == builderapi.OpRemove {
			delete(next, e.Path)
		} else {
			var v any
			if json.Unmarshal(e.Value, &v) != nil || v == nil {
				return refuse(400, "invalid", "set needs a value.")
			}
			next[e.Path] = v
		}
		p.Bundle = next
		if builderapi.PrivilegedPath("policy." + e.Path) {
			*privs = append(*privs, Priv{Path: partPath(p.Kind, p.ID), Field: "policy." + e.Path})
		}
	}
	return nil
}

func renderPart(p builderparts.Part) string {
	v := map[string]any{"name": p.Name, "summary": p.Summary, "archived": p.Archived}
	if p.Kind == builderparts.KindPrompt {
		v["body"] = p.Text
	} else {
		v["bundle"] = p.Bundle
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return catalogindex.MaskText(string(b)) + "\n"
}

// ---- the part pass of build

type partPass struct {
	svc     *Service
	work    map[string]*builderparts.Part // working copies, by kind/id
	changed map[string]bool               // content changed: referencing agents need recompiling
	eff     *builderparts.Effects
}

func newPass(s *Service) *partPass {
	return &partPass{svc: s, work: map[string]*builderparts.Part{}, changed: map[string]bool{}, eff: &builderparts.Effects{}}
}

func (pp *partPass) get(ctx context.Context, kind, id string) (*builderparts.Part, bool, error) {
	k := kind + "/" + id
	if p, ok := pp.work[k]; ok {
		return p, true, nil
	}
	p, ok, err := builderparts.Get(ctx, pp.svc.DB.Reader, kind, id)
	if err != nil || !ok {
		return nil, false, err
	}
	pp.work[k] = &p
	return &p, true, nil
}

// edit applies one part group and returns the diff text.
func (pp *partPass) edit(ctx context.Context, g group) (string, []Priv, string, *APIError) {
	cur, exists, err := pp.get(ctx, g.partKind, g.id)
	if err != nil {
		return "", nil, "", refuse(500, "internal", "The part could not be read.")
	}
	creating := len(g.edits) > 0 && g.edits[0].Type == builderapi.OpCreate
	base := ""
	var p builderparts.Part
	switch {
	case creating && exists:
		return "", nil, "", refuse(409, "exists", "That id already exists.")
	case !creating && !exists:
		return "", nil, "", refuse(404, "not_found", "No such part.")
	case creating:
		p = builderparts.Part{Kind: g.partKind, ID: g.id}
	case g.expected == "":
		return "", nil, "", refuse(400, "invalid", "expected_sha (the part revision) is required to change a part.")
	case g.expected != strconv.Itoa(cur.Revision):
		return "", nil, "", refuse(409, "stale_base", "The part changed since you opened it. Reload it.")
	default:
		p, base = *cur, strconv.Itoa(cur.Revision)
	}
	before := ""
	if !creating {
		before = renderPart(p)
	}
	next, privs, perr := applyPart(p, creating, g.edits)
	if perr != nil {
		return "", nil, "", perr
	}
	if next.Archived && !p.Archived {
		refs, err := builderparts.RefsOfPart(ctx, pp.svc.DB.Reader, next.Kind, next.ID)
		if err != nil {
			return "", nil, "", refuse(500, "internal", "The part could not be read.")
		}
		if len(refs) > 0 {
			return "", nil, "", refuse(409, "part_in_use", "Detach this part from its agents before archiving it.")
		}
	}
	after := renderPart(next)
	if !creating && before == after {
		return "", nil, "", refuse(400, "no_change", "That edit changes nothing.")
	}
	next.Revision = p.Revision + 1
	pp.work[next.Kind+"/"+next.ID] = &next
	pp.changed[next.Kind+"/"+next.ID] = true
	pp.eff.Parts = append(pp.eff.Parts, next)
	return builderread.UnifiedDiff(partPath(next.Kind, next.ID), before, after), privs, base, nil
}

// affected lists the agents that carry a changed part and are not already in the change.
func (pp *partPass) affected(ctx context.Context) ([]string, *APIError) {
	seen := map[string]bool{}
	for k := range pp.changed {
		p := pp.work[k]
		if p.Archived {
			continue
		}
		refs, err := builderparts.RefsOfPart(ctx, pp.svc.DB.Reader, p.Kind, p.ID)
		if err != nil {
			return nil, refuse(500, "internal", "The part could not be read.")
		}
		for _, r := range refs {
			seen[r.AgentID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out, nil
}

// compile applies the attach/detach ops of an agent group and recompiles every changed part it carries.
func (pp *partPass) compile(ctx context.Context, g group, path, content string) (string, []Priv, *APIError) {
	cur, err := builderparts.RefsOfAgent(ctx, pp.svc.DB.Reader, g.id)
	if err != nil {
		return "", nil, refuse(500, "internal", "The agent's parts could not be read.")
	}
	have := map[string]builderparts.Ref{}
	for _, r := range cur {
		have[r.Kind+"/"+r.PartID] = r
	}
	want := map[string]bool{}
	for k := range have {
		want[k] = true
	}
	fresh := map[string]bool{}
	for _, a := range g.attach {
		k := a.kind + "/" + a.id
		switch {
		case a.typ == builderapi.OpRemove && a.path == pathPolicyPart:
			for hk, r := range have {
				if r.Kind == builderparts.KindPolicy {
					delete(want, hk)
				}
			}
		case a.typ == builderapi.OpRemove:
			if !want[k] {
				return "", nil, refuse(404, "not_found", "That prompt is not attached.")
			}
			delete(want, k)
		case a.path == pathPolicyPart:
			for hk, r := range have {
				if r.Kind == builderparts.KindPolicy {
					delete(want, hk)
				}
			}
			want[k], fresh[k] = true, true
		default:
			if want[k] {
				return "", nil, refuse(409, "already_attached", "That prompt is already attached.")
			}
			want[k], fresh[k] = true, true
		}
	}
	var privs []Priv
	for k, r := range have { // detaches
		if want[k] {
			continue
		}
		pp.eff.Detach = append(pp.eff.Detach, builderparts.RefKey{AgentID: g.id, Kind: r.Kind, PartID: r.PartID})
		if r.Kind == builderparts.KindPrompt {
			c, perr := setBody(content, func(b string) string { return builderparts.DropBlock(b, r.PartID) })
			if perr != nil {
				return "", nil, perr
			}
			content = c
		}
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kind, id := splitKey(k)
		if !fresh[k] && !pp.changed[k] {
			continue
		}
		p, ok, err := pp.get(ctx, kind, id)
		if err != nil {
			return "", nil, refuse(500, "internal", "The part could not be read.")
		}
		if !ok {
			return "", nil, refuse(404, "not_found", "No such part: "+k+".")
		}
		if p.Archived && fresh[k] {
			return "", nil, refuse(409, "part_archived", "That part is archived.")
		}
		ref := builderparts.Ref{AgentID: g.id, Kind: kind, PartID: id, Rev: p.Revision}
		if kind == builderparts.KindPrompt {
			c, perr := setBody(content, func(b string) string { return builderparts.PutBlock(b, id, p.Revision, p.Text) })
			if perr != nil {
				return "", nil, perr
			}
			content, ref.Hash = c, builderparts.TextHash(p.Text)
		} else {
			fields := make([]string, 0, len(p.Bundle))
			for f := range p.Bundle {
				if f != builderparts.RulesKey {
					fields = append(fields, f)
				}
			}
			sort.Strings(fields)
			for _, f := range fields {
				raw, _ := json.Marshal(p.Bundle[f])
				c, perr := editYAML(content, agentPaths["policy."+f], Edit{Type: builderapi.OpSet, Value: raw})
				if perr != nil {
					return "", nil, refuse(perr.Status, perr.Code, perr.Msg)
				}
				content = c
				if builderapi.PrivilegedPath("policy." + f) {
					privs = append(privs, Priv{Path: path, Field: "policy." + f})
				}
			}
			ref.Hash = builderparts.PolicyHash(p.Bundle)
		}
		pp.eff.Attach = append(pp.eff.Attach, ref)
	}
	return content, privs, nil
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// setBody rewrites an agent's body field. The compiled markers are written here and never accepted from a client.
func setBody(content string, f func(string) string) (string, *APIError) {
	var d struct {
		Body string `yaml:"body"`
	}
	if yaml.Unmarshal([]byte(content), &d) != nil {
		return "", refuse(400, "invalid", "The file is not valid YAML.")
	}
	raw, _ := json.Marshal(f(d.Body))
	c, perr := editYAML(content, []string{"body"}, Edit{Type: builderapi.OpSet, Value: raw})
	if perr != nil {
		return "", refuse(perr.Status, perr.Code, perr.Msg)
	}
	return c, nil
}
