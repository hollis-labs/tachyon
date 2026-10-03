package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
)

// These are real plugins admitted through the host's settings/spawn path.
// Providers are an isolated HTTP server, Unix HTTP socket and local Git fixture.
func TestNormalizedSettingsThroughHost(t *testing.T) {
	root, err := os.MkdirTemp(os.TempDir(), "ns-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binaries := map[string]string{}
	for _, id := range []string{"work-ops", "service-ops", "scm-ops"} {
		source := filepath.Join("..", "..", "plugins", id)
		entries, err := os.ReadDir(source)
		if err != nil {
			t.Fatal(err)
		}
		// Track the inner build's inputs so go test notices plugin source changes.
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
	t.Run("work", func(t *testing.T) {
		for _, tc := range []struct {
			name, project string
			credentials   bool
			legacySelect  bool
			defaultURL    bool
		}{
			{name: "padded_url_and_opaque_id", project: " project with edge spaces "},
			{name: "whitespace_only_project", project: " \t\n"},
			{name: "padded_declared_default", project: "fixture", defaultURL: true},
			// Credentials are accepted by current Init, but the planned write-only
			// HTTP-base rule rejects them. Startup must continue to accept this value.
			{name: "legacy_url_credentials", project: "legacy", credentials: true},
			{name: "legacy_select_option", project: "legacy", legacySelect: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := settingsTestHome(t, root, "w")
				seen := make(chan string, 1)
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.Path != "/api/v1/tasks" {
						t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
					}
					seen <- r.URL.Query().Get("project_id")
					io.WriteString(w, `{"tasks":[],"total":0,"has_more":false}`)
				}))
				defer provider.Close()
				endpoint := provider.URL
				if tc.credentials {
					endpoint = strings.Replace(endpoint, "http://", "http://fixture:private@", 1)
				}
				values := map[string]string{"torque_url": " \t" + endpoint + " \n", "default_project": tc.project}
				if tc.defaultURL {
					path := filepath.Join(filepath.Dir(binaries["work-ops"]), "capabilities.json")
					original, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var caps contract.PluginCapabilities
					if err := json.Unmarshal(original, &caps); err != nil {
						t.Fatal(err)
					}
					for i := range caps.Settings.Fields {
						if caps.Settings.Fields[i].Key == "torque_url" {
							caps.Settings.Fields[i].Default = values["torque_url"]
						}
					}
					encoded, err := json.Marshal(caps)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, encoded, 0600); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.WriteFile(path, original, 0600); err != nil {
							t.Error(err)
						}
					})
					delete(values, "torque_url")
				}
				var warnings bytes.Buffer
				if tc.legacySelect {
					// A legacy plugin may have declared a whitespace-bearing option.
					// New normalization cannot turn its accepted value into refusal.
					path := filepath.Join(filepath.Dir(binaries["work-ops"]), "capabilities.json")
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var caps contract.PluginCapabilities
					if err := json.Unmarshal(raw, &caps); err != nil {
						t.Fatal(err)
					}
					const accepted = " legacy-option "
					caps.Settings.Fields = append(caps.Settings.Fields, contract.SettingsField{Key: "legacy_select", Type: contract.SettingsFieldSelect, Options: []contract.SettingsOption{{Value: accepted}}})
					encoded, err := json.Marshal(caps)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, encoded, 0600); err != nil {
						t.Fatal(err)
					}
					values["legacy_select"] = accepted
					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewJSONHandler(&warnings, nil)))
					t.Cleanup(func() { slog.SetDefault(previous) })
				}
				settingsTestStore(t, home, "work-ops", values)
				if tc.legacySelect {
					config, err := readPluginSettings(filepath.Join(home, "data"), filepath.Dir(binaries["work-ops"]), "work-ops")
					if err != nil || config["legacy_select"] != values["legacy_select"] {
						t.Fatalf("accepted option changed: %v", err)
					}
				}
				mgr := settingsTestManager(t, binaries["work-ops"])
				settingsTestInvoke(t, mgr, "work_list", `{}`)
				want := tc.project
				if strings.TrimSpace(want) == "" {
					want = ""
				}
				if got := <-seen; got != want {
					t.Fatalf("project forwarded as %q, want %q", got, want)
				}
				if tc.legacySelect {
					log := warnings.String()
					if !strings.Contains(log, `"level":"WARN"`) || !strings.Contains(log, `"field":"legacy_select"`) || strings.Contains(log, values["legacy_select"]) {
						t.Fatalf("missing or unsafe compatibility warning: %s", log)
					}
				}
			})
		}
	})
	t.Run("service", func(t *testing.T) {
		for _, blank := range []bool{false, true} {
			name := "path_edge_spaces"
			if blank {
				name = "whitespace_only_path"
			}
			t.Run(name, func(t *testing.T) {
				home := settingsTestHome(t, root, "s")
				socket := filepath.Join(home, "socket ")
				setting := socket
				if blank {
					setting = " \t\n"
					socket = filepath.Join(home, ".cerberus", "cerberus.sock")
				}
				if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				seen := make(chan bool, 1)
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/connectors" {
						t.Errorf("unexpected provider request: %s", r.URL.Path)
					}
					seen <- true
					io.WriteString(w, `[]`)
				})}
				go server.Serve(listener)
				defer server.Close()
				settingsTestStore(t, home, "service-ops", map[string]string{"cerberus_socket": setting})
				mgr := settingsTestManager(t, binaries["service-ops"])
				settingsTestInvoke(t, mgr, "service_list", `{}`)
				<-seen
			})
		}
	})
	t.Run("scm", func(t *testing.T) {
		for _, blank := range []bool{false, true} {
			name := "root_edge_spaces"
			if blank {
				name = "whitespace_only_root"
			}
			t.Run(name, func(t *testing.T) {
				home := settingsTestHome(t, root, "g")
				repos := filepath.Join(home, "repos ")
				setting := repos
				if blank {
					setting = " \t\n"
					repos = filepath.Join(home, "dev")
				}
				repo := filepath.Join(repos, "fixture")
				if err := os.MkdirAll(repo, 0700); err != nil {
					t.Fatal(err)
				}
				if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
					t.Fatalf("fixture init: %v\n%s", err, output)
				}
				settingsTestStore(t, home, "scm-ops", map[string]string{"repos_root": setting})
				mgr := settingsTestManager(t, binaries["scm-ops"])
				result := settingsTestInvoke(t, mgr, "scm_list", `{}`)
				var found []struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(result.Data, &found); err != nil || len(found) != 1 || found[0].ID != "fixture" {
					t.Fatalf("repository fixture not reached: %s %v", result.Data, err)
				}
			})
		}
	})
}

func settingsTestHome(t *testing.T, root, prefix string) string {
	t.Helper()
	home, err := os.MkdirTemp(root, prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("TACHYON_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("TACHYON_SCM_REPOS_ROOT", "")
	return home
}
func settingsTestStore(t *testing.T, home, id string, values map[string]string) {
	t.Helper()
	dir := filepath.Join(home, "data", "config-ops")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{id: values})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
func settingsTestManager(t *testing.T, binary string) *Manager {
	t.Helper()
	mgr := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mgr.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := mgr.LoadPlugin(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
	return mgr
}
func settingsTestInvoke(t *testing.T, mgr *Manager, verb, payload string) contract.ResultEnvelope {
	t.Helper()
	raw, err := mgr.InvokeVerb(context.Background(), verb, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	var result contract.ResultEnvelope
	if err := json.Unmarshal(raw, &result); err != nil || result.Status != contract.StatusOK {
		t.Fatalf("%s: %s %v", verb, raw, err)
	}
	return result
}
