package chatterminal

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// fakeSSH stands in for ssh: it runs the command after "--" locally and records its argv.
const fakeSSH = `#!/bin/sh
echo "$@" >> "$FAKE_SSH_LOG"
while [ "$1" != "--" ] && [ $# -gt 0 ]; do shift; done
shift
exec sh -c "$1"
`

type env struct {
	log    string
	t      *testing.T
	svc    *Service
	h      http.Handler
	dir    string
	mu     sync.Mutex
	events []map[string]any
	ended  chan Run
}

func setup(t *testing.T, policy string) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := localstore.Open(context.Background(), filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bin := filepath.Join(dir, "ssh")
	logPath := filepath.Join(dir, "ssh.log")
	if err := os.WriteFile(bin, []byte(strings.ReplaceAll(fakeSSH, "$FAKE_SSH_LOG", logPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(pol, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &env{log: logPath, t: t, dir: dir, ended: make(chan Run, 16)}
	cfg := &Config{PolicyFile: pol}
	store := &Store{DB: db}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	publish := func(_ context.Context, thread, typ string, f map[string]any) {
		e.mu.Lock()
		defer e.mu.Unlock()
		c := map[string]any{"type": typ, "thread_ref": thread}
		for k, v := range f {
			c[k] = v
		}
		e.events = append(e.events, c)
	}
	mgr := &Manager{Store: store, Config: cfg, SSHBin: bin, Base: ctx, Publish: publish, Ended: func(_ context.Context, r Run) { e.ended <- r }}
	e.svc = &Service{Store: store, Mgr: mgr, Config: cfg, Publish: publish,
		Exists: func(_ context.Context, ref string) error {
			if ref == "app:gone" {
				return ErrNotFound
			}
			return nil
		}}
	e.h = (&Handler{Svc: e.svc}).Mux()
	return e
}

const basicPolicy = `{
 "machines":[{"id":"hub","label":"Hub","host":"hub.example","user":"chris"}],
 "rules":[
  {"id":"r-echo","machine":"*","prefix":["echo"],"decision":"allow"},
  {"id":"r-rm","machine":"*","prefix":["rm"],"decision":"deny"}
 ]}`

func (e *env) call(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, chatapi.TerminalPrefix+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

func (e *env) waitEnded() Run {
	e.t.Helper()
	select {
	case r := <-e.ended:
		return r
	case <-time.After(10 * time.Second):
		e.t.Fatal("run did not end")
		return Run{}
	}
}

func TestMachinesComeFromPolicyAndSSHConfig(t *testing.T) {
	e := setup(t, basicPolicy)
	cfgPath := filepath.Join(e.dir, "ssh_config")
	os.WriteFile(cfgPath, []byte("Host *\n  User x\nHost box other\n  User root\nHost *.wild\nHost -oProxyCommand=evil\n"), 0o600)
	e.svc.Config.SSHConfig = cfgPath
	ms := e.svc.Config.Machines()
	ids := []string{}
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "hub,box,other" {
		t.Fatalf("machines = %v", ids)
	}
	if ms[1].SSHUser != "root" {
		t.Fatalf("user = %q", ms[1].SSHUser)
	}
	if _, err := e.svc.Config.Machine("hub.example"); err == nil {
		t.Fatal("a host name that is not a machine id must not resolve")
	}
}

func TestSSHArgvIsTheFixedTemplate(t *testing.T) {
	m := Machine{ID: "hub", host: "hub.example", SSHUser: "chris"}
	got := strings.Join(sshArgv("ssh", m, "ls -l", true), " ")
	for _, want := range []string{"StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "ForwardAgent=no", "BatchMode=yes", "-tt", "-l chris hub.example -- ls -l"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q lacks %q", got, want)
		}
	}
	if strings.Contains(strings.Join(sshArgv("ssh", m, "", false), " "), "--") {
		t.Error("the interactive argv must carry no command")
	}
}

func TestPolicyDecisions(t *testing.T) {
	e := setup(t, basicPolicy)
	for _, c := range []struct{ cmd, want string }{
		{"echo hi", "allow"}, {"echo hi; reboot", "ask"}, {"echo $(id)", "ask"}, {"ls", "ask"}, {"rm -rf /tmp/x", "deny"}, {"echo ok && rm x", "ask"},
	} {
		if got, _ := e.svc.Config.Decide("hub", c.cmd); got != c.want {
			t.Errorf("%q: %s, want %s", c.cmd, got, c.want)
		}
	}
	// deny beats allow whatever the order
	if got, _ := (&Config{PolicyFile: e.svc.Config.PolicyFile}).Decide("hub", "rm echo"); got != "deny" {
		t.Errorf("rm: %s", got)
	}
}

func TestPolicyAllowedCommandRunsAtOnceAndIsAttributed(t *testing.T) {
	e := setup(t, basicPolicy)
	run, _, err := e.svc.Propose(context.Background(), "app:t1", "hub", "echo hello-world", "check", "msg://agent/x")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateRunning || run.Approval.Mode != "policy" || run.Approval.RuleID == nil || *run.Approval.RuleID != "r-echo" {
		t.Fatalf("run = %+v", run)
	}
	r := e.waitEnded()
	if r.State != StateFinished || r.ExitCode == nil || *r.ExitCode != 0 || !strings.Contains(r.Output, "hello-world") {
		t.Fatalf("ended = %+v", r)
	}
	log, _ := os.ReadFile(e.log)
	if !strings.Contains(string(log), "-l chris hub.example -- echo hello-world") {
		t.Fatalf("ssh argv = %s", log)
	}
	// terminal event carried the output
	e.mu.Lock()
	defer e.mu.Unlock()
	var sawChunk, sawEnd bool
	for _, ev := range e.events {
		if ev["type"] != "terminal.run" {
			t.Fatalf("event type %v", ev["type"])
		}
		if c, ok := ev["chunk"].(string); ok && strings.Contains(c, "hello-world") {
			sawChunk = true
		}
		if ev["state"] == StateFinished && ev["exit_code"] != nil {
			sawEnd = true
		}
	}
	if !sawChunk || !sawEnd {
		t.Fatalf("events = %v", e.events)
	}
}

func TestDeniedByPolicy(t *testing.T) {
	e := setup(t, basicPolicy)
	if _, _, err := e.svc.Propose(context.Background(), "app:t1", "hub", "rm -rf x", "", "a"); err != ErrDenied {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := e.svc.Propose(context.Background(), "app:t1", "nope", "ls", "", "a"); err != ErrUnknownMachine {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := e.svc.Propose(context.Background(), "app:gone", "hub", "ls", "", "a"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestApprovalFlowAndNonzeroExit(t *testing.T) {
	e := setup(t, basicPolicy)
	run, prop, err := e.svc.Propose(context.Background(), "app:t1", "hub", "echo out; exit 3", "why", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StatePending || prop.State != "pending" {
		t.Fatalf("%+v %+v", run, prop)
	}
	w := e.call("POST", "/proposals/"+prop.ID+"/run", `{"op_digest":"`+prop.CommandDigest+`"}`)
	if w.Code != 200 {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	r := e.waitEnded()
	if r.State != StateFailed || r.ExitCode == nil || *r.ExitCode != 3 || r.Approval.Mode != "user" || r.Approval.DecidedBy == nil {
		t.Fatalf("ended = %+v", r)
	}
	// approving twice is a conflict
	if w := e.call("POST", "/proposals/"+prop.ID+"/run", ``); w.Code != 409 {
		t.Fatalf("second run: %d", w.Code)
	}
}

func TestEditedCommandNeedsItsOwnApprovalAndOldOneIsRefused(t *testing.T) {
	e := setup(t, basicPolicy)
	_, prop, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "ls /tmp", "", "agent")
	w := e.call("POST", "/proposals/"+prop.ID+"/edit", `{"command":"echo edited"}`)
	if w.Code != 201 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	// the old proposal can no longer be approved
	if w := e.call("POST", "/proposals/"+prop.ID+"/run", ``); w.Code != 409 {
		t.Fatalf("old proposal ran: %d", w.Code)
	}
	old, _ := e.svc.Store.Run(context.Background(), prop.RunID)
	if old.State != StateDenied {
		t.Fatalf("old run state = %s", old.State)
	}
	// "echo edited" matches an allow rule, but an edited command is still the operator's to approve
	ps, _ := e.svc.Store.Proposals(context.Background(), "app:t1", "pending")
	if len(ps) != 1 || ps[0].Command != "echo edited" || ps[0].CommandDigest == prop.CommandDigest || ps[0].Supersedes == nil {
		t.Fatalf("proposals = %+v", ps)
	}
}

func TestApprovedBytesMustMatchTheDigest(t *testing.T) {
	e := setup(t, basicPolicy)
	_, prop, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "ls /tmp", "", "agent")
	// a stale digest from the client is refused
	if w := e.call("POST", "/proposals/"+prop.ID+"/run", `{"op_digest":"deadbeef"}`); w.Code != 409 || !strings.Contains(w.Body.String(), "digest_mismatch") {
		t.Fatalf("stale digest: %d %s", w.Code, w.Body)
	}
	// a row altered after the digest was fixed is refused at approval
	e.svc.Store.DB.Writer.Submit(context.Background(), "tamper", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE terminal_proposals SET command='ls /etc' WHERE id=?`, prop.ID)
		return err
	})
	if w := e.call("POST", "/proposals/"+prop.ID+"/run", ``); w.Code != 409 {
		t.Fatalf("tampered proposal ran: %d", w.Code)
	}
	if len(e.ended) != 0 {
		t.Fatal("nothing should have started")
	}
}

func TestAgentCredentialsCannotUseTheOperatorRoutes(t *testing.T) {
	e := setup(t, basicPolicy)
	_, prop, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "ls /tmp", "", "agent")
	for _, c := range [][2]string{{"POST", "/proposals/" + prop.ID + "/run"}, {"POST", "/proposals/" + prop.ID + "/deny"}, {"POST", "/runs/" + prop.RunID + "/stop"}, {"PATCH", "/runs/" + prop.RunID}} {
		if w := e.call(c[0], c[1], `{"closed":true}`, "Authorization", "Bearer agent-token"); w.Code != 403 {
			t.Errorf("%s %s with a bearer token: %d", c[0], c[1], w.Code)
		}
	}
	if r, _ := e.svc.Store.Run(context.Background(), prop.RunID); r.State != StatePending {
		t.Fatalf("state = %s", r.State)
	}
}

func TestDenyAndExpire(t *testing.T) {
	e := setup(t, basicPolicy)
	_, p1, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "ls", "", "a")
	if w := e.call("POST", "/proposals/"+p1.ID+"/deny", ``); w.Code != 200 {
		t.Fatalf("deny: %d", w.Code)
	}
	if r, _ := e.svc.Store.Run(context.Background(), p1.RunID); r.State != StateDenied {
		t.Fatalf("state %s", r.State)
	}
	_, p2, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "pwd", "", "a")
	e.svc.Store.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if w := e.call("POST", "/proposals/"+p2.ID+"/run", ``); w.Code != 409 {
		t.Fatalf("expired proposal ran: %d", w.Code)
	}
	if r, _ := e.svc.Store.Run(context.Background(), p2.RunID); r.State != StateExpired {
		t.Fatalf("state %s", r.State)
	}
}

func TestStopAndCloseSemantics(t *testing.T) {
	e := setup(t, `{"machines":[{"id":"hub","host":"hub","user":"u"}],"rules":[{"id":"r","machine":"*","prefix":["sleep"],"decision":"allow"}]}`)
	run, _, err := e.svc.Propose(context.Background(), "app:t1", "hub", "sleep 30", "", "agent")
	if err != nil {
		t.Fatal(err)
	}
	// a running card cannot be closed away from its output
	if w := e.call("PATCH", "/runs/"+run.RunID, `{"closed":true}`); w.Code != 409 {
		t.Fatalf("close while running: %d", w.Code)
	}
	if w := e.call("PATCH", "/runs/"+run.RunID, `{"close_on_complete":true}`); w.Code != 200 {
		t.Fatalf("close_on_complete while running: %d", w.Code)
	}
	for i := 0; i < 2; i++ { // idempotent
		if w := e.call("POST", "/runs/"+run.RunID+"/stop", ``); w.Code != 202 {
			t.Fatalf("stop: %d %s", w.Code, w.Body)
		}
	}
	r := e.waitEnded()
	if r.State != StateStopped || r.StoppedBy == nil || *r.StoppedBy != "operator" {
		t.Fatalf("ended = %+v", r)
	}
	if r.ClosedAt == nil {
		t.Fatal("close_on_complete should have closed the card")
	}
	// stop on a finished run is a no-op
	if w := e.call("POST", "/runs/"+run.RunID+"/stop", ``); w.Code != 202 {
		t.Fatalf("stop after end: %d", w.Code)
	}
	if w := e.call("PATCH", "/runs/"+run.RunID, `{"closed":false}`); w.Code != 200 {
		t.Fatalf("reopen: %d", w.Code)
	}
	if w := e.call("PATCH", "/runs/"+run.RunID, `{"closed":true}`); w.Code != 200 {
		t.Fatalf("close: %d", w.Code)
	}
	w := e.call("GET", "/runs?thread_ref=app:t1&closed=false", ``)
	if strings.Contains(w.Body.String(), run.RunID) {
		t.Fatalf("closed run listed as open: %s", w.Body)
	}
}

func TestOutputRouteAndRedaction(t *testing.T) {
	e := setup(t, basicPolicy)
	run, _, _ := e.svc.Propose(context.Background(), "app:t1", "hub", "echo password=hunter2", "", "agent")
	e.waitEnded()
	w := e.call("GET", "/runs/"+run.RunID+"/output", ``)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hunter2") || !strings.Contains(w.Body.String(), "[redacted]") {
		t.Fatalf("output: %d %s", w.Code, w.Body)
	}
	got, _ := e.svc.Store.Run(context.Background(), run.RunID)
	if strings.Contains(got.Output, "hunter2") {
		t.Fatal("stored tail leaks the secret")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.events {
		if c, _ := ev["chunk"].(string); strings.Contains(c, "hunter2") {
			t.Fatal("event leaks the secret")
		}
	}
}

func TestSessionLimitAndUnknownMachine(t *testing.T) {
	e := setup(t, basicPolicy)
	if w := e.call("POST", "/sessions", `{"machine_id":"hub.example"}`); w.Code != 404 {
		t.Fatalf("unlisted host: %d", w.Code)
	}
	for i := 0; i < maxSessions; i++ {
		if w := e.call("POST", "/sessions", `{"machine_id":"hub"}`); w.Code != 201 {
			t.Fatalf("open %d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := e.call("POST", "/sessions", `{"machine_id":"hub"}`); w.Code != 429 {
		t.Fatalf("fifth session: %d", w.Code)
	}
}

func TestInteractiveSessionRoundTrip(t *testing.T) {
	e := setup(t, basicPolicy)
	// the fake ssh with no "--" would run nothing; use a starter that runs cat in a pty instead
	e.svc.Mgr.Start = func(ctx context.Context, argv, env []string, cols, rows uint16) (Session, error) {
		return StartPTY(ctx, []string{"cat"}, os.Environ(), cols, rows)
	}
	w := e.call("POST", "/sessions", `{"machine_id":"hub"}`)
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	id := between(w.Body.String(), `"id":"`, `"`)
	att, err := e.svc.Mgr.Attach(context.Background(), id, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := att.Input([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	var got string
	deadline := time.After(5 * time.Second)
	for !strings.Contains(got, "ping") {
		select {
		case f := <-att.Frames:
			got += string(f)
		case <-deadline:
			t.Fatalf("no echo, got %q", got)
		}
	}
	if w := e.call("DELETE", "/sessions/"+id, ``); w.Code != 200 {
		t.Fatalf("close: %d", w.Code)
	}
	select {
	case <-att.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
	row, _ := e.svc.Store.Session(context.Background(), id)
	if row.State != "closed" || row.EndedAt == nil {
		t.Fatalf("row = %+v", row)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	return s[:strings.Index(s, b)]
}
