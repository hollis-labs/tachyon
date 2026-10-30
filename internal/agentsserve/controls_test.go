package agentsserve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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
