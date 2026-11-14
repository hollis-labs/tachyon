package agentsserve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatbridge"
)

type fakeSender struct {
	to, body string
	n        int
	err      error
}

func (f *fakeSender) Send(_ context.Context, to, _, body string, _ map[string]string) (string, error) {
	f.n++
	f.to, f.body = to, body
	return "msg_1", f.err
}

func controlEnv(t *testing.T, snd Sender) *env {
	e := newEnv(t)
	e.now = t0.Add(time.Hour)
	e.frame("session.work_state_changed", "m1", 55*time.Minute, `{"to":"processing","provider":"claude","agent_urn":"msg://agent/x/builder"}`)
	e.frame("session.work_state_changed", "noaddr", 55*time.Minute, `{"to":"processing","provider":"claude"}`)
	e.srv.Sender = snd
	return e
}

func TestNudgeDeliversOnceAndIsIdempotent(t *testing.T) {
	f := &fakeSender{}
	e := controlEnv(t, f)
	body := `{"request_id":"r1","text":"Run the tests"}`
	w := e.send("POST", "/sessions/m1/nudge", body)
	c := decode[control](t, w)
	if w.Code != 202 || c.State != "delivered" || c.MessageID == nil || *c.MessageID != "msg_1" || f.to != "msg://agent/x/builder" || f.body != "Run the tests" {
		t.Fatalf("%d %+v sent to %q", w.Code, c, f.to)
	}
	if w2 := e.send("POST", "/sessions/m1/nudge", body); w2.Code != 202 || f.n != 1 {
		t.Fatalf("a retried request_id sent again: %d, %d sends", w2.Code, f.n)
	}
	if g := decode[control](t, e.get("/controls/r1")); g.State != "delivered" {
		t.Fatalf("%+v", g)
	}
}

func TestNudgeFailureAndRefusals(t *testing.T) {
	f := &fakeSender{err: errors.New("boom secret-ish")}
	e := controlEnv(t, f)
	w := e.send("POST", "/sessions/m1/nudge", `{"request_id":"r2","text":"hi"}`)
	c := decode[control](t, w)
	if c.State != "failed" || c.Error == nil || strings.Contains(*c.Error, "boom") {
		t.Fatalf("a failed send must say failed without Tether's text: %+v", c)
	}
	cases := map[string]struct {
		path, body string
		code       int
		want       string
	}{
		"interrupt is never downgraded": {"/sessions/m1/nudge", `{"request_id":"r3","text":"hi","interrupt":true}`, 409, "interrupt_unsupported"},
		"no address":                    {"/sessions/noaddr/nudge", `{"request_id":"r4","text":"hi"}`, 409, "nudge_unsupported"},
		"empty text":                    {"/sessions/m1/nudge", `{"request_id":"r5","text":" "}`, 400, "invalid"},
		"restart":                       {"/sessions/m1/restart", `{"request_id":"r6","expected_session_id":"m1"}`, 409, "restart_unsupported"},
		"stale restart":                 {"/sessions/m1/restart", `{"request_id":"r7","expected_session_id":"other"}`, 409, "stale_session"},
		"unknown session":               {"/sessions/nope/nudge", `{"request_id":"r8","text":"hi"}`, 404, "not_found"},
	}
	for name, c := range cases {
		w := e.send("POST", c.path, c.body)
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if e.get("/controls/nope").Code != 404 || e.send("POST", "/controls/r2/skip", "").Code != 409 {
		t.Error("control lookups")
	}
}

func TestCapabilitiesFollowTheSender(t *testing.T) {
	e := controlEnv(t, nil)
	if s := decode[Session](t, e.get("/sessions/m1")); s.Capabilities.Nudge || s.Capabilities.Restart || s.Capabilities.Interrupt.Available {
		t.Fatalf("no sender: %+v", s.Capabilities)
	}
	e.srv.Sender = &fakeSender{}
	s := decode[Session](t, e.get("/sessions/m1"))
	if !s.Capabilities.Nudge || s.Capabilities.Interrupt.Available || s.Capabilities.Interrupt.Reason == nil || s.Capabilities.Restart {
		t.Fatalf("with a sender: %+v", s.Capabilities)
	}
}

type fakeCtl struct {
	worker                       string // the session's worker; "" is a hub session
	stopped, resumed, relaunched string
	order                        []string
	stopErr, launchErr           error
	resumeErr, relaunchErr       error
	dropped                      []string
	createdID                    string        // a relaunch that created this session but failed to start it
	holdStop                     chan struct{} // Stop waits until it is closed
	hangRelaunch                 bool          // Relaunch waits for the restart's deadline
	interrupts                   int
	intErr                       error
}

func (f *fakeCtl) Placement(_ context.Context, id string) (Placement, error) {
	if f.launchErr != nil {
		return Placement{}, f.launchErr
	}
	return Placement{Launch: "launch-" + id, Worker: f.worker}, nil
}
func (f *fakeCtl) Stop(_ context.Context, id string) error {
	if f.holdStop != nil {
		<-f.holdStop
	}
	f.stopped = id
	f.order = append(f.order, "stop")
	return f.stopErr
}
func (f *fakeCtl) Resume(_ context.Context, la, _ string) (string, error) {
	f.resumed = la
	f.order = append(f.order, "resume")
	if f.resumeErr != nil {
		return "", f.resumeErr
	}
	return "s2", nil
}
func (f *fakeCtl) Relaunch(ctx context.Context, launch, _ string) (string, error) {
	f.relaunched = launch
	f.order = append(f.order, "relaunch")
	if f.hangRelaunch {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.relaunchErr != nil {
		return f.createdID, f.relaunchErr
	}
	return "s3", nil
}
func (f *fakeCtl) ScopesDropped(_ context.Context, id string) ([]string, error) {
	if id == "s3" {
		return f.dropped, nil
	}
	return nil, nil
}
func (f *fakeCtl) Interrupt(context.Context, string, string) (string, error) {
	f.interrupts++
	return "cancelled", f.intErr
}

func TestNudgeWithInterruptUsesTheInterrupter(t *testing.T) {
	f := &fakeCtl{}
	e := controlEnv(t, &fakeSender{})
	e.srv.Interrupter = f
	c := decode[control](t, e.send("POST", "/sessions/m1/nudge", `{"request_id":"i1","text":"stop that","interrupt":true}`))
	if c.State != "interrupted_then_delivered" || c.Interrupt == nil || *c.Interrupt != "cancelled" || f.interrupts != 1 {
		t.Fatalf("%+v interrupts=%d", c, f.interrupts)
	}
	f.intErr = chatbridge.ErrNothingToInterrupt
	if w := e.send("POST", "/sessions/m1/nudge", `{"request_id":"i2","text":"again","interrupt":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), "interrupt_unsupported") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A hub-placed session (no worker) is still resumed.
func TestRestartStopsThenResumesAHubSession(t *testing.T) {
	f := &fakeCtl{}
	e := controlEnv(t, nil)
	e.frame("session.work_state_changed", "m1", 56*time.Minute, `{"to":"processing","provider":"claude","agent_urn":"msg://agent/x/builder","logical_agent_id":"la1"}`)
	e.srv.Restarter = f
	w := e.send("POST", "/sessions/m1/restart", `{"request_id":"rs1","expected_session_id":"m1"}`)
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var c control
	for i := 0; i < 100; i++ {
		c = decode[control](t, e.get("/controls/rs1"))
		if c.State == "done" || c.State == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c.State != "done" || c.NewSessionID == nil || *c.NewSessionID != "s2" || f.stopped != "m1" || f.resumed != "la1" || f.relaunched != "" ||
		c.RelaunchedBy == nil || *c.RelaunchedBy != "resume" || c.ScopesDropped != nil {
		t.Fatalf("%+v stopped=%q resumed=%q", c, f.stopped, f.resumed)
	}
}

// restartEnv is a session m1 of seat la1 whose restarts go to f.
func restartEnv(t *testing.T, f *fakeCtl) *env {
	t.Helper()
	e := controlEnv(t, nil)
	e.frame("session.work_state_changed", "m1", 56*time.Minute, `{"to":"processing","provider":"claude","agent_urn":"msg://agent/x/builder","logical_agent_id":"la1"}`)
	e.srv.Restarter = f
	return e
}

// untilEnd waits for restart id to end.
func untilEnd(t *testing.T, e *env, id string) control {
	t.Helper()
	for i := 0; i < 150; i++ {
		c := decode[control](t, e.get("/controls/"+id))
		if c.State == "done" || c.State == "failed" {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("restart did not end")
	return control{}
}

// restartUntilEnd starts a restart of m1 and waits for it to end.
func restartUntilEnd(t *testing.T, f *fakeCtl, id string) control {
	t.Helper()
	e := restartEnv(t, f)
	if w := e.send("POST", "/sessions/m1/restart", `{"request_id":"`+id+`","expected_session_id":"m1"}`); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return untilEnd(t, e, id)
}

// A restart whose Tether calls run out of time still records where it stopped: the status is written outside the
// deadline, so it never stays at "resuming" with the seat down.
func TestRestartPastItsDeadlineStillRecordsTheFailure(t *testing.T) {
	old := restartTimeout
	restartTimeout = 150 * time.Millisecond
	defer func() { restartTimeout = old }()
	c := restartUntilEnd(t, &fakeCtl{worker: "w1", hangRelaunch: true}, "rd1")
	if c.State != "failed" || c.Step == nil || *c.Step != "resuming" || c.Error == nil || !strings.Contains(*c.Error, "no new one started") {
		t.Fatalf("%+v", c)
	}
}

// The status context survives the deadline of the Tether calls (deterministic; the test above exercises it end to
// end, where a cancelled write only loses a race).
func TestRestartStatusContextOutlivesTheCallDeadline(t *testing.T) {
	call, persist, cancel := restartContexts(time.Millisecond)
	defer cancel()
	<-call.Done()
	if call.Err() == nil || persist.Err() != nil {
		t.Fatalf("call %v, persist %v", call.Err(), persist.Err())
	}
}

// Two tabs restarting the same seat: the second is refused while the first is in flight, and allowed after it ends.
func TestRestartRefusesASecondRestartOfTheSameSeat(t *testing.T) {
	f := &fakeCtl{worker: "w1", holdStop: make(chan struct{})}
	e := restartEnv(t, f)
	if w := e.send("POST", "/sessions/m1/restart", `{"request_id":"rg1","expected_session_id":"m1"}`); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w := e.send("POST", "/sessions/m1/restart", `{"request_id":"rg2","expected_session_id":"m1"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "restart_in_progress") {
		t.Fatalf("second tab: %d %s", w.Code, w.Body)
	}
	if w := e.send("POST", "/sessions/m1/restart", `{"request_id":"rg1","expected_session_id":"m1"}`); w.Code != 202 {
		t.Fatalf("a retry of the same request is still answered: %d %s", w.Code, w.Body)
	}
	close(f.holdStop)
	if c := untilEnd(t, e, "rg1"); c.State != "done" {
		t.Fatalf("%+v", c)
	}
	if w := e.send("POST", "/sessions/m1/restart", `{"request_id":"rg3","expected_session_id":"m1"}`); w.Code != 202 {
		t.Fatalf("after the first ended: %d %s", w.Code, w.Body)
	}
	untilEnd(t, e, "rg3")
}

// A relaunch whose session was created but could not be started names that session, so a retry launches it.
func TestRestartNamesASessionCreatedButNotStarted(t *testing.T) {
	f := &fakeCtl{worker: "w1", createdID: "s9", relaunchErr: errors.New("tether /sessions/s9/launch answered 502 (worker_unavailable)")}
	c := restartUntilEnd(t, f, "rl1")
	if c.State != "failed" || c.Step == nil || *c.Step != "launching" || c.NewSessionID == nil || *c.NewSessionID != "s9" ||
		c.Error == nil || !strings.Contains(*c.Error, "s9 was created but not started") || !strings.Contains(*c.Error, "502 (worker_unavailable)") {
		t.Fatalf("%+v", c)
	}
}

// A worker-placed seat (every catalog seat) is relaunched, never resumed: stop first, then the relaunch right after,
// so Tether can hand it the stopped session's grant. Scopes it still dropped are reported.
func TestRestartRelaunchesAWorkerSeatWithoutResuming(t *testing.T) {
	f := &fakeCtl{worker: "w1", dropped: []string{"tether.admin"}}
	c := restartUntilEnd(t, f, "rs2")
	if c.State != "done" || f.resumed != "" || strings.Join(f.order, ",") != "stop,relaunch" || f.relaunched != "launch-m1" ||
		c.NewSessionID == nil || *c.NewSessionID != "s3" || c.RelaunchedBy == nil || *c.RelaunchedBy != "relaunch" || fmt.Sprint(c.ScopesDropped) != "[tether.admin]" {
		t.Fatalf("%+v order=%v", c, f.order)
	}
}

// A hub session whose resume Tether refuses (409) is relaunched rather than left stopped.
func TestRestartRelaunchesAHubSessionWhenTetherRefusesTheResume(t *testing.T) {
	f := &fakeCtl{resumeErr: fmt.Errorf("%w: tether /logical-agents/la1/resume answered 409 (conflict)", ErrResumeRefused)}
	c := restartUntilEnd(t, f, "rs6")
	if c.State != "done" || strings.Join(f.order, ",") != "stop,resume,relaunch" || c.RelaunchedBy == nil || *c.RelaunchedBy != "relaunch" || *c.NewSessionID != "s3" {
		t.Fatalf("%+v order=%v", c, f.order)
	}
}

// Never stop what cannot be brought back: without the launch a relaunch needs, nothing is stopped.
func TestRestartStopsNothingWhenTheLaunchCannotBeRead(t *testing.T) {
	f := &fakeCtl{launchErr: errors.New("tether /sessions/m1 answered 404 (not_found)")}
	c := restartUntilEnd(t, f, "rs3")
	if c.State != "failed" || c.Step == nil || *c.Step != "preparing" || f.stopped != "" || c.Error == nil ||
		!strings.Contains(*c.Error, "Nothing was stopped") || !strings.Contains(*c.Error, "404 (not_found)") {
		t.Fatalf("%+v stopped=%q", c, f.stopped)
	}
}

// A relaunch that fails after the stop says plainly that the seat is down and how to bring it back.
func TestRestartThatCannotRelaunchSaysTheSeatIsDown(t *testing.T) {
	f := &fakeCtl{worker: "w1", relaunchErr: errors.New("tether /sessions answered 403 (forbidden)")}
	c := restartUntilEnd(t, f, "rs4")
	if c.State != "failed" || c.Step == nil || *c.Step != "resuming" || c.Error == nil || !strings.Contains(*c.Error, "no new one started") ||
		!strings.Contains(*c.Error, "launch-m1") || !strings.Contains(*c.Error, "403 (forbidden)") || c.RelaunchedBy != nil {
		t.Fatalf("%+v", c)
	}
	// A hub resume that fails for any other reason is not retried as a relaunch.
	f = &fakeCtl{resumeErr: errors.New("tether /logical-agents/la1/resume answered 500 (internal_error)")}
	if c = restartUntilEnd(t, f, "rs5"); c.State != "failed" || f.relaunched != "" {
		t.Fatalf("%+v relaunched=%q", c, f.relaunched)
	}
}
