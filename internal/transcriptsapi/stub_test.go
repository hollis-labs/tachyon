package transcriptsapi

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func get(path string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://x"+Prefix+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return m
}

func code(t *testing.T, w *httptest.ResponseRecorder) string {
	c, _ := decode(t, w)["code"].(string)
	return c
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|github_pat_|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	_ = fs.WalkDir(assets, "fixtures", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		var v any
		if json.Unmarshal(b, &v) != nil {
			t.Errorf("%s is not JSON", p)
		}
		if m := secret.Find(b); m != nil {
			t.Errorf("%s looks like it holds a secret: %q", p, m)
		}
		n++
		return nil
	})
	if n == 0 {
		t.Fatal("no fixtures")
	}
}

func TestSnapshotCoversEverySourceState(t *testing.T) {
	seen := map[string]bool{}
	for sc, want := range map[string]string{"": "live", "cached": "cached", "stale": "stale", "unavailable": "unavailable", "partial": "live", "empty": "live"} {
		w := get("/snapshot", "X-Stub-Scenario", sc)
		if w.Code != 200 {
			t.Fatalf("%s: %d", sc, w.Code)
		}
		m := decode(t, w)
		meta := m["snapshot_meta"].(map[string]any)
		seen[meta["source_state"].(string)] = true
		if meta["source_state"] != want {
			t.Errorf("%s: %v", sc, meta["source_state"])
		}
		sessions := m["sessions"].([]any)
		switch sc {
		case "unavailable":
			if meta["error"] == nil || len(sessions) == 0 {
				t.Error("unavailable keeps data and carries the error")
			}
		case "partial":
			if m["complete"] != false || meta["omitted_count"].(float64) != 1 {
				t.Errorf("partial: %v", meta)
			}
		case "empty":
			if len(sessions) != 0 || meta["total_available"].(float64) != 0 {
				t.Error("empty")
			}
		case "":
			if meta["omitted_count"].(float64) != 1 || meta["total_available"].(float64) != 5 || len(sessions) != 4 {
				t.Errorf("live: %v", meta)
			}
		}
	}
	for _, s := range SourceStates {
		if !seen[s] {
			t.Errorf("no fixture for source state %s", s)
		}
	}
	if get("/snapshot", "X-Stub-Scenario", "nope").Code != 400 || get("/snapshot?x=1").Code != 400 {
		t.Error("unknown scenario or parameter accepted")
	}
}

func TestDetailToolCardsPagingAndDeepLink(t *testing.T) {
	w := get("/sessions/" + SessionClaude)
	m := decode(t, w)
	turns := m["turns"].([]any)
	if len(turns) != 50 || m["next_turn"].(float64) != 50 || m["turn_total"].(float64) != 60 {
		t.Fatalf("page 1: %d next=%v", len(turns), m["next_turn"])
	}
	statuses, targets, excerpts := map[string]bool{}, 0, 0
	m2 := decode(t, get("/sessions/"+SessionClaude+"?turn_limit=200"))
	for _, tn := range m2["turns"].([]any) {
		for _, c := range tn.(map[string]any)["tool_calls"].([]any) {
			c := c.(map[string]any)
			statuses[c["status"].(string)] = true
			if c["target"] != nil {
				targets++
			}
			if c["error_excerpt"] != nil {
				excerpts++
			}
		}
	}
	for _, s := range ToolStatuses {
		if !statuses[s] {
			t.Errorf("no tool call with status %s", s)
		}
	}
	if targets == 0 || excerpts != 2 {
		t.Errorf("targets=%d excerpts=%d", targets, excerpts)
	}
	p2 := decode(t, get("/sessions/"+SessionClaude+"?turn_offset=50"))
	if len(p2["turns"].([]any)) != 10 || p2["next_turn"] != nil {
		t.Error("last page")
	}
	jump := decode(t, get("/sessions/"+SessionClaude+"?turn_from_seq=9&turn_limit=3"))
	if jump["turns"].([]any)[0].(map[string]any)["seq"].(float64) != 9 {
		t.Error("turn_from_seq")
	}
	if decode(t, get("/sessions/"+SessionCodex))["codex_errors_are_heuristic"] != true || decode(t, get("/sessions/"+SessionChatGPT))["no_tool_data"] != true {
		t.Error("codex heuristic and chatgpt no-tool-data markers")
	}
	if w := get("/sessions/" + SessionUnknown); w.Code != 404 || code(t, w) != "not_found" {
		t.Error("unknown id")
	}
	if get("/sessions/xyz").Code != 404 && get("/sessions/xyz").Code != 405 {
		t.Error("bad id")
	}
	for _, bad := range []string{"?turn_limit=0", "?turn_limit=201", "?turn_offset=-1", "?turn_offset=1000001", "?x=1", "?turn_offset=a"} {
		if get("/sessions/"+SessionClaude+bad).Code != 400 {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSearchModesNoticesAndValidation(t *testing.T) {
	for _, mode := range Modes {
		w := get("/search?q=forge&mode=" + mode)
		m := decode(t, w)
		if w.Code != 200 || m["mode"] != mode || len(m["items"].([]any)) == 0 {
			t.Errorf("%s: %d", mode, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("search must not be cached")
		}
	}
	if decode(t, get("/search?q=a&mode=semantic"))["notice"].(map[string]any)["code"] != "summary_vectors_only" {
		t.Error("semantic is summary-level and says so")
	}
	if decode(t, get("/search?q=a", "X-Stub-Scenario", "semantic_unavailable"))["notice"].(map[string]any)["code"] != "semantic_unavailable" {
		t.Error("semantic_unavailable notice")
	}
	r := decode(t, get("/search?q=a", "X-Stub-Scenario", "ranges"))["items"].([]any)[0].(map[string]any)["match"].(map[string]any)
	if r["turn_offset"] == nil || r["highlight"].(map[string]any)["format"] != "ranges" {
		t.Error("stage 2 ranges and turn offset")
	}
	if len(decode(t, get("/search?q=zzz", "X-Stub-Scenario", "empty"))["items"].([]any)) != 0 {
		t.Error("empty")
	}
	if decode(t, get("/search?q=a"))["next_cursor"] != "c2" || decode(t, get("/search?q=a&cursor=c2"))["next_cursor"] != nil {
		t.Error("cursor")
	}
	for _, bad := range []string{"", "?q=", "?q=" + strings.Repeat("x", 257), "?q=a&mode=fuzzy", "?q=a&limit=0", "?q=a&limit=51", "?q=a&x=1", "?q=a&has_error=maybe", "?q=a&cursor=zz"} {
		if w := get("/search" + bad); w.Code != 400 || code(t, w) != "invalid_args" {
			t.Errorf("%q: %d", bad, w.Code)
		}
	}
	if get("/search?q=a&tool=Bash&model=m&kind=claude&project=p&tag=t&since=1&until=2&has_error=true").Code != 200 {
		t.Error("filters")
	}
}

func TestFindErrorsStatsStatusAndGuards(t *testing.T) {
	f := decode(t, get("/sessions/"+SessionClaude+"/find?q=forge"))
	if f["total"].(float64) != 3 || get("/sessions/"+SessionClaude+"/find").Code != 400 {
		t.Error("find")
	}
	if decode(t, get("/sessions/"+SessionCodex+"/find?q=x"))["total"].(float64) != 0 {
		t.Error("find empty")
	}
	if e := decode(t, get("/sessions/"+SessionClaude+"/errors")); e["total"].(float64) != 2 {
		t.Error("errors")
	}
	if get("/sessions/"+SessionUnknown+"/errors").Code != 404 {
		t.Error("errors unknown id")
	}
	if decode(t, get("/stats?tool=Bash"))["source"] != "database" || get("/stats?x=1").Code != 400 {
		t.Error("stats")
	}
	if s := decode(t, get("/status")); len(s["modes"].([]any)) != 4 || s["ingest"] == nil {
		t.Error("status")
	}
	if d := decode(t, get("/status", "X-Stub-Scenario", "degraded")); d["semantic"] != false || len(d["modes"].([]any)) != 2 {
		t.Error("degraded status")
	}
	if w := get("/snapshot", "Authorization", "Bearer x"); w.Code != 403 || code(t, w) != "forbidden" {
		t.Error("agents are refused")
	}
	if w := get("/snapshot", "X-Stub-Status", "429"); w.Code != 429 || code(t, w) != "rate_limited" {
		t.Error("forced status")
	}
	if get("/openapi.yaml").Code != 200 || get("/nope").Code != 404 {
		t.Error("openapi or unknown route")
	}
	r := httptest.NewRequest("POST", "http://x"+Prefix+"/snapshot", nil)
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	if w.Code != 405 {
		t.Errorf("writes: %d", w.Code)
	}
}
