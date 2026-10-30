package chatapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(h http.Handler, method, path string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestEveryRouteServesValidJSON(t *testing.T) {
	for _, set := range []struct {
		h      http.Handler
		prefix string
		routes []route
	}{{Stub{}.Chat(), Prefix, chatRoutes}, {Stub{}.Terminal(), TerminalPrefix, terminalRoutes}} {
		for _, rt := range set.routes {
			method, path, _ := strings.Cut(rt.pattern, " ")
			path = strings.NewReplacer("{ref}", "app:t_01", "{id}", "x1", "{run_id}", "run_1").Replace(path)
			w := do(set.h, method, set.prefix+path)
			if w.Code != rt.status || !json.Valid(w.Body.Bytes()) {
				t.Errorf("%s: %d %s", rt.pattern, w.Code, w.Body)
			}
		}
	}
}

func TestBadRefAndForcedErrors(t *testing.T) {
	h := Stub{}.Chat()
	if w := do(h, "GET", Prefix+"/threads/nope/plan"); w.Code != 400 {
		t.Fatalf("bad ref: %d", w.Code)
	}
	w := do(h, "PUT", Prefix+"/threads/app:t_01/scratchpad", "X-Stub-Status", "409")
	var e map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != 409 || e["code"] != "conflict" {
		t.Fatalf("%d %v", w.Code, e)
	}
	if w := do(h, "GET", Prefix+"/nothing"); w.Code != 404 {
		t.Fatalf("unknown path: %d", w.Code)
	}
}

func frames(t *testing.T, body string) (ids []string, events []map[string]any) {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		l := sc.Text()
		if v, ok := strings.CutPrefix(l, "id: "); ok {
			ids = append(ids, v)
		}
		if v, ok := strings.CutPrefix(l, "data: "); ok {
			var m map[string]any
			if err := json.Unmarshal([]byte(v), &m); err != nil {
				t.Fatal(err)
			}
			events = append(events, m)
		}
	}
	return
}

func TestSSEReplayAndResume(t *testing.T) {
	h := Stub{}.Chat()
	w := do(h, "GET", Prefix+"/threads/app:t_01/events?once=1")
	ids, evs := frames(t, w.Body.String())
	if len(evs) != 8 || len(ids) != 5 {
		t.Fatalf("full replay: %d events %d ids", len(evs), len(ids))
	}
	for _, e := range evs {
		if e["thread_ref"] != "app:t_01" || e["type"] == nil || e["at"] == nil {
			t.Fatalf("envelope: %v", e)
		}
		if IDed(e["type"].(string)) != (e["type"] != "panel" && e["type"] != "context" && e["type"] != "thread") {
			t.Fatalf("IDed disagrees with fixture for %v", e["type"])
		}
	}
	w = do(h, "GET", Prefix+"/threads/app:t_01/events?once=1", "Last-Event-ID", "3")
	ids, evs = frames(t, w.Body.String())
	if len(ids) != 2 || ids[0] != "4" || len(evs) != 2 {
		t.Fatalf("resume: %v %d", ids, len(evs))
	}
	if w := do(h, "GET", Prefix+"/threads/app:t_01/events?once=1", "Last-Event-ID", "x"); w.Code != 400 {
		t.Fatalf("bad id: %d", w.Code)
	}
}
