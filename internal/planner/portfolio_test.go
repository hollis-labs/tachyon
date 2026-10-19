package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestPortfolioPreservesFullPayloadAndRefreshes(t *testing.T) {
	ctx := context.Background()
	s, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := json.RawMessage(`{"id":"DEC-1","title":"Decision","status":"decided","rev":3,"comments":[{"id":"c-1","text":"Reason"}],"options":[{"id":"o1","body":"Choice"}],"links":[{"kind":"torque","ref":"CW-1"}],"future":{"number":9007199254740993},"related_ids":["WS-1"]}`)
	obs, err := PortfolioObservation("https://torque.test", "decisions", raw, 7, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := objects.New(s).RefreshImported(ctx, obs, "migration")
	if err != nil {
		t.Fatal(err)
	}
	var def struct {
		Properties struct {
			Torque struct {
				Item json.RawMessage `json:"item"`
			} `json:"torque_planner"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(r.Definition, &def); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalJSON(raw), canonicalJSON(def.Properties.Torque.Item)) {
		t.Fatalf("payload changed: %s", def.Properties.Torque.Item)
	}
	links, err := objects.New(s).Links(ctx, r.ID, false)
	if err != nil || len(links) != 0 {
		t.Fatal("external IDs became local edges", err)
	}
}

func TestPortfolioCaptureStopsOnConcurrentSourceChange(t *testing.T) {
	reads := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("filtered snapshot")
		}
		if strings.HasSuffix(r.URL.Path, "/ideas") {
			reads++
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "ID-1", "title": "Idea", "status": "new", "rev": reads}})
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer up.Close()
	err := Command(context.Background(), t.TempDir(), catalog.Upstreams{TorqueURL: up.URL}, []string{"--snapshot", filepath.Join(t.TempDir(), "snapshot.json"), "--apply"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "changed during capture") {
		t.Fatal(err)
	}
}
