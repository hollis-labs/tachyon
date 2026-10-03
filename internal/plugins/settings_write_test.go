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
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// Exercise real config-ops persistence, host schema synchronization and plugin
// restarts. All provider traffic stays on isolated HTTP/Unix fixtures.
func TestConfigWritesThroughHost(t *testing.T) {
	root, err := os.MkdirTemp(os.TempDir(), "cw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binaries := map[string]string{}
	for _, id := range []string{"config-ops", "launch-ops", "service-ops", "observe-ops", "work-ops"} {
		source := filepath.Join("..", "..", "plugins", id)
		entries, err := os.ReadDir(source)
		if err != nil {
			t.Fatal(err)
		}
		// Account for subprocess-build inputs in go test's cache.
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
	home := settingsTestHome(t, "c")
	t.Setenv("TETHER_ADDR", "")
	t.Setenv("TACHYON_LAUNCH_DEFAULT_PROVIDER", "")
	var mu sync.Mutex
	hits := map[string]int{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/catalog/launches":
			io.WriteString(w, `{"launches":[{"id":"fixture-launch","agent":"fixture-agent"}]}`)
		case "/connectors", "/api/sessions":
			io.WriteString(w, `[]`)
		case "/api/v1/tasks":
			io.WriteString(w, `{"tasks":[],"total":0,"has_more":false}`)
		case "/api/health", "/api/v1/scheduler/status":
			io.WriteString(w, `{"status":"ok","enabled":true}`)
		default:
			t.Errorf("unexpected provider route: %s", r.URL.Path)
			http.Error(w, "fixture", 404)
		}
	})
	provider := httptest.NewServer(handler)
	defer provider.Close()
	socket := filepath.Join(home, "provider ")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	legacyURL := strings.Replace(provider.URL, "http://", "http://fixture:private@", 1)
	stored := map[string]map[string]string{
		"launch-ops":  {"nanite_url": provider.URL, "default_provider": " \ttether\n", "tether_addr": provider.URL},
		"service-ops": {"cerberus_socket": socket},
		"observe-ops": {"nanite_url": provider.URL, "torque_url": provider.URL, "tether_url": provider.URL},
		"work-ops":    {"torque_url": legacyURL, "default_project": " fixture "},
	}
	path := filepath.Join(home, "data", "config-ops", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&warnings, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	mgr := settingsTestManager(t, binaries["config-ops"])
	for _, id := range []string{"launch-ops", "service-ops", "observe-ops", "work-ops"} {
		if err := mgr.LoadPlugin(context.Background(), binaries[id]); err != nil {
			t.Fatalf("startup %s: %v", id, err)
		}
	}
	if !strings.Contains(warnings.String(), `"field":"torque_url"`) || !strings.Contains(warnings.String(), `"level":"WARN"`) || strings.Contains(warnings.String(), legacyURL) || strings.Contains(warnings.String(), "private") {
		t.Fatal("missing or unsafe legacy warning")
	}
	settingsTestInvoke(t, mgr, "work_list", `{}`) // accepted legacy URL still reaches the provider
	invoke := func(t *testing.T, verb, id string, values map[string]any) contract.ResultEnvelope {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"plugin": id, "values": values})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := mgr.InvokeVerb(context.Background(), verb, payload)
		if err != nil {
			t.Fatal(err)
		}
		var result contract.ResultEnvelope
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	readValues := func(t *testing.T, id string) map[string]any {
		t.Helper()
		env := invoke(t, "config_get", id, nil)
		if env.Status != contract.StatusOK {
			t.Fatalf("read: %+v", env)
		}
		var view struct {
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal(env.Data, &view); err != nil {
			t.Fatal(err)
		}
		return view.Values
	}
	reject := func(t *testing.T, id, key string, value any) {
		t.Helper()
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		effective := readValues(t, id)
		env := invoke(t, "config_set", id, map[string]any{key: value})
		if env.Status != contract.StatusError || env.Error == nil || env.Error.Code != "validation" {
			t.Fatalf("expected field rejection: %+v", env)
		}
		var detail struct {
			Errors map[string]string `json:"errors"`
		}
		if err := json.Unmarshal(env.Error.Detail, &detail); err != nil || len(detail.Errors) == 0 {
			t.Fatalf("field errors missing: %s %v", env.Error.Detail, err)
		}
		raw, _ := json.Marshal(env)
		if text, ok := value.(string); ok && text != "" && strings.Contains(string(raw), `"`+text+`"`) {
			t.Fatalf("value echoed in rejection: %s", raw)
		}
		if strings.Contains(string(raw), "private") {
			t.Fatal("credentials echoed in rejection")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("rejection changed persistence", err)
		}
		if !reflect.DeepEqual(effective, readValues(t, id)) {
			t.Fatal("rejection changed desired configuration")
		}
	}
	write := func(t *testing.T, id string, values map[string]any, restart bool) {
		t.Helper()
		env := invoke(t, "config_set", id, values)
		if env.Status != contract.StatusOK {
			t.Fatalf("write: %+v", env)
		}
		var result struct {
			Restart *bool `json:"restart_required"`
		}
		if err := json.Unmarshal(env.Data, &result); err != nil || result.Restart == nil || *result.Restart != restart {
			t.Fatalf("restart flag: %s %v", env.Data, err)
		}
	}
	t.Run("legacy_candidate_rejected_until_repaired", func(t *testing.T) {
		reject(t, "work-ops", "default_project", "new-fixture")
		if err := mgr.RestartPlugin("work-ops"); err != nil {
			t.Fatal("new refusal for accepted legacy URL", err)
		}
		write(t, "work-ops", map[string]any{"torque_url": " \t" + provider.URL + "\n"}, true)
		if err := mgr.RestartPlugin("work-ops"); err != nil {
			t.Fatal(err)
		}
		settingsTestInvoke(t, mgr, "work_list", `{}`)
	})
	t.Run("padded_stored_select_does_not_block_other_fields", func(t *testing.T) {
		write(t, "launch-ops", map[string]any{"nanite_url": " \t" + provider.URL + "\n"}, false)
		write(t, "launch-ops", map[string]any{"default_provider": " \ttether\n"}, false)
		if err := mgr.RestartPlugin("launch-ops"); err != nil {
			t.Fatal(err)
		}
		settingsTestInvoke(t, mgr, "launch_prepare", `{"agent_id":"fixture-agent","config":{"launch_id":"fixture-launch"}}`)
	})
	t.Run("constrained_fields_reject_without_mutation", func(t *testing.T) {
		for _, tc := range []struct{ id, key, value string }{
			{"launch-ops", "nanite_url", "http://fixture:private@invalid"},
			{"launch-ops", "default_provider", "tether\u200b"},
			{"launch-ops", "default_provider", "\ufefftether"},
			{"launch-ops", "tether_addr", "junk-private"},
			{"launch-ops", "tether_addr", "unix:"},
			{"launch-ops", "tether_addr", "tcp:fixture:70000"},
			{"launch-ops", "tether_addr", "unix:/fixture\u200b"},
			{"launch-ops", "tether_addr", "\ufeffunix:/fixture"},
			{"observe-ops", "nanite_url", "http://fixture?private"},
			{"observe-ops", "torque_url", "http://fixture#private"},
			{"observe-ops", "tether_url", "http://fixture:private@invalid"},
			{"observe-ops", "nanite_url", "http://fixture/\u200b"},
			{"observe-ops", "torque_url", "\ufeffhttp://fixture"},
			{"work-ops", "torque_url", "junk-private"},
			{"work-ops", "torque_url", "http://fixture/\ufeff"},
			{"service-ops", "cerberus_socket", "relative-private"},
			{"service-ops", "cerberus_socket", "/fixture\u200b"},
			{"service-ops", "cerberus_socket", "/fixture\ufeff"},
		} {
			reject(t, tc.id, tc.key, tc.value)
		}
		for _, id := range []string{"launch-ops", "service-ops", "observe-ops", "work-ops"} {
			if err := mgr.RestartPlugin(id); err != nil {
				t.Fatal("rejected write broke restart", id, err)
			}
		}
	})
	t.Run("padded_urls_and_preserved_path_are_noops", func(t *testing.T) {
		for _, key := range []string{"nanite_url", "torque_url", "tether_url"} {
			write(t, "observe-ops", map[string]any{key: " \t" + provider.URL + "\n"}, false)
		}
		write(t, "service-ops", map[string]any{"cerberus_socket": socket}, false)
		write(t, "work-ops", map[string]any{"torque_url": " \t" + provider.URL + "\n", "default_project": " fixture "}, false)
		for _, id := range []string{"service-ops", "observe-ops", "work-ops"} {
			if err := mgr.RestartPlugin(id); err != nil {
				t.Fatal(err)
			}
		}
		settingsTestInvoke(t, mgr, "service_list", `{}`)
		settingsTestInvoke(t, mgr, "observe_status", `{}`)
		settingsTestInvoke(t, mgr, "work_list", `{}`)
		if readValues(t, "service-ops")["cerberus_socket"] != socket || readValues(t, "work-ops")["default_project"] != " fixture " {
			t.Fatal("meaningful edge spaces changed")
		}
	})
	t.Run("unavailable_provider_is_not_a_validation_error", func(t *testing.T) {
		unavailable := httptest.NewServer(handler)
		endpoint := unavailable.URL
		unavailable.Close()
		write(t, "work-ops", map[string]any{"torque_url": endpoint}, true)
		if err := mgr.RestartPlugin("work-ops"); err != nil {
			t.Fatal("provider availability refused startup", err)
		}
		raw, err := mgr.InvokeVerb(context.Background(), "work_list", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var result contract.ResultEnvelope
		if err := json.Unmarshal(raw, &result); err != nil || result.Status != contract.StatusError || result.Error == nil || result.Error.Code != "provider_error" {
			t.Fatalf("expected unavailable provider: %s %v", raw, err)
		}
		write(t, "work-ops", map[string]any{"torque_url": provider.URL}, true)
	})
	t.Run("optional_blank_writes_keep_fallbacks", func(t *testing.T) {
		t.Setenv("TETHER_ADDR", provider.URL)
		for _, key := range []string{"nanite_url", "torque_url", "tether_url"} {
			t.Setenv("TACHYON_OBSERVE_"+strings.ToUpper(key), provider.URL)
			write(t, "observe-ops", map[string]any{key: " \t\n"}, true)
			write(t, "observe-ops", map[string]any{key: "\t "}, false)
		}
		write(t, "launch-ops", map[string]any{"tether_addr": " \t\n"}, true)
		write(t, "work-ops", map[string]any{"default_project": " \t\n"}, true)
		write(t, "service-ops", map[string]any{"cerberus_socket": " \t\n"}, true)
		for _, id := range []string{"launch-ops", "service-ops", "observe-ops", "work-ops"} {
			if err := mgr.RestartPlugin(id); err != nil {
				t.Fatal("blank write refused startup", id, err)
			}
		}
		settingsTestInvoke(t, mgr, "launch_prepare", `{"agent_id":"fixture-agent","config":{"launch_id":"fixture-launch"}}`)
		settingsTestInvoke(t, mgr, "observe_status", `{}`)
		settingsTestInvoke(t, mgr, "work_list", `{}`)
		if readValues(t, "service-ops")["cerberus_socket"] != "" || readValues(t, "work-ops")["default_project"] != "" {
			t.Fatal("blank opt-out values were not normalized")
		}
	})
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/catalog/launches", "/connectors", "/api/v1/tasks", "/api/health", "/api/v1/scheduler/status"} {
		if hits[path] == 0 {
			t.Errorf("fake provider not reached: %s", path)
		}
	}
}
