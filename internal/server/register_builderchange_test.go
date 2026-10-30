package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderchange"
)

func TestControlAddrDefaultsToTetherAddr(t *testing.T) {
	t.Setenv("TACHYON_TETHER_CONTROL_ADDR", "")
	if got := controlAddr("https://tether.example"); got != "https://tether.example" {
		t.Errorf("default: %q", got)
	}
	t.Setenv("TACHYON_TETHER_CONTROL_ADDR", " http://127.0.0.1:8948 ")
	got := controlAddr("https://tether.example")
	if got != "http://127.0.0.1:8948" {
		t.Fatalf("override: %q", got)
	}
	// the credential guard accepts a token over loopback http, so the control client builds against it
	tok := filepath.Join(t.TempDir(), "t")
	if err := os.WriteFile(tok, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := builderchange.NewTether(got, tok); err != nil {
		t.Errorf("loopback http control address refused: %v", err)
	}
}
