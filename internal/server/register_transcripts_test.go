package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscriptsStubReplacesTheBridgeOnlyWhenEnabled(t *testing.T) {
	off := New(Config{PluginBinDir: t.TempDir()}).Routes()
	t.Setenv("TACHYON_TRANSCRIPTS_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir()}).Routes()
	p := "https://tachyon.test/api/plugins/console/v1/transcripts/snapshot"
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Transcripts-Stub") != "1" {
		t.Errorf("stub snapshot: %d", w.Code)
	}
	w = httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("POST", p, strings.NewReader(`{}`)))
	if w.Code == 200 {
		t.Errorf("stub accepted a write: %d", w.Code)
	}
	w = httptest.NewRecorder()
	off.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	if w.Header().Get("X-Tachyon-Transcripts-Stub") != "" {
		t.Error("stub served with the flag off")
	}
}
