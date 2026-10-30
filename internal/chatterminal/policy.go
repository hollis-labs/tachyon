// Package chatterminal is the chat quick terminal: an interactive ssh pane for the operator and
// approval-gated command runs for agents. Credentials are the Tachyon host user's own ssh setup
// (~/.ssh/config and ssh-agent); this package never reads a key file and never takes a host name from a client.
package chatterminal

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Machine is one entry of the picker. The client sends ID, never a host name.
type Machine struct {
	ID        string `json:"machine_id"`
	Label     string `json:"label"`
	Source    string `json:"source"` // "policy" | "ssh_config"
	SSHUser   string `json:"ssh_user"`
	Reachable *bool  `json:"reachable"`
	host      string
}

// Rule decides what happens to an agent's proposed command. Exactly one of Exact or Prefix applies;
// Prefix matches the leading words of the command. Decision is allow, ask or deny; deny beats allow and
// the default is ask.
type Rule struct {
	ID       string   `json:"id"`
	Machine  string   `json:"machine"` // machine id or "*"
	Exact    string   `json:"exact,omitempty"`
	Prefix   []string `json:"prefix,omitempty"`
	Decision string   `json:"decision"`
}

// Policy is the operator-edited file. Machines listed here come in addition to the Host aliases of
// ~/.ssh/config.
type Policy struct {
	Machines []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Host  string `json:"host"`
		User  string `json:"user"`
	} `json:"machines"`
	Rules []Rule `json:"rules"`
	// AuthSock overrides SSH_AUTH_SOCK for the ssh processes Tachyon starts.
	AuthSock string `json:"ssh_auth_sock,omitempty"`
}

// Config locates the operator-edited inputs. Both files are optional and reloaded when they change.
type Config struct {
	PolicyFile string
	SSHConfig  string

	mu       sync.Mutex
	polMod   time.Time
	pol      Policy
	cfgMod   time.Time
	cfgHosts []Machine
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var userPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]{0,63}$`)

func (c *Config) policy() Policy {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.PolicyFile == "" {
		return Policy{}
	}
	info, err := os.Stat(c.PolicyFile)
	if err != nil {
		c.pol, c.polMod = Policy{}, time.Time{}
		return c.pol
	}
	if info.ModTime().Equal(c.polMod) {
		return c.pol
	}
	raw, err := os.ReadFile(c.PolicyFile)
	var p Policy
	// A file that does not parse is treated as empty: no allow rules, every command asks.
	if err == nil && json.Unmarshal(raw, &p) == nil {
		c.pol = p
	} else {
		c.pol = Policy{}
	}
	c.polMod = info.ModTime()
	return c.pol
}

// sshHosts reads exact Host aliases (no wildcards) and their User from the ssh config.
func (c *Config) sshHosts() []Machine {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.SSHConfig
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		c.cfgHosts, c.cfgMod = nil, time.Time{}
		return nil
	}
	if info.ModTime().Equal(c.cfgMod) {
		return c.cfgHosts
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Machine
	var cur []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			key, val, ok = strings.Cut(line, "=")
		}
		if !ok {
			continue
		}
		val = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(val), "="))
		switch strings.ToLower(key) {
		case "host":
			cur = cur[:0]
			for _, h := range strings.Fields(val) {
				if strings.ContainsAny(h, "*?!") || !hostPattern.MatchString(h) {
					continue
				}
				out = append(out, Machine{ID: h, Label: h, Source: "ssh_config", host: h})
				cur = append(cur, len(out)-1)
			}
		case "match":
			cur = cur[:0]
		case "user":
			for _, i := range cur {
				if userPattern.MatchString(val) {
					out[i].SSHUser = val
				}
			}
		}
	}
	c.cfgHosts, c.cfgMod = out, info.ModTime()
	return out
}

// Machines lists the policy machines then the ssh config aliases; an id seen twice keeps the first.
func (c *Config) Machines() []Machine {
	var out []Machine
	seen := map[string]bool{}
	for _, m := range c.policy().Machines {
		if !hostPattern.MatchString(m.ID) || !hostPattern.MatchString(m.Host) || seen[m.ID] || (m.User != "" && !userPattern.MatchString(m.User)) {
			continue
		}
		seen[m.ID] = true
		label := m.Label
		if label == "" {
			label = m.ID
		}
		out = append(out, Machine{ID: m.ID, Label: label, Source: "policy", SSHUser: m.User, host: m.Host})
	}
	for _, m := range c.sshHosts() {
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// Machine resolves an id from the list. An unknown id is an error: there is no free-form host.
func (c *Config) Machine(id string) (Machine, error) {
	for _, m := range c.Machines() {
		if m.ID == id {
			return m, nil
		}
	}
	return Machine{}, ErrUnknownMachine
}

var ErrUnknownMachine = errors.New("unknown machine")

// shellMeta marks a command whose words cannot be trusted to be the whole story.
const shellMeta = ";|&$`\n\r<>(){}\\'\"*?~!#"

// Decide evaluates the rules for one command. An allow only counts for a command with no shell
// metacharacters, so "uptime; rm -rf" never rides on an "uptime" rule. Deny matches regardless.
func (c *Config) Decide(machine, command string) (decision string, ruleID string) {
	cmd := strings.TrimSpace(command)
	words := strings.Fields(cmd)
	plain := !strings.ContainsAny(cmd, shellMeta)
	allow := ""
	for _, r := range c.policy().Rules {
		if r.Machine != "*" && r.Machine != machine {
			continue
		}
		hit := false
		switch {
		case r.Exact != "":
			hit = strings.TrimSpace(r.Exact) == cmd
		case len(r.Prefix) > 0:
			hit = len(words) >= len(r.Prefix)
			for i := 0; hit && i < len(r.Prefix); i++ {
				hit = words[i] == r.Prefix[i]
			}
		}
		if !hit {
			continue
		}
		switch r.Decision {
		case "deny":
			return "deny", r.ID
		case "allow":
			if plain && allow == "" {
				allow = r.ID
			}
		}
	}
	if allow != "" {
		return "allow", allow
	}
	return "ask", ""
}

// AuthSock is the ssh-agent socket handed to ssh: the policy override, else the process's own.
func (c *Config) AuthSock() string {
	if s := c.policy().AuthSock; s != "" {
		return s
	}
	return os.Getenv("SSH_AUTH_SOCK")
}

// DefaultSSHConfig is the Tachyon user's ssh config.
func DefaultSSHConfig() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".ssh", "config")
	}
	return ""
}
