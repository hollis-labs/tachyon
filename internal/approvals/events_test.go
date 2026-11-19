package approvals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
)

func fakeTangent(t *testing.T, elevated *bool, expires string, elevateCalls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session":
			level := "browser-session"
			if *elevated {
				level = "operator-token"
			}
			body := map[string]any{"elevated": *elevated, "assurance": level, "elevation_ttl": "30m"}
			if *elevated {
				body["elevation_expires_at"] = expires
			}
			writeValue(w, body)
		case "/api/session/elevate":
			*elevateCalls++
			*elevated = true
			writeValue(w, map[string]any{"elevated": true, "assurance": "operator-token"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(h http.Handler, path, cookie string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://tachyon.test"+path, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
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

func TestRequirementsTableFailsClosed(t *testing.T) {
	for a, want := range map[assurance.Action]assurance.Level{
		assurance.ActionRead: assurance.LevelBrowserSession, assurance.ActionResolveLow: assurance.LevelBrowserSession,
		assurance.ActionDefer: assurance.LevelBrowserSession, assurance.ActionResolveMedium: assurance.LevelOperatorToken,
		assurance.ActionResolveHigh: assurance.LevelOperatorToken, assurance.ActionBatch: assurance.LevelOperatorToken,
		assurance.ActionSecretSubmit: assurance.LevelOperatorToken, assurance.ActionDestructive: assurance.LevelOperatorToken,
		"never_heard_of_it": assurance.LevelOperatorToken,
	} {
		if got := assurance.Required(a); got != want {
			t.Errorf("%s: %v want %v", a, got, want)
		}
	}
	if assurance.Satisfies(assurance.LevelNone, assurance.ActionRead) || !assurance.Satisfies(assurance.LevelOperatorToken, assurance.ActionBatch) || assurance.Satisfies(assurance.LevelBrowserSession, assurance.ActionBatch) {
		t.Error("Satisfies")
	}
	if assurance.ParseLevel("operator-token") != assurance.LevelOperatorToken || assurance.ParseLevel("loopback-unverified") != assurance.LevelNone {
		t.Error("ParseLevel")
	}
}

func TestAccessIsNormalizedAndElevateGoesThroughTheProvider(t *testing.T) {
	elevated, calls := false, 0
	exp := time.Now().Add(20 * time.Minute).UTC().Format(time.RFC3339)
	up := fakeTangent(t, &elevated, exp, &calls)
	inbox := NewInbox(nativeStore(t), New(up.URL))
	if w := get(inbox, Prefix+"/access", ""); w.Code != 401 {
		t.Fatalf("no session: %d %s", w.Code, w.Body)
	}
	var a assurance.Access
	w := get(inbox, Prefix+"/access", "s1")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &a) != nil || a.State != "none" || !a.CanElevate || a.ExpiresInS != 0 || a.TTLPolicy != "30m" {
		t.Fatalf("not elevated: %d %s", w.Code, w.Body)
	}
	if !a.Required.Batch || !a.Required.Medium || !a.Required.Destructive || !a.Required.High || !a.Required.Secrets {
		t.Errorf("required_for: %+v", a.Required)
	}
	if strings.Contains(w.Body.String(), "tangent") || strings.Contains(w.Body.String(), "assurance") {
		t.Errorf("provider shapes leaked: %s", w.Body)
	}
	w = serveJSON(inbox, "POST", Prefix+"/elevate", map[string]string{}, "", "s1")
	if w.Code != 200 || calls != 1 {
		t.Fatalf("elevate: %d calls=%d", w.Code, calls)
	}
	w = get(inbox, Prefix+"/access", "s1")
	if json.Unmarshal(w.Body.Bytes(), &a) != nil || a.State != "elevated" || a.CanElevate || a.ExpiresInS < 1100 || a.ExpiresInS > 1200 {
		t.Errorf("elevated: %s", w.Body)
	}
	if w := get(inbox, Prefix+"/access", "s1", "Authorization", "Bearer x"); w.Code != 403 {
		t.Errorf("bearer: %d", w.Code)
	}
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
	if w := serveJSON(inbox, "POST", Prefix+"/items/"+h.ItemID+"/resolve", cmd, "", "s1"); w.Code != 200 {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
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
