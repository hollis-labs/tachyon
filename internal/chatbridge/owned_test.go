package chatbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakeSource struct {
	calls   int
	fail    bool
	down    bool
	current string
	key     string
	state   tether.ReplyState
	pages   []int64
}

func (f *fakeSource) Agents(context.Context) ([]Agent, error) {
	if f.down {
		return nil, errors.New("offline")
	}
	id := f.current
	if id == "" {
		id = "s1"
	}
	var a Agent
	_ = json.Unmarshal([]byte(`{"launch_id":"pm","agent_id":"pm","channel":"owner-inbox","session":{"id":"`+id+`","state":"running","at_turn_boundary":true}}`), &a)
	return []Agent{a}, nil
}
func (f *fakeSource) ChannelMessages(_ context.Context, name string, q tether.ChannelMessagesOptions) (tether.ChannelMessagesResponse, error) {
	f.pages = append(f.pages, q.Since)
	p := tether.ChannelMessagesResponse{Channel: tether.Channel{Name: name}, NextSince: q.Since}
	if q.Since == 0 {
		var m tether.ChannelMessage
		_ = json.Unmarshal([]byte(`{"seq":1,"id":"pub1","kind":"notice","channel":"owner-inbox","from":"msg://session/local/s1","to":"msg://service/local/channel/owner-inbox","thread_id":"s1","content_type":"application/json","payload":{"text":"# Hello\n\nA **formatted** reply"},"metadata":{"kind":"final","session_id":"s1","turn_id":"t1","launch_id":"pm","logical_agent_id":"pm"},"created_at":"2026-10-17T10:00:00Z"}`), &m)
		p.Messages = []tether.ChannelMessage{m}
		p.NextSince = 1
	}
	return p, nil
}
func (f *fakeSource) RoutingCapabilities(_ context.Context, id string) (tether.RoutingCapabilitiesResponse, error) {
	return tether.RoutingCapabilitiesResponse{SessionID: id, ReplyToSender: true}, nil
}
func (f *fakeSource) Reply(_ context.Context, parent, body string, opts tether.ReplyOptions) (tether.ReplyReceipt, error) {
	f.calls++
	if f.key != "" && f.key != opts.IdempotencyKey {
		return tether.ReplyReceipt{}, errors.New("retry changed key")
	}
	f.key = opts.IdempotencyKey
	if f.fail {
		return tether.ReplyReceipt{}, &tether.APIError{StatusCode: 403, Code: "denied"}
	}
	return tether.ReplyReceipt{ReplyID: "reply1", ParentID: parent, TargetSessionID: "s1", State: "queued"}, nil
}
func (f *fakeSource) ReplyDelivery(context.Context, string) (tether.ReplyDelivery, error) {
	state := f.state
	if state == "" {
		state = tether.ReplyQueued
	}
	return tether.ReplyDelivery{ReplyID: "reply1", ParentID: "pub1", OriginalSessionID: "s1", TargetSessionID: "s1", State: state}, nil
}
func ownedTest(t *testing.T) (*Owned, *fakeSource, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tachyon.db")
	db, e := localstore.Open(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	f := &fakeSource{}
	o := &Owned{Store: db, Source: f, SourceID: "tether-test"}
	return o, f, path
}
func TestOwnedHistoryPersistsCursorAndReplaysWithoutLosingReply(t *testing.T) {
	ctx := context.Background()
	o, f, _ := ownedTest(t)
	if e := o.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	inbox, e := o.Inbox(ctx)
	if e != nil || len(inbox.Pending) != 1 {
		t.Fatalf("inbox=%+v err=%v", inbox, e)
	}
	turn := inbox.Pending[0]
	got, e := o.Reply(ctx, turn.ID, turn.Revision, "go ahead")
	if e != nil {
		t.Fatal(e)
	}
	if got.Delivery != "queued" || got.Resolution.Text != "go ahead" {
		t.Fatal("acceptance presented as delivery")
	}
	if _, e = o.Reply(ctx, turn.ID, turn.Revision, "go ahead"); e != nil || f.calls != 1 {
		t.Fatal("identical reply was sent twice", e)
	}
	if _, e = o.Reply(ctx, turn.ID, got.Revision, "different"); !errors.Is(e, ErrConflict) {
		t.Fatal("resolution overwritten")
	}
	f.state = tether.ReplyDelivered
	if e = o.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ = o.get(ctx, turn.ID)
	if got.Delivery != "delivered" {
		t.Fatal("delivery not observed")
	}
	// Replayed publications cannot replace a locally owned resolution.
	e = o.Store.Writer.Submit(ctx, "test_replay", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DELETE FROM chat_cursors; DELETE FROM api_cache")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = o.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	got, _ = o.get(ctx, turn.ID)
	if got.Resolution.Text != "go ahead" {
		t.Fatal("replay lost reply")
	}
	f.down = true
	if o.Sync(ctx) == nil {
		t.Fatal("outage hidden")
	}
	inbox, e = o.Inbox(ctx)
	if e != nil || len(inbox.History) != 1 || inbox.Problem != "unreachable" {
		t.Fatal("outage lost local history", e)
	}
	// Fresh verification fails; ordinary reads continue from durable local tables.
	req := httptest.NewRequest("GET", Prefix+"/inbox", nil)
	res := httptest.NewRecorder()
	o.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
	req.Header.Set("Cache-Control", "no-cache")
	res = httptest.NewRecorder()
	o.ServeHTTP(res, req)
	if res.Code != 502 {
		t.Fatal("stale source admitted for send verification")
	}
}
func TestOwnedReplyRefusalIsDurableAndRetryUsesOriginalKey(t *testing.T) {
	ctx := context.Background()
	o, f, _ := ownedTest(t)
	if e := o.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	inbox, _ := o.Inbox(ctx)
	turn := inbox.Pending[0]
	f.current = "replacement"
	if _, e := o.Reply(ctx, turn.ID, turn.Revision, "hello"); !errors.Is(e, ErrNotReplyable) || f.calls != 0 {
		t.Fatal("replied to stale session", e)
	}
	f.current = "s1"
	f.fail = true
	if _, e := o.Reply(ctx, turn.ID, turn.Revision, "hello"); e == nil {
		t.Fatal("refusal hidden")
	}
	saved, _ := o.get(ctx, turn.ID)
	if saved.Resolution.Text != "hello" || saved.Delivery != "retryable_failure" {
		t.Fatal("failed intent lost")
	}
	f.fail = false
	if _, e := o.Reply(ctx, turn.ID, saved.Revision, "hello"); e != nil {
		t.Fatal(e)
	}
	if f.calls != 2 {
		t.Fatal("retry not submitted")
	}
}
func TestImportThenIndependentIngestionPreservesHistoryAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, e := localstore.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeSource{}
	o := &Owned{Store: db, Source: f, SourceID: "tether-test"}
	raw := []byte(`{"pending":[],"history":[{"item_id":"old-tangent-id","agent_id":"pm","session_id":"s1","turn_id":"t1","kind":"terminal","content":"old original","created_at":"2026-10-17T10:00:00Z","revision":4,"queue_sequence":1,"state":"resolved","delivery_state":"acknowledged","replyable":true,"annotations":[{"kept":"in original snapshot"}],"resolution":{"resolution_id":"old-resolution","response_text":"historical user reply","resolved_at":"2026-10-17T10:01:00Z"},"source_message":{"origin":"routed","channel":"owner-inbox","message_id":"pub1","sequence":1,"sender_urn":"msg://session/local/s1","attribution":{"launch_id":"pm"}}}]}`)
	if n, e := o.Import(ctx, raw); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, e = o.Import(ctx, raw); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = localstore.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	o.Store = db
	if e = o.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	inbox, e := o.Inbox(ctx)
	if e != nil || len(inbox.History) != 1 || len(inbox.Pending) != 0 {
		t.Fatal("duplicated or lost imported history", e)
	}
	if inbox.History[0].Resolution.Text != "historical user reply" || f.calls != 0 {
		t.Fatal("import replayed or lost operator reply")
	}
	var snapshot []byte
	if e = db.Reader.QueryRow("SELECT snapshot FROM chat_imports").Scan(&snapshot); e != nil || !strings.Contains(string(snapshot), "annotations") {
		t.Fatal("original migration evidence lost")
	}
}

var _ http.Handler = (*Owned)(nil)
