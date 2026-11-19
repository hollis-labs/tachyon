package accessstatus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeApp(t *testing.T, want string, writeOK bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(401)
			return
		}
		if r.Method == "POST" && !writeOK {
			w.WriteHeader(403)
			return
		}
		if r.Method == "POST" {
			w.WriteHeader(404) // no such task: authorized, nothing created
			return
		}
		w.WriteHeader(200)
	}))
}

func TestChecker(t *testing.T) {
	good, scopeless, strict := fakeApp(t, "t", true), fakeApp(t, "t", false), fakeApp(t, "other", true)
	defer good.Close()
	defer scopeless.Close()
	defer strict.Close()
	read, write := &Probe{Path: "/r"}, &Probe{Method: "POST", Path: "/w", Body: `{}`}
	tf := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tf, []byte("t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	specs := []Spec{
		{App: "good", BaseURL: good.URL, Token: "t", Read: read, Write: write},
		{App: "scopeless", BaseURL: scopeless.URL, Token: "t", Read: read, Write: write},
		{App: "wrong", BaseURL: strict.URL, Token: "t", Read: read, Write: write},
		{App: "missing", BaseURL: good.URL, Read: read, Write: write},
		{App: "down", BaseURL: "http://127.0.0.1:1", Token: "t", Read: read},
		{App: "nowrite", BaseURL: good.URL, Token: "t", Read: read, WriteNote: "no idempotent write"},
		{App: "unwired", Note: "not wired"},
	}
	n := time.Unix(100, 0)
	c := &Checker{Specs: func() []Spec { return specs }, now: func() time.Time { return n }}
	got := map[string]Result{}
	for _, r := range c.Check(context.Background(), false) {
		got[r.App] = r
	}
	want := map[string][3]string{
		"good":      {CredConfigured, ProbeOK, ProbeOK},
		"scopeless": {CredConfigured, ProbeOK, ProbeDenied},
		"wrong":     {CredRejected, ProbeDenied, ProbeDenied},
		"missing":   {CredMissing, ProbeDenied, ProbeDenied},
		"down":      {CredConfigured, ProbeDown, ProbeNone},
		"nowrite":   {CredConfigured, ProbeOK, ProbeNone},
		"unwired":   {CredMissing, ProbeNone, ProbeNone},
	}
	for app, w := range want {
		r := got[app]
		if r.Credential != w[0] || r.ReadProbe != w[1] || r.WriteProbe != w[2] {
			t.Errorf("%s: %+v want %v", app, r, w)
		}
	}
	if got["good"].LastCheckedMS != 100000 || got["nowrite"].WriteNote == "" {
		t.Errorf("%+v", got["good"])
	}
	// Cached inside the TTL, re-run on refresh.
	specs = specs[:1]
	if len(c.Check(context.Background(), false)) != 7 || len(c.Check(context.Background(), true)) != 1 {
		t.Error("cache or refresh")
	}
	// A token file is sent as a bearer.
	file := Spec{App: "file", BaseURL: good.URL, TokenFile: tf, Read: read}
	if r := run(context.Background(), file, n); r.ReadProbe != ProbeOK || r.Credential != CredConfigured {
		t.Errorf("token file: %+v", r)
	}
	if display("http://u:p@host:1/x?q=1") != "http://host:1/x" || display("unix:/tmp/s") != "unix:/tmp/s" {
		t.Error("display must hide userinfo and query")
	}
}
