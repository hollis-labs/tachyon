package actionrun

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"golang.org/x/crypto/ssh"
)

// localRig runs registered test actions (harmless shell snippets) through the
// real local pty transport and the real manager.
func localRig(t *testing.T, actions ...Action) *rig {
	t.Helper()
	testRegistry = actions
	t.Cleanup(func() { testRegistry = nil })
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	items := &approvals.Store{DB: db}
	inbox := approvals.NewInbox(items, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := &Manager{Store: &Store{DB: db}, Items: items, Transports: map[string]Transport{MachineAgentOS: Local{}}, Base: ctx}
	return &rig{svc: &Service{Store: mgr.Store, Manager: mgr, Approval: inbox.Service}, inbox: inbox, db: db}
}

func testAction(id string, interactive bool, script string) Action {
	return Action{ID: id, Title: id, Machines: []string{MachineAgentOS}, Argv: []string{"sh", "-c", script}, Interactive: interactive, Class: "medium"}
}

func (r *rig) approvedRun(t *testing.T, id string) Run {
	t.Helper()
	h, err := r.svc.Request(context.Background(), RequestInput{Machine: MachineAgentOS, Action: id, Reason: "lifecycle test"})
	if err != nil {
		t.Fatal(err)
	}
	r.decide(t, h.ItemID, "approved")
	run, err := r.svc.Get(context.Background(), h.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCancelledContextLaunchesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	marker := filepath.Join(t.TempDir(), "ran")
	if s, err := (Local{}).Start(ctx, []string{"sh", "-c", "touch " + marker}, 80, 24); err == nil {
		_ = s.Close()
		t.Fatal("Local.Start launched a process with an already-cancelled context")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran")
	}
	// Manager.Start with a cancelled caller context does not start the run either.
	r := newRig(t, printAndExit("", 0))
	h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
	r.decide(t, h.ItemID, "approved")
	if _, err := r.svc.Manager.Start(ctx, h.RunID, 80, 24); err == nil || r.tr.count() != 0 {
		t.Fatalf("started with a cancelled context: %v", err)
	}
}

func TestContextCancellationKillsTheProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	s, err := (Local{}).Start(ctx, []string{"sh", "-c", "sleep 60 & echo $! > " + pidFile + "; wait"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var child int
	eventually(t, "child pid", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	cancel()
	done := make(chan struct{})
	go func() { _, _ = s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the context did not end the session")
	}
	eventually(t, "grandchild to die", func() bool { return syscall.Kill(child, 0) != nil })
}

func TestMaxRuntimeEndsARun(t *testing.T) {
	r := localRig(t, testAction("test.sleep", false, "sleep 60"))
	r.svc.Manager.MaxRuntime = 300 * time.Millisecond
	run := r.approvedRun(t, "test.sleep")
	end := r.await(t, run.ID)
	if end.State != StateFailed || !strings.Contains(end.Error, "maximum run time") {
		t.Fatalf("%+v", end)
	}
}

func TestClosingTheTerminalEndsAnInteractiveRun(t *testing.T) {
	r := localRig(t, testAction("test.prompt", true, "read x; echo done"))
	run := r.approvedRun(t, "test.prompt")
	if _, err := r.svc.Manager.Start(context.Background(), run.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	_, _, detach, err := r.svc.Manager.Attach(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !r.svc.Manager.Live(run.ID) {
		t.Fatal("run should be live while a terminal is attached")
	}
	detach() // the browser went away
	eventually(t, "run to end after the terminal closed", func() bool { return !r.svc.Manager.Live(run.ID) })
	end := r.await(t, run.ID)
	if end.State != StateFailed || !strings.Contains(end.Error, "terminal closed") {
		t.Fatalf("%+v", end)
	}
}

func TestDetachGraceAllowsReattach(t *testing.T) {
	r := localRig(t, testAction("test.prompt", true, "read x; echo done"))
	r.svc.Manager.DetachGrace = 400 * time.Millisecond
	run := r.approvedRun(t, "test.prompt")
	if _, err := r.svc.Manager.Start(context.Background(), run.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	_, _, detach, _ := r.svc.Manager.Attach(context.Background(), run.ID)
	detach()
	time.Sleep(100 * time.Millisecond)
	if !r.svc.Manager.Live(run.ID) {
		t.Fatal("run ended inside the grace period")
	}
	_, frames, detach2, err := r.svc.Manager.Attach(context.Background(), run.ID) // page reload
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if !r.svc.Manager.Live(run.ID) {
		t.Fatal("a reattached run must outlive the first grace period")
	}
	if err := r.svc.Manager.Input(run.ID, []byte("go\r")); err != nil {
		t.Fatal(err)
	}
	for f := range frames {
		if f.Exit != nil {
			break
		}
	}
	detach2()
	if end := r.await(t, run.ID); end.State != StateSucceeded {
		t.Fatalf("%+v", end)
	}
	// With the grace expired and nobody attached the run is ended.
	run2 := r.approvedRun(t, "test.prompt")
	if _, err := r.svc.Manager.Start(context.Background(), run2.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	_, _, d3, _ := r.svc.Manager.Attach(context.Background(), run2.ID)
	d3()
	eventually(t, "run to end after the grace", func() bool { return !r.svc.Manager.Live(run2.ID) })
}

func TestUnattendedRunsAreNotKilledByDetach(t *testing.T) {
	r := localRig(t, testAction("test.quick", false, "sleep 1; echo ok"))
	run := r.approvedRun(t, "test.quick")
	_, _, detach, err := r.svc.Manager.Attach(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	detach()
	if end := r.await(t, run.ID); end.State != StateSucceeded || !strings.Contains(end.Output, "ok") {
		t.Fatalf("%+v", end)
	}
}

func TestConcurrencyBoundDefersStart(t *testing.T) {
	r := localRig(t, testAction("test.slow", false, "sleep 1; echo ok"))
	r.svc.Manager.MaxConcurrent = 1
	a := r.approvedRun(t, "test.slow")
	b := r.approvedRun(t, "test.slow")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ra, _ := r.svc.Get(context.Background(), a.ID)
		rb, _ := r.svc.Get(context.Background(), b.ID)
		if ra.State == StateRunning || rb.State == StateRunning {
			if ra.State == StateRunning && rb.State == StateRunning {
				t.Fatal("two runs active with MaxConcurrent=1")
			}
		}
		if ra.State == StateSucceeded && rb.State == StateSucceeded {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("both runs should eventually finish, one after the other")
}

// A passphrase typed into an echo-off prompt is not echoed, not in the output
// the viewer receives, not in the transcript and not in any stored column.
func TestTypedInputIsNeverEchoedOrPersisted(t *testing.T) {
	const secret = "pa55-phrase-7731"
	const prefix = "feedc0de1234"
	script := `printf 'Digest prefix: '; read -r d; printf 'Passphrase: '; stty -echo; read -r p; stty echo; printf '\nverified\n'`
	r := localRig(t, testAction("test.secret", true, script))
	run := r.approvedRun(t, "test.secret")
	if _, err := r.svc.Manager.Start(context.Background(), run.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	replay, frames, detach, err := r.svc.Manager.Attach(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	var viewer strings.Builder
	viewer.Write(replay)
	read := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !strings.Contains(viewer.String(), want) {
			select {
			case f, ok := <-frames:
				if !ok {
					t.Fatalf("closed before %q: %q", want, viewer.String())
				}
				viewer.Write(f.Output)
			case <-deadline:
				t.Fatalf("timeout waiting for %q in %q", want, viewer.String())
			}
		}
	}
	read("Digest prefix:")
	_ = r.svc.Manager.Input(run.ID, []byte(prefix+"\r"))
	read("Passphrase:")
	time.Sleep(150 * time.Millisecond) // let stty -echo take effect
	_ = r.svc.Manager.Input(run.ID, []byte(secret+"\r"))
	read("verified")
	if strings.Contains(viewer.String(), secret) {
		t.Fatalf("the passphrase was echoed to the terminal: %q", viewer.String())
	}
	end := r.await(t, run.ID)
	if end.State != StateSucceeded {
		t.Fatalf("%+v", end)
	}
	var stored string
	if err := r.db.Reader.QueryRow(`SELECT group_concat(output||error||params||argv||reason||labels||requested_by,' ') FROM action_runs`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var item string
	if err := r.db.Reader.QueryRow(`SELECT group_concat(record,' ') FROM approval_items`).Scan(&item); err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{secret, prefix} {
		if strings.Contains(stored, leaked) || strings.Contains(item, leaked) || strings.Contains(end.Output, leaked) {
			t.Fatalf("typed input %q reached stored data", leaked)
		}
	}
}

func sshFixture(t *testing.T, keyMode os.FileMode) (tr *SSH, addr string) {
	t.Helper()
	dir := t.TempDir()
	cpub, cpriv, _ := ed25519.GenerateKey(rand.Reader)
	blk, _ := ssh.MarshalPrivateKey(cpriv, "")
	keyFile := filepath.Join(dir, "id")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, keyMode); err != nil { // umask-proof
		t.Fatal(err)
	}
	spub, _ := ssh.NewPublicKey(cpub)
	addr, hostKey, _ := sshServer(t, spub)
	known := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(known, []byte("["+strings.Replace(addr, ":", "]:", 1)+" "+string(ssh.MarshalAuthorizedKey(hostKey))), 0o600)
	return &SSH{Addr: addr, User: "chrispian", KeyFile: keyFile, KnownHostsFile: known}, addr
}

func TestSSHRefusesKeysAndTrustFilesOthersCouldAccess(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		tr, _ := sshFixture(t, mode)
		if c, err := tr.client(context.Background()); err == nil {
			c.Close()
			t.Fatalf("private key with mode %04o accepted", mode)
		}
		if _, err := tr.Start(context.Background(), []string{"x"}, 80, 24); err == nil {
			t.Fatalf("Start accepted a key with mode %04o", mode)
		}
	}
	tr, _ := sshFixture(t, 0o600)
	c, err := tr.client(context.Background())
	if err != nil {
		t.Fatalf("a 0600 key must work: %v", err)
	}
	c.Close()
	if err := os.Chmod(tr.KnownHostsFile, 0o666); err != nil {
		t.Fatal(err)
	}
	if c, err := tr.client(context.Background()); err == nil {
		c.Close()
		t.Fatal("a world-writable known_hosts was trusted")
	}
	if err := os.Chmod(tr.KnownHostsFile, 0o600); err != nil {
		t.Fatal(err)
	}
	tr.KeyFile = filepath.Dir(tr.KeyFile) // a directory is not a key
	if _, err := tr.client(context.Background()); err == nil {
		t.Fatal("a directory was accepted as a key")
	}
}

func TestSSHHandshakeHonoursTheContext(t *testing.T) {
	tr, _ := sshFixture(t, 0o600)
	a, b := net.Pipe() // a peer that never speaks
	defer a.Close()
	defer b.Close()
	tr.Dial = func(context.Context, string, string) (net.Conn, error) { return a, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := tr.client(ctx)
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake with a silent peer succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("the handshake ignored the context deadline")
	}
	// Cancellation (not just a deadline) also interrupts it.
	a2, b2 := net.Pipe()
	defer a2.Close()
	defer b2.Close()
	tr.Dial = func(context.Context, string, string) (net.Conn, error) { return a2, nil }
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel2() }()
	done2 := make(chan error, 1)
	go func() {
		c, err := tr.client(ctx2)
		if c != nil {
			c.Close()
		}
		done2 <- err
	}()
	select {
	case <-done2:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cancelling the context did not interrupt the handshake")
	}
}

func TestSSHCommandAndOutputHonourCancellation(t *testing.T) {
	tr, _ := sshFixture(t, 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := tr.Start(ctx, []string{"hang"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan struct{})
	go func() { _, _ = s.Wait(); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the context did not end a hung ssh command")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	start := time.Now()
	if _, err := tr.Output(ctx2, []string{"hang"}); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("Output ignored its deadline: err=%v after %s", err, time.Since(start))
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// An interactive terminal's output is never recorded, so a program that echoes
// typed input late (here 700ms, past any echo-matching window) leaves nothing.
func TestDelayedEchoOfTypedInputIsNeverStoredLoggedOrReturned(t *testing.T) {
	const sentinel = "SENTINEL-typed-after-prompt-9d41"
	logs := &lockedBuf{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	script := `printf 'Passphrase: '; read -r p; sleep 0.7; printf 'echo:%s\n' "$p"`
	r := localRig(t, testAction("test.delayed", true, script))
	run := r.approvedRun(t, "test.delayed")
	if _, err := r.svc.Manager.Start(context.Background(), run.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	replay, frames, detach, err := r.svc.Manager.Attach(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	var viewer strings.Builder
	viewer.Write(replay)
	for !strings.Contains(viewer.String(), "Passphrase:") {
		f := <-frames
		viewer.Write(f.Output)
	}
	if err := r.svc.Manager.Input(run.ID, []byte(sentinel+"\r")); err != nil {
		t.Fatal(err)
	}
	end := r.await(t, run.ID)
	if end.State != StateSucceeded || end.Output != "" || end.Note != InteractiveNote {
		t.Fatalf("interactive output must not be recorded: %+v", end)
	}
	// The attached operator's own terminal is where the stream goes.
	for f := range frames {
		viewer.Write(f.Output)
	}
	if !strings.Contains(viewer.String(), "echo:"+sentinel) {
		t.Fatalf("the operator terminal should have seen the program output: %q", viewer.String())
	}
	var stored, items string
	_ = r.db.Reader.QueryRow(`SELECT group_concat(output||error||params||argv||reason||labels||requested_by||state,' ') FROM action_runs`).Scan(&stored)
	_ = r.db.Reader.QueryRow(`SELECT group_concat(record,' ') FROM approval_items`).Scan(&items)
	agent, _ := json.Marshal(end)
	for name, hay := range map[string]string{"store": stored, "approval items": items, "agent view": string(agent), "logs": logs.String()} {
		if strings.Contains(hay, sentinel) {
			t.Fatalf("typed input reached %s", name)
		}
	}
	// Replaying a finished run gives only the (empty) recorded output.
	again, _, cancel, err := r.svc.Manager.Attach(context.Background(), run.ID)
	if err != nil || len(again) != 0 {
		t.Fatalf("finished interactive run replayed output: %q %v", again, err)
	}
	cancel()
}

func TestUnattendedActionsTakeNoInput(t *testing.T) {
	r := localRig(t, testAction("test.noinput", false, "sleep 1; echo ok"))
	run := r.approvedRun(t, "test.noinput")
	eventually(t, "run to be live", func() bool { return r.svc.Manager.Live(run.ID) })
	if err := r.svc.Manager.Input(run.ID, []byte("typed")); err == nil {
		t.Fatal("an unattended action accepted keystrokes")
	}
	if end := r.await(t, run.ID); end.State != StateSucceeded || !strings.Contains(end.Output, "ok") || end.Note != "" {
		t.Fatalf("%+v", end)
	}
}
