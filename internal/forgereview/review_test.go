package forgereview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	sha1 = "0da8631e75cfa86e2506e6b076cceaa4a56f4aa3"
	sha2 = "b9bde5dfabfb122d47d9d8b15ed443be34836f3a"
	pr   = "/api/plugins/console/v1/forge/repos/github/o/r/pulls/7"
)

const patch = "@@ -10,3 +10,4 @@ func x()\n a\n-b\n+b2\n+c\n d"

type gh struct {
	head    string
	reviews []map[string]any
	posts   atomic.Int32
	last    map[string]any
	status  int
}

func (g *gh) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(p, "/pulls/7/reviews"):
			g.posts.Add(1)
			g.last = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&g.last)
			if g.status != 0 {
				w.WriteHeader(g.status)
				_, _ = w.Write([]byte(`{"message":"Can not approve your own pull request"}`))
				return
			}
			rv := map[string]any{"id": 9001, "body": g.last["body"], "state": "COMMENTED"}
			g.reviews = append(g.reviews, rv)
			_ = json.NewEncoder(w).Encode(rv)
		case strings.HasSuffix(p, "/pulls/7/reviews"):
			_ = json.NewEncoder(w).Encode(g.reviews)
		case strings.HasSuffix(p, "/comments"):
			_, _ = w.Write([]byte(`[{"id":11},{"id":12}]`))
		case strings.HasSuffix(p, "/pulls/7/files"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"filename": "a.go", "patch": patch}})
		case strings.HasSuffix(p, "/pulls/7"):
			_, _ = w.Write([]byte(`{"state":"open","head":{"sha":"` + g.head + `"}}`))
		default:
			t.Error("unexpected path", r.Method, p)
			w.WriteHeader(500)
		}
	}))
}

func setup(t *testing.T) (*gh, http.Handler, func()) {
	g := &gh{head: sha1}
	srv := g.server(t)
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Token: "tok", Base: srv.URL, DB: db}
	return g, h.Mux(), func() { srv.Close(); db.Close() }
}

func call(h http.Handler, method, path, body, key string) (int, map[string]any) {
	r := httptest.NewRequest(method, pr+path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestValidateFlagsOutdatedAndInvalidLines(t *testing.T) {
	g, h, done := setup(t)
	defer done()
	body := func(head string) string {
		return `{"head_sha":"` + head + `","comments":[
 {"path":"a.go","line":11,"side":"RIGHT","body":"new line"},
 {"path":"a.go","line":11,"side":"LEFT","body":"old line 11 is context"},
 {"path":"a.go","line":11,"side":"LEFT","body":"x"},
 {"path":"a.go","line":99,"side":"RIGHT","body":"off the diff"},
 {"path":"missing.go","line":1,"side":"RIGHT","body":"no such file"},
 {"path":"a.go","line":12,"side":"BOTH","body":"bad side"}]}`
	}
	s, out := call(h, "POST", "/reviews/validate", body(sha1), "")
	d := out["data"].(map[string]any)
	if s != 200 || d["head_moved"] != false || d["head_sha_current"] != sha1 {
		t.Fatal(s, out)
	}
	var got []string
	for _, c := range d["comments"].([]any) {
		got = append(got, c.(map[string]any)["status"].(string))
	}
	if strings.Join(got, ",") != "ok,ok,ok,invalid_line,invalid_line,invalid_line" {
		t.Fatal(got)
	}
	g.head = sha2
	_, out = call(h, "POST", "/reviews/validate", body(sha1), "")
	d = out["data"].(map[string]any)
	if d["head_moved"] != true || d["head_sha_current"] != sha2 || d["comments"].([]any)[0].(map[string]any)["status"] != "outdated" || d["comments"].([]any)[3].(map[string]any)["status"] != "invalid_line" {
		t.Fatal(out)
	}
	if s, _ := call(h, "POST", "/reviews/validate", `{"head_sha":"nope","comments":[]}`, ""); s != 400 {
		t.Fatal(s)
	}
}

func TestSubmitIsOneReviewPinnedToTheHeadAndIdempotent(t *testing.T) {
	g, h, done := setup(t)
	defer done()
	body := `{"verdict":"request_changes","body":"Please fix.","head_sha":"` + sha1 + `","comments":[{"path":"a.go","line":11,"side":"RIGHT","body":"nit"}]}`
	if s, out := call(h, "POST", "/reviews", body, ""); s != 400 || out["error"].(map[string]any)["code"] != "idempotency_key_required" {
		t.Fatal(s, out)
	}
	s, out := call(h, "POST", "/reviews", body, "k1")
	d, _ := out["data"].(map[string]any)
	if s != 201 || d["review_id"] != "9001" || d["state"] != "changes_requested" || len(d["comment_ids"].([]any)) != 2 {
		t.Fatal(s, out)
	}
	if g.last["commit_id"] != sha1 || g.last["event"] != "REQUEST_CHANGES" || len(g.last["comments"].([]any)) != 1 ||
		!strings.HasPrefix(g.last["body"].(string), "Please fix.") || !strings.Contains(g.last["body"].(string), "<!-- tachyon:idem=k1 -->") {
		t.Fatal("one review with the pin, the event, the comments and the marker", g.last)
	}
	// A retry with the same key finds the earlier review; nothing is posted again, even if the head has moved.
	g.head = sha2
	s, out = call(h, "POST", "/reviews", body, "k1")
	if d = out["data"].(map[string]any); s != 200 || d["replayed"] != true || d["review_id"] != "9001" || g.posts.Load() != 1 {
		t.Fatal("retry", s, out, g.posts.Load())
	}
	// A new key against the moved head is refused with the current head.
	if s, out = call(h, "POST", "/reviews", body, "k2"); s != 409 || out["error"].(map[string]any)["code"] != "head_moved" || out["error"].(map[string]any)["head_sha"] != sha2 || g.posts.Load() != 1 {
		t.Fatal(s, out)
	}
}

func TestSubmitValidatesAndMapsGitHubRefusals(t *testing.T) {
	g, h, done := setup(t)
	defer done()
	for _, bad := range []string{
		`{"verdict":"nope","head_sha":"` + sha1 + `"}`,
		`{"verdict":"comment","head_sha":"` + sha1 + `"}`,
		`{"verdict":"approve","head_sha":"short"}`,
		`{"verdict":"comment","body":"x","head_sha":"` + sha1 + `","comments":[{"path":"a.go","line":0,"side":"RIGHT","body":"x"}]}`,
		`{"verdict":"comment","body":"x","head_sha":"` + sha1 + `","extra":1}`,
	} {
		if s, out := call(h, "POST", "/reviews", bad, "k"); s != 400 {
			t.Fatal("bad review allowed", bad, s, out)
		}
	}
	if g.posts.Load() != 0 {
		t.Fatal("an invalid review reached GitHub")
	}
	g.status = 422
	if s, out := call(h, "POST", "/reviews", `{"verdict":"approve","head_sha":"`+sha1+`"}`, "k3"); s != 422 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "own pull request") {
		t.Fatal(s, out)
	}
}

func TestDraftRoundTrip(t *testing.T) {
	_, h, done := setup(t)
	defer done()
	if s, _ := call(h, "GET", "/review-draft", "", ""); s != 404 {
		t.Fatal("no draft yet", s)
	}
	put := `{"head_sha":"` + sha1 + `","draft":{"verdict":"comment","body":"WIP","comments":[{"path":"a.go","line":11,"side":"RIGHT","body":"Nit"}]}}`
	if s, out := call(h, "PUT", "/review-draft", put, ""); s != 200 || out["data"].(map[string]any)["pr_key"] != "github:o/r#7" {
		t.Fatal(s, out)
	}
	s, out := call(h, "GET", "/review-draft", "", "")
	d := out["data"].(map[string]any)
	if s != 200 || d["head_sha"] != sha1 || d["draft"].(map[string]any)["body"] != "WIP" || d["updated_ms"].(float64) == 0 {
		t.Fatal(s, out)
	}
	if s, _ := call(h, "PUT", "/review-draft", `{"head_sha":"x","draft":{}}`, ""); s != 400 {
		t.Fatal("bad draft allowed", s)
	}
	if s, out := call(h, "DELETE", "/review-draft", "", ""); s != 200 || out["data"].(map[string]any)["deleted"] != true {
		t.Fatal(s, out)
	}
	if s, _ := call(h, "GET", "/review-draft", "", ""); s != 404 {
		t.Fatal("draft not deleted", s)
	}
}
