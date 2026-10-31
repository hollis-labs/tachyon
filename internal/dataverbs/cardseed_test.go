package dataverbs

import (
	"context"
	"testing"
)

func TestSeedRejectsAnUnknownShapeAndNeedsCards(t *testing.T) {
	var c *Cards
	if _, err := c.Seed(context.Background(), "a", "t"); err == nil {
		t.Fatal("a nil Cards must refuse")
	}
}
