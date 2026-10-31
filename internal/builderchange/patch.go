package builderchange

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// OpError is a refusal of one edit, with the HTTP status the API answers.
type OpError struct {
	Status int
	Code   string
	Msg    string
}

func (e *OpError) Error() string { return e.Code }

func bad(code, msg string) *OpError { return &OpError{400, code, msg} }

// Edit is one operation on a file.
type Edit struct {
	Type        string          `json:"type"`
	Path        string          `json:"path,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	SnapshotSeq int             `json:"snapshot_seq,omitempty"`
}

// agentPaths maps the editor model (GET agents/{id}) onto YAML field paths. Paths are never accepted from a
// client; only these names are.
var agentPaths = map[string][]string{
	"identity.name": {"name"}, "identity.description": {"description"}, "identity.roles": {"roles"},
	"runtime.provider": {"provider"}, "runtime.model": {"model"}, "runtime.extends": {"extends"},
	"runtime.abstract": {"abstract"}, "runtime.enabled": {"enabled"},
	"prompt.system_prompt": {"system_prompt"}, "prompt.agent_prompt": {"agent_prompt"}, "prompt.body": {"body"},
	"prompt.boot_fragments":    {"boot_fragments"},
	"tools_skills.mcp_servers": {"mcp", "servers"}, "tools_skills.skills": {"skills"},
	"policy.permission_mode": {"permissions", "permission_mode"}, "policy.network": {"permissions", "network"},
	"policy.default_sandbox": {"permissions", "default_sandbox"}, "policy.mcp_scopes": {"mcp_scopes"},
	"policy.lifecycle": {"lifecycle"}, "policy.recovery": {"recovery"},
}

// Launch and project files are edited by their own top-level field names (the read model returns them flat).
var launchFields = map[string]bool{"display_name": true, "enabled": true, "worker": true, "chat": true, "route": true,
	"agent": true, "provider": true, "project": true, "workspace.mode": true, "mcp.servers": true}
var projectFields = map[string]bool{"name": true, "repo_root": true, "repo_url": true, "workspace": true, "mcp.servers": true, "description": true}

var secretNames = map[string]bool{"env": true, "provider_overrides": true, "environment": true, "overrides": true}

const masked = "***"

// yamlPath resolves an edit path for a kind, or refuses it.
func yamlPath(kind, p string) ([]string, *OpError) {
	if p == "" || !builderapi.FieldPath.MatchString(p) {
		return nil, bad("invalid", "path is not a field of the editor model.")
	}
	for _, seg := range strings.Split(p, ".") {
		if secretNames[strings.ToLower(seg)] {
			return nil, bad("env_write_unsupported", "Environment values are write-only and are not changed from the Builder yet.")
		}
	}
	switch kind {
	case builderapi.TargetAgent:
		if m, ok := agentPaths[p]; ok {
			return m, nil
		}
	case builderapi.TargetLaunch:
		if launchFields[p] {
			return strings.Split(p, "."), nil
		}
	case builderapi.TargetProject:
		if projectFields[p] {
			return strings.Split(p, "."), nil
		}
	}
	return nil, bad("invalid", "That field cannot be edited.")
}

// Priv is a privileged field an edit touches.
type Priv struct {
	Path  string `json:"path"`
	Field string `json:"field"`
}

// Apply runs the edits for one target against the live file and returns the new content. snapshot returns the
// masked content of a snapshot by sequence for restore_version. exists is false when the file is new.
func Apply(kind, id, path, live string, exists bool, edits []Edit, snapshot func(seq int) (string, bool)) (string, []Priv, *OpError) {
	var privs []Priv
	content := live
	created := false
	for _, e := range edits {
		switch e.Type {
		case builderapi.OpCreate:
			if exists || created {
				return "", nil, &OpError{409, "exists", "That id already exists."}
			}
			c, err := newFile(kind, id)
			if err != nil {
				return "", nil, err
			}
			content, created = c, true
			if len(e.Value) > 0 && e.Path != "" {
				return "", nil, bad("invalid", "create takes no path; send set operations after it.")
			}
			continue
		case builderapi.OpArchive:
			return "", nil, bad("invalid", "archive applies to Library parts only.")
		case builderapi.OpRestoreVersion:
			if !exists || created {
				return "", nil, bad("invalid", "Nothing to restore on a new file.")
			}
			snap, ok := snapshot(e.SnapshotSeq)
			if !ok {
				return "", nil, &OpError{404, "not_found", "No such snapshot."}
			}
			c, err := restore(kind, content, snap)
			if err != nil {
				return "", nil, err
			}
			content = c
			continue
		case builderapi.OpSet, builderapi.OpAppend, builderapi.OpRemove:
		default:
			return "", nil, bad("invalid", "Unknown operation.")
		}
		if !exists && !created {
			return "", nil, &OpError{404, "not_found", "No such file; create it first."}
		}
		if kind == builderapi.TargetSkill {
			c, err := editSkill(content, e)
			if err != nil {
				return "", nil, err
			}
			content = c
			continue
		}
		if kind == builderapi.TargetLaunch && strings.HasPrefix(e.Path, "env.") {
			c, err := editEnv(content, e)
			if err != nil {
				return "", nil, err
			}
			content = c
			continue
		}
		yp, err := yamlPath(kind, e.Path)
		if err != nil {
			return "", nil, err
		}
		if err := checkValue(e.Value); err != nil {
			return "", nil, err
		}
		c, err := editYAML(content, yp, e)
		if err != nil {
			return "", nil, err
		}
		content = c
		if kind == builderapi.TargetAgent && builderapi.PrivilegedPath(e.Path) {
			privs = append(privs, Priv{Path: path, Field: e.Path})
		}
	}
	if len(content) > builderapi.MaxFileBytes {
		return "", nil, &OpError{413, "too_large", "The file would be over 256 KiB."}
	}
	if kind != builderapi.TargetSkill {
		var chk yaml.Node
		if yaml.Unmarshal([]byte(content), &chk) != nil {
			return "", nil, bad("invalid", "The edit would not leave valid YAML.")
		}
	}
	return content, privs, nil
}

var envPath = regexp.MustCompile(`^env\.[A-Z_][A-Z0-9_]{0,63}$`)

const maxEnvValue = 4096

// editEnv sets or removes one environment variable of a launch (path env.NAME). Values are write-only: they are
// stored in the file but every read, diff and snapshot masks them, and a launch with env values is never handed to a
// session. Only string values; append is not an env operation.
func editEnv(content string, e Edit) (string, *OpError) {
	if !envPath.MatchString(e.Path) {
		return "", bad("invalid", "An environment variable is env.NAME with an upper-case name.")
	}
	switch e.Type {
	case builderapi.OpSet:
		var v string
		if json.Unmarshal(e.Value, &v) != nil {
			return "", bad("invalid", "An environment value is a string.")
		}
		if len(v) > maxEnvValue {
			return "", &OpError{413, "too_large", "An environment value is at most 4 KiB."}
		}
	case builderapi.OpRemove:
	default:
		return "", bad("invalid", "Environment variables are set or removed.")
	}
	return editYAML(content, []string{"env", strings.TrimPrefix(e.Path, "env.")}, e)
}

var idSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func newFile(kind, id string) (string, *OpError) {
	if !idSlug.MatchString(id) {
		return "", bad("invalid", "id must be a lower-case slug.")
	}
	switch kind {
	case builderapi.TargetAgent, builderapi.TargetLaunch, builderapi.TargetProject:
		return "id: " + id + "\nname: " + id + "\n", nil
	case builderapi.TargetSkill:
		return "---\nid: " + id + "\nname: " + id + "\ndescription: \"\"\n---\n", nil
	}
	return "", bad("invalid", "That kind cannot be created here.")
}

// checkValue refuses marker text (a prompt must not forge a compiled block) and oversize prompt text.
func checkValue(raw json.RawMessage) *OpError {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return bad("invalid", "value is not valid JSON.")
	}
	var walk func(any) *OpError
	walk = func(x any) *OpError {
		switch t := x.(type) {
		case string:
			if strings.Contains(t, builderapi.MarkerOpen) || strings.Contains(t, builderapi.MarkerClose) {
				return bad("marker_forbidden", "Text must not contain a tachyon:prompt marker.")
			}
			if len(t) > builderapi.MaxPromptBytes {
				return &OpError{413, "too_large", "Text is over 64 KiB."}
			}
		case []any:
			for _, i := range t {
				if err := walk(i); err != nil {
					return err
				}
			}
		case map[string]any:
			for k, i := range t {
				if secretNames[strings.ToLower(k)] {
					return bad("env_write_unsupported", "Environment values are write-only and are not changed from the Builder yet.")
				}
				if err := walk(i); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(v)
}

func parseDoc(content string) (*yaml.Node, *OpError) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, bad("invalid", "The file is not valid YAML.")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, bad("invalid", "The file is not a YAML mapping.")
	}
	return &doc, nil
}

func encode(doc *yaml.Node) (string, *OpError) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", bad("invalid", "The edit could not be written.")
	}
	return buf.String(), nil
}

// child finds or (when create) adds a key in a mapping.
func child(m *yaml.Node, key string, create bool) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	if !create {
		return nil
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

func dropKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = slices.Delete(m.Content, i, i+2)
			return
		}
	}
}

func editYAML(content string, yp []string, e Edit) (string, *OpError) {
	doc, perr := parseDoc(content)
	if perr != nil {
		return "", perr
	}
	m := doc.Content[0]
	for _, k := range yp[:len(yp)-1] {
		next := child(m, k, true)
		if next.Kind != yaml.MappingNode {
			if next.Kind == yaml.ScalarNode && next.Value == "" || next.Tag == "!!null" {
				*next = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			} else {
				return "", bad("invalid", "That field is not a mapping.")
			}
		}
		m = next
	}
	last := yp[len(yp)-1]
	if last == "id" && len(yp) == 1 {
		return "", bad("invalid", "id cannot be changed.")
	}
	var val any
	if len(e.Value) > 0 {
		_ = json.Unmarshal(e.Value, &val)
	}
	switch e.Type {
	case builderapi.OpSet:
		if len(e.Value) == 0 {
			return "", bad("invalid", "set needs a value.")
		}
		var n yaml.Node
		if err := n.Encode(val); err != nil {
			return "", bad("invalid", "That value cannot be written.")
		}
		if old := child(m, last, false); old != nil {
			n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
			*old = n
		} else {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last}, &n)
		}
	case builderapi.OpAppend:
		items, ok := val.([]any)
		if !ok {
			items = []any{val}
		}
		seq := child(m, last, false)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			if seq != nil && seq.Kind != yaml.SequenceNode && !(seq.Kind == yaml.ScalarNode && seq.Tag == "!!null") && !(seq.Kind == yaml.MappingNode && len(seq.Content) == 0) {
				return "", bad("invalid", "That field is not a list.")
			}
			if seq == nil {
				seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last}, seq)
			} else {
				*seq = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			}
		}
		for _, it := range items {
			var n yaml.Node
			if err := n.Encode(it); err != nil {
				return "", bad("invalid", "That value cannot be written.")
			}
			dup := false
			for _, ex := range seq.Content {
				dup = dup || (ex.Kind == yaml.ScalarNode && n.Kind == yaml.ScalarNode && ex.Value == n.Value)
			}
			if !dup {
				seq.Content = append(seq.Content, &n)
			}
		}
	case builderapi.OpRemove:
		cur := child(m, last, false)
		if cur == nil {
			break
		}
		if len(e.Value) == 0 {
			dropKey(m, last)
			break
		}
		if cur.Kind != yaml.SequenceNode {
			return "", bad("invalid", "A value can only be removed from a list.")
		}
		items, ok := val.([]any)
		if !ok {
			items = []any{val}
		}
		drop := map[string]bool{}
		for _, it := range items {
			drop[fmt.Sprint(it)] = true
		}
		kept := cur.Content[:0]
		for _, c := range cur.Content {
			if !(c.Kind == yaml.ScalarNode && drop[c.Value]) {
				kept = append(kept, c)
			}
		}
		cur.Content = kept
	}
	out, err := encode(doc)
	return out, err
}

// ---- skills (markdown with frontmatter)

func splitSkill(content string) (front, body string, ok bool) {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return "", content, false
	}
	fm, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		if fm, ok = strings.CutSuffix(rest, "\n---"); ok {
			return fm, "", true
		}
		return "", content, false
	}
	return fm, body, true
}

func editSkill(content string, e Edit) (string, *OpError) {
	if e.Type != builderapi.OpSet {
		return "", bad("invalid", "Skills take set operations on name, description and body.")
	}
	var s string
	if json.Unmarshal(e.Value, &s) != nil {
		return "", bad("invalid", "value must be text.")
	}
	if err := checkValue(e.Value); err != nil {
		return "", err
	}
	front, body, ok := splitSkill(content)
	if !ok {
		return "", bad("invalid", "The skill has no frontmatter.")
	}
	switch e.Path {
	case "body":
		body = s
		if !strings.HasPrefix(body, "\n") && body != "" {
			body = "\n" + body
		}
	case "name", "description":
		doc, perr := parseDoc(front + "\n")
		if perr != nil {
			return "", perr
		}
		raw, _ := json.Marshal(s)
		fm, perr := editYAML(front+"\n", []string{e.Path}, Edit{Type: builderapi.OpSet, Path: e.Path, Value: raw})
		if perr != nil {
			return "", perr
		}
		_ = doc
		front = strings.TrimSuffix(fm, "\n")
	default:
		return "", bad("invalid", "That field cannot be edited.")
	}
	return "---\n" + front + "\n---\n" + strings.TrimPrefix(body, ""), nil
}

// ---- restore_version

// restore returns the snapshot's content with every environment value and every masked value taken from the
// live file, so a restore never writes or exposes a secret. A masked value with no live counterpart refuses.
func restore(kind, live, snap string) (string, *OpError) {
	if kind == builderapi.TargetSkill {
		if catalogindex.MaskText(live) != live {
			return "", bad("restore_masked", "The current skill holds secret-like text, so a masked snapshot cannot be restored over it.")
		}
		return snap, nil
	}
	sd, perr := parseDoc(snap)
	if perr != nil {
		return "", perr
	}
	ld, perr := parseDoc(live)
	if perr != nil {
		return "", perr
	}
	if err := mergeLive(sd.Content[0], ld.Content[0]); err != nil {
		return "", err
	}
	return encode(sd)
}

func mergeLive(snap, live *yaml.Node) *OpError {
	if snap.Kind != yaml.MappingNode {
		return nil
	}
	// env-ish keys the live file has are kept exactly; ones it lacks are dropped from the restore.
	for i := 0; i+1 < len(snap.Content); {
		k, v := snap.Content[i], snap.Content[i+1]
		if secretNames[strings.ToLower(k.Value)] {
			if lv := child(live, k.Value, false); lv != nil {
				snap.Content[i+1] = lv
				i += 2
			} else {
				snap.Content = slices.Delete(snap.Content, i, i+2)
			}
			continue
		}
		var lv *yaml.Node
		if live != nil && live.Kind == yaml.MappingNode {
			lv = child(live, k.Value, false)
		}
		switch {
		case v.Kind == yaml.ScalarNode && v.Value == masked:
			if lv == nil || lv.Kind != yaml.ScalarNode {
				return bad("restore_masked", "The snapshot holds a masked value the current file cannot supply.")
			}
			snap.Content[i+1] = lv
		case v.Kind == yaml.MappingNode:
			if err := mergeLive(v, lv); err != nil {
				return err
			}
		case v.Kind == yaml.ScalarNode && strings.Contains(v.Value, masked):
			return bad("restore_masked", "The snapshot holds a masked value.")
		}
		i += 2
	}
	return nil
}
