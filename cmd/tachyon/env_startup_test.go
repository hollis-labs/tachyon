package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/hitl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The authored host binary consumes env configuration before listening. Its
// only plugin is the existing isolated ask fixture; Tangent is a fake MCP server.
func TestTangentEnvironmentThroughRealHost(t *testing.T) {
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "tachyon")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build host: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name, endpoint, base, query string
		enabled, linked, invalid    bool
	}{
		{name: "blank_mcp_and_item_env", endpoint: " \t\n", base: " \t\n"},
		{name: "padded_mcp_query_and_item_env", endpoint: " \t%s/mcp?fixture=1\n", base: " \t%s/approved\n", query: "fixture=1", enabled: true, linked: true},
		{name: "blank_item_env", endpoint: " \t%s/mcp\n", base: " \t\n", enabled: true},
		{name: "blank_mcp_valid_item_env", endpoint: " \t\n", base: " \t%s/approved\n"},
		{name: "mcp_credentials_still_refused", endpoint: "credentials", invalid: true},
		{name: "mcp_fragment_still_refused", endpoint: "%s/mcp#private", invalid: true},
		{name: "item_query_still_refused", endpoint: "%s/mcp", base: "%s/approved?private=1", invalid: true},
		{name: "item_credentials_still_refused", endpoint: "%s/mcp", base: "credentials", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			fake := mcp.NewServer(&mcp.Implementation{Name: "fake-tangent", Version: "1"}, nil)
			fake.AddTool(&mcp.Tool{Name: "tangent.hitl_enqueue", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return &mcp.CallToolResult{StructuredContent: hitl.EnqueueHandle{ContractVersion: "1.0", ItemID: "fixture-item", ItemURL: "/hitl/items/fixture-item", State: "submitted", Revision: 1}}, nil
			})
			stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fake }, &mcp.StreamableHTTPOptions{Stateless: true})
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != tc.query {
					t.Errorf("MCP query changed: %q", r.URL.RawQuery)
				}
				stream.ServeHTTP(w, r)
			}))
			defer provider.Close()
			value := func(template string) string {
				if template == "credentials" {
					return strings.Replace(provider.URL, "http://", "http://fixture:private@", 1)
				}
				return strings.ReplaceAll(template, "%s", provider.URL)
			}
			home := t.TempDir()
			children := filepath.Join(home, "plugins")
			dir := fixturePlugin(t, children, "asker", `{"modules":["work"],"verbs":{"work_write":{"effect":"writes"}}}`, true)
			if err := os.WriteFile(filepath.Join(dir, "verb-result"), []byte(`{"status":"ask","ask":{"prompt":"Proceed?","options":["yes"]}}`), 0600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			t.Cleanup(cancel)
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = home
			cmd.Env = append(os.Environ(), "HOME="+home, "TACHYON_DATA_DIR="+filepath.Join(home, "data"), "TACHYON_ADDR="+addr, "TACHYON_TANGENT_MCP_URL="+value(tc.endpoint), "TACHYON_TANGENT_ITEM_BASE="+value(tc.base))
			logPath := filepath.Join(home, "host.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { log.Close() })
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			t.Cleanup(func() {
				if finished {
					return
				}
				cmd.Process.Signal(syscall.SIGTERM)
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("host shutdown: %v", err)
					}
				case <-time.After(5 * time.Second):
					cmd.Process.Kill()
					<-done
					t.Error("host did not stop")
				}
			})
			if tc.invalid {
				err := <-done
				finished = true
				var exit *exec.ExitError
				if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("invalid env startup exit: %v", err)
				}
				output, err := os.ReadFile(logPath)
				if err != nil || !strings.Contains(string(output), "invalid HITL configuration") || strings.Contains(string(output), "private") {
					t.Fatalf("missing or unsafe refusal: %s %v", output, err)
				}
				conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
				if err == nil {
					conn.Close()
					t.Fatal("invalid env left a listener")
				}
				if calls.Load() != 0 {
					t.Fatal("invalid startup contacted provider")
				}
				return
			}
			client := &http.Client{Timeout: 2 * time.Second}
			ready := false
			for !ready {
				select {
				case err := <-done:
					finished = true
					output, _ := os.ReadFile(logPath)
					t.Fatalf("host exited before listening: %v\n%s", err, output)
				case <-ctx.Done():
					t.Fatal("host did not listen")
				default:
				}
				resp, err := client.Get("http://" + addr + "/api/health")
				if err == nil {
					resp.Body.Close()
					ready = resp.StatusCode == 200
				}
				if !ready {
					time.Sleep(10 * time.Millisecond)
				}
			}
			resp, err := client.Post("http://"+addr+"/api/verb/work_write", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var env contract.ResultEnvelope
			if err := json.Unmarshal(body, &env); err != nil || resp.StatusCode != 200 || env.Status != contract.StatusAsk || env.Ask == nil {
				t.Fatalf("ask: %s %v", body, err)
			}
			if tc.enabled {
				if calls.Load() == 0 || env.Ask.OperationID == "" || env.Ask.ItemID != "fixture-item" || env.Ask.Unavailable != "" {
					t.Fatalf("trimmed env did not reach fake MCP: %s", body)
				}
				want := ""
				if tc.linked {
					want = provider.URL + "/hitl/items/fixture-item"
				}
				if env.Ask.ItemURL != want {
					t.Fatalf("item-base result: %q", env.Ask.ItemURL)
				}
			} else if calls.Load() != 0 || env.Ask.OperationID != "" {
				t.Fatal("blank MCP env did not disable the bridge")
			}
		})
	}
}
