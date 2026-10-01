package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func declarationFor(module, label string) contract.PluginCapabilities {
	verb := module + "_list"
	return contract.PluginCapabilities{Modules: []string{module}, Verbs: map[string]contract.VerbDeclaration{verb: {Effect: contract.EffectReads}},
		Nav:      &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "shared", Label: label}}, Items: []contract.NavItem{{ID: verb, Group: "shared", RequiresVerb: verb}}},
		Settings: &contract.SettingsDeclaration{Fields: []contract.SettingsField{{Key: "endpoint", Type: contract.SettingsFieldString, Required: true}}},
	}
}

func TestCapabilityCarrierRetainsDeclarationsAndMergesNav(t *testing.T) {
	var logs bytes.Buffer
	m := NewManager(slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := context.Background()
	first := declarationFor("first", "First label")
	second := declarationFor("second", "Losing label")
	second.Nav.Items = append(second.Nav.Items, contract.NavItem{ID: "first_list", Group: "shared"})
	if err := m.initializePlugin(ctx, fakeProcess(t, "first-plugin", "declared", first)); err != nil {
		t.Fatal(err)
	}
	if err := m.initializePlugin(ctx, fakeProcess(t, "second-plugin", "declared", second)); err != nil {
		t.Fatal(err)
	}
	caps := m.AllCapabilities()
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]contract.PluginCapabilities
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["first"].Nav == nil || decoded["first"].Settings == nil || decoded["first"].Settings.Fields[0].Key != "endpoint" {
		t.Fatalf("lost declarations: %s", raw)
	}
	nav := m.MergedNav()
	if len(nav.Groups) != 1 || nav.Groups[0].Label != "First label" || len(nav.Items) != 2 || nav.Items[0].ID != "first_list" || nav.Items[1].ID != "second_list" {
		t.Fatalf("merged nav: %+v", nav)
	}
	for _, expected := range []string{"nav group collision", "nav item collision", `"winner":"first-plugin"`, `"loser":"second-plugin"`} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("missing collision log %q: %s", expected, logs.String())
		}
	}
	// Response snapshots cannot mutate the manager's merged metadata.
	nav.Groups[0].Label = "changed"
	if got := m.MergedNav(); got.Groups[0].Label != "First label" {
		t.Fatalf("aliased snapshot: %+v", got)
	}
}

func TestInvalidDeclarationsAreHardLoadErrors(t *testing.T) {
	for _, kind := range []string{"nav", "settings"} {
		t.Run(kind, func(t *testing.T) {
			m := NewManager(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
			caps := declarationFor("bad", "Bad")
			if kind == "nav" {
				caps.Nav.Items[0].RequiresVerb = "other_list"
			} else {
				caps.Settings.Fields[0].Default = false
			}
			err := m.initializePlugin(context.Background(), fakeProcess(t, "bad-plugin", "declared", caps))
			if err == nil || !strings.Contains(err.Error(), "validation failed") || m.ModuleOwner("bad") != "" || len(m.MergedNav().Groups) != 0 || len(m.AllCapabilities()) != 0 {
				t.Fatalf("invalid declaration registered: %v", err)
			}
		})
	}
}

func TestMergedNavPriorityAndEmptyState(t *testing.T) {
	m := NewManager(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if nav := m.MergedNav(); len(nav.Groups) != 0 || len(nav.Items) != 0 {
		t.Fatalf("nonempty nav: %+v", nav)
	}
	caps := declarationFor("nav", "Shared")
	caps.Nav.Groups = append(caps.Nav.Groups, contract.NavGroup{ID: "early", Label: "Early", Priority: 100})
	caps.Nav.Items = append(caps.Nav.Items, contract.NavItem{ID: "early_item", Group: "early", Priority: 100})
	if err := m.initializePlugin(context.Background(), fakeProcess(t, "nav-plugin", "declared", caps)); err != nil {
		t.Fatal(err)
	}
	if nav := m.MergedNav(); nav.Groups[0].ID != "early" || nav.Items[0].ID != "early_item" {
		t.Fatalf("incorrect priority order: %+v", nav)
	}
}
