package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

type cerbRig struct {
	h     http.Handler
	mu    sync.Mutex
	calls []string
}

func newCerbRig(t *testing.T, mode string, withActions bool) *cerbRig {
	t.Helper()
	t.Setenv("TACHYON_CERBERUS_CONTROL", mode)
	rig := &cerbRig{}
	sock := filepath.Join(t.TempDir(), "c.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	cer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rig.mu.Lock()
		rig.calls = append(rig.calls, r.Method+" "+r.URL.RequestURI()+" "+string(b))
		rig.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go cer.Serve(l)
	t.Cleanup(func() { _ = cer.Close() })
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"elevated":false,"assurance":"loopback-unverified"}`))
	}))
	t.Cleanup(auth.Close)
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL, Upstreams: catalog.Upstreams{CerberusAddr: "unix:" + sock}})
	if withActions {
		db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		s.store = db
		s.objects = objects.New(db)
		s.approvalInbox = approvals.NewInbox(&approvals.Store{DB: db}, approvals.New(auth.URL))
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		s.startActions(ctx, db)
		s.actions.Manager.Labels = func(context.Context, string, string) (actionrun.Labels, error) {
			return actionrun.Labels{Env: "dev", Owner: "self", Admin: "self"}, nil
		}
	}
	rig.h = s.Routes()
	return rig
}

func (r *cerbRig) do(method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://tachyon.test"+CerberusPrefix+path, strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://tachyon.test")
	req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "operator-session"})
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func TestCerberusDirectReadsAndWrites(t *testing.T) {
	r := newCerbRig(t, "", false) // default is direct
	if w := r.do("GET", "/control"); w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"direct"`) {
		t.Fatalf("control %d %s", w.Code, w.Body)
	}
	if w := r.do("GET", "/resources"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	if w := r.do("GET", "/resources/tachyon-kit-frontend/logs?lines=5"); w.Code != 200 {
		t.Fatalf("logs %d %s", w.Code, w.Body)
	}
	if w := r.do("GET", "/health"); w.Code != 200 {
		t.Fatalf("health %d", w.Code)
	}
	for _, verb := range []string{"start", "restart", "reload", "stop", "deploy", "apply", "sync"} {
		if w := r.do("POST", "/resources/tachyon-kit-frontend/"+verb); w.Code != 200 {
			t.Fatalf("%s %d %s", verb, w.Code, w.Body)
		}
	}
	w := r.do("POST", "/resources/tachyon-kit-frontend/remove")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"destructive":true`) {
		t.Fatalf("remove must run in dev and be flagged: %d %s", w.Code, w.Body)
	}
	if w := r.do("POST", "/resources/tachyon-kit-frontend/explode"); w.Code != 404 {
		t.Fatalf("unknown verb %d", w.Code)
	}
	if w := r.do("POST", "/resources/..%2Fx/reload"); w.Code == 200 {
		t.Fatalf("bad id accepted")
	}
	r.mu.Lock()
	calls := strings.Join(r.calls, "\n")
	r.mu.Unlock()
	for _, want := range []string{
		"GET /resources ", "GET /resources/tachyon-kit-frontend/logs?lines=5 ", "GET /health ",
		`POST /resources/tachyon-kit-frontend/reload {"acknowledged":true}`,
		`POST /resources/tachyon-kit-frontend/stop {"acknowledged":true}`,
		`POST /resources/tachyon-kit-frontend/deploy {"acknowledged":true}`,
		`POST /resources/tachyon-kit-frontend/remove {"acknowledged":true}`,
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("cerberus never saw %q in:\n%s", want, calls)
		}
	}
	var ctl struct {
		Recent []struct{ Verb, Outcome string } `json:"recent"`
	}
	_ = json.Unmarshal(r.do("GET", "/control").Body.Bytes(), &ctl)
	if len(ctl.Recent) != 8 || ctl.Recent[0].Verb != "remove" || ctl.Recent[0].Outcome != "done" {
		t.Fatalf("journal %+v", ctl.Recent)
	}
}

func TestCerberusWritesNeedOperatorOrigin(t *testing.T) {
	r := newCerbRig(t, "direct", false)
	req := httptest.NewRequest("POST", "https://tachyon.test"+CerberusPrefix+"/resources/x/reload", nil)
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("no origin: %d", w.Code)
	}
	if len(r.calls) != 0 {
		t.Fatalf("reached cerberus: %v", r.calls)
	}
}

func TestCerberusApproveAndRunModeFilesRequestInsteadOfCalling(t *testing.T) {
	r := newCerbRig(t, "approve-and-run", true)
	w := r.do("POST", "/resources/tachyon-kit-frontend/start")
	if w.Code != 202 || !strings.Contains(w.Body.String(), "cerberus.resource.reload") || !strings.Contains(w.Body.String(), "Nothing has run") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasPrefix(c, "POST ") {
			t.Fatalf("approve-and-run mode wrote to cerberus: %s", c)
		}
	}
}

func TestCerberusElevationCanBeRequired(t *testing.T) {
	t.Setenv("TACHYON_CERBERUS_ELEVATION", "required")
	r := newCerbRig(t, "direct", false)
	if w := r.do("POST", "/resources/x/reload"); w.Code != 403 || !strings.Contains(w.Body.String(), "assurance_insufficient") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(r.calls) != 0 {
		t.Fatalf("reached cerberus: %v", r.calls)
	}
}
