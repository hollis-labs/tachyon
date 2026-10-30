package approvals

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeKeyFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret.key")
	key := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(p, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type secretFix struct {
	inbox    *Inbox
	elevated *bool
	item     string
}

func newSecretFix(t *testing.T, keyFile string) secretFix {
	t.Helper()
	elevated := true
	sessions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := "browser-session"
		if elevated {
			a = "operator-token"
		}
		writeValue(w, map[string]any{"elevated": elevated, "assurance": a})
	}))
	t.Cleanup(sessions.Close)
	t.Setenv(SecretKeyFileEnv, keyFile)
	inbox := NewInbox(nativeStore(t), New(sessions.URL))
	form := cardRequest("k-secret", map[string]any{"data": map[string]any{"fields": []map[string]any{{"id": "name", "type": "text"}, {"id": "token", "type": "secret"}}}})
	form.Kind = "card.form"
	if err := prepareRequest(&form, ProducerPolicy{ApplicationID: "planner", MinimumClass: "medium"}); err != nil {
		t.Fatal(err)
	}
	h, err := inbox.Service.Enqueue(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	return secretFix{inbox: inbox, elevated: &elevated, item: h.ItemID}
}

func postSecret(f secretFix, body map[string]any, hdr ...string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "http://tachyon.test"+Prefix+"/secrets", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://tachyon.test")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "session"})
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	f.inbox.ServeHTTP(w, r)
	return w
}

func submitSecretRef(t *testing.T, in *Inbox, item, field, value string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"item_id": item, "field": field, "value": value})
	r := httptest.NewRequest("POST", "http://tachyon.test"+Prefix+"/secrets", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://tachyon.test")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "session"})
	w := httptest.NewRecorder()
	in.ServeHTTP(w, r)
	var m SecretMeta
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil || m.Ref == "" {
		t.Fatalf("submit: %d %s", w.Code, w.Body)
	}
	return m.Ref
}

func TestSecretIsEncryptedAtRestAndNeverReturned(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	w := postSecret(f, map[string]any{"item_id": f.item, "field": "token", "value": "hunter2-value"})
	var m SecretMeta
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.HasPrefix(m.Ref, SecretRefPrefix) || m.Size != len("hunter2-value") || !strings.HasPrefix(m.Fingerprint, "sha256:") || m.ExpiresAt.Before(m.CreatedAt) || strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("%+v %s", m, w.Body)
	}
	var ct []byte
	id := m.Ref[len(SecretRefPrefix):]
	if err := f.inbox.Store.DB.Reader.QueryRow(`SELECT ciphertext FROM approval_secrets WHERE id=?`, id).Scan(&ct); err != nil || bytes.Contains(ct, []byte("hunter2")) || len(ct) <= len("hunter2-value") {
		t.Fatalf("ciphertext: %v %x", err, ct)
	}
	// The audit event holds the reference and size, never the value.
	rows, err := f.inbox.Store.DB.Reader.Query(`SELECT kind, extra FROM approval_decision_log WHERE item_id=?`, f.item)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	logged := false
	for rows.Next() {
		var kind, extra string
		_ = rows.Scan(&kind, &extra)
		if kind == "secret_submitted" {
			logged = strings.Contains(extra, m.Ref) && !strings.Contains(extra, "hunter2")
		}
	}
	if !logged {
		t.Error("no audit event with the reference")
	}
	// Redemption is a server-side method, bound to the item and field.
	if v, err := f.inbox.Secrets.Redeem(context.Background(), f.item, "token", m.Ref); err != nil || string(v) != "hunter2-value" {
		t.Fatalf("redeem: %q %v", v, err)
	}
	if _, err := f.inbox.Secrets.Redeem(context.Background(), f.item, "name", m.Ref); err == nil {
		t.Error("redeemed under another field")
	}
}

func TestSecretSubmitErrors(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	ok := map[string]any{"item_id": f.item, "field": "token", "value": "v"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, c := range map[string]struct {
		body map[string]any
		want int
	}{
		"unknown item":    {with("item_id", "ap_missing"), 404},
		"unknown field":   {with("field", "name"), 409},
		"no such field":   {with("field", "nope"), 409},
		"empty value":     {with("value", ""), 400},
		"too large":       {with("value", strings.Repeat("a", MaxSecretValue+1)), 413},
		"unknown member":  {with("extra", 1), 400},
		"bad item id":     {with("item_id", "../x"), 400},
		"largest allowed": {with("value", strings.Repeat("a", MaxSecretValue)), 201},
	} {
		if w := postSecret(f, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	*f.elevated = false
	if w := postSecret(f, ok); w.Code != 403 || !strings.Contains(w.Body.String(), "assurance_insufficient") {
		t.Errorf("not elevated: %d %s", w.Code, w.Body)
	}
	*f.elevated = true
	if w := postSecret(f, ok, "Origin", "https://other.test"); w.Code != 403 {
		t.Errorf("cross origin: %d", w.Code)
	}
	if w := postSecret(f, ok, "Content-Type", "text/plain"); w.Code != 400 {
		t.Errorf("content type: %d", w.Code)
	}
}

func TestSecretMultipartAndLimits(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	post := func(file []byte) *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		_ = mw.WriteField("item_id", f.item)
		_ = mw.WriteField("field", "token")
		part, _ := mw.CreateFormFile("file", "key.pem")
		_, _ = part.Write(file)
		_ = mw.Close()
		r := httptest.NewRequest("POST", "http://tachyon.test"+Prefix+"/secrets", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("Origin", "http://tachyon.test")
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "session"})
		w := httptest.NewRecorder()
		f.inbox.ServeHTTP(w, r)
		return w
	}
	if w := post(bytes.Repeat([]byte("k"), MaxSecretFile)); w.Code != 201 {
		t.Errorf("64 KiB file: %d %s", w.Code, w.Body)
	}
	if w := post(bytes.Repeat([]byte("k"), MaxSecretFile+1)); w.Code != 413 {
		t.Errorf("over 64 KiB: %d", w.Code)
	}
	// 10 per minute per session; the two above already counted.
	now := time.Now()
	f.inbox.Secrets.Now = func() time.Time { return now }
	got429 := false
	for n := 0; n < 12 && !got429; n++ {
		got429 = post([]byte("x")).Code == 429
	}
	if !got429 {
		t.Error("no rate limit")
	}
	f.inbox.Secrets.Now = func() time.Time { return now.Add(2 * time.Minute) }
	if w := post([]byte("x")); w.Code == 429 {
		t.Error("rate limit did not reset")
	}
}

func TestSecretIdempotencyAndLiveCap(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	body := map[string]any{"item_id": f.item, "field": "token", "value": "same"}
	a := postSecret(f, body, "Idempotency-Key", "k1")
	b := postSecret(f, body, "Idempotency-Key", "k1")
	var ma, mb SecretMeta
	_ = json.Unmarshal(a.Body.Bytes(), &ma)
	_ = json.Unmarshal(b.Body.Bytes(), &mb)
	if a.Code != 201 || b.Code != 201 || ma.Ref != mb.Ref {
		t.Fatalf("replay: %d %d %s %s", a.Code, b.Code, ma.Ref, mb.Ref)
	}
	body["value"] = "different"
	if w := postSecret(f, body, "Idempotency-Key", "k1"); w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_key_reuse") {
		t.Errorf("reuse: %d %s", w.Code, w.Body)
	}
	// 20 live per item (one used above); the rate limit is lifted for the loop.
	clock := time.Now()
	for n := 0; n < 30; n++ {
		clock = clock.Add(time.Minute)
		now := clock
		f.inbox.Secrets.Now = func() time.Time { return now }
		w := postSecret(f, map[string]any{"item_id": f.item, "field": "token", "value": "v"})
		if w.Code == 409 && strings.Contains(w.Body.String(), "secret_limit") {
			return
		}
	}
	t.Error("no cap on live secrets per item")
}

func TestSecretsUnavailableWithoutAKey(t *testing.T) {
	for name, key := range map[string]string{"unset": "", "missing": filepath.Join(t.TempDir(), "nope"), "wrong size": func() string {
		p := filepath.Join(t.TempDir(), "k")
		_ = os.WriteFile(p, []byte("short"), 0o600)
		return p
	}()} {
		f := newSecretFix(t, key)
		w := postSecret(f, map[string]any{"item_id": f.item, "field": "token", "value": "v"})
		if w.Code != 503 || !strings.Contains(w.Body.String(), "secrets_unavailable") || strings.Contains(w.Body.String(), "nope") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestResolveValidatesSecretReferences(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	other := newSecretFix(t, writeKeyFile(t)) // a different item (and store)
	foreign := submitSecretRef(t, other.inbox, other.item, "token", "x")
	good := submitSecretRef(t, f.inbox, f.item, "token", "x")
	resolve := func(ref any) *httptest.ResponseRecorder {
		body := map[string]any{"expected_revision": 1, "presented_projection_revision": 1, "response": map[string]any{
			"kind": "card.form", "decision": "submitted", "answers": []map[string]any{{"questionId": "name", "value": "n"}, {"questionId": "token", "value": ref}}}}
		return serveJSON(f.inbox, "POST", Prefix+"/items/"+f.item+"/resolve", body, "", "session")
	}
	for name, ref := range map[string]any{
		"typed string":      "hunter2",
		"not a reference":   map[string]string{"secret_ref": "hunter2"},
		"unknown reference": map[string]string{"secret_ref": SecretRefPrefix + strings.Repeat("0", 32)},
		"another item":      map[string]string{"secret_ref": foreign},
	} {
		w := resolve(ref)
		if (name == "typed string" && w.Code != 400) || (name != "typed string" && (w.Code != 422 || !strings.Contains(w.Body.String(), "secret_ref_invalid"))) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := resolve(map[string]string{"secret_ref": good}); w.Code != 200 {
		t.Fatalf("good reference: %d %s", w.Code, w.Body)
	}
	var consumed int64
	id := good[len(SecretRefPrefix):]
	if err := f.inbox.Store.DB.Reader.QueryRow(`SELECT COALESCE(consumed_ns,0) FROM approval_secrets WHERE id=?`, id).Scan(&consumed); err != nil || consumed == 0 {
		t.Errorf("not consumed: %v %d", err, consumed)
	}
	if err := f.inbox.Secrets.Validate(context.Background(), f.item, "token", good); err == nil {
		t.Error("a consumed reference validated again")
	}
}

func TestPurgeRemovesExpiredAndOrphanedSecrets(t *testing.T) {
	f := newSecretFix(t, writeKeyFile(t))
	ref := submitSecretRef(t, f.inbox, f.item, "token", "x")
	ctx := context.Background()
	if n, err := f.inbox.Secrets.Purge(ctx); err != nil || n != 0 {
		t.Fatalf("live secret purged: %d %v", n, err)
	}
	// The item ends without using the reference (denied/withdrawn): once five minutes old it goes.
	err := f.inbox.Store.DB.Writer.Submit(ctx, "test_terminal", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE approval_items SET terminal=1 WHERE id=?`, f.item)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(10 * time.Minute)
	f.inbox.Secrets.Now = func() time.Time { return now }
	if n, err := f.inbox.Secrets.Purge(ctx); err != nil || n != 1 {
		t.Fatalf("orphan purge: %d %v", n, err)
	}
	if err := f.inbox.Secrets.Validate(ctx, f.item, "token", ref); err == nil {
		t.Error("purged reference still validates")
	}
}
