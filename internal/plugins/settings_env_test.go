package plugins

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestEnvironmentFallbacksThroughHost(t *testing.T) {
	root, err := os.MkdirTemp(os.TempDir(), "ef-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binaries := map[string]string{}
	for _, id := range []string{"launch-ops", "observe-ops", "scm-ops"} {
		source := filepath.Join("..", "..", "plugins", id)
		entries, err := os.ReadDir(source)
		if err != nil {
			t.Fatal(err)
		}
		// Include subprocess build inputs in go test's cache tracking.
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".go") {
				if _, err := os.ReadFile(filepath.Join(source, entry.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}
		dir := filepath.Join(root, id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, id)
		if output, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", id, err, output)
		}
		caps, err := os.ReadFile(filepath.Join(source, "capabilities.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "capabilities.json"), caps, 0600); err != nil {
			t.Fatal(err)
		}
		binaries[id] = binary
	}
	newManager := func(t *testing.T) *Manager {
		t.Helper()
		mgr := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := mgr.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		})
		return mgr
	}
	t.Run("launch", func(t *testing.T) {
		for _, tc := range []struct {
			name, env, configured        string
			omitDefault, masked, invalid bool
		}{
			{name: "padded_provider_env", env: " \ttether\n", omitDefault: true},
			{name: "blank_provider_env", env: " \t\n", omitDefault: true},
			{name: "empty_provider_env", omitDefault: true},
			{name: "configured_provider_wins", env: "private-invalid", configured: "tether"},
			{name: "declaration_masks_env", env: "private-invalid", masked: true},
			{name: "invalid_provider_env", env: "private-invalid", omitDefault: true, invalid: true},
			{name: "invisible_provider_env", env: "tether\u200b", omitDefault: true, invalid: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := settingsTestHome(t, "l")
				var mu sync.Mutex
				paths := map[string]int{}
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					paths[r.URL.Path]++
					mu.Unlock()
					switch r.URL.Path {
					case "/catalog/launches":
						io.WriteString(w, `{"launches":[{"id":"fixture-launch","agent":"fixture-agent"}]}`)
					case "/api/agents/fixture-agent":
						io.WriteString(w, `{"agent":{"id":"fixture-agent","name":"Fixture","enabled":true}}`)
					default:
						t.Errorf("unexpected provider route: %s", r.URL.Path)
						http.Error(w, "fixture", 404)
					}
				}))
				defer provider.Close()
				t.Setenv("TACHYON_LAUNCH_DEFAULT_PROVIDER", tc.env)
				t.Setenv("TETHER_ADDR", " \t"+provider.URL+"\n")
				capsPath := filepath.Join(filepath.Dir(binaries["launch-ops"]), "capabilities.json")
				original, err := os.ReadFile(capsPath)
				if err != nil {
					t.Fatal(err)
				}
				if tc.omitDefault {
					// Isolate the env branch that the authored default otherwise masks.
					var caps contract.PluginCapabilities
					if err := json.Unmarshal(original, &caps); err != nil {
						t.Fatal(err)
					}
					for i := range caps.Settings.Fields {
						if caps.Settings.Fields[i].Key == "default_provider" {
							caps.Settings.Fields[i].Default = nil
						}
					}
					encoded, err := json.Marshal(caps)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(capsPath, encoded, 0600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.WriteFile(capsPath, original, 0600); err != nil {
							t.Error(err)
						}
					})
				}
				values := map[string]string{"nanite_url": provider.URL}
				if tc.configured != "" {
					values["default_provider"] = tc.configured
				}
				settingsTestStore(t, home, "launch-ops", values)
				mgr := newManager(t)
				err = mgr.LoadPlugin(context.Background(), binaries["launch-ops"])
				if tc.invalid {
					if err == nil {
						t.Fatal("invalid env admitted")
					}
					if strings.Contains(err.Error(), tc.env) || strings.Contains(err.Error(), "private") {
						t.Fatal("env value echoed in startup error")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if tc.masked {
					settingsTestInvoke(t, mgr, "launch_list", `{}`)
					return
				}
				payload := `{"agent_id":"fixture-agent","config":{"launch_id":"fixture-launch"}}`
				want := "/catalog/launches"
				if strings.TrimSpace(tc.env) == "" {
					// Choose an explicit backend: prove blank env is unset without
					// pinning the plugin's fallback provider value.
					payload = `{"agent_id":"fixture-agent","backend":"nanite"}`
					want = "/api/agents/fixture-agent"
				}
				settingsTestInvoke(t, mgr, "launch_prepare", payload)
				mu.Lock()
				defer mu.Unlock()
				if paths[want] == 0 {
					t.Fatalf("env/config selection did not reach fixture: %v", paths)
				}
			})
		}
	})
	t.Run("observe", func(t *testing.T) {
		for _, tc := range []struct {
			name                               string
			configured, blank, masked, invalid bool
		}{
			{name: "padded_env_blank_config"},
			{name: "config_precedes_env", configured: true},
			{name: "blank_env_unset", blank: true},
			{name: "declaration_masks_env", masked: true},
			{name: "invalid_env", invalid: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := settingsTestHome(t, "o")
				var mu sync.Mutex
				paths := map[string]int{}
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					paths[r.URL.Path]++
					mu.Unlock()
					if strings.HasSuffix(r.URL.Path, "/api/sessions") {
						io.WriteString(w, `[]`)
					} else {
						io.WriteString(w, `{"status":"ok","enabled":true}`)
					}
				}))
				defer provider.Close()
				values := map[string]string{}
				for _, key := range []string{"nanite_url", "torque_url", "tether_url"} {
					endpoint := " \t" + provider.URL + "/env/" + key + "\n"
					if tc.blank {
						endpoint = " \t\n"
					}
					if tc.masked || tc.invalid {
						endpoint = "private-invalid"
					}
					t.Setenv("TACHYON_OBSERVE_"+strings.ToUpper(key), endpoint)
					if !tc.masked {
						values[key] = " \t\n"
					}
					if tc.configured {
						values[key] = provider.URL + "/config/" + key
					}
				}
				settingsTestStore(t, home, "observe-ops", values)
				mgr := newManager(t)
				err := mgr.LoadPlugin(context.Background(), binaries["observe-ops"])
				if tc.invalid {
					if err == nil {
						t.Fatal("invalid env admitted")
					}
					if strings.Contains(err.Error(), "private-invalid") {
						t.Fatal("env value echoed in startup error")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				// Constructors are inert. Blank or masked env cases prove admission
				// only, without probing any plugin-default provider endpoint.
				if tc.blank || tc.masked {
					return
				}
				settingsTestInvoke(t, mgr, "observe_status", `{}`)
				prefix := "/env/"
				if tc.configured {
					prefix = "/config/"
				}
				mu.Lock()
				defer mu.Unlock()
				for _, path := range []string{prefix + "nanite_url/api/health", prefix + "nanite_url/api/sessions", prefix + "torque_url/api/v1/scheduler/status", prefix + "tether_url/api/health"} {
					if paths[path] == 0 {
						t.Errorf("env/config fixture not reached: %s", path)
					}
				}
			})
		}
	})
	t.Run("scm", func(t *testing.T) {
		for _, blank := range []bool{false, true} {
			name := "meaningful_env_path_spaces"
			if blank {
				name = "blank_env_unset"
			}
			t.Run(name, func(t *testing.T) {
				home := settingsTestHome(t, "s")
				root := filepath.Join(home, "repos ")
				if blank {
					t.Setenv("TACHYON_SCM_REPOS_ROOT", " \t\n")
				} else {
					fixture := filepath.Join(root, "fixture")
					if err := os.MkdirAll(fixture, 0700); err != nil {
						t.Fatal(err)
					}
					if output, err := exec.Command("git", "init", "--quiet", fixture).CombinedOutput(); err != nil {
						t.Fatalf("git fixture: %v %s", err, output)
					}
					t.Setenv("TACHYON_SCM_REPOS_ROOT", root)
				}
				settingsTestStore(t, home, "scm-ops", map[string]string{"repos_root": " \t\n"})
				mgr := newManager(t)
				if err := mgr.LoadPlugin(context.Background(), binaries["scm-ops"]); err != nil {
					t.Fatal(err)
				}
				if blank {
					return
				}
				result := settingsTestInvoke(t, mgr, "scm_list", `{}`)
				var found []struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(result.Data, &found); err != nil || len(found) != 1 || found[0].ID != "fixture" {
					t.Fatalf("env path fixture not reached: %s %v", result.Data, err)
				}
			})
		}
	})
}
