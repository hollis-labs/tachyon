package catalogindex

import (
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entity kinds.
const (
	KindAgent     = "agent"
	KindLaunch    = "launch"
	KindProject   = "project"
	KindSkill     = "skill"
	KindMCPServer = "mcp_server"
	KindProvider  = "provider"
)

var idShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Entity is one catalog file as the Builder sees it. Fields holds only references and counts, never
// environment values.
type Entity struct {
	Path, Kind, ID, Layer, SHA, Fingerprint string
	Name, State, Provider, Model            string
	Fields                                  map[string]any
}

// kindOf maps an allow-listed catalog path to its kind. mcp-servers/, providers/ and
// sandbox-profiles/ are listed but never read (credentials); other paths are ignored.
func kindOf(p string) (kind string, readable bool, ok bool) {
	dir, file := path.Split(p)
	ext := path.Ext(file)
	if strings.Count(p, "/") != 1 || (ext != ".yaml" && ext != ".yml" && ext != ".md") {
		return "", false, false
	}
	id := strings.TrimSuffix(file, ext)
	if !idShape.MatchString(id) {
		return "", false, false
	}
	switch dir {
	case "agents/":
		return KindAgent, ext != ".md", ext != ".md"
	case "launches/":
		return KindLaunch, true, ext != ".md"
	case "projects/":
		return KindProject, true, ext != ".md"
	case "skills/":
		return KindSkill, true, ext == ".md"
	case "mcp-servers/":
		return KindMCPServer, false, ext != ".md"
	case "providers/":
		return KindProvider, false, ext != ".md"
	}
	return "", false, false
}

type wireDoc struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Enabled     *bool    `yaml:"enabled"`
	Provider    string   `yaml:"provider"`
	Model       string   `yaml:"model"`
	Extends     any      `yaml:"extends"`
	Agent       string   `yaml:"agent"`
	Project     string   `yaml:"project"`
	Worker      string   `yaml:"worker"`
	Skills      []string `yaml:"skills"`
	Roles       []string `yaml:"roles"`
	Abstract    bool     `yaml:"abstract"`
	MCP         struct {
		Servers *[]string `yaml:"servers"`
	} `yaml:"mcp"`
	Permissions struct {
		Mode string `yaml:"permission_mode"`
	} `yaml:"permissions"`
	Spec struct {
		Lifecycle string `yaml:"lifecycle_state"`
	} `yaml:"spec"`
	Env       map[string]any `yaml:"env"`
	Overrides struct {
		Env map[string]any `yaml:"env"`
	} `yaml:"overrides"`
}

// parseEntity builds the typed row from the raw content. secrets is the count of secret-like
// environment variable names (launches); the values themselves are dropped here.
func parseEntity(p, kind, content string) (Entity, error) {
	e := Entity{Path: p, Kind: kind, State: "active", Fields: map[string]any{}}
	base := path.Base(p)
	e.ID = strings.TrimSuffix(base, path.Ext(base))
	if kind == KindSkill {
		e.Name, e.Fields = skillMeta(content, e.ID)
		return e, nil
	}
	var d wireDoc
	if err := yaml.Unmarshal([]byte(content), &d); err != nil {
		return e, err
	}
	if d.ID != "" && idShape.MatchString(d.ID) {
		e.ID = d.ID
	}
	e.Name, e.Provider, e.Model = clean(d.Name, 120), clean(d.Provider, 60), clean(d.Model, 80)
	if d.Enabled != nil && !*d.Enabled {
		e.State = "disabled"
	}
	if e.State == "disabled" && d.Spec.Lifecycle == "retired" {
		e.State = "retired"
	}
	switch kind {
	case KindAgent:
		e.Fields["skills"] = nonNil(d.Skills)
		e.Fields["roles"] = nonNil(d.Roles)
		e.Fields["abstract"] = d.Abstract
		e.Fields["permission_mode"] = clean(d.Permissions.Mode, 40)
		if s, ok := d.Extends.(string); ok && s != "" {
			e.Fields["extends"] = []string{s}
		} else if l, ok := d.Extends.([]any); ok {
			ex := []string{}
			for _, v := range l {
				if s, ok := v.(string); ok {
					ex = append(ex, s)
				}
			}
			e.Fields["extends"] = ex
		}
		if d.MCP.Servers != nil {
			e.Fields["mcp_servers"] = nonNil(*d.MCP.Servers)
		}
	case KindLaunch:
		e.Fields["agent"], e.Fields["project"], e.Fields["worker"] = d.Agent, d.Project, d.Worker
		keys := []string{}
		for k := range d.Env {
			keys = append(keys, k)
		}
		for k := range d.Overrides.Env {
			keys = append(keys, k)
		}
		secrets := 0
		for _, k := range keys {
			if secretKey.MatchString(k) {
				secrets++
			}
		}
		e.Fields["env_keys"], e.Fields["secret_like_count"] = sortedCopy(keys), secrets
	case KindProject:
		if d.MCP.Servers != nil {
			e.Fields["mcp_servers"] = nonNil(*d.MCP.Servers)
		}
	}
	return e, nil
}

func clean(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return MaskText(s)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// skillMeta reads id/name/description from a skill's frontmatter.
func skillMeta(content, id string) (string, map[string]any) {
	f := map[string]any{}
	name := id
	if rest, ok := strings.CutPrefix(content, "---\n"); ok {
		if fm, _, ok := strings.Cut(rest, "\n---"); ok {
			var m struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if yaml.Unmarshal([]byte(fm), &m) == nil {
				if m.Name != "" {
					name = clean(m.Name, 120)
				}
				f["description"] = clean(m.Description, 300)
			}
		}
	}
	return name, f
}
