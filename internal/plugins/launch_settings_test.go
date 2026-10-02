package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
	_ "modernc.org/sqlite"
)

// Exercise the authored settings through the real host spawn path and real
// launch subprocess. The only provider is a fake Unix HTTP daemon; no model
// CLI or running Tether instance participates.
func TestLaunchSettingsReachTetherThroughHost(t *testing.T) {
	// Short names keep Unix socket paths within the platform limit while keeping
	// all scratch under the caller's TMPDIR (the shared box must not use /tmp).
	root, err := os.MkdirTemp(os.TempDir(), "ls-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	pluginDir := filepath.Join(root, "launch-ops")
	if err := os.Mkdir(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(pluginDir, "launch-ops")
	cmd := exec.Command("go", "build", "-p", "2", "-o", binary, "../../plugins/launch-ops")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build launch plugin: %v\n%s", err, output)
	}
	caps, err := os.ReadFile("../../plugins/launch-ops/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "capabilities.json"), caps, 0600); err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		name, socket, env string
		override          *string
		fails             bool
	}{
		{name: "client_default", socket: ".tether/run/tetherd.sock"},
		{name: "persisted_blank", socket: ".tether/run/tetherd.sock", override: stringPtr("")},
		{name: "environment", socket: "env.sock", env: "unix:~/env.sock"},
		{name: "explicit_override", socket: "override.sock", env: "unix:~/missing.sock", override: stringPtr("unix:~/override.sock")},
		{name: "stale_override_survives", socket: ".tether/run/tetherd.sock", env: "unix:~/.tether/run/tetherd.sock", override: stringPtr("unix:~/.tether/run/muxd.sock"), fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(root, string(rune('a'+i)))
			socketPath := filepath.Join(home, tc.socket)
			if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("TETHER_ADDR", tc.env)
			t.Setenv("TACHYON_DATA_DIR", filepath.Join(home, "data"))
			t.Setenv("TACHYON_LAUNCH_DATA_DIR", "")
			t.Setenv("TACHYON_LAUNCH_DEFAULT_PROVIDER", "")
			// Nanite is also fake so incorrect backend routing cannot contact it live.
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected Nanite request %s", r.URL.Path)
				http.Error(w, "fixture", 500)
			}))
			defer provider.Close()
			settings := map[string]string{"nanite_url": provider.URL}
			if tc.override != nil {
				settings["tether_addr"] = *tc.override
			}
			settingsDir := filepath.Join(home, "data", "config-ops")
			if err := os.MkdirAll(settingsDir, 0700); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(map[string]any{"launch-ops": settings})
			if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			state := "created"
			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "GET /catalog/launches":
					io.WriteString(w, `{"launches":[{"id":"catalog-launch","agent":"test-agent"}]}`)
				case "POST /sessions":
					var req struct {
						Launch string `json:"launch"`
						Key    string `json:"idempotency_key"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Launch != "catalog-launch" || req.Key != "tachyon:launch:seed-prepared" {
						t.Errorf("create: %+v %v", req, err)
					}
					state = "created"
					w.WriteHeader(201)
					io.WriteString(w, `{"id":"fixture-session"}`)
				case "GET /sessions/fixture-session":
					json.NewEncoder(w).Encode(map[string]string{"id": "fixture-session", "state": state})
				case "POST /sessions/fixture-session/launch":
					state = "running"
					io.WriteString(w, `{"id":"fixture-session"}`)
				case "POST /sessions/fixture-session/stop":
					state = "killed"
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected daemon request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})}
			go server.Serve(listener)
			defer server.Close()
			mgr := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := mgr.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			}()
			if err := mgr.LoadPlugin(context.Background(), binary); err != nil {
				t.Fatal(err)
			}
			// Seed independently so execute and active reads still run when prepare
			// cannot reach the configured socket.
			db, err := sql.Open("sqlite", filepath.Join(home, "data", "launch-ops", "launches.db"))
			if err != nil {
				t.Fatal(err)
			}
			for _, seed := range []struct{ id, state, sid string }{{"seed-prepared", "prepared", ""}, {"seed-running", "running", "fixture-session"}} {
				body, _ := json.Marshal(map[string]any{"id": seed.id, "backend": "tether", "agent_id": "test-agent", "state": seed.state, "session_id": seed.sid, "config": map[string]string{"launch_id": "catalog-launch"}, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()})
				if _, err := db.Exec("INSERT INTO launches (id,body) VALUES (?,?)", seed.id, body); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			for _, call := range []struct {
				verb, payload, state string
				requests             []string
			}{
				{"launch_prepare", `{"backend":"tether","agent_id":"test-agent"}`, "prepared", []string{"GET /catalog/launches"}},
				{"launch_execute", `{"launch_id":"seed-prepared"}`, "running", []string{"POST /sessions", "GET /sessions/fixture-session", "POST /sessions/fixture-session/launch", "GET /sessions/fixture-session"}},
				{"launch_read", `{"launch_id":"seed-running"}`, "running", []string{"GET /sessions/fixture-session"}},
				{"launch_status", `{"launch_id":"seed-running"}`, "running", []string{"GET /sessions/fixture-session"}},
				{"launch_cancel", `{"launch_id":"seed-running"}`, "cancelled", []string{"POST /sessions/fixture-session/stop"}},
			} {
				mu.Lock()
				requests = nil
				mu.Unlock()
				raw, err := mgr.InvokeVerb(context.Background(), call.verb, json.RawMessage(call.payload))
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Status string                        `json:"status"`
					Error  *contract.ErrorDetail         `json:"error"`
					Data   struct{ State, Error string } `json:"data"`
				}
				if err := json.Unmarshal(raw, &result); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got := append([]string(nil), requests...)
				mu.Unlock()
				if tc.fails {
					if len(got) != 0 {
						t.Fatalf("%s reached daemon despite stale override: %v", call.verb, got)
					}
					if call.verb == "launch_execute" {
						if result.Status != "ok" || result.Data.State != "executing" || result.Data.Error == "" {
							t.Fatalf("execute lost failure checkpoint: %s", raw)
						}
					} else if result.Status != "error" || result.Error == nil {
						t.Fatalf("%s hid failure: %s", call.verb, raw)
					}
				} else {
					if result.Status != "ok" || result.Data.State != call.state || (call.verb != "launch_cancel" && result.Data.Error != "") {
						t.Fatalf("%s: %s", call.verb, raw)
					}
					if !reflect.DeepEqual(got, call.requests) {
						t.Fatalf("%s daemon traffic: %v; want %v", call.verb, got, call.requests)
					}
				}
			}
		})
	}
}

func stringPtr(s string) *string { return &s }
