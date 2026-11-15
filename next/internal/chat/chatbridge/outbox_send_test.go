package chatbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// fakeTether is a stand-in for Tether's send endpoint. Like Tether it treats the idempotency key as
// the message identity: a repeated key is answered with the first delivery and not delivered again.
type fakeTether struct {
	mu        sync.Mutex
	posts     int            // every request received
	delivered map[string]int // deliveries per key
	fail      bool           // answer 502 without delivering
}

func newFakeTether(t *testing.T) (*fakeTether, string) {
	t.Helper()
	f := &fakeTether{delivered: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.posts++
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/chat/agents/") || !strings.HasSuffix(r.URL.Path, "/send") {
			http.NotFound(w, r)
			return
		}
		if f.fail {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"code":"unavailable","message":"agent unreachable"}`)
			return
		}
		var in struct {
			Text string `json:"text"`
			Key  string `json:"idempotency_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if f.delivered[in.Key] == 0 {
			f.delivered[in.Key] = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"session_id":"sess-`+in.Key+`"}`)
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeTether) counts() (posts, deliveries int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.delivered {
		deliveries += n
	}
	return f.posts, deliveries
}

func send(t *testing.T, s *Sender, launch, text, key string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"text": text, "idempotency_key": key})
	req := httptest.NewRequest(http.MethodPost, "/x/agents/"+launch+"/send", strings.NewReader(string(b)))
	req.SetPathValue("launch", launch)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func outboxRow(t *testing.T, db *store.Store, identity, key string) (status, session string) {
	t.Helper()
	if err := db.Reader.QueryRowContext(context.Background(), `SELECT status, session_id FROM chat_outbox WHERE identity=? AND id=?`, identity, key).Scan(&status, &session); err != nil {
		t.Fatalf("row %s: %v", key, err)
	}
	return status, session
}

func insertOutbox(t *testing.T, db *store.Store, identity, key, launch, text, status string, at time.Time) {
	t.Helper()
	if _, err := db.Lib().ExecContext(context.Background(), `INSERT INTO chat_outbox(identity,id,launch_id,body,created_at,status,error,session_id) VALUES(?,?,?,?,?,?,'','')`,
		identity, key, launch, text, at.UnixMilli(), status); err != nil {
		t.Fatal(err)
	}
}

// With tachyon.features.chat_sync off, a send is refused before anything is recorded or dialed.
func TestSendOffSendsNothing(t *testing.T) {
	db := storetest.New(t).Open(t)
	fake, url := newFakeTether(t)
	s := NewSender(db, url, nil, false)
	if code, out := send(t, s, "pm", "hello", "k1"); code != 503 || out["code"] != "disabled" {
		t.Fatalf("switch off: %d %v", code, out)
	}
	var n int
	if err := db.Reader.QueryRowContext(context.Background(), `SELECT count(*) FROM chat_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("outbox rows with the switch off: %d %v", n, err)
	}
	if posts, _ := fake.counts(); posts != 0 {
		t.Fatalf("%d requests to Tether with the switch off", posts)
	}
	// Not configured at all: the configuration answer, still nothing recorded.
	if code, out := send(t, NewSender(db, "", nil, true), "pm", "hello", "k1"); code != 503 || out["code"] != "configuration" {
		t.Fatalf("no Tether address: %d %v", code, out)
	}
}

// With the switch on: a send is delivered once and recorded sent with its session; repeating the key
// replays the record without calling Tether; the same key with other text is a conflict.
func TestSendDeliversOnce(t *testing.T) {
	db := storetest.New(t).Open(t)
	fake, url := newFakeTether(t)
	s := NewSender(db, url, nil, true)
	if code, out := send(t, s, "pm", "hello", "k1"); code != 200 || out["session_id"] != "sess-k1" {
		t.Fatalf("send: %d %v", code, out)
	}
	if st, sess := outboxRow(t, db, s.Identity, "k1"); st != "sent" || sess != "sess-k1" {
		t.Fatalf("row: %s %s", st, sess)
	}
	if code, out := send(t, s, "pm", "hello", "k1"); code != 200 || out["replayed"] != true {
		t.Fatalf("repeat: %d %v", code, out)
	}
	if code, out := send(t, s, "pm", "other text", "k1"); code != 409 || out["code"] != "conflict" {
		t.Fatalf("same key, other text: %d %v", code, out)
	}
	if posts, deliveries := fake.counts(); posts != 1 || deliveries != 1 {
		t.Fatalf("posts %d deliveries %d, want 1 and 1", posts, deliveries)
	}
}

// A copied pending row (the old build's unconfirmed send) is delivered once when the operator
// retries it, then replays; a sent copied row is never sent again; an unconfirmed row older than a
// day is refused without calling Tether.
func TestCopiedOutboxRowsDeliverOnce(t *testing.T) {
	db := storetest.New(t).Open(t)
	fake, url := newFakeTether(t)
	s := NewSender(db, url, nil, true)
	insertOutbox(t, db, s.Identity, "pending", "pm", "copied", "failed", time.Now().Add(-time.Hour))
	insertOutbox(t, db, s.Identity, "done", "pm", "already sent", "sent", time.Now().Add(-time.Hour))
	insertOutbox(t, db, s.Identity, "stale", "pm", "too old", "sending", time.Now().Add(-25*time.Hour))
	for range 2 {
		if code, _ := send(t, s, "pm", "copied", "pending"); code != 200 {
			t.Fatalf("pending retry: %d", code)
		}
	}
	if code, out := send(t, s, "pm", "already sent", "done"); code != 200 || out["replayed"] != true {
		t.Fatalf("sent row: %d %v", code, out)
	}
	if code, out := send(t, s, "pm", "too old", "stale"); code != 409 || out["code"] != "delivery_unknown" {
		t.Fatalf("stale row: %d %v", code, out)
	}
	if posts, deliveries := fake.counts(); posts != 1 || deliveries != 1 {
		t.Fatalf("posts %d deliveries %d, want 1 and 1", posts, deliveries)
	}
}

// A restart mid-delivery leaves the row 'sending' after Tether already took the message. The retry
// carries the same key, so Tether answers with the first delivery: the message is not delivered twice,
// and the row ends sent.
func TestRestartMidDeliveryDoesNotDuplicate(t *testing.T) {
	db := storetest.New(t).Open(t)
	fake, url := newFakeTether(t)
	s := NewSender(db, url, nil, true)
	fake.delivered["k1"] = 1 // Tether received it before the restart
	insertOutbox(t, db, s.Identity, "k1", "pm", "hello", "sending", time.Now().Add(-time.Minute))
	again := NewSender(db, url, nil, true) // the restarted process
	if code, _ := send(t, again, "pm", "hello", "k1"); code != 200 {
		t.Fatalf("retry after restart: %d", code)
	}
	if st, _ := outboxRow(t, db, s.Identity, "k1"); st != "sent" {
		t.Fatalf("row after retry: %s", st)
	}
	if _, deliveries := fake.counts(); deliveries != 1 {
		t.Fatalf("delivered %d times", deliveries)
	}
}

// A failed send is recorded failed with Tether's message and answered with Tether's status.
func TestSendFailureIsRecorded(t *testing.T) {
	db := storetest.New(t).Open(t)
	fake, url := newFakeTether(t)
	fake.fail = true
	s := NewSender(db, url, nil, true)
	if code, _ := send(t, s, "pm", "hello", "k1"); code != 502 {
		t.Fatalf("failed send: %d", code)
	}
	var st, msg string
	if err := db.Reader.QueryRowContext(context.Background(), `SELECT status, error FROM chat_outbox WHERE identity=? AND id=?`, s.Identity, "k1").Scan(&st, &msg); err != nil || st != "failed" || msg != "agent unreachable" {
		t.Fatalf("row: %s %q %v", st, msg, err)
	}
}
