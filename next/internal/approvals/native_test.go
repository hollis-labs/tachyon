package approvals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitl/hitltest"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

func nativeStore(t *testing.T) *Store {
	t.Helper()
	db := storetest.New(t).Open(t)
	return &Store{DB: db}
}
func TestNativeStoreContract(t *testing.T) {
	// The concurrent-swap case opens a fresh store for each of its 20 rounds inside one subtest, and
	// a store's cleanup runs only when the subtest ends. On Postgres that holds 20 pools open at once,
	// past the server's connection limit, so each new store for a test closes the one before it (the
	// contract never uses two at a time).
	last := map[*testing.T]*Store{}
	hitltest.RunStoreContract(t, func(t *testing.T) hitl.Store {
		if prev := last[t]; prev != nil {
			_ = prev.DB.Close()
		}
		s := nativeStore(t)
		last[t] = s
		return s
	})
}
func TestNativeServiceConformance(t *testing.T) {
	inbox := NewInbox(nativeStore(t), nil)
	hitltest.RunConformance(t, hitltest.NewServiceAdapter(inbox.Service), hitltest.ServiceCaps()...)
}
func requestFixture(t *testing.T, key, class string) hitl.EnqueueRequest {
	t.Helper()
	var req hitl.EnqueueRequest
	raw := fmt.Sprintf(`{"contract_version":"1.0","kind":"approval","idempotency_key":%q,"source":{"application_id":"test-producer","agent_id":"test-agent"},"class":%q,"title":"Deploy","summary":"Deploy one resource","request":"Deploy resource revision one","op_digest":"sha256:%s","handoff_url":"https://cerberus.example/approvals/one"}`, key, class, strings.Repeat("a", 64))
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	return req
}
func serveJSON(h http.Handler, method, path string, value any, token, cookie string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(value)
	req := httptest.NewRequest(method, "http://tachyon.test"+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://tachyon.test")
	req = asCaller(req, token, cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestNativeBrowserDecisionAuthority(t *testing.T) {
	elevated := false
	sessions := fakeTangent(t, &elevated, "", nil)
	inbox := NewInbox(nativeStore(t), New(sessions.URL))
	for _, class := range []string{"low", "medium", "high"} {
		t.Run(class, func(t *testing.T) {
			elevated = false
			handle, err := inbox.Service.Enqueue(context.Background(), requestFixture(t, class, class))
			if err != nil {
				t.Fatal(err)
			}
			path := Prefix + "/items/" + handle.ItemID + "/resolve"
			cmd := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved"}}
			w := serveJSON(inbox, "POST", path, cmd, "requester", "session")
			if w.Code != 403 {
				t.Fatalf("bearer decision: %d %s", w.Code, w.Body)
			}
			// No signed-in person: refused (cross-site browser writes are the framework guard's).
			req := httptest.NewRequest("POST", "http://tachyon.test"+path, strings.NewReader(`{}`))
			denied := httptest.NewRecorder()
			inbox.ServeHTTP(denied, req)
			if denied.Code != 401 {
				t.Fatal("a decision without a signed-in person was accepted")
			}
			cmd["participant"] = map[string]string{"assurance": "operator-token"}
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if w.Code != 400 {
				t.Fatalf("injected participant: %d %s", w.Code, w.Body)
			}
			delete(cmd, "participant")
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if class == "medium" || class == "high" {
				if w.Code != 403 {
					t.Fatalf("unelevated decision: %d %s", w.Code, w.Body)
				}
				elevated = true
				w = serveJSON(inbox, "POST", path, cmd, "", "session")
			}
			if class == "high" {
				// A3a: decided here with the server-derived phrase.
				if w.Code != 422 {
					t.Fatalf("high decision without the phrase: %d %s", w.Code, w.Body)
				}
				g := serveJSON(inbox, "GET", Prefix+"/items/"+handle.ItemID, nil, "", "session")
				var item struct {
					Confirm struct{ Phrase string } `json:"confirm"`
				}
				_ = json.Unmarshal(g.Body.Bytes(), &item)
				cmd["response"] = map[string]string{"kind": "approval", "decision": "approved", "confirmation": " " + item.Confirm.Phrase + " "}
				w = serveJSON(inbox, "POST", path, cmd, "", "session")
			}
			if w.Code != 200 {
				t.Fatalf("authorized decision: %d %s", w.Code, w.Body)
			}
			rec, err := inbox.Store.Get(context.Background(), handle.ItemID)
			if err != nil || rec.State != hitl.StateResolved {
				t.Fatalf("retained result: %+v %v", rec, err)
			}
			if strings.Contains(w.Body.String(), `"proof"`) || !strings.Contains(w.Body.String(), `"user:session"`) {
				t.Fatalf("fabricated authority: %s", w.Body)
			}
			w = serveJSON(inbox, "POST", path, cmd, "", "session")
			if w.Code != 409 {
				t.Fatalf("stale replay: %d %s", w.Code, w.Body)
			}
		})
	}
	w := serveJSON(inbox, "GET", Prefix+"/inbox", nil, "", "session")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"history"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inbox: %d %s", w.Code, w.Body)
	}
}
func TestNativeRestartAndPaging(t *testing.T) {
	ctx := context.Background()
	testDB := storetest.New(t)
	db := testDB.Open(t)
	inbox := NewInbox(&Store{DB: db}, nil)
	handle, err := inbox.Service.Enqueue(ctx, requestFixture(t, "retain", "low"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: handle.ItemID, Response: hitl.Response{Kind: "approval", Decision: "denied"}, Participant: hitl.Participant{PrincipalRef: "operator", Assurance: "browser-session"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testDB.Open(t)
	inbox = NewInbox(&Store{DB: db}, nil)
	rec, err := inbox.Store.Get(ctx, handle.ItemID)
	if err != nil || rec.State != hitl.StateResolved || rec.Outcome == nil {
		t.Fatalf("restart: %+v %v", rec, err)
	}
	for n := 0; n < 4; n++ {
		_, err = inbox.Service.Enqueue(ctx, requestFixture(t, fmt.Sprint(n), "low"))
		if err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, err := inbox.Store.Page(ctx, false, "", 2)
	if err != nil || len(first) != 2 || cursor == "" {
		t.Fatalf("first page: %v %q %v", first, cursor, err)
	}
	second, next, err := inbox.Store.Page(ctx, false, cursor, 2)
	if err != nil || len(second) != 2 || next != "" || second[0].ItemID == first[0].ItemID {
		t.Fatalf("second page: %v %q %v", second, next, err)
	}
}
