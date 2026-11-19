package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// groupGone waits for every process in the group to be gone (a killed grandchild is reaped by init).
func groupGone(pgid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func pgidOf(s Session) int {
	if l, ok := s.(*localSession); ok && l.cmd.Process != nil {
		return l.cmd.Process.Pid // the child leads its own session, so its pid is the group id
	}
	return 0
}

// Shutdown leaves no pty child behind: an interactive pane and an approved run each start a process that
// forks a long-lived child of its own; canceling Base kills both groups and Wait returns once both
// leaders are reaped.
func TestNoPTYChildSurvivesShutdown(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "ssh")
	if err := os.WriteFile(bin, []byte(strings.ReplaceAll(fakeSSH, "$FAKE_SSH_LOG", filepath.Join(dir, "ssh.log"))), 0o700); err != nil { //nolint:gosec // the fake ssh must be executable
		t.Fatal(err)
	}
	pol := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"machines":[{"id":"hub","host":"hub-host"}],"rules":[{"id":"r-sleep","machine":"*","prefix":["sleep"],"decision":"allow"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{PolicyFile: pol}
	st := &Store{DB: storetest.New(t).Open(t)}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &Manager{Store: st, Config: cfg, SSHBin: bin, Base: base}
	svc := &Service{Store: st, Mgr: mgr, Config: cfg}
	ctx := context.Background()

	// The interactive pane: a shell with a background child, standing in for ssh.
	mgr.Start = func(ctx context.Context, argv, env []string, cols, rows uint16) (Session, error) {
		if argv[len(argv)-2] == "--" {
			return StartPTY(ctx, argv, env, cols, rows) // a run: the fake ssh runs its command
		}
		return StartPTY(ctx, []string{"sh", "-c", "sleep 300 & sleep 300"}, os.Environ(), cols, rows)
	}
	row, err := mgr.OpenSession(ctx, "hub", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mgr.Attach(ctx, row.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	ls := mgr.liveSession(row.ID)
	ls.mu.Lock()
	pane := pgidOf(ls.sess)
	ls.mu.Unlock()

	// The approved run: allowed by policy, so it starts at once.
	run, _, err := svc.Propose(ctx, "app:t1", "hub", "sleep 300", "", "agent")
	if err != nil || run.State != StateRunning {
		t.Fatalf("propose: %+v %v", run, err)
	}
	var job int
	for deadline := time.Now().Add(5 * time.Second); job == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		mgr.mu.Lock()
		if lr := mgr.runs[run.RunID]; lr != nil {
			lr.mu.Lock()
			job = pgidOf(lr.sess)
			lr.mu.Unlock()
		}
		mgr.mu.Unlock()
	}
	if pane == 0 || job == 0 {
		t.Fatalf("process groups: pane %d run %d", pane, job)
	}
	for _, g := range []int{pane, job} {
		if err := syscall.Kill(-g, 0); err != nil {
			t.Fatalf("group %d is not running before shutdown: %v", g, err)
		}
	}

	// Whatever happens below, the test leaves no process behind.
	defer func() {
		for _, g := range []int{pane, job} {
			_ = syscall.Kill(-g, syscall.SIGKILL)
		}
	}()
	cancel()
	done := make(chan struct{})
	go func() { mgr.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after shutdown")
	}
	for name, g := range map[string]int{"pane": pane, "run": job} {
		if !groupGone(g) {
			t.Errorf("the %s's process group %d survived shutdown", name, g)
		}
	}
}
