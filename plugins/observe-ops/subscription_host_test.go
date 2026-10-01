package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

// Exercise the actual host mux and plugin wire, never a copy of the route.
func TestPollingDescriptorRealHost(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	pluginDir := filepath.Join(fixture, "plugins", "observe-ops")
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, build := range []struct{ out, pkg string }{
		{filepath.Join(fixture, "host"), "./cmd/tachyon"},
		{filepath.Join(pluginDir, "observe-ops"), "./plugins/observe-ops"},
	} {
		cmd := exec.Command("go", "build", "-o", build.out, build.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, "plugins/observe-ops/plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	workerDir := filepath.Join(fixture, "plugins", "fixture-work")
	if err := os.MkdirAll(workerDir, 0700); err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shellQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	workerScript := "#!/bin/sh\nexec env TACHYON_OBSERVE_FAKE_WORKER=1 " + shellQuote(testBinary) + " -test.run=^TestHostFeedFakeWorker$\n"
	if err := os.WriteFile(filepath.Join(workerDir, "fixture-work"), []byte(workerScript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workerDir, "plugin.yaml"), []byte("id: fixture-work"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("provider method: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/api/sessions":
			io.WriteString(w, `[{"id":"fixture","status":"active","updated_at":"2026-01-01T00:00:00Z"}]`)
		case "/api/v1/scheduler/status":
			io.WriteString(w, `{"enabled":true}`)
		default:
			io.WriteString(w, `{"status":"ok"}`)
		}
	}))
	defer provider.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	cmd := exec.Command(filepath.Join(fixture, "host"))
	cmd.Dir = fixture
	cmd.Env = append(os.Environ(), "HOME="+fixture, "TACHYON_ADDR="+addr, "TACHYON_DATA_DIR="+filepath.Join(fixture, "data"),
		"TACHYON_TANGENT_MCP_URL=",
		"TACHYON_OBSERVE_NANITE_URL="+provider.URL, "TACHYON_OBSERVE_TORQUE_URL="+provider.URL, "TACHYON_OBSERVE_TETHER_URL="+provider.URL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + addr
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		resp, err := client.Get(base + "/api/verbs")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if bytes.Contains(body, []byte("observe_subscribe")) {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("host never registered Observe")
	}
	invoke := func(endpoint, method string, payload []byte) contract.ResultEnvelope {
		t.Helper()
		req, err := http.NewRequest(method, base+endpoint, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var env contract.ResultEnvelope
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s: %d", method, endpoint, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	for _, channel := range []string{"activity", "logs", "events"} {
		filter := `{"source":"nanite","kind":"session_snapshot","limit":1}`
		if channel == "logs" {
			filter = `{"source":"observe-ops","level":"info","search":"initialized","limit":1}`
		}
		env := invoke("/api/verb/observe_subscribe", "POST", mustMarshal(t, SubscribeRequest{Channel: channel, Filter: filter}))
		if env.Status != contract.StatusOK {
			t.Fatalf("subscribe: %+v", env)
		}
		var d SubscriptionHandle
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatal(err)
		}
		if d.Method != "POST" || d.Endpoint != "/api/verb/observe_"+channel || d.Transport != "polling" || !d.Supported || d.Mode != "snapshot" || d.CursorSupported || d.Cursor != nil || d.DurableReplay || d.PollIntervalMS < 2000 {
			t.Fatalf("descriptor: %+v", d)
		}
		env = invoke(d.Endpoint, d.Method, d.Payload)
		if env.Status != contract.StatusOK {
			t.Fatalf("poll %s: %+v", channel, env)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(env.Data, &rows); err != nil || len(rows) != 1 || len(rows) > d.MaxLimit {
			t.Fatalf("poll data: %s %v", env.Data, err)
		}
		resp, err := client.Get(base + d.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET: %d", resp.StatusCode)
		}
	}
	for _, tc := range []struct{ channel, code string }{{"metrics", "unsupported"}, {"no-such-channel", "validation"}} {
		env := invoke("/api/verb/observe_subscribe", "POST", mustMarshal(t, SubscribeRequest{Channel: tc.channel}))
		if env.Status != contract.StatusError || env.Error == nil || env.Error.Code != tc.code {
			t.Fatalf("%s: %+v", tc.channel, env)
		}
	}
	// Exercise real host -> private SDK command -> actual Observe reads.
	for _, verb := range []string{"work_read", "work_write", "work_ask", "work_error"} {
		env := invoke("/api/verb/"+verb, "POST", []byte(`{"payload":"PAYLOAD-SECRET","config":"CONFIG-SECRET","credential":"CREDENTIAL-SECRET"}`))
		if verb == "work_ask" && env.Status != "ask" || verb == "work_error" && env.Status != "error" || (verb == "work_read" || verb == "work_write") && env.Status != "ok" {
			t.Fatal("operation semantics changed", verb, env)
		}
	}
	readEvents := func() []Event {
		env := invoke("/api/verb/observe_events", "POST", []byte(`{"source":"fixture-work","limit":200}`))
		var events []Event
		if err := json.Unmarshal(env.Data, &events); err != nil {
			t.Fatal(err)
		}
		return events
	}
	var events []Event
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		events = readEvents()
		if len(events) == 9 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 9 {
		t.Fatal("cross-plugin feed missing", events)
	}
	statuses := map[string]string{}
	for _, event := range events {
		if event.Kind == "operation_result" {
			statuses[event.Payload["verb"].(string)] = event.Payload["status"].(string)
		}
	}
	if statuses["work_read"] != "ok" || statuses["work_write"] != "ok" || statuses["work_ask"] != "ask" || statuses["work_error"] != "error" {
		t.Fatal(statuses)
	}
	for _, verb := range []string{"observe_activity", "observe_events", "observe_logs", "observe_metrics", "observe_status"} {
		env := invoke("/api/verb/"+verb, "POST", []byte(`{}`))
		for _, secret := range []string{"PAYLOAD-SECRET", "CONFIG-SECRET", "CREDENTIAL-SECRET", "OUTPUT-SECRET", "PROMPT-SECRET", "UPSTREAM-SECRET", observefeed.TokenConfig} {
			if bytes.Contains(env.Data, []byte(secret)) {
				t.Fatal("secret leaked into Observe", verb, secret)
			}
		}
	}
	for _, path := range []string{"/api/verb/" + observefeed.Command, "/api/observe/commands"} {
		resp, err := client.Post(base+path, "application/json", strings.NewReader(`{"token":"forged","records":[{"payload":"FORGED-SECRET"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("HTTP ingestion exposed", path, resp.StatusCode, string(body))
		}
		if strings.Contains(path, "/verb/") && !bytes.Contains(body, []byte("unknown_verb")) {
			t.Fatal("wrong refusal", string(body))
		}
	}
	if len(readEvents()) != 9 {
		t.Fatal("HTTP forged events or recursive Observe calls")
	}
	for _, path := range []string{"/api/verbs", "/api/plugins/registry"} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("listing unavailable", path, resp.StatusCode)
		}
		if bytes.Contains(body, []byte(observefeed.Command)) || bytes.Contains(body, []byte(observefeed.TokenConfig)) {
			t.Fatal("private carrier advertised", string(body))
		}
	}
	resp, err := client.Post(base+"/api/plugins/fixture-work/restart", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("fixture restart", resp.StatusCode)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		events = readEvents()
		if len(events) == 13 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 13 {
		t.Fatal("restart lifecycle missing", events)
	}
	want := []string{"plugin_restart:ok", "plugin_load:ok", "plugin_unload:ok", "plugin_restart:started"}
	for n := range want {
		if events[n].Kind+":"+events[n].Payload["status"].(string) != want[n] {
			t.Fatal("restart event order", events)
		}
	}
	if events[0].Payload["generation"] == events[3].Payload["generation"] {
		t.Fatal("restart generation unchanged")
	}
}
