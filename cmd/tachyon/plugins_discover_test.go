package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverPlugins(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("b-ops/plugin.yaml", 0o644)
	write("b-ops/b-ops", 0o755) // ready
	write("a-ops/plugin.yaml", 0o644)
	write("a-ops/a-ops", 0o755) // ready, sorts first
	write("unbuilt/plugin.yaml", 0o644)
	write("noexec/plugin.yaml", 0o644)
	write("noexec/noexec", 0o644) // present but not executable
	write("hello/hello", 0o755)   // no manifest: not a plugin
	write("stray.txt", 0o644)     // not a directory

	found, missing, err := discoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range found {
		ids = append(ids, p.ID)
		if want := filepath.Join(root, p.ID, p.ID); p.Binary != want {
			t.Errorf("%s binary = %q, want %q", p.ID, p.Binary, want)
		}
	}
	if want := []string{"a-ops", "b-ops"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("found = %v, want %v", ids, want)
	}
	if want := []string{"noexec", "unbuilt"}; !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %v, want %v", missing, want)
	}
}

func TestDiscoverPluginsMissingRoot(t *testing.T) {
	if _, _, err := discoverPlugins(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}
