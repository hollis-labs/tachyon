package forgereads

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
)

const sha = "0da8631e75cfa86e2506e6b076cceaa4a56f4aa3"

func gh(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"login":"chrispian","avatar_url":"https://a/x.png"}`)
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		fs := []map[string]any{
			{"filename": "a.go", "status": "modified", "additions": 1, "deletions": 1, "patch": "@@ -1,2 +1,2 @@ func A()\n ctx\n-old\n+new\n"},
			{"filename": "logo.png", "status": "added"},
			{"filename": "big.json", "status": "modified", "additions": 9000},
		}
		json.NewEncoder(w).Encode(fs)
	})
	mux.HandleFunc("/repos/o/r/pulls/2/files", func(w http.ResponseWriter, r *http.Request) {
		var fs []map[string]any
		if r.URL.Query().Get("page") == "1" {
			for i := 0; i < 100; i++ {
				fs = append(fs, map[string]any{"filename": fmt.Sprintf("f%03d", i), "status": "added"})
			}
		} else {
			fs = []map[string]any{{"filename": "last", "status": "added"}}
		}
		json.NewEncoder(w).Encode(fs)
	})
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"head":{"sha":%q}}`, sha)
	})
	mux.HandleFunc("/repos/o/r/commits/"+sha+"/check-runs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"check_runs":[
{"name":"lint","status":"completed","conclusion":"success","started_at":"2026-10-30T00:00:00Z","completed_at":"2026-10-30T00:01:00Z","html_url":"u1","output":{"summary":"","annotations_count":0}},
{"name":"go","status":"completed","conclusion":"failure","started_at":"2026-10-30T00:00:00Z","completed_at":"2026-10-30T00:01:00Z","html_url":"u2","output":{"summary":"1 test failed","annotations_count":3}},
{"name":"e2e","status":"in_progress","conclusion":null,"started_at":"2026-10-30T00:00:00Z","html_url":"u3","output":{}}]}`)
	})
	mux.HandleFunc("/repos/o/r/commits/"+sha+"/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"statuses":[{"context":"ci/ok","state":"success"},{"context":"ci/bad","state":"failure","target_url":"u","description":"nope"}]}`)
	})
	mux.HandleFunc("/repos/o/r/commits/"+sha, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sha":"`+sha+`","html_url":"cu","author":{"login":"dep[bot]","type":"Bot"},"commit":{"message":"m","author":{"name":"n","date":"2026-10-30T00:00:00Z"}},"parents":[{"sha":"p1"}],"files":[{},{}]}`)
	})
	mux.HandleFunc("/repos/o/r/commits/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/repos/o/r/pulls/9/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(42*time.Second).Unix()))
		w.WriteHeader(403)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func call(h *Handler, path string) (int, map[string]any) {
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, forgeapi.Prefix+path, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func data(m map[string]any) map[string]any { d, _ := m["data"].(map[string]any); return d }

func TestViewerAndCredential(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	if c, m := call(&Handler{Token: "tok", Base: s.URL}, "/viewer"); c != 200 || data(m)["login"] != "chrispian" {
		t.Fatalf("%d %v", c, m)
	}
	if c, m := call(&Handler{Base: s.URL}, "/viewer"); c != 401 || m["error"].(map[string]any)["code"] != "credential_missing" {
		t.Fatalf("%d %v", c, m)
	}
}

func TestFilesDiffLazy(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	h := &Handler{Token: "tok", Base: s.URL}
	c, m := call(h, "/repos/github/o/r/pulls/1/files")
	items := data(m)["items"].([]any)
	if c != 200 || len(items) != 3 || items[0].(map[string]any)["path"] != "a.go" || items[1].(map[string]any)["binary"] != true || items[2].(map[string]any)["truncated"] != true || data(m)["next_cursor"] != nil {
		t.Fatalf("%d %v", c, m)
	}
	c, m = call(h, "/repos/github/o/r/pulls/1/diff?path=a.go")
	items = data(m)["items"].([]any)
	hunk := items[0].(map[string]any)["hunks"].([]any)[0].(map[string]any)
	if c != 200 || len(items) != 1 || hunk["header"] != "func A()" || len(hunk["lines"].([]any)) != 3 {
		t.Fatalf("%d %v", c, m)
	}
	call(h, "/repos/github/o/r/pulls/1/diff")
	if hits != 2 { // files list (1) + diff reads once and then serves the cached list
		t.Fatalf("hits %d", hits)
	}
	if c, _ := call(h, "/repos/github/o/r/pulls/1/diff?path=nope.go"); c != 404 {
		t.Fatalf("missing path: %d", c)
	}
	// binary and withheld files carry no hunks
	_, m = call(h, "/repos/github/o/r/pulls/1/diff?path=logo.png")
	if hs := data(m)["items"].([]any)[0].(map[string]any)["hunks"].([]any); len(hs) != 0 {
		t.Fatalf("%v", m)
	}
}

func TestFilesPagination(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	h := &Handler{Token: "tok", Base: s.URL}
	_, m := call(h, "/repos/github/o/r/pulls/2/files")
	if data(m)["next_cursor"] != "2" {
		t.Fatalf("%v", m)
	}
	_, m = call(h, "/repos/github/o/r/pulls/2/files?cursor=2")
	if data(m)["next_cursor"] != nil || len(data(m)["items"].([]any)) != 1 {
		t.Fatalf("%v", m)
	}
	if c, _ := call(h, "/repos/github/o/r/pulls/2/files?cursor=x"); c != 400 {
		t.Fatalf("%d", c)
	}
}

func TestChecksFailuresFirst(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	c, m := call(&Handler{Token: "tok", Base: s.URL}, "/repos/github/o/r/pulls/1/checks")
	d := data(m)
	cs := d["checks"].([]any)
	if c != 200 || d["head_sha"] != sha || cs[0].(map[string]any)["name"] != "go" || cs[1].(map[string]any)["name"] != "e2e" || cs[2].(map[string]any)["name"] != "lint" {
		t.Fatalf("%d %v", c, m)
	}
	if cs[0].(map[string]any)["annotations_count"].(float64) != 3 || cs[1].(map[string]any)["conclusion"] != nil || cs[1].(map[string]any)["completed_ms"] != nil {
		t.Fatalf("%v", cs)
	}
	if st := d["statuses"].([]any); st[0].(map[string]any)["name"] != "ci/bad" {
		t.Fatalf("%v", st)
	}
}

func TestCommitRef(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	h := &Handler{Token: "tok", Base: s.URL}
	c, m := call(h, "/repos/github/o/r/commits/"+sha)
	d := data(m)
	if c != 200 || d["files"].(float64) != 2 || d["author"].(map[string]any)["isAgent"] != true || d["parents"].([]any)[0] != "p1" {
		t.Fatalf("%d %v", c, m)
	}
	if c, _ := call(h, "/repos/github/o/r/commits/gone"); c != 404 {
		t.Fatalf("%d", c)
	}
	if c, _ := call(h, "/repos/github/o/r/commits/a%2F..%2Fb"); c != 400 && c != 404 {
		t.Fatalf("%d", c)
	}
}

func TestRateLimitAndHostAndBadArgs(t *testing.T) {
	var hits int
	s := gh(t, &hits)
	h := &Handler{Token: "tok", Base: s.URL}
	c, m := call(h, "/repos/github/o/r/pulls/9/files")
	e := m["error"].(map[string]any)
	if c != 429 || e["code"] != "rate_limited" || e["retry_after_s"].(float64) < 40 {
		t.Fatalf("%d %v", c, m)
	}
	if c, _ := call(h, "/repos/gitlab/o/r/pulls/1/files"); c != 501 {
		t.Fatalf("host %d", c)
	}
	if c, _ := call(h, "/repos/github/o/r/pulls/0/files"); c != 400 {
		t.Fatalf("num %d", c)
	}
	if c, _ := call(h, "/repos/github/o/..%2Fx/pulls/1/files"); c != 400 && c != 404 {
		t.Fatalf("owner %d", c)
	}
}
