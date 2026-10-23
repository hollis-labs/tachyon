package actionrun

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// fakeSession scripts a terminal: it writes out, then echoes input until it
// sees a carriage return, then exits with code.
type fakeSession struct {
	out  chan []byte
	in   chan []byte
	exit int
	once sync.Once
	shut chan struct{}
	rest []byte
	mu   sync.Mutex
	got  []byte
}

func (f *fakeSession) Read(p []byte) (int, error) {
	if len(f.rest) > 0 {
		n := copy(p, f.rest)
		f.rest = f.rest[n:]
		return n, nil
	}
	var b []byte
	var ok bool
	select {
	case b, ok = <-f.out:
	case <-f.shut:
		return 0, io.EOF
	}
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, b)
	f.rest = b[n:]
	return n, nil
}
func (f *fakeSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.got = append(f.got, p...)
	f.mu.Unlock()
	f.in <- append([]byte(nil), p...)
	return len(p), nil
}
func (f *fakeSession) Resize(uint16, uint16) error { return nil }
func (f *fakeSession) Close() error                { f.once.Do(func() { close(f.shut) }); return nil }
func (f *fakeSession) Wait() (int, error)          { <-f.done(); return f.exit, nil }
func (f *fakeSession) done() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

type fakeTransport struct {
	mu       sync.Mutex
	started  [][]string
	sessions []*fakeSession
	script   func(*fakeSession)
	labels   string
}

func (t *fakeTransport) Start(_ context.Context, argv []string, _, _ uint16) (Session, error) {
	s := &fakeSession{out: make(chan []byte, 16), in: make(chan []byte, 16), shut: make(chan struct{})}
	t.mu.Lock()
	t.started = append(t.started, argv)
	t.sessions = append(t.sessions, s)
	t.mu.Unlock()
	go t.script(s)
	return s, nil
}
func (t *fakeTransport) Output(context.Context, []string) ([]byte, error) {
	return []byte(t.labels), nil
}

func (t *fakeTransport) count() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.started) }

type rig struct {
	svc   *Service
	inbox *approvals.Inbox
	tr    *fakeTransport
	db    *localstore.Store
}

func newRig(t *testing.T, script func(*fakeSession)) *rig {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	items := &approvals.Store{DB: db}
	inbox := approvals.NewInbox(items, nil)
	tr := &fakeTransport{script: script, labels: `[{"id":"hub-tether","env":"dev","owner":"self","admin":"self"},{"id":"prod-db","env":"prod","owner":"self","admin":"owner"},{"id":"bare","env":"","owner":"","admin":""}]`}
	mgr := &Manager{Store: &Store{DB: db}, Items: items, Transports: map[string]Transport{MachineAgentOS: tr, MachineHub: tr}}
	return &rig{svc: &Service{Store: mgr.Store, Manager: mgr, Approval: inbox.Service}, inbox: inbox, tr: tr, db: db}
}

func (r *rig) decide(t *testing.T, itemID, decision string) {
	t.Helper()
	rec, err := r.inbox.Store.Get(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	rev := rec.Revision
	_, err = r.inbox.Service.Respond(context.Background(), hitl.RespondCommand{ItemID: itemID, ExpectedRevision: &rev,
		Response:    hitl.Response{Kind: "approval", Decision: decision},
		Participant: hitl.Participant{PrincipalRef: "test", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func printAndExit(out string, code int) func(*fakeSession) {
	return func(s *fakeSession) { s.exit = code; s.out <- []byte(out); close(s.out) }
}

func (r *rig) await(t *testing.T, id string) Run {
	t.Helper()
	run, err := r.svc.Await(context.Background(), id, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestResolveAndDigest(t *testing.T) {
	spec, _, err := Resolve("hub", "cerberus.resource.reload", map[string]string{"resource_id": "hub-tether"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Argv, " ") != "cerberus resource reload hub-tether --ack" {
		t.Fatalf("argv %v", spec.Argv)
	}
	again, _, _ := Resolve("hub", "cerberus.resource.reload", map[string]string{"resource_id": "hub-tether"})
	other, _, _ := Resolve("agent-os", "cerberus.resource.reload", map[string]string{"resource_id": "hub-tether"})
	if spec.Digest() != again.Digest() || spec.Digest() == other.Digest() || !strings.HasPrefix(spec.Digest(), "sha256:") || len(spec.Digest()) != 71 {
		t.Fatalf("digest not stable/machine-bound: %s %s", spec.Digest(), other.Digest())
	}
	for _, bad := range []map[string]string{{"resource_id": "a b"}, {"resource_id": "x;rm -rf /"}, {"resource_id": "$(id)"}, {"resource_id": "-flag"}, {}, {"resource_id": "ok", "extra": "1"}} {
		if _, _, err := Resolve("hub", "cerberus.resource.reload", bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	var verr *ValidationError
	if _, _, err := Resolve("hub", "shell.exec", nil); !errors.As(err, &verr) || !strings.Contains(err.Error(), "cerberus.resource.reload") {
		t.Fatalf("unknown action error should list allowed actions: %v", err)
	}
	if _, _, err := Resolve("agent-os", "tether.catalog.approve", map[string]string{"plan_id": "p1"}); err == nil {
		t.Fatal("tether catalog approve must be hub-only")
	}
	approve, _, err := Resolve("hub", "tether.catalog.approve", map[string]string{"plan_id": "plan-1", "ack_privileged": "true"})
	if err != nil || strings.Join(approve.Argv, " ") != "tether catalog approve plan-1 --ack-privileged" {
		t.Fatalf("%v %v", approve.Argv, err)
	}
}

func TestAutoRunAfterApprovalReportsExitAndOutput(t *testing.T) {
	r := newRig(t, printAndExit("reloaded hub-tether\r\n", 0))
	h, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.reload", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "pick up config", AgentID: "agent-x"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Class != "medium" || h.State != StatePending || h.Command != "cerberus resource reload hub-tether --ack" {
		t.Fatalf("%+v", h)
	}
	if r.tr.count() != 0 {
		t.Fatal("ran before approval")
	}
	if run, _ := r.svc.Get(context.Background(), h.RunID); run.State != StatePending || r.tr.count() != 0 {
		t.Fatalf("pending run must not start: %+v", run)
	}
	r.decide(t, h.ItemID, "approved")
	run := r.await(t, h.RunID)
	if run.State != StateSucceeded || run.ExitCode == nil || *run.ExitCode != 0 || !strings.Contains(run.Output, "reloaded hub-tether") || run.EndedAt == nil || run.StartedAt == nil {
		t.Fatalf("%+v", run)
	}
	if got := strings.Join(r.tr.started[0], " "); got != "cerberus resource reload hub-tether --ack" {
		t.Fatalf("ran %q", got)
	}
}

func TestNonZeroExitFails(t *testing.T) {
	r := newRig(t, printAndExit("boom\n", 3))
	h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
	r.decide(t, h.ItemID, "approved")
	run := r.await(t, h.RunID)
	if run.State != StateFailed || *run.ExitCode != 3 {
		t.Fatalf("%+v", run)
	}
}

func TestDeniedNeverRuns(t *testing.T) {
	r := newRig(t, printAndExit("", 0))
	h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
	r.decide(t, h.ItemID, "denied")
	run := r.await(t, h.RunID)
	if run.State != StateDenied || r.tr.count() != 0 {
		t.Fatalf("%+v", run)
	}
	if _, err := r.svc.Manager.Start(context.Background(), h.RunID, 80, 24); err == nil || r.tr.count() != 0 {
		t.Fatal("a denied run must not start")
	}
}

func TestStartRefusedBeforeApproval(t *testing.T) {
	r := newRig(t, printAndExit("", 0))
	h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
	if _, err := r.svc.Manager.Start(context.Background(), h.RunID, 80, 24); err == nil || r.tr.count() != 0 {
		t.Fatalf("started an unapproved run: %v", err)
	}
}

func TestDigestMismatchRefuses(t *testing.T) {
	for name, tamper := range map[string]string{
		"stored digest":  "UPDATE action_runs SET op_digest='sha256:" + strings.Repeat("0", 64) + "' WHERE id=?",
		"stored params":  `UPDATE action_runs SET params='{"resource_id":"prod-db"}' WHERE id=?`,
		"stored argv":    `UPDATE action_runs SET argv='["cerberus","resource","stop","prod-db","--ack"]' WHERE id=?`,
		"stored machine": `UPDATE action_runs SET machine='agent-os' WHERE id=?`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, printAndExit("", 0))
			h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
			r.decide(t, h.ItemID, "approved")
			err := r.db.Writer.Submit(context.Background(), "tamper", func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, tamper, h.RunID)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			run := r.await(t, h.RunID)
			if run.State != StateRefused || r.tr.count() != 0 || !strings.Contains(run.Error, "") {
				t.Fatalf("%+v ran=%d", run, r.tr.count())
			}
		})
	}
}

func TestEnvironmentClassification(t *testing.T) {
	r := newRig(t, printAndExit("", 0))
	for _, id := range []string{"prod-db", "bare", "missing"} {
		_, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": id}, Reason: "x"})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("%s should be refused as not dev: %v", id, err)
		}
	}
	if _, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.stop", Params: map[string]string{"resource_id": "hub-tether"}}); err == nil {
		t.Fatal("reason is required")
	}
}

func TestInteractiveWaitsForAttachAndNeverStoresInput(t *testing.T) {
	script := func(s *fakeSession) {
		s.out <- []byte("Digest prefix: ")
		in := <-s.in // digest prefix, echoed back
		s.out <- in
		s.out <- []byte("\r\nPassphrase: ")
		<-s.in // passphrase, echo off: nothing comes back
		s.out <- []byte("\r\napproved plan\r\n")
		s.exit = 0
		close(s.out)
	}
	r := newRig(t, script)
	h, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "tether.catalog.approve", Params: map[string]string{"plan_id": "plan-1"}, Reason: "approve it"})
	if err != nil {
		t.Fatal(err)
	}
	r.decide(t, h.ItemID, "approved")
	run, err := r.svc.Get(context.Background(), h.RunID)
	if err != nil || run.State != StateWaitingInput || r.tr.count() != 0 {
		t.Fatalf("interactive run must wait for attach: %+v %v", run, err)
	}
	if _, err := r.svc.Manager.Start(context.Background(), h.RunID, 100, 30); err != nil {
		t.Fatal(err)
	}
	replay, frames, cancel, err := r.svc.Manager.Attach(context.Background(), h.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_ = replay
	waitFor := func(substr string) {
		t.Helper()
		var seen strings.Builder
		seen.Write(replay)
		replay = nil
		deadline := time.After(3 * time.Second)
		for !strings.Contains(seen.String(), substr) {
			select {
			case f, ok := <-frames:
				if !ok {
					t.Fatalf("closed before %q: %q", substr, seen.String())
				}
				seen.Write(f.Output)
			case <-deadline:
				t.Fatalf("timeout waiting for %q in %q", substr, seen.String())
			}
		}
	}
	waitFor("Digest prefix:")
	if err := r.svc.Manager.Input(h.RunID, []byte("abcdef012345\r")); err != nil {
		t.Fatal(err)
	}
	waitFor("Passphrase:")
	time.Sleep(10 * time.Millisecond)
	if err := r.svc.Manager.Input(h.RunID, []byte("hunter2-secret\r")); err != nil {
		t.Fatal(err)
	}
	done := r.await(t, h.RunID)
	if done.State != StateSucceeded || !strings.Contains(done.Output, "approved plan") {
		t.Fatalf("%+v", done)
	}
	for _, leaked := range []string{"hunter2", "abcdef012345"} {
		if strings.Contains(done.Output, leaked) {
			t.Fatalf("input %q reached the stored transcript: %q", leaked, done.Output)
		}
	}
	var stored string
	err = r.db.Reader.QueryRow(`SELECT group_concat(output||error||params||argv||reason,' ') FROM action_runs`).Scan(&stored)
	if err != nil || strings.Contains(stored, "hunter2") || strings.Contains(stored, "abcdef012345") {
		t.Fatalf("input in stored data: %v %q", err, stored)
	}
}

func TestRedact(t *testing.T) {
	in := "\x1b[32mok\x1b[0m password: hunter2 Authorization: Bearer abcdefghijklmnop token=abc123 -----BEGIN OPENSSH PRIVATE KEY-----\nxxx\n-----END OPENSSH PRIVATE KEY-----"
	out := Redact(in)
	for _, leak := range []string{"hunter2", "abcdefghijklmnop", "abc123", "xxx", "\x1b"} {
		if strings.Contains(out, leak) {
			t.Fatalf("%q still in %q", leak, out)
		}
	}
	if !strings.Contains(out, "ok") {
		t.Fatal(out)
	}
}

func TestTranscriptIsBounded(t *testing.T) {
	var tr transcript
	tr.output([]byte(strings.Repeat("x", 10*MaxStoredOutput)))
	if got := tr.text(); len(got) > MaxStoredOutput+64 || !strings.HasPrefix(got, "[earlier output truncated]") {
		t.Fatalf("len %d", len(got))
	}
}

func TestLocalTransportRunsArgvOnPty(t *testing.T) {
	s, err := Local{}.Start(context.Background(), []string{"sh", "-c", "read x; echo got:$x; exit 4"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := s.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	code, err := s.Wait()
	if err != nil || code != 4 || !strings.Contains(sb.String(), "got:hello") {
		t.Fatalf("code=%d err=%v out=%q", code, err, sb.String())
	}
	_ = s.Close()
}

func TestLabelsAreRecheckedAtStartAndFailClosed(t *testing.T) {
	for name, relabel := range map[string]string{
		"relabelled prod":        `[{"id":"hub-tether","env":"prod","owner":"self","admin":"self"}]`,
		"relabelled admin owner": `[{"id":"hub-tether","env":"dev","owner":"self","admin":"owner"}]`,
		"label removed":          `[{"id":"hub-tether","env":"","owner":"","admin":""}]`,
		"resource gone":          `[]`,
		"lookup unparseable":     `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, printAndExit("ran\n", 0))
			h, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.reload", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
			if err != nil {
				t.Fatal(err)
			}
			r.tr.labels = relabel // between request and approval
			r.decide(t, h.ItemID, "approved")
			run := r.await(t, h.RunID)
			if run.State != StateRefused || r.tr.count() != 0 || run.Error == "" {
				t.Fatalf("a non-dev or unreadable target must not run: %+v ran=%d", run, r.tr.count())
			}
		})
	}
	t.Run("lookup transport failure", func(t *testing.T) {
		r := newRig(t, printAndExit("ran\n", 0))
		h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.reload", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
		r.svc.Manager.Labels = func(context.Context, string, string) (Labels, error) {
			return Labels{}, errors.New("ssh: connection refused")
		}
		r.decide(t, h.ItemID, "approved")
		run := r.await(t, h.RunID)
		if run.State != StateRefused || r.tr.count() != 0 {
			t.Fatalf("%+v", run)
		}
	})
	t.Run("still dev runs", func(t *testing.T) {
		r := newRig(t, printAndExit("ran\n", 0))
		h, _ := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "cerberus.resource.reload", Params: map[string]string{"resource_id": "hub-tether"}, Reason: "x"})
		r.decide(t, h.ItemID, "approved")
		if run := r.await(t, h.RunID); run.State != StateSucceeded {
			t.Fatalf("%+v", run)
		}
	})
}
