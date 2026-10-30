package approvals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

type gateEnv struct {
	inbox    *Inbox
	elevated *bool
}

func newGateEnv(t *testing.T) gateEnv {
	t.Helper()
	elevated := true
	sessions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		level := "browser-session"
		if elevated {
			level = "operator-token"
		}
		writeValue(w, map[string]any{"elevated": elevated, "assurance": level})
	}))
	t.Cleanup(sessions.Close)
	return gateEnv{inbox: NewInbox(nativeStore(t), New(sessions.URL)), elevated: &elevated}
}

func (g gateEnv) enqueue(t *testing.T, key, class, app string, digest string) string {
	t.Helper()
	req := requestFixture(t, key, class)
	if app != "" {
		req.Source.ApplicationID = app
	}
	if digest != "" {
		req.Extra["op_digest"], _ = json.Marshal(digest)
	}
	h, err := g.inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return h.ItemID
}

func (g gateEnv) item(t *testing.T, id string) map[string]any {
	t.Helper()
	w := serveJSON(g.inbox, "GET", Prefix+"/items/"+id, nil, "", "session")
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("%v %s", err, w.Body)
	}
	return m
}

func (g gateEnv) resolve(id string, rev int, decision, phrase string) *httptest.ResponseRecorder {
	resp := map[string]string{"kind": "approval", "decision": decision}
	if phrase != "" {
		resp["confirmation"] = phrase
	}
	return serveJSON(g.inbox, "POST", Prefix+"/items/"+id+"/resolve", map[string]any{"expected_revision": rev, "presented_projection_revision": rev, "response": resp}, "", "session")
}

func phraseOf(m map[string]any) string {
	c, _ := m["confirm"].(map[string]any)
	p, _ := c["phrase"].(string)
	return p
}

func TestApprovableProjection(t *testing.T) {
	g := newGateEnv(t)
	for class, want := range map[string]bool{"low": true, "medium": true, "high": true} {
		m := g.item(t, g.enqueue(t, "k-"+class, class, "", ""))
		if m["approvable"] != want || m["approvable_reason"] != nil {
			t.Errorf("%s: %v %v", class, m["approvable"], m["approvable_reason"])
		}
		if (class == "high") != (phraseOf(m) != "") {
			t.Errorf("%s: confirm %v", class, m["confirm"])
		}
	}
	if m := g.item(t, g.enqueue(t, "k-cerb", "low", "cerberus", "")); m["approvable"] != true || phraseOf(m) == "" {
		t.Errorf("cerberus source needs the phrase: %v", m)
	}
	done := g.enqueue(t, "k-done", "low", "", "")
	if w := g.resolve(done, 1, "approved", ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if m := g.item(t, done); m["approvable"] != false || m["approvable_reason"] != ReasonAlreadyResolved {
		t.Errorf("resolved: %v %v", m["approvable"], m["approvable_reason"])
	}
}

func TestCanResolveReasons(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	mk := func(class string, exp *time.Time, state hitl.State) hitl.Record {
		raw, _ := json.Marshal(map[string]any{"class": class, "source": map[string]string{"application_id": "x"}})
		return hitl.Record{ItemID: "item_1", State: state, ExpiresAt: exp, Request: raw}
	}
	if g := CanResolve(mk("low", &past, hitl.StatePresented), now); g.Approvable || g.Reason != ReasonExpired {
		t.Errorf("expired: %+v", g)
	}
	if g := CanResolve(mk("weird", nil, hitl.StatePresented), now); g.Approvable || g.Reason != ReasonUnsupportedKind {
		t.Errorf("unsupported: %+v", g)
	}
	if g := CanResolve(mk("high", nil, hitl.StatePresented), now); !g.Approvable || g.Phrase == "" {
		t.Errorf("high: %+v", g)
	}
}

func TestHighResolveNeedsPhraseAndOperator(t *testing.T) {
	g := newGateEnv(t)
	id := g.enqueue(t, "h1", "high", "", "")
	phrase := phraseOf(g.item(t, id))
	if !strings.HasPrefix(phrase, "APPROVE ") {
		t.Fatalf("phrase %q", phrase)
	}
	*g.elevated = false
	if w := g.resolve(id, 1, "approved", phrase); w.Code != 403 || !strings.Contains(w.Body.String(), "assurance_insufficient") {
		t.Fatalf("no elevation: %d %s", w.Code, w.Body)
	}
	*g.elevated = true
	for name, p := range map[string]string{"missing": "", "wrong": "APPROVE 00000000", "case": strings.ToLower(phrase)} {
		if w := g.resolve(id, 1, "approved", p); w.Code != 422 || !strings.Contains(w.Body.String(), "confirmation_required") {
			t.Errorf("%s phrase: %d %s", name, w.Code, w.Body)
		}
	}
	if w := g.resolve(id, 7, "approved", phrase); w.Code != 409 {
		t.Errorf("stale revision: %d %s", w.Code, w.Body)
	}
	if n := countEvents(t, g, id, "confirmation_failed"); n != 3 {
		t.Errorf("confirmation_failed events: %d", n)
	}
	if w := g.resolve(id, 1, "approved", phrase); w.Code != 200 {
		t.Fatalf("right phrase: %d %s", w.Code, w.Body)
	}
	if w := g.resolve(id, 1, "approved", phrase); w.Code != 409 {
		t.Errorf("double submit: %d %s", w.Code, w.Body)
	}
}

func TestDenyNeedsNoPhrase(t *testing.T) {
	g := newGateEnv(t)
	id := g.enqueue(t, "h2", "high", "", "")
	if w := g.resolve(id, 1, "denied", ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The phrase is derived from the op_digest, so changing what would run changes what must be typed.
func TestPhraseIsBoundToTheDigest(t *testing.T) {
	g := newGateEnv(t)
	a := g.enqueue(t, "d1", "high", "", "sha256:"+strings.Repeat("1", 64))
	b := g.enqueue(t, "d2", "high", "", "sha256:"+strings.Repeat("2", 64))
	pa, pb := phraseOf(g.item(t, a)), phraseOf(g.item(t, b))
	if pa == "" || pa == pb {
		t.Fatalf("phrases %q %q", pa, pb)
	}
	if w := g.resolve(b, 1, "approved", pa); w.Code != 422 {
		t.Errorf("phrase of another digest: %d %s", w.Code, w.Body)
	}
}

func countEvents(t *testing.T, g gateEnv, id, kind string) int {
	t.Helper()
	var n int
	row := g.inbox.Store.DB.Reader.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM approval_decision_log WHERE item_id = ? AND kind = ?`, id, kind)
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
