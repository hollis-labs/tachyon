package approvals

import (
	"context"
	"testing"
)

// Migrated decisions keep the old build's actor labels: the old build's operator session ref maps to the
// owner and shows "You" to them; other old browser-session refs show "Operator session". A
// non-operator never sees "You" on the legacy ref. Stored rows are not changed.
func TestLegacyActorLabels(t *testing.T) {
	ctx := context.Background()
	in := newInbox(t)
	a := enqueue(t, in, seed{key: "a", class: "low", agent: "bob"})
	b := enqueue(t, in, seed{key: "b", class: "low", agent: "bob"})
	other := "browser-session:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, e := range []Event{
		{EventID: "evt_legacy_op", ItemID: a, Kind: "presented", ActorRef: legacyOperatorRef, Assurance: "operator-token", Revision: 1},
		{EventID: "evt_legacy_other", ItemID: b, Kind: "presented", ActorRef: other, Assurance: "browser-session", Revision: 1},
	} {
		if err := in.Store.LogEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	labels := func(path string) map[string]string {
		t.Helper()
		w := get(in, path, "s1")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		out := map[string]string{}
		for _, e := range jsonOf(t, w.Body.String())["events"].([]any) {
			m := e.(map[string]any)
			actor := m["actor"].(map[string]any)
			out[actor["ref"].(string)] = actor["label"].(string)
		}
		return out
	}
	elevated := true
	fakeTangent(t, &elevated, "", nil)
	if l := labels(Prefix + "/items/" + a + "/audit")[legacyOperatorRef]; l != "You" {
		t.Errorf("operator, audit: legacy operator ref labeled %q", l)
	}
	if l := labels(Prefix + "/items/" + b + "/audit")[other]; l != "Operator session" {
		t.Errorf("operator, audit: another old session labeled %q", l)
	}
	if got := labels(Prefix + "/history?kind=presented"); got[legacyOperatorRef] != "You" || got[other] != "Operator session" {
		t.Errorf("operator, history: %v", got)
	}
	elevated = false
	if l := labels(Prefix + "/items/" + a + "/audit")[legacyOperatorRef]; l != "Operator session" {
		t.Errorf("non-operator: legacy operator ref labeled %q", l)
	}
	var stored string
	if err := in.Store.DB.Reader.QueryRowContext(ctx, `SELECT actor_ref FROM approval_decision_log WHERE event_id='evt_legacy_op'`).Scan(&stored); err != nil || stored != legacyOperatorRef {
		t.Errorf("stored ref changed: %q %v", stored, err)
	}
}
