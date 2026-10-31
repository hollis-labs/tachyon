package agentsserve

import (
	"context"
	"errors"
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
	stopped, resumed string
	stopErr          error
	interrupts       int
	intErr           error
}

func (f *fakeCtl) Stop(_ context.Context, id string) error { f.stopped = id; return f.stopErr }
func (f *fakeCtl) Resume(_ context.Context, la, _ string) (string, error) {
	f.resumed = la
	return "s2", nil
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

func TestRestartStopsThenResumesTheLogicalAgent(t *testing.T) {
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
	if c.State != "done" || c.NewSessionID == nil || *c.NewSessionID != "s2" || f.stopped != "m1" || f.resumed != "la1" {
		t.Fatalf("%+v stopped=%q resumed=%q", c, f.stopped, f.resumed)
	}
}
