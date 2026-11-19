package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
)

// fakeTerminalSSH stands in for ssh on PATH: it records its process group, then runs the command after "--"
// (an approved run) or, for an interactive pane, says it is ready and echoes its input back like a remote
// shell.
const fakeTerminalSSH = `#!/bin/sh
echo $$ >> "$PIDS"
while [ "$1" != "--" ] && [ $# -gt 0 ]; do shift; done
if [ "$1" = "--" ]; then shift; exec sh -c "$1"; fi
echo fake-ssh-ready
exec cat
`

// terminalHost is a served binary with the terminal on, a fake ssh first on PATH and an empty HOME (no
// ~/.ssh/config), and a policy file listing one machine. It returns the base URL, the app and the file
// the fake ssh writes its pids to.
func terminalHost(t *testing.T, on bool) (string, *identity.Service, string) {
	t.Helper()
	return terminalHostIn(t, t.TempDir(), on)
}

// terminalHostIn is terminalHost with the fake ssh, HOME and policy in dir.
func terminalHostIn(t *testing.T, dir string, on bool) (string, *identity.Service, string) {
	t.Helper()
	pids := filepath.Join(dir, "pids")
	script := strings.ReplaceAll(fakeTerminalSSH, `"$PIDS"`, strconv.Quote(pids))
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o700); err != nil { //nolint:gosec // the fake ssh must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", dir)
	policy := filepath.Join(dir, "terminal-policy.json")
	if err := os.WriteFile(policy, []byte(`{"machines":[{"id":"hub","label":"Hub","host":"hub-host"}],"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base, app := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  files: { terminal_policy: %q }\n  features: { terminal: %v }\n", policy, on))
	return base, identity.From(app), pids
}

// sessionCookie is the owner client's session cookie for base.
func sessionCookie(t *testing.T, c *client) *http.Cookie {
	t.Helper()
	u, _ := url.Parse(c.base)
	for _, k := range c.http.Jar.Cookies(u) {
		if strings.HasSuffix(k.Name, "_session") {
			return k
		}
	}
	t.Fatal("no session cookie")
	return nil
}

// dialTerminal opens the session's WebSocket with the owner's cookie and the given extra headers.
func dialTerminal(ctx context.Context, base, wsPath string, cookie *http.Cookie, headers map[string]string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	h.Set("Cookie", cookie.Name+"="+cookie.Value)
	for k, v := range headers {
		if v != "" {
			h.Set(k, v)
		}
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+wsPath, &websocket.DialOptions{HTTPHeader: h})
}

// Off, the terminal is the old build with no terminal service: nothing is mounted and nothing runs.
func TestTerminalOff(t *testing.T) {
	base, _, pids := terminalHost(t, false)
	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	owner.want(404, "GET", chatapi.TerminalPrefix+"/machines", nil, xrw)
	owner.want(404, "POST", chatapi.TerminalPrefix+"/sessions", map[string]string{"machine_id": "hub"}, xrw)
	if _, err := os.Stat(pids); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ssh ran with the terminal off: %v", err)
	}
}

// The guards in the served binary. Anonymous is 401; a key holding the scope, and a person's token without
// the operator scope, are refused by the operator check (403 operator_required); a token the owner's session
// mints for itself, with the scope and the operator scope, passes the scope and operator checks and is
// refused only by the bearer check (403 requester_cannot_approve); the owner's browser session is served. The WebSocket upgrade is same-origin only: a foreign Origin, no Origin, or a
// cross-site fetch is 403 origin_denied, and the browser's own origin gets a live pane.
func TestTerminalGuardsAndSession(t *testing.T) {
	base, ids, _ := terminalHost(t, true)
	machines := chatapi.TerminalPrefix + "/machines"
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	anon.want(401, "GET", machines, nil, xrw)
	k, _, kerr := ids.CreateKey(context.Background(), identity.KeyInput{Name: "terminal-test", Scopes: []string{"tachyon.terminal.control", "tachyon.operator"}})
	if kerr != nil {
		t.Fatal(kerr)
	}
	if code := flatCode(t, keys.want(403, "GET", machines, nil, map[string]string{"Authorization": "Bearer " + k})); code != "operator_required" {
		t.Errorf("a key with the scope: %s", code)
	}
	// A signed-in person's token without the operator scope fails the operator check first.
	var plain struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(owner.want(200, "POST", "/api/token", map[string]any{"scopes": []string{"tachyon.terminal.control"}}, xrw), &plain); err != nil || plain.Token == "" {
		t.Fatalf("mint a token: %v", err)
	}
	if code := flatCode(t, keys.want(403, "GET", machines, nil, map[string]string{"Authorization": "Bearer " + plain.Token})); code != "operator_required" {
		t.Errorf("a person without the operator scope: %s", code)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(owner.want(200, "POST", "/api/token", map[string]any{"scopes": []string{"tachyon.terminal.control", "tachyon.operator"}}, xrw), &minted); err != nil || minted.Token == "" {
		t.Fatalf("mint a token: %v", err)
	}
	for _, r := range [][2]string{{"GET", machines}, {"POST", chatapi.TerminalPrefix + "/sessions"}, {"GET", chatapi.TerminalPrefix + "/runs"}} {
		if code := flatCode(t, keys.want(403, r[0], r[1], map[string]string{"machine_id": "hub"}, map[string]string{"Authorization": "Bearer " + minted.Token})); code != "requester_cannot_approve" {
			t.Errorf("%s %s with a minted token: %s", r[0], r[1], code)
		}
	}
	if body := owner.want(200, "GET", machines, nil, xrw); !strings.Contains(string(body), `"machine_id":"hub"`) {
		t.Errorf("machines: %s", body)
	}

	var row struct {
		ID     string `json:"id"`
		WSPath string `json:"ws_path"`
	}
	if err := json.Unmarshal(owner.want(201, "POST", chatapi.TerminalPrefix+"/sessions", map[string]string{"machine_id": "hub"}, xrw), &row); err != nil || row.WSPath == "" {
		t.Fatalf("open a session: %v %+v", err, row)
	}
	cookie := sessionCookie(t, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for name, h := range map[string]map[string]string{
		"a foreign origin":  {"Origin": "http://elsewhere.test"},
		"no origin":         {},
		"a cross-site page": {"Origin": base, "Sec-Fetch-Site": "cross-site"},
	} {
		c, res, err := dialTerminal(ctx, base, row.WSPath, cookie, h)
		if err == nil {
			_ = c.CloseNow()
			t.Errorf("%s: the upgrade was accepted", name)
			continue
		}
		if res == nil || res.StatusCode != 403 {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var body struct{ Code string }
		_ = json.NewDecoder(res.Body).Decode(&body)
		if body.Code != "origin_denied" {
			t.Errorf("%s: code %q", name, body.Code)
		}
	}

	c, _, err := dialTerminal(ctx, base, row.WSPath, cookie, map[string]string{"Origin": base})
	if err != nil {
		t.Fatalf("same-origin upgrade: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":90,"rows":20}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("ping-terminal\n")); err != nil {
		t.Fatal(err)
	}
	// The pty echoes the line itself; the fake's ready marker and cat's copy prove the process behind it.
	state, echoed := false, ""
	for !state || !strings.Contains(echoed, "fake-ssh-ready") || strings.Count(echoed, "ping-terminal") < 2 {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (state %v, got %q)", err, state, echoed)
		}
		if typ == websocket.MessageText && string(data) == `{"type":"state","state":"open"}` {
			state = true
		} else if typ == websocket.MessageBinary {
			echoed += string(data)
		}
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	owner.want(200, "DELETE", chatapi.TerminalPrefix+"/sessions/"+row.ID, nil, xrw)
	if _, res, err := dialTerminal(ctx, base, row.WSPath, cookie, map[string]string{"Origin": base}); err == nil || res == nil || res.StatusCode != 409 {
		t.Errorf("an ended session's upgrade: %v", err)
	}
}

// Shutdown leaves no pty child: a pane is live when the server stops, and afterwards the fake ssh's
// process group is gone.
func TestTerminalShutdownReapsPanes(t *testing.T) {
	var pids string
	dir := t.TempDir() // outlives the subtest, so the fake ssh's pid file is still there to read
	t.Run("serve", func(t *testing.T) {
		var base string
		base, _, pids = terminalHostIn(t, dir, true)
		owner := newClient(t, base)
		owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
		var row struct {
			WSPath string `json:"ws_path"`
		}
		if err := json.Unmarshal(owner.want(201, "POST", chatapi.TerminalPrefix+"/sessions", map[string]string{"machine_id": "hub"}, xrw), &row); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, _, err := dialTerminal(ctx, base, row.WSPath, sessionCookie(t, owner), map[string]string{"Origin": base})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.CloseNow() }()
		// The browser sends its size first, as the pane does.
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
			t.Fatal(err)
		}
		// Wait for the fake ssh itself, not only the pane's state.
		for out := ""; !strings.Contains(out, "fake-ssh-ready"); {
			typ, data, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if typ == websocket.MessageBinary {
				out += string(data)
			}
		}
		// The server stops when this subtest ends, with the pane still open.
	})
	if pids == "" {
		t.Skip("the served binary did not start (on Postgres the full-binary tests skip: conversion-notes gap 10)")
	}
	raw, err := os.ReadFile(pids) //nolint:gosec // this test's own pid file
	if err != nil {
		t.Fatalf("the fake ssh never ran: %v", err)
	}
	for _, f := range strings.Fields(string(raw)) {
		pid, _ := strconv.Atoi(f)
		gone := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
				gone = true
				break
			}
		}
		if !gone {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			t.Errorf("pty process group %d survived shutdown", pid)
		}
	}
}
