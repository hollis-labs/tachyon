package cerbcontrol

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type seen struct{ method, path, body string }

func fakeCerberus(t *testing.T) (string, *[]seen) {
	t.Helper()
	var mu sync.Mutex
	var calls []seen
	sock := filepath.Join(t.TempDir(), "c.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, seen{r.Method, r.URL.RequestURI(), string(b)})
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/missing/") {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go srv.Serve(l)
	t.Cleanup(func() { _ = srv.Close() })
	return "unix:" + sock, &calls
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"": ModeDirect, "direct": ModeDirect, "bogus": ModeDirect, "Approve-and-Run": ModeApproveAndRun, " approve-and-run ": ModeApproveAndRun} {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestVerbsMapToCerberusRoutes(t *testing.T) {
	want := map[string]string{"start": "reload", "restart": "reload", "reload": "reload", "stop": "stop", "deploy": "deploy", "apply": "apply", "sync": "sync", "remove": "remove"}
	for name, route := range want {
		v, ok := VerbByName(name)
		if !ok || v.Route != route {
			t.Errorf("verb %s: %+v ok=%v", name, v, ok)
		}
	}
	if v, _ := VerbByName("remove"); !v.Destructive {
		t.Error("remove must be flagged destructive")
	}
	if _, ok := VerbByName("rm -rf"); ok {
		t.Error("unknown verb accepted")
	}
}

func TestClientOverUnixSocket(t *testing.T) {
	addr, calls := fakeCerberus(t)
	c, err := New(addr, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.List(ctx, "", "local", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Logs(ctx, "tachyon-kit-frontend", 10, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(ctx, ""); err != nil {
		t.Fatal(err)
	}
	restart, _ := VerbByName("restart")
	if _, err := c.Mutate(ctx, restart, "tachyon-kit-frontend"); err != nil {
		t.Fatal(err)
	}
	want := []seen{
		{"GET", "/resources?connector=local", ""},
		{"GET", "/resources/tachyon-kit-frontend/logs?lines=10", ""},
		{"GET", "/health", ""},
		{"POST", "/resources/tachyon-kit-frontend/reload", `{"acknowledged":true}`},
	}
	if len(*calls) != len(want) {
		t.Fatalf("calls %+v", *calls)
	}
	for i, w := range want {
		if (*calls)[i] != w {
			t.Errorf("call %d = %+v want %+v", i, (*calls)[i], w)
		}
	}
}

func TestClientRefusesBadIDAndSurfacesStatus(t *testing.T) {
	addr, calls := fakeCerberus(t)
	c, _ := New(addr, "")
	stop, _ := VerbByName("stop")
	for _, id := range []string{"", "../x", "A B", "a/b", "x?y"} {
		if _, err := c.Mutate(context.Background(), stop, id); !errors.Is(err, ErrInvalid) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid ids reached cerberus: %+v", *calls)
	}
	// "missing" resource id makes the fake answer 500
	_, err := c.Mutate(context.Background(), Verb{Route: "missing"}, "x")
	_ = err
	var se *StatusError
	if _, err := c.do(context.Background(), "POST", "/resources/x/missing/stop", nil, 5e9); !errors.As(err, &se) || se.Status != 500 {
		t.Fatalf("want StatusError 500, got %v", err)
	}
}

func TestNewRequiresAddress(t *testing.T) {
	if _, err := New("", ""); err == nil {
		t.Error("empty addr accepted")
	}
	if _, err := New("ftp://x", ""); err == nil {
		t.Error("bad scheme accepted")
	}
}

func TestJournalNewestFirstAndBounded(t *testing.T) {
	var j Journal
	for i := 0; i < journalMax+5; i++ {
		j.Add(Op{Verb: "reload", Resource: string(rune('a' + i%26)), Status: i})
	}
	r := j.Recent()
	if len(r) != journalMax || r[0].Status != journalMax+4 {
		t.Fatalf("len=%d first=%d", len(r), r[0].Status)
	}
}
