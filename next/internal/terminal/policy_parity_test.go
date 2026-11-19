package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The policy decisions as the old build made them: this table runs unchanged against the old package
// (chatterminal) and this one. Exact and prefix rules, machine scoping, deny beating allow in any order,
// an allow never covering a command with shell metacharacters, and a missing or unparsable policy file
// (no allow rules: every command asks).
func TestPolicyParityTable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	policy := write("policy.json", `{
 "machines":[{"id":"hub","host":"hub-host","user":"ops"},{"id":"db","host":"db-host"},{"id":"-bad","host":"x"},{"id":"dup","host":"a"},{"id":"dup","host":"b"},{"id":"badhost","host":"-oProxyCommand=x"}],
 "rules":[
  {"id":"allow-uptime","machine":"*","exact":"uptime","decision":"allow"},
  {"id":"allow-df","machine":"hub","prefix":["df","-h"],"decision":"allow"},
  {"id":"allow-git","machine":"*","prefix":["git"],"decision":"allow"},
  {"id":"deny-git-push","machine":"*","prefix":["git","push"],"decision":"deny"},
  {"id":"ask-ls","machine":"*","prefix":["ls"],"decision":"ask"},
  {"id":"deny-rm","machine":"db","prefix":["rm"],"decision":"deny"},
  {"id":"odd","machine":"*","prefix":["cat"],"decision":"maybe"}
 ]}`)
	c := &Config{PolicyFile: policy}
	for _, tc := range []struct{ machine, cmd, want, rule string }{
		{"hub", "uptime", "allow", "allow-uptime"},
		{"db", "  uptime  ", "allow", "allow-uptime"},
		{"hub", "uptime -p", "ask", ""},
		{"hub", "df -h /", "allow", "allow-df"},
		{"db", "df -h /", "ask", ""},
		{"hub", "df", "ask", ""},
		{"hub", "git status", "allow", "allow-git"},
		{"hub", "git push origin", "deny", "deny-git-push"},
		{"hub", "git status; rm -rf /", "ask", ""},
		{"hub", "git log | head", "ask", ""},
		{"hub", "git log $(id)", "ask", ""},
		{"hub", "git log `id`", "ask", ""},
		{"hub", "git log > f", "ask", ""},
		{"hub", "git log && x", "ask", ""},
		{"hub", "git log 'x'", "ask", ""},
		{"hub", "git log *", "ask", ""},
		{"hub", "git log ~", "ask", ""},
		{"hub", "git log\nrm x", "ask", ""},
		{"hub", "ls -la", "ask", ""},
		{"db", "rm -rf /tmp/x", "deny", "deny-rm"},
		{"db", "rm x; echo", "deny", "deny-rm"},
		{"hub", "rm -rf /tmp/x", "ask", ""},
		{"hub", "cat f", "ask", ""},
		{"hub", "echo hi", "ask", ""},
	} {
		got, rule := c.Decide(tc.machine, tc.cmd)
		if got != tc.want || rule != tc.rule {
			t.Errorf("%s %q: %s/%s, want %s/%s", tc.machine, tc.cmd, got, rule, tc.want, tc.rule)
		}
	}
	var ids []string
	for _, m := range c.Machines() {
		ids = append(ids, m.ID+"@"+m.host+"/"+m.SSHUser)
	}
	if got, want := len(ids), 3; got != want || ids[0] != "hub@hub-host/ops" || ids[1] != "db@db-host/" || ids[2] != "dup@a/" {
		t.Errorf("machines: %v", ids)
	}
	if _, err := c.Machine("badhost"); !errors.Is(err, ErrUnknownMachine) {
		t.Errorf("a host starting with '-': %v", err)
	}
	for name, p := range map[string]string{"missing": filepath.Join(dir, "none.json"), "unparsable": write("bad.json", `{"rules":[`)} {
		c := &Config{PolicyFile: p}
		if got, _ := c.Decide("hub", "uptime"); got != "ask" {
			t.Errorf("%s policy file: %s", name, got)
		}
		if len(c.Machines()) != 0 {
			t.Errorf("%s policy file lists machines", name)
		}
	}
}
