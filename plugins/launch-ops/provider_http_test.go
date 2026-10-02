package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const qaSentinel = "QA_SECRET_SENTINEL"

func TestProviderErrorsAreRedacted(t *testing.T) {
	for _, backend := range []string{"nanite", "tether"} {
		for _, body := range []string{qaSentinel, `{"error":{"code":"QA_SECRET_SENTINEL","message":"QA_SECRET_SENTINEL"}}`} {
			t.Run(backend+body, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); io.WriteString(w, body) }))
				defer srv.Close()
				store := testStore(t)
				var adapter LaunchAdapter
				if backend == "nanite" {
					adapter = NewNaniteLaunchAdapter(srv.URL, store)
				} else {
					adapter = tetherAdapter(t, srv.URL, store)
				}
				p := &plugin{adapter: adapter}
				env, err := p.HandleVerb(context.Background(), "launch_prepare", json.RawMessage(`{"agent_id":"test-agent"}`))
				if err != nil {
					t.Fatal(err)
				}
				if env.Error == nil || strings.Contains(env.Error.Message, qaSentinel) || !strings.Contains(env.Error.Message, "HTTP 500") {
					t.Fatalf("unsafe envelope: %+v", env)
				}
			})
		}
	}
}

func TestOversizedProviderResponses(t *testing.T) {
	for _, backend := range []string{"nanite", "tether"} {
		for _, status := range []int{200, 500} {
			t.Run(backend+http.StatusText(status), func(t *testing.T) {
				// A valid JSON prefix must not bypass checking the rest of the body.
				body := `{"agent":{"id":"test-agent"},"launches":[]}` + strings.Repeat(" ", maxProviderResponse) + qaSentinel
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); io.WriteString(w, body) }))
				defer srv.Close()
				store := testStore(t)
				var adapter LaunchAdapter
				if backend == "nanite" {
					adapter = NewNaniteLaunchAdapter(srv.URL, store)
				} else {
					adapter = tetherAdapter(t, srv.URL, store)
				}
				p := &plugin{adapter: adapter}
				env, err := p.HandleVerb(context.Background(), "launch_prepare", json.RawMessage(`{"agent_id":"test-agent"}`))
				if err != nil {
					t.Fatal(err)
				}
				if env.Error == nil || strings.Contains(env.Error.Message, qaSentinel) || !strings.Contains(env.Error.Message, "exceeds") {
					t.Fatalf("oversize envelope: %+v", env)
				}
			})
		}
	}
}

type repeatingBody struct {
	read   int
	closed bool
}

func (r *repeatingBody) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = 'x'
	}
	r.read += len(b)
	return len(b), nil
}
func (r *repeatingBody) Close() error { r.closed = true; return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportActuallyBoundsReadsAndCloses(t *testing.T) {
	for _, status := range []int{200, 500} {
		body := &repeatingBody{}
		transport := boundedProviderTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
		})}
		req, _ := http.NewRequest(http.MethodGet, "http://fixture.invalid", nil)
		_, err := transport.RoundTrip(req)
		if err == nil || body.read != maxProviderResponse+1 || !body.closed {
			t.Fatalf("status %d: read %d closed %v err %v", status, body.read, body.closed, err)
		}
	}
}

func TestInvalidJSONDoesNotEchoProviderData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, qaSentinel) }))
	defer srv.Close()
	for _, adapter := range []LaunchAdapter{NewNaniteLaunchAdapter(srv.URL, testStore(t)), tetherAdapter(t, srv.URL, testStore(t))} {
		_, err := adapter.Prepare(context.Background(), PrepareRequest{AgentID: "test-agent"})
		if err == nil || strings.Contains(err.Error(), qaSentinel) {
			t.Fatalf("unsafe decode error: %v", err)
		}
	}
}

func TestExecutionErrorsStoredSafely(t *testing.T) {
	for _, backend := range []string{"nanite", "tether"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); io.WriteString(w, qaSentinel) }))
		store := testStore(t)
		var adapter LaunchAdapter
		if backend == "nanite" {
			adapter = NewNaniteLaunchAdapter(srv.URL, store)
		} else {
			adapter = tetherAdapter(t, srv.URL, store)
		}
		l := &Launch{ID: "fixture", AgentID: "test-agent", Backend: backend, State: LaunchStatePrepared, Config: map[string]any{"launch_id": "catalog-launch"}}
		if err := store.Create(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		result, err := adapter.Execute(context.Background(), ExecuteRequest{LaunchID: l.ID})
		if err != nil || result.Error == "" || strings.Contains(result.Error, qaSentinel) {
			t.Fatalf("unsafe execution result: %+v %v", result, err)
		}
		stored, err := store.Get(context.Background(), l.ID)
		if err != nil || strings.Contains(stored.Error, qaSentinel) {
			t.Fatalf("unsafe saved error: %+v %v", stored, err)
		}
		srv.Close()
	}
}

func TestBoundedTetherUnixTransport(t *testing.T) {
	home, err := os.MkdirTemp(os.TempDir(), "lu-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	listener, err := net.Listen("unix", filepath.Join(home, "fixture.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"launches":[{"id":"catalog-launch","agent":"test-agent"}]}`)
	})}
	go server.Serve(listener)
	defer server.Close()
	adapter := tetherAdapter(t, "unix:~/fixture.sock", testStore(t))
	l, err := adapter.Prepare(context.Background(), PrepareRequest{AgentID: "test-agent"})
	if err != nil || l.Backend != "tether" {
		t.Fatalf("Unix transport: %+v %v", l, err)
	}
}
