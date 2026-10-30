package approvalsapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

func do(t *testing.T, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://x"+Prefix+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	Stub{}.Handler().ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
}

func TestPrefixMatchesTheRealInbox(t *testing.T) {
	if Prefix != approvals.Prefix {
		t.Fatalf("%s != %s", Prefix, approvals.Prefix)
	}
	for _, r := range Routes {
		if strings.Contains(r, "/session") || strings.Contains(r, "/status") || strings.Contains(r, "elevate") {
			t.Errorf("stub must not take over %s", r)
		}
	}
}

func TestFixturesAreJSONAndHoldNoSecrets(t *testing.T) {
	secret := regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN|api[_-]?key["']?\s*[:=]|password["']?\s*[:=]|AKIA[0-9A-Z]{12,})`)
	n := 0
	err := fs.WalkDir(assets, "fixtures", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		var v any
		if json.Unmarshal(b, &v) != nil {
			t.Errorf("%s is not valid JSON", p)
		}
		if m := secret.Find(b); m != nil {
			t.Errorf("%s looks like it holds a secret: %q", p, m)
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatal("no fixtures", err)
	}
}

// The shared table is the agreement between this function and the UI's.
func TestEligibilityMatchesTheSharedTable(t *testing.T) {
	var tab struct {
		Whitelist []string `json:"whitelist"`
		Order     []string `json:"order"`
		Cases     []struct {
			Name      string   `json:"name"`
			Subject   Subject  `json:"subject"`
			Batchable bool     `json:"batchable"`
			Blockers  []string `json:"blockers"`
		} `json:"cases"`
	}
	if !load("eligibility_table", &tab) || len(tab.Cases) < 20 {
		t.Fatal("table missing")
	}
	if !reflect.DeepEqual(tab.Whitelist, slices.Sorted(slices.Values(BatchOps))) || !reflect.DeepEqual(tab.Order, BlockerOrder) {
		t.Errorf("whitelist or order drifted: %v %v", tab.Whitelist, tab.Order)
	}
	for _, c := range tab.Cases {
		got := Eligibility(c.Subject)
		if len(got) != len(c.Blockers) || len(got) > 0 && !reflect.DeepEqual(got, c.Blockers) {
			t.Errorf("%s: got %v want %v", c.Name, got, c.Blockers)
		}
		if (len(got) == 0) != c.Batchable {
			t.Errorf("%s: batchable %v", c.Name, c.Batchable)
		}
	}
	// A setting can add ops but never delete, archive or run.
	s := Subject{Kind: "approval", Class: "low", State: "presented", OperatorAccess: true, EffectOps: []string{"rename"}}
	if len(Eligibility(s)) == 0 || len(Eligibility(s, "rename")) != 0 {
		t.Error("batch_ops addition not honored")
	}
	s.EffectOps = []string{"delete"}
	if len(Eligibility(s, "delete")) == 0 {
		t.Error("delete must never be batchable")
	}
}

// Every pending fixture item carries the flags its own facts produce.
func TestPendingFixturesAgreeWithEligibility(t *testing.T) {
	var d struct {
		Pending []item `json:"pending"`
	}
	load("inbox", &d)
	seen := map[string]bool{}
	for _, it := range d.Pending {
		snap := it["request_snapshot"].(map[string]any)
		var ops []string
		for _, e := range snap["effects"].([]any) {
			ops = append(ops, e.(map[string]any)["op"].(string))
		}
		reasons := map[string]bool{}
		for _, r := range it["risk_reasons"].([]any) {
			reasons[r.(string)] = true
		}
		rev := int64(it["server_now_ms"].(float64))
		exp, _ := it["expires_at"].(string)
		sub := Subject{Kind: str(it, "kind"), Class: str(it, "risk"), Interactive: reasons["interactive"], HasSecret: reasons["secret_field"],
			CommandExec: reasons["command_exec"], Destructive: it["destructive"] != nil, EffectOps: ops, State: str(it, "state"),
			Expired: exp != "" && exp <= "2026-10-30T03:00:00Z" && rev > 0, Deferred: it["deferred_until"] != nil, OperatorAccess: true}
		want := Eligibility(sub)
		var got []string
		for _, b := range it["batch_blockers"].([]any) {
			got = append(got, b.(string))
		}
		if len(got) != len(want) || len(want) > 0 && !reflect.DeepEqual(got, want) || it["batchable"] != (len(want) == 0) {
			t.Errorf("%v: fixture %v batchable=%v, rule %v", it["item_id"], got, it["batchable"], want)
		}
		seen[str(it, "group")] = true
	}
	for _, g := range Groups {
		if g != "info" && !seen[g] { // info shares the attention fixture's group "notice"
			t.Errorf("no pending fixture for group %s", g)
		}
	}
}

func TestInboxProjectionOrderingAndFilters(t *testing.T) {
	var d struct {
		Pending []item         `json:"pending"`
		History []item         `json:"history"`
		Counts  map[string]any `json:"counts"`
	}
	decode(t, do(t, "GET", "/inbox", ""), &d)
	if len(d.Pending) < 10 || len(d.History) < 5 {
		t.Fatalf("thin fixtures: %d/%d", len(d.Pending), len(d.History))
	}
	if d.Pending[len(d.Pending)-1]["item_id"] != "ap_deferred" {
		t.Errorf("deferred item must sort last, got %v", d.Pending[len(d.Pending)-1]["item_id"])
	}
	if d.Pending[0]["item_id"] != "ap_change_c" {
		t.Errorf("soonest expiry first, got %v", d.Pending[0]["item_id"])
	}
	decode(t, do(t, "GET", "/inbox?group=destructive&status=pending", ""), &d)
	if len(d.Pending) != 2 || len(d.History) != 0 {
		t.Errorf("filter: %d pending %d history", len(d.Pending), len(d.History))
	}
	decode(t, do(t, "GET", "/inbox?batchable=1", ""), &d)
	if len(d.Pending) != 3 {
		t.Errorf("batchable filter: %d", len(d.Pending))
	}
	if do(t, "GET", "/inbox?order=nope", "").Code != 400 {
		t.Error("bad order accepted")
	}
}

func TestDestructiveAndHighClassNeedTheTypedPhrase(t *testing.T) {
	var med, high item
	decode(t, do(t, "GET", "/items/ap_destructive_med", ""), &med)
	decode(t, do(t, "GET", "/items/ap_destructive_high", ""), &high)
	if med["risk"] != "medium" || med["destructive"] == nil || len(med["alternatives"].([]any)) != 1 || med["affected_total"].(float64) != 3 {
		t.Errorf("medium destructive fixture: %v", med)
	}
	if high["risk"] != "high" || high["destructive"] == nil || high["confirm"] == nil || high["provider_handoff"] != nil {
		t.Errorf("high destructive fixture: %v", high)
	}
	body := func(rev int, resp string) string {
		return `{"expected_revision":` + string(rune('0'+rev)) + `,"response":` + resp + `}`
	}
	w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"approved"}`))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "confirmation_required") {
		t.Errorf("no phrase: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"approved","confirmation":"delete 3 items"}`)); w.Code != 422 {
		t.Errorf("phrase is case-sensitive: %d", w.Code)
	}
	if w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"approved","confirmation":"  DELETE 3 ITEMS "}`)); w.Code != 200 {
		t.Errorf("trimmed exact phrase: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"approved","alternative_id":"archive"}`)); w.Code != 200 || !strings.Contains(w.Body.String(), `"alternative_id":"archive"`) {
		t.Errorf("archive instead: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"denied"}`)); w.Code != 422 || !strings.Contains(w.Body.String(), "reason_required") {
		t.Errorf("deny needs a reason: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/items/ap_destructive_med/resolve", body(1, `{"decision":"approved","confirmation":"DELETE 3 ITEMS"}`), "X-Stub-Scenario", "not_elevated"); w.Code != 403 {
		t.Errorf("destructive needs operator access: %d", w.Code)
	}
	// Addendum 2: class high resolves in the browser with the typed phrase.
	for _, c := range []struct{ id, phrase string }{{"ap_destructive_high", "PURGE TRANSCRIPT STORE"}, {"ap_high_action", "RUN DEPLOY PRODUCTION"}, {"ap_high_cerberus", "RESTART GATEWAY"}} {
		if w := do(t, "POST", "/items/"+c.id+"/resolve", body(1, `{"decision":"approved"}`)); w.Code != 422 || !strings.Contains(w.Body.String(), "confirmation_required") {
			t.Errorf("%s without phrase: %d %s", c.id, w.Code, w.Body)
		}
		if w := do(t, "POST", "/items/"+c.id+"/resolve", body(1, `{"decision":"approved","confirmation":"`+strings.ToLower(c.phrase)+`"}`)); w.Code != 422 {
			t.Errorf("%s wrong case: %d", c.id, w.Code)
		}
		if w := do(t, "POST", "/items/"+c.id+"/resolve", body(1, `{"decision":"approved","confirmation":"`+c.phrase+`"}`)); w.Code != 200 {
			t.Errorf("%s with phrase: %d %s", c.id, w.Code, w.Body)
		}
		if w := do(t, "POST", "/items/"+c.id+"/resolve", body(1, `{"decision":"approved","confirmation":"`+c.phrase+`"}`), "X-Stub-Scenario", "not_elevated"); w.Code != 403 {
			t.Errorf("%s needs operator access: %d", c.id, w.Code)
		}
	}
	if w := do(t, "POST", "/items/ap_high_cerberus/resolve", body(1, `{"decision":"denied"}`)); w.Code != 200 {
		t.Errorf("deny needs no phrase: %d %s", w.Code, w.Body)
	}
	var cer item
	decode(t, do(t, "GET", "/items/ap_high_cerberus", ""), &cer)
	if src := cer["request_snapshot"].(map[string]any)["source"].(map[string]any)["app"]; src != "cerberus" {
		t.Errorf("cerberus fixture source: %v", src)
	}
	for _, it := range []string{"ap_cmd_deploy"} {
		var m item
		decode(t, do(t, "GET", "/items/"+it, ""), &m)
		if m["confirm"] != nil {
			t.Errorf("%s: medium non-destructive items need no phrase", it)
		}
	}
}

func TestResolveStaleAndConflict(t *testing.T) {
	w := do(t, "POST", "/items/ap_change_b/resolve", `{"expected_revision":1,"response":{"decision":"approved"}}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"revision":4`) || !strings.Contains(w.Body.String(), "stale_revision") {
		t.Errorf("stale: %d %s", w.Code, w.Body)
	}
	w = do(t, "POST", "/items/ap_h_approved/resolve", `{"expected_revision":3,"response":{"decision":"approved"}}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "already_resolved") || !strings.Contains(w.Body.String(), "res_01") {
		t.Errorf("conflict carries the outcome: %d %s", w.Code, w.Body)
	}
	if do(t, "POST", "/items/ap_change_a/resolve", `{"expected_revision":1,"response":{"decision":"approved"},"surprise":1}`).Code != 400 {
		t.Error("unknown field accepted")
	}
	if do(t, "POST", "/items/ap_nope/resolve", `{"expected_revision":1,"response":{"decision":"approved"}}`).Code != 404 {
		t.Error("unknown item")
	}
	if w := do(t, "POST", "/items/ap_secret/resolve", `{"expected_revision":1,"response":{"decision":"approved","answers":{"deploy_token":"typed-by-hand"}}}`, "X-Stub-Scenario", ""); w.Code != 422 || !strings.Contains(w.Body.String(), "invalid_secret_ref") {
		t.Errorf("free-typed secret: %d %s", w.Code, w.Body)
	}
	if w := do(t, "POST", "/items/ap_secret/resolve", `{"expected_revision":1,"response":{"decision":"approved","answers":{"deploy_token":"secret://tachyon/sec_01hqexample"}}}`); w.Code != 200 {
		t.Errorf("secret ref: %d %s", w.Code, w.Body)
	}
}

func TestBatchMixedIdempotentAndGuarded(t *testing.T) {
	req := `{"batch_id":"11111111-2222-4333-8444-555555555555","decision":"approved","items":[
	 {"item_id":"ap_change_a","expected_revision":1},{"item_id":"ap_change_b","expected_revision":1},
	 {"item_id":"ap_cmd_deploy","expected_revision":2},{"item_id":"ap_h_expired","expected_revision":2},
	 {"item_id":"ap_h_approved","expected_revision":3},{"item_id":"ap_change_c","expected_revision":1}]}`
	var res struct {
		Results []struct{ ItemID, Status string } `json:"results"`
		Counts  map[string]int                    `json:"counts"`
	}
	w := do(t, "POST", "/batch/resolve", req, "X-Stub-Fail-Item", "ap_change_c")
	decode(t, w, &res)
	want := []string{"resolved", "stale", "ineligible", "expired", "conflict", "error"}
	for i, r := range res.Results {
		if r.Status != want[i] {
			t.Errorf("%s: %s want %s", r.ItemID, r.Status, want[i])
		}
	}
	var fix struct {
		Counts map[string]int `json:"counts"`
	}
	load("batch_mixed", &fix)
	if !reflect.DeepEqual(fix.Counts, res.Counts) {
		t.Errorf("batch_mixed fixture counts %v differ from the computed %v", fix.Counts, res.Counts)
	}
	again := do(t, "POST", "/batch/resolve", req, "X-Stub-Fail-Item", "ap_change_c")
	if again.Header().Get("X-Tachyon-Batch-Replay") != "1" || again.Body.String() != w.Body.String() {
		t.Error("same batch_id and body must replay the stored response")
	}
	other := strings.Replace(req, `"approved"`, `"denied"`, 1)
	if w := do(t, "POST", "/batch/resolve", other); w.Code != 409 || !strings.Contains(w.Body.String(), "batch_id_reuse") {
		t.Errorf("reuse: %d %s", w.Code, w.Body)
	}
	for name, body := range map[string]string{
		"empty":     `{"batch_id":"b1","decision":"approved","items":[]}`,
		"decision":  `{"batch_id":"b2","decision":"maybe","items":[{"item_id":"ap_change_a","expected_revision":1}]}`,
		"duplicate": `{"batch_id":"b3","decision":"approved","items":[{"item_id":"ap_change_a","expected_revision":1},{"item_id":"ap_change_a","expected_revision":1}]}`,
		"no id":     `{"decision":"approved","items":[{"item_id":"ap_change_a","expected_revision":1}]}`,
	} {
		if w := do(t, "POST", "/batch/resolve", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	var many strings.Builder
	many.WriteString(`{"batch_id":"b4","decision":"approved","items":[`)
	for i := 0; i < MaxBatchItems+1; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"item_id":"ap_x` + string(rune('a'+i)) + `","expected_revision":1}`)
	}
	many.WriteString("]}")
	if w := do(t, "POST", "/batch/resolve", many.String()); w.Code != 400 {
		t.Errorf("26 items: %d", w.Code)
	}
	if w := do(t, "POST", "/batch/resolve", `{"batch_id":"b5","decision":"approved","items":[{"item_id":"ap_change_a","expected_revision":1}]}`, "X-Stub-Scenario", "not_elevated"); w.Code != 403 {
		t.Errorf("batch needs elevated access even for low items: %d", w.Code)
	}
}

func TestDeferClampsToExpiry(t *testing.T) {
	var r struct {
		Until   string `json:"deferred_until"`
		Clamped bool   `json:"expires_before_snooze_ends"`
	}
	decode(t, do(t, "POST", "/items/ap_change_a/defer", `{"expected_revision":1}`), &r)
	if r.Until != "2026-10-30T04:00:00Z" || r.Clamped {
		t.Errorf("default 1h: %+v", r)
	}
	decode(t, do(t, "POST", "/items/ap_change_c/defer", `{"expected_revision":1,"duration_s":3600}`), &r)
	if !r.Clamped || r.Until != "2026-10-30T03:01:30Z" {
		t.Errorf("clamped to expiry: %+v", r)
	}
	for _, body := range []string{`{"expected_revision":1,"duration_s":10}`, `{"expected_revision":1,"duration_s":700000}`, `{"expected_revision":1,"duration_s":60,"until_ms":1}`, `{}`} {
		if w := do(t, "POST", "/items/ap_change_a/defer", body); w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := do(t, "POST", "/items/ap_h_approved/defer", `{"expected_revision":3}`); w.Code != 409 {
		t.Errorf("terminal item cannot be deferred: %d", w.Code)
	}
	if w := do(t, "DELETE", "/items/ap_deferred/defer", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"deferred_until":null`) {
		t.Errorf("undefer: %d %s", w.Code, w.Body)
	}
}

func TestHistoryAuditAccessAndSecrets(t *testing.T) {
	var h struct {
		Events []item `json:"events"`
		Next   string `json:"next_cursor"`
	}
	decode(t, do(t, "GET", "/history", ""), &h)
	if len(h.Events) != 6 || h.Next != "h2" {
		t.Fatalf("history: %d %q", len(h.Events), h.Next)
	}
	reasons := 0
	for _, e := range h.Events {
		if e["reason"] != nil || e["note"] != nil {
			reasons++
		}
	}
	if reasons < 4 {
		t.Error("history rows should carry reasons")
	}
	decode(t, do(t, "GET", "/history?status=denied", ""), &h)
	if len(h.Events) != 1 || h.Events[0]["reason"] != "wrong_target" {
		t.Errorf("denied filter: %v", h.Events)
	}
	if do(t, "GET", "/history?status=bogus", "").Code != 400 || do(t, "GET", "/history?cursor=zz", "").Code != 400 {
		t.Error("bad history params accepted")
	}
	if w := do(t, "GET", "/items/ap_h_denied/audit", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "wrong_target") {
		t.Errorf("audit: %d %s", w.Code, w.Body)
	}
	if do(t, "GET", "/items/ap_nope/audit", "").Code != 404 {
		t.Error("audit of unknown item")
	}
	var a struct {
		State string `json:"state"`
	}
	decode(t, do(t, "GET", "/access", ""), &a)
	if a.State != "elevated" {
		t.Error(a)
	}
	decode(t, do(t, "GET", "/access", "", "X-Stub-Scenario", "not_elevated"), &a)
	if a.State != "none" {
		t.Error(a)
	}
	w := do(t, "POST", "/secrets", `{"item_id":"ap_secret","field":"deploy_token","value":"hunter2-not-a-real-secret"}`)
	if w.Code != 201 || strings.Contains(w.Body.String(), "hunter2") || !strings.Contains(w.Body.String(), "secret://tachyon/") {
		t.Errorf("secret: %d %s", w.Code, w.Body)
	}
	if do(t, "POST", "/secrets", `{"item_id":"ap_secret","field":"nope","value":"x"}`).Code != 400 {
		t.Error("unknown secret field")
	}
	if do(t, "POST", "/secrets", `{"item_id":"ap_secret","field":"deploy_token","value":"x"}`, "X-Stub-Scenario", "secrets_unavailable").Code != 503 {
		t.Error("secrets_unavailable scenario")
	}
}

func TestEventsReplayResumeAndResync(t *testing.T) {
	ids := func(body string) (out []string) {
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "id: ") {
				out = append(out, l[4:])
			}
		}
		return
	}
	w := do(t, "GET", "/events?once=1", "")
	if got := ids(w.Body.String()); len(got) != 8 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("replay: %v", got)
	}
	for _, typ := range StreamEvents[:7] {
		if !strings.Contains(w.Body.String(), `"type":"`+typ+`"`) {
			t.Errorf("script lacks %s", typ)
		}
	}
	if got := ids(do(t, "GET", "/events?once=1", "", "Last-Event-ID", "6").Body.String()); len(got) != 2 || got[0] != "7" {
		t.Errorf("resume: %v", got)
	}
	if b := do(t, "GET", "/events?once=1&after=99", "").Body.String(); !strings.Contains(b, `"type":"resync"`) || strings.Contains(b, "item.created") {
		t.Errorf("past the ring: %s", b)
	}
	if b := do(t, "GET", "/events?once=1", "", "X-Stub-Scenario", "gap").Body.String(); !strings.Contains(b, `"type":"resync"`) {
		t.Error("gap scenario")
	}
	if do(t, "GET", "/events?once=1&after=x", "").Code != 400 {
		t.Error("bad after")
	}
}

func TestForcedErrorsAndNoFallThrough(t *testing.T) {
	for _, s := range []string{"401", "403", "404", "409", "422", "429", "503"} {
		if w := do(t, "GET", "/inbox", "", "X-Stub-Status", s); w.Code != mustAtoi(s) || !strings.Contains(w.Body.String(), `"code"`) {
			t.Errorf("%s: %d %s", s, w.Code, w.Body)
		}
	}
	for _, p := range []string{"/session", "/status", "/elevate", "/de-elevate", "/nope"} {
		if w := do(t, "GET", p, ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("stub answered %s: %d", p, w.Code)
		}
	}
	if w := do(t, "GET", "/openapi.yaml", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "openapi: 3.0.3") {
		t.Error("openapi")
	}
}

func mustAtoi(s string) (n int) {
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return
}
