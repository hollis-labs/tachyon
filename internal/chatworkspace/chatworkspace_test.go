package chatworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const base = "/api/plugins/console/v1/chat"

type env struct {
	h      *Handler
	srv    *httptest.Server
	dir    string
	mu     sync.Mutex
	events []string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{dir: filepath.Join(t.TempDir(), "workspace")}
	e.h = &Handler{
		Store: &Store{DB: db, Dir: e.dir},
		Exists: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("no such thread")
			}
			return nil
		},
		Published: func(_ context.Context, thread, op, id string) {
			e.mu.Lock()
			e.events = append(e.events, thread+" "+op)
			e.mu.Unlock()
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/", e.h.Mux())
	mux.Handle(ContentRoute, e.h.Content())
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, ctype string, body io.Reader) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+base+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func (e *env) json(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	code, _, b := e.do(t, method, path, "application/json", strings.NewReader(body))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return code, m
}

func multi(t *testing.T, filename string, content []byte, fields map[string]string) (string, io.Reader) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	fw, _ := w.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = w.Close()
	return w.FormDataContentType(), &buf
}

func (e *env) upload(t *testing.T, thread, filename string, content []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	ct, body := multi(t, filename, content, fields)
	code, _, b := e.do(t, "POST", "/threads/"+thread+"/workspace", ct, body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return code, m
}

func TestUploadListContentRoundTrip(t *testing.T) {
	e := setup(t)
	content := []byte("# Design\nhello\n")
	code, it := e.upload(t, "app:t1", "design.md", content, map[string]string{"shared": "true"})
	if code != 201 || it["type"] != "file" || it["mime"] != "text/markdown" || it["shared"] != true || it["added_by"] != "user" {
		t.Fatal(code, it)
	}
	sum := sha256.Sum256(content)
	if it["sha256"] != hex.EncodeToString(sum[:]) || it["size_bytes"] != float64(len(content)) {
		t.Fatalf("digest/size: %v", it)
	}
	id := it["id"].(string)
	_, list := e.json(t, "GET", "/threads/app:t1/workspace", "")
	lim := list["limits"].(map[string]any)
	if len(list["items"].([]any)) != 1 || lim["used_items"] != 1.0 || lim["max_items"] != 100.0 || lim["max_file_bytes"] != 26214400.0 {
		t.Fatalf("list: %v", list)
	}
	code, h, b := e.do(t, "GET", "/threads/app:t1/workspace/"+id+"/content", "", nil)
	if code != 200 || !bytes.Equal(b, content) {
		t.Fatal(code, string(b))
	}
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Type") != "text/markdown" {
		t.Fatalf("headers: %v", h)
	}
	// The stored file is where the thread/id says, mode 0600, and nowhere else.
	p := e.h.Store.Path("app:t1", id)
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatal(err, fi)
	}
	// Another thread cannot reach it, by id or by content.
	if code, _ := e.json(t, "PATCH", "/threads/app:t2/workspace/"+id, `{"shared":false}`); code != 404 {
		t.Fatalf("cross-thread patch: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/threads/app:t2/workspace/"+id+"/content", "", nil); code != 404 {
		t.Fatalf("cross-thread content: %d", code)
	}
	if code, _ := e.json(t, "DELETE", "/threads/app:t2/workspace/"+id, ""); code != 404 {
		t.Fatalf("cross-thread delete: %d", code)
	}
}

func TestUploadClassificationAndLimits(t *testing.T) {
	e := setup(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	cases := []struct {
		name    string
		content []byte
		want    int
		mime    string
	}{
		{"notes.txt", []byte("plain text"), 201, "text/plain"},
		{"data.json", []byte(`{"a":1}`), 201, "application/json"},
		{"bad.json", []byte(`{"a":`), 415, ""},
		{"pic.png", png, 201, "image/png"},
		{"doc.pdf", []byte("%PDF-1.7\n1 0 obj\n"), 201, "application/pdf"},
		{"logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 201, "image/svg+xml"},
		{"page.html", []byte("<html><body>hi</body></html>"), 415, ""},
		{"renamed.txt", []byte("<!DOCTYPE html><html><script>x</script></html>"), 415, ""},
		{"run.sh", []byte("#!/bin/sh\necho hi\n"), 415, ""},
		{"prog.exe", []byte("MZ\x90\x00\x03\x00\x00\x00"), 415, ""},
		{"app.bin", append([]byte("PK\x03\x04"), make([]byte, 40)...), 415, ""},
		{"nul.txt", []byte("abc\x00def"), 415, ""},
	}
	for _, c := range cases {
		code, it := e.upload(t, "app:t1", c.name, c.content, nil)
		if code != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, code, it, c.want)
			continue
		}
		if c.want == 201 && it["mime"] != c.mime {
			t.Errorf("%s: mime %v, want %s", c.name, it["mime"], c.mime)
		}
	}
	// SVG is download-only: served as an opaque attachment.
	_, list := e.json(t, "GET", "/threads/app:t1/workspace", "")
	for _, v := range list["items"].([]any) {
		it := v.(map[string]any)
		if it["name"] == "logo.svg" {
			_, h, _ := e.do(t, "GET", "/threads/app:t1/workspace/"+it["id"].(string)+"/content", "", nil)
			if h.Get("Content-Type") != "application/octet-stream" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
				t.Fatalf("svg served as %v", h)
			}
		}
		if it["name"] == "pic.png" && it["type"] != "image" {
			t.Fatalf("png type: %v", it["type"])
		}
	}
	// Over 25 MiB is refused and leaves nothing behind.
	big := bytes.Repeat([]byte("a"), MaxFileBytes+1)
	if code, _ := e.upload(t, "app:t3", "big.txt", big, nil); code != 413 {
		t.Fatalf("oversize: %d", code)
	}
	if n := countFiles(t, filepath.Join(e.dir)); n != 5 { // the five accepted uploads above only
		t.Fatalf("%d files on disk, want 5 (a refused upload left bytes behind)", n)
	}
	// Not multipart, and no file part.
	if code, _ := e.json(t, "POST", "/threads/app:t1/workspace", `{"type":"file","name":"x"}`); code != 400 {
		t.Fatalf("json file: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/threads/app:t1/workspace", "text/plain", strings.NewReader("x")); code != 415 {
		t.Fatalf("text/plain: %d", code)
	}
}

func countFiles(t *testing.T, dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestNameIsNeverAPath(t *testing.T) {
	e := setup(t)
	code, it := e.upload(t, "app:t1", `../../etc/pass wd\..\evil.txt`, []byte("x"), nil)
	if code != 201 || it["name"] != "evil.txt" {
		t.Fatal(code, it)
	}
	_ = filepath.WalkDir(filepath.Dir(e.dir), func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() && !strings.HasPrefix(p, e.dir) && !strings.HasSuffix(p, ".db") && !strings.Contains(p, "data.db") {
			t.Errorf("file outside the workspace dir: %s", p)
		}
		return nil
	})
	if code, it = e.upload(t, "app:t1", "..", []byte("y"), nil); code != 201 || it["name"] != "file" {
		t.Fatal(code, it)
	}
}

func TestLinksRefsAndPatchDelete(t *testing.T) {
	e := setup(t)
	for body, want := range map[string]int{
		`{"type":"link","name":"Design","url":"https://example.invalid/d"}`: 201,
		`{"type":"link","name":"x","url":"javascript:alert(1)"}`:            400,
		`{"type":"link","name":"x","url":"file:///etc/passwd"}`:             400,
		`{"type":"link","name":"x","url":"/relative"}`:                      400,
		`{"type":"ref","name":"T0","ref":"torque:task/CW-20261030-0001"}`:   201,
		`{"type":"ref","name":"x","ref":"data:text/html,hi"}`:               400,
		`{"type":"ref","name":"x","ref":"nocolon"}`:                         400,
		`{"type":"ref","name":"","ref":"torque:task/1"}`:                    400,
		`{"type":"nope","name":"x"}`:                                        400,
		`{"type":"link","name":"x","url":"https://a.b","extra":1}`:          400,
	} {
		if code, out := e.json(t, "POST", "/threads/app:t1/workspace", body); code != want {
			t.Errorf("%s: %d %v, want %d", body, code, out, want)
		}
	}
	_, list := e.json(t, "GET", "/threads/app:t1/workspace", "")
	items := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items: %v", items)
	}
	id := items[0].(map[string]any)["id"].(string)
	if code, _, _ := e.do(t, "GET", "/threads/app:t1/workspace/"+id+"/content", "", nil); code != 404 {
		t.Fatalf("link content: %d", code)
	}
	code, it := e.json(t, "PATCH", "/threads/app:t1/workspace/"+id, `{"shared":true,"name":"  Renamed "}`)
	if code != 200 || it["shared"] != true || it["name"] != "Renamed" {
		t.Fatal(code, it)
	}
	e.json(t, "PATCH", "/threads/app:t1/workspace/"+id, `{"shared":false}`)
	if code, _ := e.json(t, "PATCH", "/threads/app:t1/workspace/"+id, `{}`); code != 400 {
		t.Fatalf("empty patch: %d", code)
	}
	if code, _ := e.json(t, "DELETE", "/threads/app:t1/workspace/"+id, ""); code != 200 {
		t.Fatal(code)
	}
	if code, _ := e.json(t, "DELETE", "/threads/app:t1/workspace/"+id, ""); code != 404 {
		t.Fatalf("second delete: %d", code)
	}
	e.mu.Lock()
	got := strings.Join(e.events, ",")
	e.mu.Unlock()
	if got != "app:t1 add,app:t1 add,app:t1 share,app:t1 unshare,app:t1 remove" {
		t.Fatalf("events: %s", got)
	}
	// Unknown thread.
	if code, _ := e.json(t, "GET", "/threads/app:ghost/workspace", ""); code != 404 {
		t.Fatalf("ghost thread: %d", code)
	}
	if code, _ := e.json(t, "GET", "/threads/bogus/workspace", ""); code != 400 {
		t.Fatalf("bad ref: %d", code)
	}
}

func TestDeleteRemovesBytesAndPurgeRemovesThread(t *testing.T) {
	e := setup(t)
	_, a := e.upload(t, "app:t1", "a.txt", []byte("a"), nil)
	e.upload(t, "app:t1", "b.txt", []byte("b"), nil)
	e.upload(t, "app:t2", "c.txt", []byte("c"), nil)
	e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"note","expected_revision":0}`)
	e.json(t, "DELETE", "/threads/app:t1/workspace/"+a["id"].(string), "")
	if countFiles(t, e.dir) != 2 {
		t.Fatalf("files after delete: %d", countFiles(t, e.dir))
	}
	if err := e.h.Store.PurgeThread(context.Background(), "app:t1"); err != nil {
		t.Fatal(err)
	}
	if countFiles(t, e.dir) != 1 {
		t.Fatalf("files after purge: %d", countFiles(t, e.dir))
	}
	_, list := e.json(t, "GET", "/threads/app:t1/workspace", "")
	_, pad := e.json(t, "GET", "/threads/app:t1/scratchpad", "")
	if len(list["items"].([]any)) != 0 || pad["body"] != "" {
		t.Fatalf("purge left data: %v %v", list, pad)
	}
	if _, l2 := e.json(t, "GET", "/threads/app:t2/workspace", ""); len(l2["items"].([]any)) != 1 {
		t.Fatal("purge touched another thread")
	}
}

func TestItemAndByteLimits(t *testing.T) {
	e := setup(t)
	for i := 0; i < MaxItems; i++ {
		if code, out := e.json(t, "POST", "/threads/app:t1/workspace", `{"type":"ref","name":"r","ref":"torque:task/1"}`); code != 201 {
			t.Fatal(i, code, out)
		}
	}
	if code, out := e.json(t, "POST", "/threads/app:t1/workspace", `{"type":"ref","name":"r","ref":"torque:task/1"}`); code != 413 || out["code"] != "limit_reached" {
		t.Fatalf("item cap: %d %v", code, out)
	}
	// The byte cap counts what is stored.
	ctx := context.Background()
	size := int64(200 << 20)
	if _, err := e.h.Store.Add(ctx, "app:t9", Item{Type: "file", Name: "big", SizeBytes: &size, AddedBy: "user"}); err != nil {
		t.Fatal(err)
	}
	more := int64(60 << 20)
	if _, err := e.h.Store.Add(ctx, "app:t9", Item{Type: "file", Name: "more", SizeBytes: &more, AddedBy: "user"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("byte cap: %v", err)
	}
}

func TestScratchpad(t *testing.T) {
	e := setup(t)
	_, pad := e.json(t, "GET", "/threads/app:t1/scratchpad", "")
	if pad["body"] != "" || pad["revision"] != 0.0 {
		t.Fatalf("default: %v", pad)
	}
	code, pad := e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"first","expected_revision":0}`)
	if code != 200 || pad["revision"] != 1.0 || pad["body"] != "first" {
		t.Fatal(code, pad)
	}
	if code, out := e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"stale","expected_revision":0}`); code != 409 || out["code"] != "conflict" {
		t.Fatalf("stale: %d %v", code, out)
	}
	if code, pad = e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"second","expected_revision":1}`); code != 200 || pad["revision"] != 2.0 {
		t.Fatal(code, pad)
	}
	// Per thread.
	if _, other := e.json(t, "GET", "/threads/app:t2/scratchpad", ""); other["body"] != "" {
		t.Fatalf("leak: %v", other)
	}
	exact := strings.Repeat("x", MaxScratchpad)
	if code, _ := e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"`+exact+`","expected_revision":2}`); code != 200 {
		t.Fatalf("exactly 64 KiB: %d", code)
	}
	if code, _ := e.json(t, "PUT", "/threads/app:t1/scratchpad", `{"body":"`+exact+`x","expected_revision":3}`); code != 413 {
		t.Fatalf("over 64 KiB: %d", code)
	}
	for _, body := range []string{`{"body":"x"}`, `{"expected_revision":1}`, `{"body":"x","expected_revision":-1}`, `{"body":"x","expected_revision":1,"z":1}`} {
		if code, _ := e.json(t, "PUT", "/threads/app:t1/scratchpad", body); code != 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
}

func TestSettings(t *testing.T) {
	e := setup(t)
	if _, s := e.json(t, "GET", "/settings", ""); s["scratchpad_placement"] != "drawer" {
		t.Fatalf("default: %v", s)
	}
	if code, _ := e.json(t, "PUT", "/settings", `{"scratchpad_placement":"aside"}`); code != 200 {
		t.Fatal(code)
	}
	if _, s := e.json(t, "GET", "/settings", ""); s["scratchpad_placement"] != "aside" {
		t.Fatalf("saved: %v", s)
	}
	for _, body := range []string{`{"scratchpad_placement":"floating"}`, `{}`, `{"scratchpad_placement":"aside","x":1}`} {
		if code, _ := e.json(t, "PUT", "/settings", body); code != 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
}

func TestClientContext(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if cc, err := e.h.Store.ClientContextFor(ctx, "app:t1"); err != nil || cc != nil {
		t.Fatal("empty thread should have no context", cc, err)
	}
	_, small := e.upload(t, "app:t1", "small.txt", []byte("tiny"), map[string]string{"shared": "true"})
	_, large := e.upload(t, "app:t1", "large.txt", bytes.Repeat([]byte("x"), InlineMax+1), map[string]string{"shared": "true"})
	e.upload(t, "app:t1", "private.txt", []byte("secret"), nil)
	e.json(t, "POST", "/threads/app:t1/workspace", `{"type":"ref","name":"T0","ref":"torque:task/CW-1","shared":true}`)
	cc, err := e.h.Store.ClientContextFor(ctx, "app:t1")
	if err != nil || cc == nil || cc.Version != 1 || len(cc.Workspace) != 3 {
		t.Fatal(cc, err)
	}
	by := map[string]ClientContextItem{}
	for _, it := range cc.Workspace {
		by[it.Name] = it
	}
	if by["small.txt"].Text != "tiny" || by["small.txt"].Pointer != "" || by["small.txt"].ID != small["id"] {
		t.Fatalf("small: %+v", by["small.txt"])
	}
	if by["large.txt"].Text != "" || by["large.txt"].Pointer == "" || by["large.txt"].SHA256 == "" || by["large.txt"].ID != large["id"] {
		t.Fatalf("large: %+v", by["large.txt"])
	}
	if by["T0"].Ref != "torque:task/CW-1" || by["T0"].Pointer != "" {
		t.Fatalf("ref: %+v", by["T0"])
	}
	if _, ok := by["private.txt"]; ok {
		t.Fatal("unshared item leaked into client_context")
	}
	// Unsharing removes it from the next turn.
	e.json(t, "PATCH", "/threads/app:t1/workspace/"+small["id"].(string), `{"shared":false}`)
	cc, _ = e.h.Store.ClientContextFor(ctx, "app:t1")
	if len(cc.Workspace) != 2 {
		t.Fatalf("after unshare: %d", len(cc.Workspace))
	}
}

// The server mounts Routes behind RequireOperator and the content route behind
// RequireOperatorReads; prove the contract with the same wrappers.
func TestWritesAndContentRefusedWithoutOperatorSession(t *testing.T) {
	e := setup(t)
	_, it := e.upload(t, "app:t1", "a.txt", []byte("a"), nil)
	id := it["id"].(string)
	var elevated bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if elevated {
			w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
			return
		}
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer up.Close()
	op := approvals.New(up.URL)
	mux := http.NewServeMux()
	mux.Handle("/", op.RequireOperator(e.h.Mux()))
	mux.Handle(ContentRoute, op.RequireOperatorReads(e.h.Content()))
	call := func(method, path, origin string, cookie bool) int {
		r := httptest.NewRequest(method, "https://tachyon.test"+base+path, strings.NewReader(`{"shared":true}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "t"})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	writes := [][2]string{{"POST", "/threads/app:t1/workspace"}, {"PATCH", "/threads/app:t1/workspace/" + id}, {"DELETE", "/threads/app:t1/workspace/" + id},
		{"PUT", "/threads/app:t1/scratchpad"}, {"PUT", "/settings"}}
	for _, w := range writes {
		if c := call(w[0], w[1], "https://tachyon.test", false); c != 401 && c != 403 {
			t.Errorf("%v without session: %d", w, c)
		}
		if c := call(w[0], w[1], "https://evil.test", true); c != 403 {
			t.Errorf("%v cross-origin: %d", w, c)
		}
		if c := call(w[0], w[1], "", true); c != 403 {
			t.Errorf("%v without origin: %d", w, c)
		}
	}
	content := "/threads/app:t1/workspace/" + id + "/content"
	if c := call("GET", content, "", false); c != 401 && c != 403 {
		t.Errorf("content without session: %d", c)
	}
	if c := call("GET", content, "", true); c != 403 {
		t.Errorf("content with non-elevated session: %d", c)
	}
	// An ordinary GET (list) stays open, like the other chat reads.
	if c := call("GET", "/threads/app:t1/workspace", "", false); c != 200 {
		t.Errorf("open list: %d", c)
	}
	elevated = true
	if c := call("GET", content, "", true); c != 200 {
		t.Errorf("content when elevated: %d", c)
	}
}
