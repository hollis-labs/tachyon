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
	"path/filepath"
	"strings"
	"testing"

	tether "github.com/hollis-labs/go-tether-client"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestSessionProviderBodiesNeverReachErrorEnvelope(t *testing.T) {
	const sentinel = "QA_SECRET_SENTINEL"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"raw error", 500, sentinel, "provider_error"},
		{"structured error", 503, `{"error":{"code":"` + sentinel + `","message":"` + sentinel + `"}}`, "provider_error"},
		{"oversize error", 500, strings.Repeat(sentinel, maxTetherResponseBytes/len(sentinel)+1), "provider_error"},
		{"oversize success", 200, `{"id":"` + strings.Repeat(sentinel, maxTetherResponseBytes/len(sentinel)+1) + `"}`, "response_too_large"},
		{"valid JSON with oversize tail", 200, `{"id":"qa"}` + strings.Repeat(" ", maxTetherResponseBytes), "response_too_large"},
		{"malformed success", 200, `{"id":` + sentinel, "provider_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			a, err := NewTetherAdapter(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			p := &plugin{adapter: a}
			env, err := p.HandleVerb(context.Background(), "session_read", json.RawMessage(`{"id":"qa"}`))
			if err != nil || env.Status != contract.StatusError || env.Error.Code != tc.code {
				t.Fatalf("envelope: %+v %v", env, err)
			}
			raw, _ := json.Marshal(env)
			if strings.Contains(string(raw), sentinel) {
				t.Fatalf("provider body leaked: %s", raw)
			}
			if tc.status >= 400 && !strings.Contains(env.Error.Message, "HTTP ") {
				t.Fatalf("safe status missing: %+v", env)
			}
		})
	}
}

func TestSessionResultSanitizesArbitraryErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("QA_SECRET_SENTINEL"),
		&tether.APIError{StatusCode: 500, Code: "QA_SECRET_SENTINEL", Message: "QA_SECRET_SENTINEL", Body: "QA_SECRET_SENTINEL"},
	} {
		env, _ := sessionResult(nil, err)
		raw, _ := json.Marshal(env)
		if strings.Contains(string(raw), "QA_SECRET_SENTINEL") || env.Status != contract.StatusError {
			t.Fatalf("unsafe envelope: %s", raw)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countingBody struct {
	reads  int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads += len(p)
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func (b *countingBody) Close() error { b.closed = true; return nil }
func TestTetherBoundaryReadBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		length    int64
		wantReads bool
	}{
		{"unknown success length", 200, -1, true},
		{"declared oversize success", 200, maxTetherResponseBytes + 1, false},
		{"unknown error length", 500, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countingBody{}
			transport := boundedTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Body: body}, nil
			})}
			req, _ := http.NewRequest(http.MethodGet, "http://mock/sessions/qa", nil)
			_, err := transport.RoundTrip(req)
			if err == nil || !body.closed {
				t.Fatalf("not rejected/closed: %v %+v", err, body)
			}
			if tc.wantReads {
				if body.reads != maxTetherResponseBytes+1 {
					t.Fatalf("read budget: %d", body.reads)
				}
			} else if body.reads != 0 {
				t.Fatalf("unexpected body read: %d", body.reads)
			}
		})
	}
}

func TestTetherBoundaryPreservesUnixAndDefaultHome(t *testing.T) {
	// Keep Unix socket paths short even when the caller supplies a long TMPDIR.
	home, err := os.MkdirTemp(os.TempDir(), "so-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	for _, addr := range []string{"unix:" + filepath.Join(home, "explicit.sock"), "unix:~/relative.sock", ""} {
		t.Run(addr, func(t *testing.T) {
			resolved, _, err := sessionHTTPClient(addr)
			if err != nil {
				t.Fatal(err)
			}
			socket := strings.TrimPrefix(resolved, "unix:")
			// Default socket's nested directory needs to exist inside the temporary home.
			if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"id":"qa","state":"running"}`)
			})}
			go server.Serve(listener)
			defer server.Close()
			a, err := NewTetherAdapter(addr)
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.Read(context.Background(), "qa")
			if err != nil || got.ID != "qa" {
				t.Fatalf("unix read: %+v %v", got, err)
			}
		})
	}
}
