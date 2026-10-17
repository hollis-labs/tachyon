package apicache

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestImportedTeamsSurviveRestartAndStayImportedOnRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db := openTest(t, path)
	c := New(db, Defaults(), nil)
	body := []byte(`[{"id":"first-team","name":"First team","version":2,"slots":[{"name":"workers","role":"","definition_ref":{"id":"worker","revision":"r1","digest":"sha256:abc"}}],"authority":{"mode":"strict","grants":[]},"extension":{"preserve":true}}]`)
	count, err := c.ImportTeams(context.Background(), "agent-os Tether", body)
	if err != nil || count != 1 {
		t.Fatalf("import: %d %v", count, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTest(t, path)
	defer db.Close()
	c = New(db, Defaults(), func(string) (string, error) {
		t.Fatal("manual snapshots must not access an upstream identity")
		return "", nil
	})
	first := request(http.HandlerFunc(c.ImportedTeamsHandler), "GET", ImportedTeamsPath, false)
	state(t, first, "imported")
	var snap TeamSnapshot
	if err := json.Unmarshal(first.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Source != "agent-os Tether" || snap.ImportedAt == "" || len(snap.Teams) != 1 {
		t.Fatalf("snapshot metadata: %+v", snap)
	}
	var original, copied any
	_ = json.Unmarshal(body, &original)
	copiedJSON, _ := json.Marshal(snap.Teams)
	_ = json.Unmarshal(copiedJSON, &copied)
	originalJSON, _ := json.Marshal(original)
	normalizedJSON, _ := json.Marshal(copied)
	if string(originalJSON) != string(normalizedJSON) {
		t.Fatal("import changed original definition fields")
	}
	refreshed := request(http.HandlerFunc(c.ImportedTeamsHandler), "GET", ImportedTeamsPath, true)
	state(t, refreshed, "imported")
	if refreshed.Body.String() != first.Body.String() {
		t.Fatal("refresh changed import timestamp or contents")
	}
	if got := request(http.HandlerFunc(c.ImportedTeamsHandler), "POST", ImportedTeamsPath, false); got.Code != 405 {
		t.Fatalf("HTTP write accepted: %d", got.Code)
	}
	if _, err := c.ImportTeams(context.Background(), "source", []byte(`[{"id":"bad"}]`)); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	if got := request(http.HandlerFunc(c.ImportedTeamsHandler), "GET", ImportedTeamsPath, false); got.Body.String() != first.Body.String() {
		t.Fatal("invalid import replaced existing snapshot")
	}
	c.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	expired := request(http.HandlerFunc(c.ImportedTeamsHandler), "GET", ImportedTeamsPath, false)
	if expired.Header().Get("X-Tachyon-Cache-Enabled") != "false" {
		t.Fatal("expired import still cached")
	}
	var empty TeamSnapshot
	_ = json.Unmarshal(expired.Body.Bytes(), &empty)
	if len(empty.Teams) != 0 {
		t.Fatal("expired snapshot served")
	}
}
