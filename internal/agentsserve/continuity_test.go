package agentsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/chatthreads"
)

// Exercise Restart through immediate plan AND message reads, with no replacement
// agent request. Both hub checkpoint resume and the typed-refusal relaunch use
// server-owned lineage evidence and preserve resolved operator replies.
func TestHubRestartImmediatelyCarriesPlanAndHistory(t *testing.T) {
	for _, relaunch := range []bool{false, true} {
		name := "resume"
		if relaunch {
			name = "relaunch"
		}
		t.Run(name, func(t *testing.T) {
			f := &fakeCtl{}
			replacement := "s2"
			if relaunch {
				f.resumeErr = ErrResumeRefused
				replacement = "s3"
			}
			e := restartEnv(t, f)
			plans := &chatplan.Service{Store: &chatplan.Store{DB: e.srv.DB}}
			title := "Keep this plan"
			original, err := plans.Write(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "m1"}, "thread", chatplan.WriteIn{By: "agent", Title: &title, HasTitle: true, Steps: []chatplan.StepIn{{Title: &title}}})
			if err != nil {
				t.Fatal(err)
			}
			turn := chatbridge.Turn{ID: "question", AgentID: "la1", SessionID: "m1", Kind: "question", Content: "Which one?", Created: "2026-11-19T10:00:00Z", Revision: 1, State: "resolved", Resolution: &chatbridge.Resolution{ID: "reply", Text: "Keep it", At: "2026-11-19T10:01:00Z"}}
			raw, _ := json.Marshal(turn)
			err = e.srv.DB.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(id,source,channel,publication_id,launch_id,session_id,created_at,revision,body) VALUES(?,?,?,?,?,?,?,?,?)`, "question", "src", "ch", "", "", "m1", turn.Created, 1, raw)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			tether := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/sessions/m1" {
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "logical_agent_id": "la1", "state": "completed"})
					return
				}
				if r.URL.Path == "/sessions/"+replacement {
					_ = json.NewEncoder(w).Encode(map[string]string{"id": replacement, "logical_agent_id": "la1", "state": "running", "intent": "resume", "parent_session_id": "m1"})
					return
				}
				http.NotFound(w, r)
			}))
			defer tether.Close()
			verifier := &chatplan.TetherContinuationVerifier{Base: tether.URL, Client: tether.Client()}
			e.srv.AssociateRestart = func(ctx context.Context, source, next, agent string) error {
				if err := verifier.VerifyHubLineage(ctx, source, next, agent); err != nil {
					return err
				}
				_, err := plans.AssociateContinuation(ctx, source, next, agent)
				return err
			}
			e.send("POST", "/sessions/m1/restart", `{"request_id":"carry","expected_session_id":"m1"}`)
			if c := untilEnd(t, e, "carry"); c.State != "done" {
				t.Fatalf("%+v", c)
			}
			planRead := httptest.NewRecorder()
			(&chatplan.Handler{Service: plans}).Mux().ServeHTTP(planRead, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:"+replacement+"/plan", nil))
			if planRead.Code != 200 || !strings.Contains(planRead.Body.String(), original.ID) {
				t.Fatalf("immediate plan: %d %s", planRead.Code, planRead.Body.String())
			}
			threads := &chatthreads.Handler{Agg: &chatthreads.Aggregator{Chat: &chatbridge.Owned{Store: e.srv.DB, SourceID: "src"}, State: &chatthreads.StateStore{DB: e.srv.DB}}, Ancestors: plans.Store.Ancestors}
			threads.Agg.Sessions = func(ctx context.Context) []chatthreads.SessionStub {
				var out []chatthreads.SessionStub
				for _, st := range plans.Store.Sessions(ctx) {
					out = append(out, chatthreads.SessionStub{Ref: st.Ref, Agent: st.Agent, SeenNS: st.SeenNS, HasPlan: st.HasPlan})
				}
				return out
			}
			messages := httptest.NewRecorder()
			threads.Mux().ServeHTTP(messages, httptest.NewRequest("GET", chatapi.Prefix+"/threads/tether:"+replacement+"/messages", nil))
			if messages.Code != 200 || !strings.Contains(messages.Body.String(), "Which one?") || !strings.Contains(messages.Body.String(), "Keep it") || !strings.Contains(messages.Body.String(), `"continued_from":"m1"`) {
				t.Fatalf("immediate history: %d %s", messages.Code, messages.Body.String())
			}
		})
	}
}
