package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
)

type protocolFixture struct{ init subprocess.InitParams }

func (p *protocolFixture) Init(_ context.Context, in subprocess.InitParams) (subprocess.InitResult, error) {
	p.init = in
	return subprocess.InitResult{ID: in.Incarnation.OwnerID, Name: "Protocol fixture", Version: "0.1.0", Protocol: subprocess.ProtocolVersion, CapabilityContract: 1}, nil
}
func (*protocolFixture) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (*protocolFixture) Unload(context.Context) error { return nil }
func (p *protocolFixture) Command(_ context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	if req.Name != "identity" {
		return subprocess.CommandResult{}, fmt.Errorf("unknown command")
	}
	raw, err := json.Marshal(p.init)
	return subprocess.CommandResult{Action: "message", Content: string(raw)}, err
}
func TestProtocolFixture(t *testing.T) {
	if os.Getenv("TACHYON_PROTOCOL_FIXTURE") != "1" {
		return
	}
	if err := subprocess.Serve(&protocolFixture{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestProtocol2FreshGenerationAndRepeatedCalls(t *testing.T) {
	t.Setenv("TACHYON_DATA_DIR", t.TempDir())
	t.Setenv("TACHYON_CACHE_DIR", t.TempDir())
	t.Setenv("TACHYON_PROTOCOL_FIXTURE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture")
	script := "#!/bin/sh\nexec " + fmt.Sprintf("%q", exe) + " -test.run=^TestProtocolFixture$\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil { // #nosec G306 -- private executable test launcher, no credentials.
		t.Fatal(err)
	}
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.LoadPlugin(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	if err := m.BuildRegistry().Validate(); err != nil {
		t.Fatalf("invalid registry-v2 response: %v", err)
	}
	get := func() subprocess.InitParams {
		raw, err := m.CallPlugin(context.Background(), "fixture", "command/execute", subprocess.CommandExecParams{Name: "identity"})
		if err != nil {
			t.Fatal(err)
		}
		var out subprocess.CommandExecResult
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		var init subprocess.InitParams
		if err := json.Unmarshal([]byte(out.Content), &init); err != nil {
			t.Fatal(err)
		}
		return init
	}
	first := get()
	second := get()
	if first.Incarnation != second.Incarnation || first.Incarnation.HostInstance != m.hostInstance || first.Incarnation.OwnerGeneration == 0 || len(first.Grants) != 0 || first.HostServices != nil || first.HooksProfile != nil {
		t.Fatal("incorrect host incarnation or authority")
	}
	if first.DataDir == first.CacheDir || first.PluginDir != dir {
		t.Fatal("incorrect runtime roots")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.CallPlugin(context.Background(), "fixture", "command/execute", subprocess.CommandExecParams{Name: "identity"})
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}

	if err := m.RestartPlugin("fixture"); err != nil {
		t.Fatal(err)
	}
	next := get()
	if next.Incarnation.HostInstance != first.Incarnation.HostInstance || next.Incarnation.OwnerGeneration <= first.Incarnation.OwnerGeneration {
		t.Fatal("restart reused incarnation")
	}
	if next.DataDir != first.DataDir || next.CacheDir != first.CacheDir {
		t.Fatal("restart changed persistent roots")
	}
}

func TestRuntimeRootsRejectBundleAndSymlink(t *testing.T) {
	bundle := t.TempDir()
	safe := t.TempDir()
	link := filepath.Join(t.TempDir(), "redirect")
	if err := os.Symlink(bundle, link); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{bundle, filepath.Join(bundle, "data"), filepath.Join(link, "data"), "relative"} {
		if err := validateRuntimeRoots(bundle, data, safe); err == nil {
			t.Fatalf("unsafe root admitted: %s", data)
		}
	}
	if err := validateRuntimeRoots(bundle, t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
