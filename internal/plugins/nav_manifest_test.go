package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestPluginManifests_ConformToNavVocabulary(t *testing.T) {
	pluginsDir := filepath.Join("..", "..", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("failed to read plugins dir: %v", err)
	}

	manifestCount := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "hello" {
			continue
		}
		capFile := filepath.Join(pluginsDir, entry.Name(), "capabilities.json")
		raw, err := os.ReadFile(capFile)
		if err != nil {
			t.Fatalf("failed to read %s: %v", capFile, err)
		}

		var caps contract.PluginCapabilities
		if err := json.Unmarshal(raw, &caps); err != nil {
			t.Fatalf("failed to parse %s: %v", capFile, err)
		}
		if caps.Nav == nil {
			continue
		}
		manifestCount++

		// Check groups
		groupIDs := map[string]bool{}
		for _, g := range caps.Nav.Groups {
			if groupIDs[g.ID] {
				t.Errorf("%s: duplicate nav group ID %q", entry.Name(), g.ID)
			}
			groupIDs[g.ID] = true

			if g.Icon != "" && !contract.IsValidNavIcon(g.Icon) {
				t.Errorf("%s: group %q specifies unknown icon %q", entry.Name(), g.ID, g.Icon)
			}
		}

		// Check items
		itemIDs := map[string]bool{}
		routes := map[string]bool{}
		for _, item := range caps.Nav.Items {
			if itemIDs[item.ID] {
				t.Errorf("%s: duplicate nav item ID %q", entry.Name(), item.ID)
			}
			itemIDs[item.ID] = true

			if item.Route != "" {
				if !strings.HasPrefix(item.Route, "/") {
					t.Errorf("%s: item %q route %q missing leading slash", entry.Name(), item.ID, item.Route)
				}
				if routes[item.Route] {
					t.Errorf("%s: duplicate route %q in item %q", entry.Name(), item.Route, item.ID)
				}
				routes[item.Route] = true

				// Check reserved routes
				if allowedOwner, isReserved := matchReservedRoute(item.Route); isReserved {
					if allowedOwner == "" || allowedOwner != entry.Name() {
						t.Errorf("%s: item %q unlawfully claims reserved host route %q", entry.Name(), item.ID, item.Route)
					}
				}
			}
		}
	}

	if manifestCount < 8 {
		t.Fatalf("expected at least 8 manifests audited, got %d", manifestCount)
	}
}
