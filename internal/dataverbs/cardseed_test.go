package dataverbs

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

func TestSeedRejectsAnUnknownShapeAndNeedsCards(t *testing.T) {
	var c *Cards
	if _, err := c.Seed(context.Background(), "a", "t"); err == nil {
		t.Fatal("a nil Cards must refuse")
	}
}

func TestSeedEveryShapeEnqueues(t *testing.T) {
	_, _, db := setup(t)
	inbox := approvals.NewInbox(&approvals.Store{DB: db}, nil)
	c := &Cards{Service: inbox.Service, Registry: approvals.CardRegistry}
	want := map[string]int{"a": 1, "b": 1, "c": 1, "d": 1, "e": 3}
	for shape, n := range want {
		ids, err := c.Seed(context.Background(), shape, "t1")
		if err != nil || len(ids) != n {
			t.Fatalf("shape %s: %v %v", shape, ids, err)
		}
		if shape == "e" {
			res, err := c.Service.Get(context.Background(), hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: ids[0], Caller: hitl.CallerAssertion{ApplicationID: "tachyon-test"}})
			if err != nil || !strings.Contains(string(res.Item.RequestSnapshot), `"class":"low"`) {
				t.Fatalf("shape e must stay low: %v", err)
			}
		}
	}
	if _, err := c.Seed(context.Background(), "z", "t1"); err == nil {
		t.Fatal("unknown shape accepted")
	}
}
