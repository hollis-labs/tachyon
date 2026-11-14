package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/chatthreads"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/needsyou"
)

// GET needs-you is mounted and reads Tether live: a typed ask waiting on @chrispian is listed.
func TestNeedsYouRouteReadsTetherLive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages/pending-replies":
			if r.URL.Query().Get("as") != needsyou.Operator {
				t.Errorf("pending replies asked as %q", r.URL.Query().Get("as"))
			}
			fmt.Fprint(w, `{"handle":"@chrispian","pending":[{"message_id":"m1","from":"msg://agent/agent-mux/agt_b","kind":"decision","subject":"Keep or patch?","created_at":"2026-11-14T10:00:00Z"}]}`)
		case "/messages/list":
			fmt.Fprint(w, `{"messages":[],"total":0}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("t"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), ApprovalQueueURL: upstream.URL, TranscriptMaxSessions: 5000,
		Upstreams: catalog.Upstreams{TetherAddr: upstream.URL, TetherTokenFile: token}})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+needsyou.Path, nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r needsyou.Result
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Sources["asks"].OK || !r.Sources["mail"].OK || !r.Sources["chat"].OK || !r.Sources["approvals"].OK || len(r.Items) != 1 ||
		r.Items[0].ID != "ask:m1" || r.Items[0].Title != "Keep or patch?" {
		t.Fatalf("%+v", r)
	}
}

// The approvals adapter counts the whole pending queue (not one page), counts high risk, and finds the oldest item
// by walking the pages to the end.
func TestNeedsYouApprovalQueueCountsTheWholeQueue(t *testing.T) {
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	in := approvals.NewInbox(&approvals.Store{DB: db}, approvals.New(""))
	enqueue := func(key, class string) {
		t.Helper()
		req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: "approval", IdempotencyKey: key,
			Source: hitl.SourceAssertion{ApplicationID: "test-producer", AgentID: "a"}, Extra: map[string]json.RawMessage{}}
		for k, v := range map[string]any{"title": "T " + key, "summary": "S", "request": "do it", "class": class, "op_digest": "sha256:" + strings.Repeat("a", 64)} {
			req.Extra[k], _ = json.Marshal(v)
		}
		if _, err := in.Service.Enqueue(ctx, req); err != nil {
			t.Fatal(key, err)
		}
	}
	empty, err := approvalQueue{db}.Waiting(ctx, time.Now())
	if err != nil || empty.Pending != 0 || !empty.Oldest.IsZero() {
		t.Fatalf("empty queue: %+v %v", empty, err)
	}
	enqueue("first", "low")
	time.Sleep(5 * time.Millisecond)
	first := time.Now()
	for i := 0; i < 130; i++ { // more than one page of 100
		enqueue(fmt.Sprintf("k%03d", i), map[bool]string{true: "high", false: "low"}[i%50 == 0])
	}
	q, err := approvalQueue{db}.Waiting(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.Pending != 131 || q.High != 3 || q.Oldest.IsZero() || !q.Oldest.Before(first) {
		t.Fatalf("queue: %+v (oldest must be the first item, before %s)", q, first)
	}
}

// The chat adapter lists threads with open question or approval turns, with the agent id for the link (a Tether
// chat agent's id is its launch, which the chat page keys its rows by);
// answered and archived threads are not listed.
func TestNeedsYouChatAsksReadTheChatThreads(t *testing.T) {
	ctx := context.Background()
	db, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed := func(id, session, kind, state string) {
		t.Helper()
		raw, _ := json.Marshal(chatbridge.Turn{ID: id, AgentID: "chief", SessionID: session, TurnID: "tn-" + id, Kind: kind, Content: "Keep or patch?",
			Created: "2026-11-14T09:00:00Z", Revision: 1, State: state, Replyable: kind == "question" || kind == "approval",
			Source: &chatbridge.SourceMessage{Origin: "routed", Attribution: map[string]string{"launch_id": "chief"}}})
		if err := db.Writer.Submit(ctx, "seed", func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(id,source,channel,publication_id,launch_id,session_id,created_at,revision,body) VALUES(?,?,?,?,?,?,?,?,?)`,
				id, "src", "ch", "", "chief", session, "2026-11-14T09:00:00Z", 1, raw)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("q1", "s1", "question", "presented")
	seed("a1", "s1", "approval", "presented")
	seed("q2", "s2", "question", "resolved")
	seed("t3", "s3", "terminal", "presented")
	agg := &chatthreads.Aggregator{Chat: &chatbridge.Owned{Store: db, SourceID: "src"}, State: &chatthreads.StateStore{DB: db}}
	asks, err := chatAsks{agg}.OpenAsks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 1 || asks[0].Ref != "tether:s1" || asks[0].AgentID != "chief" || asks[0].Questions != 1 || asks[0].Approvals != 1 || asks[0].LastAt.IsZero() {
		t.Fatalf("%+v", asks)
	}
}
