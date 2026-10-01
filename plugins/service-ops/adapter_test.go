package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func socketServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "service-ops-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "cerberus.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Cerberus-Api") != "v1" {
			t.Errorf("unexpected request: %s %s header=%s", r.Method, r.URL, r.Header.Get("X-Cerberus-Api"))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		handler(w, r)
	}))
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	return path
}

// Fixtures match Cerberus's socket routes and connector.Definition/DaemonHealth DTOs.
func catalogServer(t *testing.T) string {
	return socketServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connectors":
			w.Write([]byte(`[{"id":"docker","version":"1","resource_types":["container"],"capabilities":{"runtime":true},"config":{"fields":[{"name":"host","type":"string"}]},"operations":[{"name":"list"}]},{"id":"ssh","version":"2","resource_types":["remote"],"capabilities":{},"config":{},"operations":[]}]`))
		case "/connectors/live":
			w.Write([]byte(`["docker"]`))
		case "/health":
			w.Write([]byte(`{"daemon_running":true,"services":[],"resources":[{"resource_id":"app","status":"running","healthy":true}],"services_protected":0,"services_failed":0}`))
		default:
			t.Errorf("invented endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func TestCerberusCatalogAndHealth(t *testing.T) {
	adapter, err := NewCerberusAdapter(catalogServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.client.CloseIdleConnections()
	ctx := context.Background()
	list, err := adapter.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %+v, %v", list, err)
	}
	detail, err := adapter.Read(ctx, "docker")
	if err != nil || detail.Version != "1" || string(detail.Config) != `{"fields":[{"name":"host","type":"string"}]}` {
		t.Fatalf("Read lost definition schema: %+v, %v", detail, err)
	}
	for _, id := range []string{"docker", "ssh"} {
		status, err := adapter.Status(ctx, id)
		if err != nil || status.ServiceID != id || status.Live != (id == "docker") {
			t.Fatalf("Status: %+v, %v", status, err)
		}
	}
	if _, err := adapter.Read(ctx, "missing"); err == nil {
		t.Fatal("expected missing service error")
	}
	if _, err := adapter.Status(ctx, "missing"); err == nil {
		t.Fatal("expected missing service status error")
	}
	health, err := adapter.Health(ctx)
	if err != nil || len(health.Connectors) != 2 {
		t.Fatalf("Health: %+v, %v", health, err)
	}
	var runtime struct {
		DaemonRunning bool `json:"daemon_running"`
		Resources     []struct {
			Healthy bool `json:"healthy"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(health.Runtime, &runtime); err != nil || !runtime.DaemonRunning || len(runtime.Resources) != 1 || !runtime.Resources[0].Healthy {
		t.Fatalf("runtime health lost: %s, %v", health.Runtime, err)
	}
	if !health.Connectors[0].Live || health.Connectors[1].Live {
		t.Fatalf("connector liveness: %+v", health.Connectors)
	}
}

func TestProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", http.StatusServiceUnavailable, `{"error":"private backend detail"}`},
		{"invalid_json", http.StatusOK, "{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := socketServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) })
			adapter, err := NewCerberusAdapter(path)
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.client.CloseIdleConnections()
			p := &plugin{adapter: adapter}
			for _, verb := range []string{"service_list", "service_read", "service_status", "service_health"} {
				env, err := p.HandleVerb(context.Background(), verb, json.RawMessage(`{"service_id":"docker"}`))
				if err != nil || env.Status != contract.StatusError || env.Error.Code != "provider_error" {
					t.Fatalf("%s: %+v, %v", verb, env, err)
				}
			}
		})
	}
}

func TestMissingSocketAndCancelledContext(t *testing.T) {
	adapter, err := NewCerberusAdapter(filepath.Join(t.TempDir(), "missing.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.List(context.Background()); err == nil {
		t.Fatal("missing socket succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Health(ctx); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}

func TestInitSocketExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := &plugin{}
	result, err := p.Init(context.Background(), subprocess.InitParams{})
	if err != nil || result.ID != "service-ops" {
		t.Fatalf("Init: %+v, %v", result, err)
	}
	// No socket is dialled or created during plugin initialization.
	if _, err := os.Stat(filepath.Join(home, ".cerberus")); !os.IsNotExist(err) {
		t.Fatalf("Init touched HOME: %v", err)
	}
	if _, err := NewCerberusAdapter("relative.sock"); err == nil {
		t.Fatal("relative socket accepted")
	}
	p.Unload(context.Background())
}

func TestCommandDispatch(t *testing.T) {
	p := &plugin{}
	_, err := p.Init(context.Background(), subprocess.InitParams{Config: map[string]string{"cerberus_socket": catalogServer(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Unload(context.Background())
	ctx := context.Background()
	result, err := p.Command(ctx, subprocess.CommandRequest{Name: "plugin_capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	var caps contract.PluginCapabilities
	if err := json.Unmarshal([]byte(result.Content), &caps); err != nil {
		t.Fatal(err)
	}
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	for verb, decl := range caps.Verbs {
		if decl.Effect != contract.EffectReads {
			t.Fatalf("write effect: %s", verb)
		}
		result, err := p.Command(ctx, subprocess.CommandRequest{Name: verb, Args: `{"service_id":"docker"}`})
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		var env contract.ResultEnvelope
		if err := json.Unmarshal([]byte(result.Content), &env); err != nil || env.Status != contract.StatusOK {
			t.Fatalf("%s: %s, %v", verb, result.Content, err)
		}
	}
	if _, err := p.Command(ctx, subprocess.CommandRequest{Name: "service_restart"}); err == nil {
		t.Fatal("undeclared command accepted")
	}
	for _, tc := range []struct{ verb, payload string }{
		{"service_read", `{}`}, {"service_status", `{}`}, {"service_list", `{`},
	} {
		env, err := p.HandleVerb(ctx, tc.verb, json.RawMessage(tc.payload))
		if err != nil || env.Status != contract.StatusError || env.Error.Code != "validation" {
			t.Fatalf("validation: %+v, %v", env, err)
		}
	}
	env, err := p.HandleVerb(ctx, "service_restart", nil)
	if err != nil || env.Status != contract.StatusError || env.Error.Code != "unknown_verb" {
		t.Fatalf("unknown verb: %+v, %v", env, err)
	}
}

func TestEmptyCatalog(t *testing.T) {
	adapter, err := NewCerberusAdapter(socketServer(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("null")) }))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.client.CloseIdleConnections()
	list, err := adapter.List(context.Background())
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty catalog: %+v, %v", list, err)
	}
}

func TestInFlightCancellation(t *testing.T) {
	entered := make(chan struct{})
	path := socketServer(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() })
	adapter, err := NewCerberusAdapter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-entered; cancel() }()
	if _, err := adapter.List(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
	<-entered
}
