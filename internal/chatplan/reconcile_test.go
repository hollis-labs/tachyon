package chatplan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

func reconcileFixture(t *testing.T, rows map[string]map[string]any) (*Service, *ReadReconciler) {
	t.Helper()
	s, _ := lineageSetup(t)
	s.Publish, s.Carried = nil, nil
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/sessions/")
		if row := rows[id]; row != nil {
			_ = json.NewEncoder(w).Encode(row)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return s, &ReadReconciler{Service: s, Verifier: &TetherContinuationVerifier{Base: server.URL, Client: server.Client()}}
}
func hubChain() map[string]map[string]any {
	return map[string]map[string]any{
		"a": {"id": "a", "logical_agent_id": "agent", "intent": "fresh", "state": "killed"},
		"b": {"id": "b", "logical_agent_id": "agent", "intent": "resume", "parent_session_id": "a", "state": "killed"},
		"c": {"id": "c", "logical_agent_id": "agent", "intent": "resume", "parent_session_id": "b", "state": "running"},
	}
}

func TestReadReconcileWholeChainBeforePlanGET(t *testing.T) {
	s, r := reconcileFixture(t, hubChain())
	original := writePlan(t, s, "a", "Survive two restarts")
	h := r.Wrap((&Handler{Service: s}).Mux())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:c/plan", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), original.ID) {
		t.Fatalf("immediate plan: %d %s", w.Code, w.Body.String())
	}
	got, err := planOf(s, "c")
	if err != nil || got.Revision != original.Revision+2 {
		t.Fatalf("plan: %+v %v", got, err)
	}
	if chain := s.Store.Ancestors(context.Background(), "c"); fmt.Sprint(chain) != "[a b]" {
		t.Fatalf("ancestry: %v", chain)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Ensure(context.Background(), "c"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	again, _ := planOf(s, "c")
	if again.Revision != got.Revision {
		t.Fatal("read replay moved plan again")
	}
}

func TestReadReconcileLastLocalConflictRollsBackEveryEdge(t *testing.T) {
	s, r := reconcileFixture(t, hubChain())
	original := writePlan(t, s, "a", "Stay here on failure")
	if _, err := s.Store.Carry(context.Background(), "c", "other", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Ensure(context.Background(), "c"); err != ErrContinuationUnverified {
		t.Fatalf("conflict: %v", err)
	}
	got, err := planOf(s, "a")
	if err != nil || got.Revision != original.Revision {
		t.Fatalf("partial plan movement: %+v %v", got, err)
	}
	var rows, receipts int
	_ = s.Store.DB.Reader.QueryRow(`SELECT count(*) FROM chat_session_lineage`).Scan(&rows)
	_ = s.Store.DB.Reader.QueryRow(`SELECT count(*) FROM chat_continuation_associations`).Scan(&receipts)
	if rows != 1 || receipts != 0 {
		t.Fatalf("partial chain committed: rows=%d receipts=%d", rows, receipts)
	}
}

func TestReadReconcileRejectsUnverifiedWholeChainBeforeWrites(t *testing.T) {
	for _, kind := range []string{"ancestor-agent", "cycle", "missing", "depth", "unsettled", "worker-no-proof", "orphan-no-proof"} {
		t.Run(kind, func(t *testing.T) {
			rows := hubChain()
			switch kind {
			case "ancestor-agent":
				rows["a"]["logical_agent_id"] = "other"
			case "cycle":
				rows["b"]["parent_session_id"] = "c"
			case "missing":
				delete(rows, "a")
			case "unsettled":
				rows["b"]["state"] = "running"
			case "worker-no-proof":
				rows["b"]["worker_id"] = "worker"
				rows["c"]["worker_id"] = "worker"
			case "orphan-no-proof":
				rows["b"]["state"] = "orphaned"
			case "depth":
				rows["a"]["intent"] = "resume"
				rows["a"]["parent_session_id"] = "x0"
				for i := 0; i < MaxLineageHops; i++ {
					id := fmt.Sprintf("x%d", i)
					rows[id] = map[string]any{"id": id, "logical_agent_id": "agent", "state": "killed", "intent": "resume", "parent_session_id": fmt.Sprintf("x%d", i+1)}
				}
			}
			s, r := reconcileFixture(t, rows)
			original := writePlan(t, s, "a", "Unchanged")
			if err := r.Ensure(context.Background(), "c"); err == nil {
				t.Fatal("unverified chain accepted")
			}
			got, err := planOf(s, "a")
			if err != nil || got.Revision != original.Revision {
				t.Fatalf("plan changed: %+v %v", got, err)
			}
			var n int
			_ = s.Store.DB.Reader.QueryRow(`SELECT count(*) FROM chat_session_lineage`).Scan(&n)
			if n != 0 {
				t.Fatalf("unverified rows committed: %d", n)
			}
		})
	}
}

func TestReadReconcilePreservesContinueAndClearFences(t *testing.T) {
	for _, fence := range []string{"off", "clear"} {
		t.Run(fence, func(t *testing.T) {
			s, r := reconcileFixture(t, hubChain())
			writePlan(t, s, "a", "Fenced")
			if fence == "off" {
				if err := s.Store.SetContinue(context.Background(), "a", false); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Store.Clear(context.Background(), "tether:a"); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Ensure(context.Background(), "c"); err != nil {
				t.Fatal(err)
			}
			if _, err := planOf(s, "c"); err != ErrNotFound {
				t.Fatalf("fenced plan carried: %v", err)
			}
		})
	}
}

func TestReadReconcileConcurrentFirstReadsMoveOncePerEdge(t *testing.T) {
	s, r := reconcileFixture(t, hubChain())
	original := writePlan(t, s, "a", "Concurrent")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := r.Ensure(context.Background(), "c"); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	got, err := planOf(s, "c")
	if err != nil || got.Revision != original.Revision+2 {
		t.Fatalf("concurrent plan: %+v %v", got, err)
	}
	var n int
	_ = s.Store.DB.Reader.QueryRow(`SELECT count(*) FROM chat_continuation_associations`).Scan(&n)
	if n != 2 {
		t.Fatalf("receipts: %d", n)
	}
}

func TestReadReconcileTeamReceiptAllowsOnlyCommittedOrphanLineage(t *testing.T) {
	for _, bad := range []string{"", "missing", "source", "agent", "worker", "generation", "source-actor", "replacement-actor", "committed", "intent-key"} {
		t.Run(bad, func(t *testing.T) {
			rows := hubChain()
			delete(rows, "b")
			rows["a"]["state"] = "orphaned"
			rows["c"]["parent_session_id"] = "a"
			receipt := map[string]any{"kind": "retained_team_v1", "source_session_id": "a", "replacement_session_id": "c", "logical_agent_id": "agent", "actor_uri": "msg://agent/team/seat", "source_actor_uri": "msg://agent/team/seat", "replacement_actor_uri": "msg://agent/team/seat", "committed_at": "2026-11-19T17:00:00Z", "intent_key": "intent", "binding_id": "binding", "binding_generation": 1}
			rows["c"]["replacement_lineage"] = receipt
			switch bad {
			case "missing":
				delete(rows["c"], "replacement_lineage")
			case "source":
				receipt["source_session_id"] = "other"
			case "agent":
				receipt["logical_agent_id"] = "other"
			case "worker":
				rows["c"]["worker_id"] = "worker"
			case "generation":
				receipt["binding_generation"] = 0
			case "source-actor":
				receipt["source_actor_uri"] = "other"
			case "replacement-actor":
				receipt["replacement_actor_uri"] = "other"
			case "committed":
				receipt["committed_at"] = ""
			case "intent-key":
				receipt["intent_key"] = ""
			}
			s, r := reconcileFixture(t, rows)
			original := writePlan(t, s, "a", "Team")
			err := r.Ensure(context.Background(), "c")
			if (err == nil) != (bad == "") {
				t.Fatalf("verification: %v", err)
			}
			ref := "a"
			if bad == "" {
				ref = "c"
			}
			got, e := planOf(s, ref)
			if e != nil || got.ID != original.ID {
				t.Fatalf("plan: %+v %v", got, e)
			}
		})
	}
}

func TestReadReconcileHTTPFailuresAndHistoricalReads(t *testing.T) {
	rows := hubChain()
	s, r := reconcileFixture(t, rows)
	writePlan(t, s, "a", "Historical")
	h := r.Wrap((&Handler{Service: s}).Mux())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:a/plan", nil))
	if w.Code != 200 {
		t.Fatalf("historical read: %d", w.Code)
	}
	rows["b"]["logical_agent_id"] = "other"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:c/plan", nil))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "continuation_unverified") {
		t.Fatalf("unverified read: %d %s", w.Code, w.Body.String())
	}
	delete(rows, "c")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:c/plan", nil))
	if w.Code != 503 {
		t.Fatalf("unavailable read: %d %s", w.Code, w.Body.String())
	}
}

func TestCanonicalContinuationDiscoveryPagesWithoutAgentTraffic(t *testing.T) {
	for _, loop := range []bool{false, true} {
		t.Run(fmt.Sprint(loop), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/sessions" || r.URL.Query().Get("state") != "running" {
					t.Errorf("unexpected discovery: %s", r.URL)
				}
				cursor := r.URL.Query().Get("cursor")
				if cursor == "" {
					fmt.Fprint(w, `{"sessions":[{"id":"b","state":"running","intent":"resume","parent_session_id":"a"},{"id":"fresh","state":"running"}],"next_cursor":"page2"}`)
					return
				}
				next := ""
				if loop {
					next = "page2"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]string{{"id": "c", "state": "running", "intent": "resume", "parent_session_id": "b"}}, "next_cursor": next})
			}))
			defer server.Close()
			v := &TetherContinuationVerifier{Base: server.URL, Client: server.Client()}
			ids, err := v.RunningContinuations(context.Background())
			if loop {
				if err == nil {
					t.Fatal("looping discovery accepted")
				}
			} else if err != nil || fmt.Sprint(ids) != "[b c]" || calls != 2 {
				t.Fatalf("discovery: %v %v calls=%d", ids, err, calls)
			}
		})
	}
}

func TestReadReconcileFreshSpawnParentIsNotContinuation(t *testing.T) {
	rows := hubChain()
	rows["child"] = map[string]any{"id": "child", "logical_agent_id": "child-agent", "intent": "fresh", "parent_session_id": "c", "state": "running"}
	// The old source itself may be an ordinary spawned child. Its spawn parent
	// must not become a continuation ancestor of the restarted seat.
	rows["a"]["parent_session_id"] = "unrelated-spawn-parent"
	s, _ := lineageSetup(t)
	s.Publish, s.Carried = nil, nil
	childPlan := writePlan(t, s, "child", "Fresh child plan")
	original := writePlan(t, s, "a", "Continue this seat only")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/sessions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{rows["child"], rows["c"]}})
			return
		}
		if row := rows[strings.TrimPrefix(req.URL.Path, "/sessions/")]; row != nil {
			_ = json.NewEncoder(w).Encode(row)
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()
	v := &TetherContinuationVerifier{Base: server.URL, Client: server.Client()}
	r := &ReadReconciler{Service: s, Verifier: v, Candidates: v.RunningContinuations}
	w := httptest.NewRecorder()
	r.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads", nil))
	if w.Code != 200 {
		t.Fatalf("fresh child poisoned list: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.Wrap((&Handler{Service: s}).Mux()).ServeHTTP(w, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:child/plan", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), childPlan.ID) {
		t.Fatalf("own fresh child read: %d %s", w.Code, w.Body.String())
	}
	got, err := planOf(s, "c")
	if err != nil || got.ID != original.ID {
		t.Fatalf("valid continuation not associated: %+v %v", got, err)
	}
	found, err := s.Store.HasReconciliationBoundary(context.Background(), "child")
	if err != nil || found {
		t.Fatalf("spawn child associated: %v %v", found, err)
	}
	if chain := s.Store.Ancestors(context.Background(), "c"); fmt.Sprint(chain) != "[a b]" {
		t.Fatalf("spawn ancestry leaked: %v", chain)
	}
}

func TestReadReconcileWorkerLineageRequiresExactLaunchReceipt(t *testing.T) {
	for _, bad := range []string{"", "missing", "source", "replacement", "agent", "source-worker", "worker", "command", "time", "wrong-time", "native-time-alias"} {
		t.Run(bad, func(t *testing.T) {
			rows := hubChain()
			delete(rows, "b")
			rows["a"]["worker_id"] = "old-worker"
			rows["c"]["worker_id"] = "new-worker"
			rows["c"]["parent_session_id"] = "a"
			receipt := map[string]any{"kind": "worker_lineage_v1", "source_session_id": "a", "replacement_session_id": "c", "logical_agent_id": "agent", "source_worker_id": "old-worker", "worker_id": "new-worker", "command_id": "launch:c", "acknowledged_at": "2026-11-19T17:00:00Z"}
			rows["c"]["worker_lineage"] = receipt
			switch bad {
			case "missing":
				delete(rows["c"], "worker_lineage")
			case "source":
				receipt["source_session_id"] = "other"
			case "replacement":
				receipt["replacement_session_id"] = "other"
			case "agent":
				receipt["logical_agent_id"] = "other"
			case "source-worker":
				receipt["source_worker_id"] = "other"
			case "worker":
				receipt["worker_id"] = "other"
			case "command":
				receipt["command_id"] = "launch:other"
			case "time":
				receipt["acknowledged_at"] = ""
			case "wrong-time":
				receipt["acknowledged_at"] = "not-a-time"
			case "native-time-alias":
				delete(receipt, "acknowledged_at")
				receipt["admitted_at"] = "2026-11-19T17:00:00Z"
			}
			s, r := reconcileFixture(t, rows)
			original := writePlan(t, s, "a", "Worker history")
			err := r.Ensure(context.Background(), "c")
			if (err == nil) != (bad == "") {
				t.Fatalf("worker proof: %v", err)
			}
			ref := "a"
			if bad == "" {
				ref = "c"
			}
			got, e := planOf(s, ref)
			if e != nil || got.ID != original.ID {
				t.Fatalf("plan: %+v %v", got, e)
			}
		})
	}
}

func TestUILineageReceiptsNeverAuthorizeNativePOST(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const replacement = "22222222-2222-4222-8222-222222222222"
	for _, kind := range []string{"team", "worker"} {
		t.Run(kind, func(t *testing.T) {
			old := map[string]any{"id": source, "logical_agent_id": "agent", "intent": "fresh", "state": "killed"}
			next := map[string]any{"id": replacement, "logical_agent_id": "agent", "intent": "resume", "parent_session_id": source, "state": "running"}
			if kind == "team" {
				old["state"] = "orphaned"
				next["replacement_lineage"] = map[string]any{"kind": "retained_team_v1", "source_session_id": source, "replacement_session_id": replacement, "logical_agent_id": "agent", "actor_uri": "msg://agent/team/seat", "source_actor_uri": "msg://agent/team/seat", "replacement_actor_uri": "msg://agent/team/seat", "committed_at": "2026-11-19T17:00:00Z", "intent_key": "intent", "binding_id": "binding", "binding_generation": 1}
			} else {
				old["worker_id"] = "worker"
				next["worker_id"] = "worker"
				next["worker_lineage"] = map[string]any{"kind": "worker_lineage_v1", "source_session_id": source, "replacement_session_id": replacement, "logical_agent_id": "agent", "source_worker_id": "worker", "worker_id": "worker", "command_id": "launch:" + replacement, "acknowledged_at": "2026-11-19T17:00:00Z"}
			}
			s, r := reconcileFixture(t, map[string]map[string]any{source: old, replacement: next})
			original := writePlan(t, s, source, "Never native proof")
			if err := r.Ensure(context.Background(), replacement); err != nil {
				t.Fatal(err)
			}
			before, err := planOf(s, replacement)
			if err != nil || before.ID != original.ID {
				t.Fatalf("UI plan: %+v %v", before, err)
			}
			h := &Handler{Service: s, VerifyContinuation: r.Verifier.Verify}
			body := fmt.Sprintf(`{"source_session_id":%q,"new_session_id":%q,"logical_agent_id":"agent","idempotency_key":%q}`, source, replacement, "continuation:"+source+":"+replacement)
			w := httptest.NewRecorder()
			h.Mux().ServeHTTP(w, httptest.NewRequest("POST", chatapi.Prefix+"/continuations", strings.NewReader(body)))
			if w.Code != 409 || !strings.Contains(w.Body.String(), "native_continuation_unverified") {
				t.Fatalf("native bypass: %d %s", w.Code, w.Body.String())
			}
			after, err := planOf(s, replacement)
			if err != nil || after.Revision != before.Revision {
				t.Fatalf("native refusal mutated plan: %+v %v", after, err)
			}
		})
	}
}

func TestReadReconcileHistoricalTeamChain(t *testing.T) {
	rows := hubChain()
	rows["a"]["state"], rows["b"]["state"] = "orphaned", "orphaned"
	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}} {
		rows[pair[1]]["replacement_lineage"] = map[string]any{"kind": "retained_team_v1", "source_session_id": pair[0], "replacement_session_id": pair[1], "logical_agent_id": "agent", "actor_uri": "msg://agent/team/seat", "source_actor_uri": "msg://agent/team/seat", "replacement_actor_uri": "msg://agent/team/seat", "committed_at": "2026-11-19T17:00:00Z", "intent_key": "intent:" + pair[1], "binding_id": "binding:" + pair[1], "binding_generation": 1}
	}
	s, r := reconcileFixture(t, rows)
	original := writePlan(t, s, "a", "Survive repeated team recovery")
	if err := r.Ensure(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	got, err := planOf(s, "c")
	if err != nil || got.ID != original.ID || got.Revision != original.Revision+2 {
		t.Fatalf("team chain: %+v %v", got, err)
	}
	if ancestors := s.Store.Ancestors(context.Background(), "c"); fmt.Sprint(ancestors) != "[a b]" {
		t.Fatalf("team ancestry: %v", ancestors)
	}
}

func TestReadReconcileFillsAncestryBehindPriorOnePairReceipt(t *testing.T) {
	for _, kind := range []string{"hub-ui", "native"} {
		t.Run(kind, func(t *testing.T) {
			rows := hubChain()
			if kind == "native" {
				for _, row := range rows {
					row["worker_id"] = "worker"
				}
				for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}} {
					rows[pair[1]]["native_admission"] = map[string]any{"kind": "codex_same_worker_v1", "source_session_id": pair[0], "replacement_session_id": pair[1], "logical_agent_id": "agent", "worker_id": "worker", "source_generation": "g:" + pair[0], "replacement_generation": "g:" + pair[1], "native_thread_id": "thread", "admitted_at": "2026-11-19T17:00:00Z"}
				}
			}
			s, r := reconcileFixture(t, rows)
			original := writePlan(t, s, "a", "Unobserved first restart")
			var err error
			if kind == "native" {
				err = r.Verifier.Verify(context.Background(), "b", "c", "agent")
			} else {
				err = r.Verifier.VerifyHubLineage(context.Background(), "b", "c", "agent")
			}
			if err != nil {
				t.Fatal(err)
			}
			pair, err := s.Store.AssociateContinuation(context.Background(), "b", "c", "agent")
			if err != nil || pair.PlanMoved {
				t.Fatalf("one-pair: %+v %v", pair, err)
			}
			for i := 0; i < 3; i++ {
				if err := r.Ensure(context.Background(), "c"); err != nil {
					t.Fatal(err)
				}
				got, err := planOf(s, "c")
				if err != nil || got.ID != original.ID || got.Revision != original.Revision+2 {
					t.Fatalf("repaired plan: %+v %v", got, err)
				}
				if ancestry := s.Store.Ancestors(context.Background(), "c"); fmt.Sprint(ancestry) != "[a b]" {
					t.Fatalf("ancestry: %v", ancestry)
				}
			}
			replay, err := s.Store.AssociateContinuation(context.Background(), "b", "c", "agent")
			if err != nil || !replay.Replayed || replay.Carried.Moved {
				t.Fatalf("pair replay: %+v %v", replay, err)
			}
		})
	}
}

func TestReadReconcileHistoricalWorkerChain(t *testing.T) {
	rows := hubChain()
	for _, row := range rows {
		row["worker_id"] = "worker"
	}
	for _, pair := range [][2]string{{"a", "b"}, {"b", "c"}} {
		rows[pair[1]]["worker_lineage"] = map[string]any{"kind": "worker_lineage_v1", "source_session_id": pair[0], "replacement_session_id": pair[1], "logical_agent_id": "agent", "source_worker_id": "worker", "worker_id": "worker", "command_id": "launch:" + pair[1], "acknowledged_at": "2026-11-19T17:00:00Z"}
	}
	s, r := reconcileFixture(t, rows)
	original := writePlan(t, s, "a", "Repeated worker relaunch")
	if err := r.Ensure(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	got, err := planOf(s, "c")
	if err != nil || got.ID != original.ID || got.Revision != original.Revision+2 {
		t.Fatalf("worker chain: %+v %v", got, err)
	}
}

func TestReadReconcileClearedTipRemainsReadable(t *testing.T) {
	s, r := reconcileFixture(t, hubChain())
	original := writePlan(t, s, "a", "Do not restore after clear")
	if _, err := s.Store.AssociateContinuation(context.Background(), "b", "c", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Clear(context.Background(), "tether:c"); err != nil {
		t.Fatal(err)
	}
	if err := r.Ensure(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	got, err := planOf(s, "a")
	if err != nil || got.Revision != original.Revision {
		t.Fatalf("clear moved ancestor: %+v %v", got, err)
	}
	if ancestors := s.Store.Ancestors(context.Background(), "c"); len(ancestors) != 0 {
		t.Fatalf("clear exposed history: %v", ancestors)
	}
}
