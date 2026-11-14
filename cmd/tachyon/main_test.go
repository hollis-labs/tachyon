package main

import (
	"bytes"
	"strings"
	"testing"
)

// A stray `./tachyon --help` used to start a full server on all interfaces against the prod data dir. Any
// argument the server does not accept now prints usage and exits; only a bare `tachyon` serves.
func TestUsageNeverStartsTheServer(t *testing.T) {
	for _, c := range []struct {
		arg  string
		code int
	}{{"--help", 0}, {"-h", 0}, {"help", 0}, {"-help", 0}, {"serve", 2}, {"--port=1", 2}} {
		var out bytes.Buffer
		if got := usage(&out, c.arg); got != c.code {
			t.Errorf("%s: exit %d, want %d", c.arg, got, c.code)
		}
		if !strings.Contains(out.String(), "usage: tachyon") {
			t.Errorf("%s: no usage text: %q", c.arg, out.String())
		}
	}
}
