package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tether "github.com/hollis-labs/substrate/mesh/tetherclient"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

// Verify Init's config/environment precedence through observable daemon reads,
// including the client default within an isolated HOME, rather than asserting
// an address string. No real Tether instance or model CLI participates.
func TestSessionInitTrimsTetherAddress(t *testing.T) {
	home, err := os.MkdirTemp(os.TempDir(), "si-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	defaultSocket := strings.TrimPrefix(tether.DefaultListenAddr, "unix:~/")
	for _, tc := range []struct {
		name, config, env, socket string
		invalid                   bool
	}{
		{name: "client_default", socket: defaultSocket},
		{name: "config_space_default", config: " ", socket: defaultSocket},
		{name: "config_spaces_tab_default", config: "  \t", socket: defaultSocket},
		{name: "config_space_environment", config: " ", env: "unix:~/env.sock", socket: "env.sock"},
		{name: "config_spaces_tab_environment", config: "  \t", env: "unix:~/env.sock", socket: "env.sock"},
		{name: "environment_space_default", env: " ", socket: defaultSocket},
		{name: "environment_spaces_tab_default", env: "  \t", socket: defaultSocket},
		{name: "both_whitespace_default", config: " ", env: "  \t", socket: defaultSocket},
		{name: "padded_environment", env: " \tunix:~/env.sock \t", socket: "env.sock"},
		{name: "padded_override", config: " \tunix:~/override.sock \t", env: "unix:~/missing.sock", socket: "override.sock"},
		{name: "nonblank_invalid_not_defaulted", config: "http://%", socket: defaultSocket, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TETHER_ADDR", tc.env)
			socketPath := filepath.Join(home, tc.socket)
			if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/sessions/fixture" {
					t.Errorf("unexpected daemon request %s %s", r.Method, r.URL.Path)
				}
				io.WriteString(w, `{"id":"fixture","state":"running"}`)
			})}
			go server.Serve(listener)
			defer server.Close()
			p := &plugin{}
			_, err = p.Init(context.Background(), subprocess.InitParams{Config: map[string]string{"tether_addr": tc.config}})
			if tc.invalid {
				if err == nil {
					_, err = p.adapter.Read(context.Background(), "fixture")
				}
				if err == nil || requests.Load() != 0 {
					t.Fatalf("invalid nonblank address silently defaulted: %v; daemon reads %d", err, requests.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.adapter.Read(context.Background(), "fixture")
			if err != nil || got.ID != "fixture" || requests.Load() != 1 {
				t.Fatalf("session read: %+v %v; daemon reads %d", got, err, requests.Load())
			}
		})
	}
}
