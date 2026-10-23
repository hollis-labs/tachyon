package actionrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func terminalRig(t *testing.T) (*rig, *httptest.Server, Handle) {
	t.Helper()
	r := newRig(t, func(s *fakeSession) {
		s.out <- []byte("Digest prefix: ")
		in := <-s.in
		s.out <- in
		s.out <- []byte("\r\ndone\r\n")
		s.exit = 0
		close(s.out)
	})
	srv := httptest.NewServer(&Handler{Service: r.svc})
	t.Cleanup(srv.Close)
	h, err := r.svc.Request(context.Background(), RequestInput{Machine: "hub", Action: "tether.catalog.approve", Params: map[string]string{"plan_id": "plan-1"}, Reason: "go"})
	if err != nil {
		t.Fatal(err)
	}
	return r, srv, h
}

func dial(t *testing.T, srv *httptest.Server, path string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if hdr == nil {
		hdr = http.Header{}
	}
	if hdr.Get("Origin") == "" {
		hdr.Set("Origin", srv.URL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: hdr})
}

func TestTerminalAttachRunsInteractiveActionAfterApproval(t *testing.T) {
	r, srv, h := terminalRig(t)
	path := Prefix + "/" + h.RunID + "/terminal"

	// Before approval the socket refuses to start anything.
	c, _, err := dial(t, srv, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, data, _ := c.Read(context.Background())
	if !strings.Contains(string(data), `"pending_approval"`) || r.tr.count() != 0 {
		t.Fatalf("%s ran=%d", data, r.tr.count())
	}
	c.CloseNow()

	r.decide(t, h.ItemID, "approved")
	c, _, err = dial(t, srv, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":90,"rows":25}`)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var exit serverEvent
	typed := false
	for exit.Type != "exit" {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v out=%q", err, out.String())
		}
		if typ == websocket.MessageBinary {
			out.Write(data)
			if strings.Contains(out.String(), "Digest prefix:") && !typed {
				typed = true
				_ = c.Write(ctx, websocket.MessageBinary, []byte("abcdef012345\r"))
			}
			continue
		}
		var ev serverEvent
		_ = json.Unmarshal(data, &ev)
		if ev.Type == "exit" {
			exit = ev
		}
	}
	if exit.State != StateSucceeded || exit.Code == nil || *exit.Code != 0 || !strings.Contains(out.String(), "done") {
		t.Fatalf("%+v %q", exit, out.String())
	}
	// Reattach after the run ended replays the recorded output and the exit.
	c2, _, err := dial(t, srv, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	_, data, _ = c2.Read(ctx)
	if !strings.Contains(string(data), "done") {
		t.Fatalf("replay %q", data)
	}
	run, _ := r.svc.Store.Get(context.Background(), h.RunID)
	if strings.Contains(run.Output, "abcdef012345") {
		t.Fatalf("input stored: %q", run.Output)
	}
}

func TestTerminalRejectsBearerForeignOriginAndUnknownRun(t *testing.T) {
	_, srv, h := terminalRig(t)
	path := Prefix + "/" + h.RunID + "/terminal"
	if _, resp, err := dial(t, srv, path, http.Header{"Authorization": {"Bearer x"}}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("bearer must be refused: %v %v", err, resp)
	}
	if _, resp, err := dial(t, srv, path, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("foreign origin must be refused: %v %v", err, resp)
	}
	if _, resp, err := dial(t, srv, Prefix+"/run_nope/terminal", nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("unknown run: %v %v", err, resp)
	}
	req, _ := http.NewRequest("GET", srv.URL+Prefix+"/"+h.RunID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", err, resp)
	}
	var run Run
	_ = json.NewDecoder(resp.Body).Decode(&run)
	if run.ID != h.RunID || run.OpDigest != h.OpDigest {
		t.Fatalf("%+v", run)
	}
}
