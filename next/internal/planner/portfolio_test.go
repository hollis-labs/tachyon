package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

func TestPortfolioPreservesFullPayloadAndRefreshes(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t).Open(t)
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
	if ierr := json.Unmarshal(r.Definition, &def); ierr != nil {
		t.Fatal(ierr)
	}
	if !bytes.Equal(canonicalJSON(raw), canonicalJSON(def.Properties.Torque.Item)) {
		t.Fatalf("payload changed: %s", def.Properties.Torque.Item)
	}
	links, err := objects.New(s).Links(ctx, r.ID, false)
	if err != nil || len(links) != 0 {
		t.Fatal("external IDs became local edges", err)
	}
}
