package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

func TestParseUnifiedPatch(t *testing.T) {
	patch := "@@ -10,3 +10,4 @@ func demo() {\n context line\n-old line\n+new line 1\n+new line 2"
	hunks := parseUnifiedPatch(patch)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 10 || h.OldLines != 3 || h.NewStart != 10 || h.NewLines != 4 {
		t.Fatalf("unexpected hunk header: %+v", h)
	}
	if len(h.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(h.Lines))
	}
	if h.Lines[0].Kind != "context" || *h.Lines[0].OldLine != 10 || *h.Lines[0].NewLine != 10 {
		t.Fatalf("unexpected context line: %+v", h.Lines[0])
	}
	if h.Lines[1].Kind != "del" || *h.Lines[1].OldLine != 11 || h.Lines[1].NewLine != nil {
		t.Fatalf("unexpected del line: %+v", h.Lines[1])
	}
	if h.Lines[2].Kind != "add" || h.Lines[2].OldLine != nil || *h.Lines[2].NewLine != 11 {
		t.Fatalf("unexpected add line 1: %+v", h.Lines[2])
	}
	if h.Lines[3].Kind != "add" || h.Lines[3].OldLine != nil || *h.Lines[3].NewLine != 12 {
		t.Fatalf("unexpected add line 2: %+v", h.Lines[3])
	}
}

func TestForgeHandlerEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"number": 410, "repository_url": "https://api.github.com/repos/hollis-labs/tether"},
			},
		})
	})
	mux.HandleFunc("/repos/hollis-labs/tether/pulls/410", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":          410,
			"title":           "ci: enforce changelog fragments (CW-20261016-0159)",
			"state":           "open",
			"user":            map[string]any{"login": "chrispian"},
			"assignees":       []any{},
			"base":            map[string]any{"ref": "main"},
			"head":            map[string]any{"ref": "feat/changelog", "sha": "abc1234"},
			"mergeable_state": "clean",
			"additions":       15,
			"deletions":       2,
			"changed_files":   1,
			"created_at":      "2026-10-16T12:00:00Z",
			"updated_at":      time.Now().UTC().Format(time.RFC3339),
			"html_url":        "https://github.com/hollis-labs/tether/pull/410",
		})
	})
	mux.HandleFunc("/repos/hollis-labs/tether/pulls/410/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/repos/hollis-labs/tether/commits/abc1234/check-runs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"check_runs": []map[string]any{
				{"id": 1, "name": "test", "status": "completed", "conclusion": "failure"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	fh := NewForgeHandler("test-token", "hollis-labs")
	fh.baseURL = srv.URL

	resp := fh.Handle(context.Background(), subprocess.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/api/plugins/console/v1/forge/pulls/pending",
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Status, string(resp.Body))
	}
	var env struct {
		Data struct {
			Items []pendingPrSummary `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data.Items) != 1 {
		t.Fatalf("expected 1 pending PR, got %d", len(env.Data.Items))
	}
	got := env.Data.Items[0]
	if got.Ref.Number != 410 || got.ChecksSummary == nil || got.ChecksSummary.Failed != 1 {
		t.Fatalf("unexpected pending PR summary: %+v", got)
	}
}

func TestForgeMerge(t *testing.T) {
	const sha = "0123456789012345678901234567890123456789"
	for _, tc := range []struct {
		name, body     string
		upstream, want int
		merged         bool
		calls          int
	}{
		{"merge", `{"method":"merge","headSha":"` + sha + `"}`, 200, 200, true, 1},
		{"squash", `{"method":"squash","headSha":"` + sha + `"}`, 200, 200, true, 1},
		{"rebase", `{"method":"rebase","headSha":"` + sha + `"}`, 200, 200, true, 1},
		{"missing sha", `{"method":"merge"}`, 200, 400, true, 0},
		{"invalid method", `{"method":"force","headSha":"` + sha + `"}`, 200, 400, true, 0},
		{"malformed", `{`, 200, 400, true, 0},
		{"changed head", `{"method":"merge","headSha":"` + sha + `"}`, 409, 409, false, 1},
		{"rules blocked", `{"method":"merge","headSha":"` + sha + `"}`, 405, 409, false, 1},
		{"permission denied", `{"method":"merge","headSha":"` + sha + `"}`, 403, 403, false, 1},
		{"unconfirmed", `{"method":"merge","headSha":"` + sha + `"}`, 200, 409, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/repos/owner/repo/pulls/42/merge" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var payload map[string]string
				_ = json.NewDecoder(r.Body).Decode(&payload)
				var input map[string]string
				_ = json.Unmarshal([]byte(tc.body), &input)
				if payload["sha"] != sha || payload["merge_method"] != input["method"] {
					t.Errorf("incorrect payload: %v", payload)
				}
				w.WriteHeader(tc.upstream)
				_ = json.NewEncoder(w).Encode(map[string]any{"merged": tc.merged, "sha": sha, "message": "merge result"})
			}))
			defer upstream.Close()
			f := NewForgeHandler("test-token", "owner")
			f.baseURL = upstream.URL
			f.pendingAt = time.Now()
			res := f.Handle(context.Background(), subprocess.HTTPRequest{Method: "PUT", Path: forgePathPrefix + "/repos/github/owner/repo/pulls/42/merge", Body: []byte(tc.body)})
			if res.Status != tc.want || calls != tc.calls {
				t.Fatalf("status %d, calls %d; want %d, %d: %s", res.Status, calls, tc.want, tc.calls, res.Body)
			}
			if tc.want == 200 && !f.pendingAt.IsZero() {
				t.Fatal("pending list not invalidated")
			}
		})
	}
}
