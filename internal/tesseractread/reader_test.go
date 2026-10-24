package tesseractread

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const secret = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type fake struct {
	*httptest.Server
	mu     sync.Mutex
	calls  []string
	bodies []map[string]any
	auth   []string
}

func rev(id, ns, key string) map[string]any {
	return map[string]any{"item_id": id, "namespace": ns, "memory_key": key, "domain": "memory", "status": "canonical", "tags": []string{"decision", "x"},
		"confidence": 0.9, "created_at": "2026-10-24T10:00:00Z", "payload": map[string]any{"summary": "short " + secret, "body": "long body " + secret},
		"author": map[string]any{"agent_id": "claude-code"}, "session_id": "SESSION", "provenance": map[string]any{"write_context": map[string]any{"session_id": "SESSION"}}, "embedding_model": "m"}
}

func newFake(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) (*fake, *Reader) {
	t.Helper()
	f := &fake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		f.bodies = append(f.bodies, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		handler(w, r, body)
	}))
	t.Cleanup(f.Close)
	tf := filepath.Join(t.TempDir(), "t.token")
	os.WriteFile(tf, []byte("tess-token-0123456789abcdef\n"), 0o600)
	r, err := New(f.URL, tf)
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	return f, r
}

func TestNewIsOffWithoutAnAddress(t *testing.T) {
	if r, err := New("", ""); r != nil || err != nil {
		t.Fatalf("%v %v", r, err)
	}
}

func TestRecallSendsOnlyTheWhitelistedBodyAndShapesResults(t *testing.T) {
	f, r := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []any{
				map[string]any{"revision": rev("i1", "project/tachyon/memory/decisions", "k1"), "score": 0.42},
				map[string]any{"revision": rev("i2", "user/chrispian/memory", "private"), "score": 0.9}, // must never pass through
			},
			"manifest": map[string]any{"results_total": 38, "truncated": true, "next_cursor": "abc_DEF-1="},
		})
	})
	page, err := r.Recall(context.Background(), RecallRequest{Namespaces: []string{"project/tachyon/memory/decisions"}, Query: " embedded agents ", Tags: []string{"decision"}, Domains: []string{"memory"}, Limit: 5, Cursor: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	out := string(raw)
	for _, banned := range []string{"SESSION", "embedding_model", "provenance", "private", "user/chrispian", secret, "long body"} {
		if strings.Contains(out, banned) {
			t.Fatalf("leaked %q: %s", banned, out)
		}
	}
	if page.Returned != 1 || page.Total != 38 || !page.HasMore || page.NextCursor != "abc_DEF-1=" || page.ContentTrust != ContentTrust || page.Items[0].Key != "k1" || !strings.Contains(page.Items[0].Summary, "[REDACTED]") {
		t.Fatalf("page: %+v", page)
	}
	if f.calls[0] != "POST /v1/tesseract/lookup" || f.auth[0] != "Bearer tess-token-0123456789abcdef" {
		t.Fatalf("call: %v %v", f.calls, f.auth)
	}
	b := f.bodies[0]
	want := map[string]bool{"namespaces": true, "query": true, "tags": true, "domains": true, "limit": true, "cursor": true, "payload_mode": true}
	for k := range b {
		if !want[k] {
			t.Fatalf("unexpected body field %q: %v", k, b)
		}
	}
	if b["payload_mode"] != "summary" || b["query"] != "embedded agents" || b["limit"] != float64(5) || b["cursor"] != "c1" {
		t.Fatalf("body: %v", b)
	}
}

func TestRecallRefusesBadInputBeforeAnyNetworkCall(t *testing.T) {
	f, r := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.Write([]byte(`{"results":[],"manifest":{}}`))
	})
	ok := []string{"project/tachyon/memory/decisions"}
	bad := map[string]RecallRequest{
		"none":           {},
		"six namespaces": {Namespaces: []string{"project/a", "project/b", "project/c", "project/d", "project/e", "project/f"}},
		"uppercase":      {Namespaces: []string{"project/Tachyon"}},
		"traversal":      {Namespaces: []string{"project/../user/x"}},
		"dot segment":    {Namespaces: []string{"project/./x"}},
		"empty segment":  {Namespaces: []string{"project//x"}},
		"trailing slash": {Namespaces: []string{"project/x/"}},
		"long query":     {Namespaces: ok, Query: strings.Repeat("q", 301)},
		"nul query":      {Namespaces: ok, Query: "a\x00b"},
		"bad tag":        {Namespaces: ok, Tags: []string{"a b"}},
		"many tags":      {Namespaces: ok, Tags: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}},
		"bad domain":     {Namespaces: ok, Domains: []string{"event"}},
		"limit over":     {Namespaces: ok, Limit: MaxLimit + 1},
		"negative":       {Namespaces: ok, Limit: -1},
		"bad cursor":     {Namespaces: ok, Cursor: "has space"},
	}
	for name, q := range bad {
		if _, err := r.Recall(context.Background(), q); err != ErrInvalid {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, ns := range []string{"user/chrispian/memory", "private/x", "projects/x"} {
		if _, err := r.Recall(context.Background(), RecallRequest{Namespaces: []string{ns}}); err != ErrForbidden {
			t.Fatalf("%s: %v", ns, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("invalid or forbidden requests reached Tesseract: %v", f.calls)
	}
	if _, err := r.Recall(context.Background(), RecallRequest{Namespaces: []string{"system/memory/decisions"}}); err != nil {
		t.Fatalf("system/ should be readable: %v", err)
	}
}

func TestGetShapesBoundsAndRefusesForbiddenNamespaces(t *testing.T) {
	long := strings.Repeat("é", 10_000)
	_, r := newFake(t, func(w http.ResponseWriter, req *http.Request, _ map[string]any) {
		switch req.URL.Path {
		case "/v1/items/good":
			m := rev("good", "project/tachyon/knowledge/adr", "adr")
			m["payload"] = map[string]any{"summary": "s", "body": long + secret}
			json.NewEncoder(w).Encode(m)
		case "/v1/items/priv":
			json.NewEncoder(w).Encode(rev("priv", "user/chrispian/memory", "p"))
		case "/v1/items/other":
			json.NewEncoder(w).Encode(rev("different", "project/x", "p"))
		default:
			http.NotFound(w, req)
		}
	})
	it, err := r.Get(context.Background(), "good")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(it)
	if !it.BodyTruncated || it.BodyBytes < 20_000 || len(it.Body) > BodyBytes || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "SESSION") || it.AuthorAgent != "claude-code" || it.ContentTrust != ContentTrust {
		t.Fatalf("item: truncated=%v bytes=%d len=%d %s", it.BodyTruncated, it.BodyBytes, len(it.Body), string(raw)[:200])
	}
	if strings.ContainsRune(it.Body, '�') {
		t.Fatal("cut inside a rune")
	}
	if _, err := r.Get(context.Background(), "priv"); err != ErrForbidden {
		t.Fatalf("private namespace: %v", err)
	}
	if _, err := r.Get(context.Background(), "other"); err != ErrUnavailable {
		t.Fatalf("mismatched id: %v", err)
	}
	if _, err := r.Get(context.Background(), "missing"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	for _, id := range []string{"", "../x", "a/b", "a b", strings.Repeat("a", 65), "x?y"} {
		if _, err := r.Get(context.Background(), id); err != ErrInvalid {
			t.Fatalf("id %q: %v", id, err)
		}
	}
}

func TestUpstreamFailuresAreTypedAndDoNotLeak(t *testing.T) {
	for code, want := range map[int]error{400: ErrInvalid, 404: ErrNotFound, 401: ErrUnavailable, 500: ErrUnavailable} {
		_, r := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
			w.WriteHeader(code)
			w.Write([]byte("internal detail " + secret))
		})
		_, err := r.Get(context.Background(), "x")
		if err == nil || !strings.HasPrefix(err.Error(), want.Error()) || strings.Contains(err.Error(), "internal detail") {
			t.Fatalf("status %d: %v", code, err)
		}
	}
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	_, r := newFake(t, func(w http.ResponseWriter, req *http.Request, _ map[string]any) {
		http.Redirect(w, req, other.URL, http.StatusFound)
	})
	if _, err := r.Get(context.Background(), "x"); err == nil || hit {
		t.Fatalf("redirect followed: %v %v", err, hit)
	}
}
