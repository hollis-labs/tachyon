package itemstate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const prefix = "/api/plugins/console/v1/console/state"

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Unix(1_800_000_000, 0).UTC()
	return &Store{DB: db, Prefixes: []string{"pr-inbox:", "pr-assign:"}, Now: func() time.Time { return now }}, &now
}

func call(s *Store, method, path, body string, hdr ...string) (int, map[string]any) {
	r := httptest.NewRequest(method, "http://x"+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.Handler(prefix).ServeHTTP(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

const k = "pr-inbox:github:hollis-labs/tachyon%23201"

func TestOpsVersionsAndTheUIShape(t *testing.T) {
	s, _ := newStore(t)
	code, m := call(s, "PUT", prefix+"/"+k, `{"type":"snooze","preset":"4h","note":"later"}`)
	d := m["data"].(map[string]any)
	if code != 200 || d["status"] != "snoozed" || d["version"].(float64) != 1 || d["note"] != "later" || d["key"] != "pr-inbox:github:hollis-labs/tachyon#201" || d["by"] != "operator" {
		t.Fatalf("snooze: %d %v", code, m)
	}
	if d["snoozeUntil"] != "2027-01-15T12:00:00Z" {
		t.Errorf("snoozeUntil: %v", d["snoozeUntil"])
	}
	_, m = call(s, "PUT", prefix+"/"+k, `{"type":"settle","resolution":"merged"}`)
	d = m["data"].(map[string]any)
	if d["status"] != "settled" || d["resolution"] != "merged" || d["version"].(float64) != 2 || d["snoozeUntil"] != nil || d["note"] != "later" {
		t.Errorf("settle: %v", d)
	}
	_, m = call(s, "PUT", prefix+"/"+k, `{"type":"reopen"}`)
	d = m["data"].(map[string]any)
	if d["status"] != "unread" || d["resolution"] != nil || d["note"] != nil {
		t.Errorf("reopen clears resolution and note: %v", d)
	}
	for typ, want := range map[string]string{"read": "read", "dismiss": "dismissed"} {
		_, m = call(s, "PUT", prefix+"/"+k, `{"type":"`+typ+`"}`)
		if m["data"].(map[string]any)["status"] != want {
			t.Errorf("%s: %v", typ, m)
		}
	}
	code, m = call(s, "GET", prefix+"?prefix=pr-inbox:", "")
	if rows := m["data"].([]any); code != 200 || len(rows) != 1 || rows[0].(map[string]any)["version"].(float64) != 5 {
		t.Errorf("list: %v", m)
	}
	if _, m = call(s, "GET", prefix+"?prefix=pr-assign:", ""); len(m["data"].([]any)) != 0 {
		t.Error("prefix filter")
	}
}

func TestOptimisticVersioning(t *testing.T) {
	s, _ := newStore(t)
	if c, m := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"read","expected_version":1}`); c != 409 {
		t.Errorf("new row expects version 0: %d %v", c, m)
	}
	if c, _ := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"read","expected_version":0}`); c != 200 {
		t.Error("create with 0")
	}
	c, m := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"dismiss","expected_version":0}`)
	if c != 409 || m["error"].(map[string]any)["version"].(float64) != 1 || m["error"].(map[string]any)["code"] != "conflict" {
		t.Errorf("stale write carries the current version: %d %v", c, m)
	}
	if c, _ := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"dismiss","expected_version":1}`); c != 200 {
		t.Error("matching version")
	}
}

func TestValidation(t *testing.T) {
	s, _ := newStore(t)
	for name, tc := range map[string][2]string{
		"type":     {"pr-inbox:a", `{"type":"nuke"}`},
		"prefix":   {"other:thing", `{"type":"read"}`},
		"emptykey": {"pr-inbox:", `{"type":"read"}`},
		"past":     {"pr-inbox:a", `{"type":"snooze","until":"2020-01-01T00:00:00Z"}`},
		"far":      {"pr-inbox:a", `{"type":"snooze","until":"2030-01-01T00:00:00Z"}`},
		"preset":   {"pr-inbox:a", `{"type":"snooze","preset":"9y"}`},
		"unknown":  {"pr-inbox:a", `{"type":"read","x":1}`},
		"note":     {"pr-inbox:a", `{"type":"read","note":"` + strings.Repeat("x", 501) + `"}`},
		"negver":   {"pr-inbox:a", `{"type":"read","expected_version":-1}`},
		"longkey":  {"pr-inbox:" + strings.Repeat("a", 200), `{"type":"read"}`},
	} {
		if c, _ := call(s, "PUT", prefix+"/"+tc[0], tc[1]); c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	until := time.Unix(1_800_000_000, 0).Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if c, _ := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"snooze","until":"`+until+`"}`); c != 200 {
		t.Error("explicit until")
	}
	if c, _ := call(s, "PUT", prefix+"/pr-inbox:a", `{"type":"read"}`, "Authorization", "Bearer x"); c != 403 {
		t.Error("agents are refused")
	}
	if c, _ := call(s, "POST", prefix+"/bulk", `{"ops":[]}`); c != 400 {
		t.Error("empty bulk")
	}
	if c, _ := call(s, "POST", prefix+"/bulk", `{"ops":[{"key":"pr-inbox:a","type":"read"}]}`, "Authorization", "Bearer x"); c != 403 {
		t.Error("bulk refuses agents")
	}
}

func TestBulkReportsPerKey(t *testing.T) {
	s, _ := newStore(t)
	_, m := call(s, "POST", prefix+"/bulk", `{"ops":[{"key":"pr-inbox:a","type":"dismiss"},{"key":"pr-inbox:b","type":"snooze","preset":"1h"},{"key":"bad","type":"read"},{"key":"pr-inbox:c","type":"read","expected_version":3}]}`)
	rs := m["data"].(map[string]any)["results"].([]any)
	if len(rs) != 4 {
		t.Fatal(rs)
	}
	ok := func(i int) bool { return rs[i].(map[string]any)["ok"].(bool) }
	if !ok(0) || !ok(1) || ok(2) || ok(3) {
		t.Errorf("results: %v", rs)
	}
	if rs[3].(map[string]any)["error"].(map[string]any)["code"] != "conflict" || rs[2].(map[string]any)["error"].(map[string]any)["code"] != "invalid_args" {
		t.Error("error codes")
	}
	over := `{"ops":[` + strings.TrimSuffix(strings.Repeat(`{"key":"pr-inbox:a","type":"read"},`, 101), ",") + `]}`
	if c, _ := call(s, "POST", prefix+"/bulk", over); c != 400 {
		t.Error("over the cap")
	}
}

func TestSnoozeWakesAndReportsKeys(t *testing.T) {
	s, now := newStore(t)
	var woke []string
	s.OnWake = func(keys []string) { woke = append(woke, keys...) }
	if _, err := s.Apply(context.Background(), "pr-inbox:a", "operator", Op{Type: "snooze", Preset: "1h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), "pr-inbox:b", "operator", Op{Type: "snooze", Preset: "4h"}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.Wake(context.Background()); len(keys) != 0 {
		t.Error("nothing is due yet")
	}
	*now = now.Add(90 * time.Minute)
	keys, err := s.Wake(context.Background())
	if err != nil || len(keys) != 1 || keys[0] != "pr-inbox:a" || len(woke) != 1 {
		t.Fatalf("wake: %v %v", keys, err)
	}
	a, _, _ := s.Get(context.Background(), "pr-inbox:a")
	b, _, _ := s.Get(context.Background(), "pr-inbox:b")
	if a.Status != "unread" || a.SnoozeUtil != nil || a.WakeAt == nil || a.Version != 2 || b.Status != "snoozed" {
		t.Errorf("a=%+v b=%+v", a, b)
	}
	if keys, _ := s.Wake(context.Background()); len(keys) != 0 {
		t.Error("wake is once")
	}
}

func TestErrorTypes(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Apply(context.Background(), "x", "o", Op{Type: "read"})
	if !errors.Is(err, ErrInvalid) {
		t.Error(err)
	}
	_, _ = s.Apply(context.Background(), "pr-inbox:z", "o", Op{Type: "read"})
	zero := 0
	_, err = s.Apply(context.Background(), "pr-inbox:z", "o", Op{Type: "read", ExpectedVersion: &zero})
	if !errors.Is(err, ErrConflict) {
		t.Error(err)
	}
}
