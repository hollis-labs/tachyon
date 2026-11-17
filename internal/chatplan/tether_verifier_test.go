package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTetherVerifierRequiresExactServerNativeReceipt(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const replacement = "22222222-2222-4222-8222-222222222222"
	baseReceipt := map[string]any{"kind": "codex_same_worker_v1", "source_session_id": source, "source_generation": "g1", "replacement_session_id": replacement,
		"replacement_generation": "g2", "worker_id": "worker", "logical_agent_id": "agent-a", "native_thread_id": "thread", "admitted_at": "2026-11-17T16:00:00Z"}
	for _, tc := range []struct {
		name, mutate string
		wantOK       bool
	}{
		{"exact", "", true}, {"absent_receipt", "absent", false}, {"wrong_source", "source", false},
		{"hub_checkpoint_resume", "hub", false},
		{"wrong_replacement", "replacement", false}, {"wrong_agent", "agent", false},
		{"fresh_intent", "intent", false}, {"failed_state", "state", false}, {"wrong_parent", "parent", false},
		{"missing_generation", "generation", false}, {"other_worker", "worker", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := map[string]any{}
			for k, v := range baseReceipt {
				receipt[k] = v
			}
			old := map[string]any{"id": source, "logical_agent_id": "agent-a", "worker_id": "worker", "state": "completed"}
			newSession := map[string]any{"id": replacement, "logical_agent_id": "agent-a", "parent_session_id": source, "intent": "resume", "state": "running", "worker_id": "worker", "native_admission": receipt}
			switch tc.mutate {
			case "absent":
				delete(newSession, "native_admission")
			case "hub":
				delete(newSession, "native_admission")
				old["worker_id"], newSession["worker_id"] = "", ""
			case "source":
				receipt["source_session_id"] = "other"
			case "replacement":
				receipt["replacement_session_id"] = "other"
			case "agent":
				receipt["logical_agent_id"] = "other"
			case "intent":
				newSession["intent"] = "fresh"
			case "state":
				newSession["state"] = "failed"
			case "parent":
				newSession["parent_session_id"] = "other"
			case "generation":
				receipt["source_generation"] = ""
			case "worker":
				receipt["worker_id"] = "other"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/sessions/"+source {
					_ = json.NewEncoder(w).Encode(old)
					return
				}
				if r.URL.Path == "/sessions/"+replacement {
					_ = json.NewEncoder(w).Encode(newSession)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			v := &TetherContinuationVerifier{Base: server.URL, Client: server.Client()}
			err := v.Verify(context.Background(), source, replacement, "agent-a")
			if tc.wantOK && err != nil || !tc.wantOK && !errors.Is(err, ErrNativeAdmissionUnverified) {
				t.Fatalf("verification: %v", err)
			}
		})
	}
}
