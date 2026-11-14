package migratefrom

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The ledger list is the old tree's migration files, when this checkout has the old tree (slice P1
// deletes it). A new old-tree migration arriving from main fails this until the port carries it.
func TestLedgerNamesAreTheOldTreesMigrations(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "internal", "localstore", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("old tree not in this checkout: %v", err)
	}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	if !slices.Equal(names, ledgerNames) {
		t.Fatalf("old tree has %d migrations, the ledger list %d:\n%v\n%v", len(names), len(ledgerNames), names, ledgerNames)
	}
	if len(ledgerNames) != 56 {
		t.Fatalf("%d names", len(ledgerNames))
	}
}
