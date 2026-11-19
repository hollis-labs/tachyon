package alertsstream

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

var t0 = time.UnixMilli(1793332800000)

func pub(t *testing.T, h *Hub, id int64, typ string) {
	t.Helper()
	if err := h.Publish(id, map[string]any{"type": typ}); err != nil {
		t.Fatal(err)
	}
}

func once(t *testing.T, s *Stream, hdr ...string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/alerts/stream?once=1", nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func TestReplayAfterCursorKeepsFramesSharingAnID(t *testing.T) {
	h := NewHub(100)
	pub(t, h, 5, "alert.created")
	pub(t, h, 6, "alert.state")
	pub(t, h, 6, "counts")
	pub(t, h, 7, "alert.updated")
	s := &Stream{Hub: h}
	_, body := once(t, s, "Last-Event-ID", "5")
	if strings.Contains(body, "alert.created") || !strings.Contains(body, "alert.state") || !strings.Contains(body, `"counts"`) || !strings.Contains(body, "alert.updated") {
		t.Errorf("after 5:\n%s", body)
	}
	if strings.Index(body, "alert.state") > strings.Index(body, `"counts"`) {
		t.Error("frames must replay in publish order")
	}
	_, body = once(t, s, "Last-Event-ID", "6")
	if strings.Contains(body, "alert.state") || !strings.Contains(body, "alert.updated") {
		t.Errorf("after 6:\n%s", body)
	}
	if _, body := once(t, s, "Last-Event-ID", "7"); strings.Contains(body, "data:") {
		t.Errorf("up to date client got frames:\n%s", body)
	}
}

func TestGapAndFutureCursorsResync(t *testing.T) {
	h := NewHub(2)
	for id := int64(1); id <= 5; id++ {
		pub(t, h, id, "alert.created")
	}
	s := &Stream{Hub: h}
	for _, c := range []string{"1", "99"} { // evicted; ahead of the server (it restarted)
		_, body := once(t, s, "Last-Event-ID", c)
		if !strings.Contains(body, `"type":"resync"`) || !strings.Contains(body, "id: 5") || strings.Count(body, "data:") != 1 {
			t.Errorf("cursor %s:\n%s", c, body)
		}
	}
	if _, body := once(t, s, "Last-Event-ID", "3"); strings.Contains(body, "resync") || strings.Count(body, "data:") != 2 {
		t.Errorf("cursor 3 is still in the ring:\n%s", body)
	}
	if code, _ := once(t, s, "Last-Event-ID", "x"); code != 400 {
		t.Errorf("bad cursor: %d", code)
	}
	if code, _ := once(t, s, "Last-Event-ID", "-1"); code != 400 {
		t.Errorf("negative cursor: %d", code)
	}
}

func TestFreshConnectionGetsCountsAndNoReplay(t *testing.T) {
	h := NewHub(10)
	pub(t, h, 1, "alert.created")
	s := &Stream{Hub: h, Counts: func() alertsapi.Counts { return alertsapi.Counts{Unread: 3, Seq: 1} }, Now: func() time.Time { return t0 }}
	_, body := once(t, s)
	if strings.Contains(body, "alert.created") || !strings.Contains(body, `"unread":3`) || strings.Contains(body, "id:") {
		t.Errorf("fresh connection:\n%s", body)
	}
}

func TestLiveFramesFlowAfterReplayWithoutDuplicates(t *testing.T) {
	h := NewHub(100)
	pub(t, h, 1, "alert.created")
	srv := httptest.NewServer(&Stream{Hub: h, Heartbeat: 10 * time.Millisecond})
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	req.Header.Set("Last-Event-ID", "0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(30 * time.Millisecond)
		pub(t, h, 2, "alert.woke")
	}()
	var ids []string
	sc := bufio.NewScanner(resp.Body)
	heartbeat := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "id: ") {
			ids = append(ids, line[4:])
		}
		heartbeat = heartbeat || strings.HasPrefix(line, ": keepalive")
		if len(ids) == 2 && heartbeat {
			break
		}
	}
	if strings.Join(ids, ",") != "1,2" || !heartbeat {
		t.Errorf("ids %v heartbeat %v", ids, heartbeat)
	}
}

func TestSlowSubscriberIsDroppedNotBlockedOn(t *testing.T) {
	h := NewHub(1000)
	sub := h.Subscribe()
	done := make(chan struct{})
	go func() {
		for i := int64(1); i <= 200; i++ {
			pub(t, h, i, "x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	n, closed := 0, false
	for !closed {
		select {
		case _, open := <-sub.C:
			if !open {
				closed = true
			}
			n++
		default:
			t.Fatal("subscriber channel was not closed")
		}
	}
	if n > 65 {
		t.Errorf("buffer is 64, drained %d", n)
	}
	sub.Close() // idempotent
	if _, ok := h.Replay(0); !ok {
		t.Error("ring must still serve the dropped client's reconnect")
	}
}

func TestFrameConstructorsAndSources(t *testing.T) {
	f := AlertFrame("alert.escalated", alertsapi.Alert{AlertID: "al_1", Seq: 9}, true, "unread 10 min", t0)
	if f["type"] != "alert.escalated" || f["toast"] != true || f["reason"] != "unread 10 min" || f["at_ms"] != t0.UnixMilli() {
		t.Errorf("%v", f)
	}
	if _, has := AlertFrame("alert.updated", alertsapi.Alert{}, false, "", t0)["reason"]; has {
		t.Error("empty reason must be omitted")
	}
	if RemovedFrame("al_1", 3, t0)["alert_id"] != "al_1" {
		t.Error("removed frame")
	}
	w := httptest.NewRecorder()
	Sources(func() []SourceState { return []SourceState{{Source: "activity", State: "down", Note: "stream ended"}} }, func() time.Time { return t0 }).ServeHTTP(w, httptest.NewRequest("GET", "/alerts/sources", nil))
	if !strings.Contains(w.Body.String(), `"state":"down"`) || !strings.Contains(w.Body.String(), `"server_now_ms":1793332800000`) {
		t.Errorf("sources: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	Sources(func() []SourceState { return nil }, nil).ServeHTTP(w, httptest.NewRequest("GET", "/alerts/sources", nil))
	if !strings.Contains(w.Body.String(), `"sources":[]`) {
		t.Errorf("empty health must be [], got %s", w.Body.String())
	}
}
