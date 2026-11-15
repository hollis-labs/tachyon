package approvals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func get(h http.Handler, path, cookie string, hdr ...string) *httptest.ResponseRecorder {
	req := asCaller(httptest.NewRequest("GET", "http://tachyon.test"+path, nil), "", cookie)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// sseEvents returns (id, decoded data) pairs from a stream body.
func sseEvents(body string) (out []struct {
	ID   int64
	Data map[string]any
}) {
	var id int64
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "id: "):
			id, _ = strconv.ParseInt(l[4:], 10, 64)
		case strings.HasPrefix(l, "data: "):
			var m map[string]any
			_ = json.Unmarshal([]byte(l[6:]), &m)
			out = append(out, struct {
				ID   int64
				Data map[string]any
			}{id, m})
		}
	}
	return
}

func TestBrokerReplayGapAndSlowClients(t *testing.T) {
	b := newBroker()
	base := b.Head()
	for i := 0; i < 3; i++ {
		b.Publish(Change{Type: "item.updated", ItemID: "i", Revision: int64(i + 2)})
	}
	if m, ch, gap := b.Since(base+1, false); gap || len(m) != 2 || m[0].Seq != base+2 {
		t.Errorf("resume: gap=%v %v", gap, m)
	} else {
		b.unsubscribe(ch)
	}
	if _, ch, gap := b.Since(base-5, false); !gap {
		t.Error("an id from an earlier epoch must resync")
	} else {
		b.unsubscribe(ch)
	}
	if _, ch, gap := b.Since(base+99, false); !gap {
		t.Error("an id past the head must resync")
	} else {
		b.unsubscribe(ch)
	}
	if m, ch, gap := b.Since(0, true); gap || len(m) != 0 {
		t.Error("a fresh connection replays nothing")
	} else {
		b.unsubscribe(ch)
	}
	for i := 0; i < ringSize+10; i++ {
		b.Publish(Change{Type: "item.updated", ItemID: "i"})
	}
	if _, ch, gap := b.Since(base+1, false); !gap {
		t.Error("an id older than the ring must resync")
	} else {
		b.unsubscribe(ch)
	}
	_, slow, _ := b.Since(0, true)
	for i := 0; i < 200; i++ {
		b.Publish(Change{Type: "item.updated", ItemID: "i"})
	}
	n := 0
	for range slow { // closed by the broker once its buffer overflowed
		n++
	}
	if n == 0 || n > 64 {
		t.Errorf("slow client buffered %d", n)
	}
}

func TestEventsStreamTheQueueLifecycle(t *testing.T) {
	elevated, calls := false, 0
	up := fakeTangent(t, &elevated, "", &calls)
	inbox := NewInbox(nativeStore(t), New(up.URL))
	if w := get(inbox, Prefix+"/events?once=1", ""); w.Code != 401 {
		t.Fatalf("events need a session: %d", w.Code)
	}
	w := get(inbox, Prefix+"/events?once=1", "s1")
	ev := sseEvents(w.Body.String())
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" || len(ev) != 1 || ev[0].Data["type"] != "inbox.changed" {
		t.Fatalf("baseline: %d %s", w.Code, w.Body)
	}
	resume := ev[0].ID
	h, err := inbox.Service.Enqueue(context.Background(), requestFixture(t, "ev1", "low"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]string{"kind": "approval", "decision": "approved"}}
	if rw := serveJSON(inbox, "POST", Prefix+"/items/"+h.ItemID+"/resolve", cmd, "", "s1"); rw.Code != 200 {
		t.Fatalf("resolve: %d %s", rw.Code, rw.Body)
	}
	w = get(inbox, Prefix+"/events?once=1", "s1", "Last-Event-ID", strconv.FormatInt(resume, 10))
	ev = sseEvents(w.Body.String())
	if len(ev) < 2 || ev[0].Data["type"] != "item.created" || ev[0].Data["item_id"] != h.ItemID {
		t.Fatalf("replay: %s", w.Body)
	}
	last := ev[len(ev)-1]
	if last.Data["type"] != "item.resolved" || last.Data["state"] != "resolved" {
		t.Errorf("last event: %v", last.Data)
	}
	for _, e := range ev {
		if _, leaked := e.Data["title"]; leaked || e.Data["request_snapshot"] != nil {
			t.Error("events must not carry item content")
		}
	}
	if ev2 := sseEvents(get(inbox, Prefix+"/events?once=1&after="+strconv.FormatInt(last.ID, 10), "s1").Body.String()); len(ev2) != 0 {
		t.Errorf("caught up: %v", ev2)
	}
	if ev3 := sseEvents(get(inbox, Prefix+"/events?once=1&after=1", "s1").Body.String()); len(ev3) != 1 || ev3[0].Data["type"] != "resync" {
		t.Errorf("old id: %v", ev3)
	}
	if get(inbox, Prefix+"/events?once=1&after=x", "s1").Code != 400 {
		t.Error("bad after")
	}
}
