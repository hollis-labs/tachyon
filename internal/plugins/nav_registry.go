package plugins

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// NavDescriptors publishes ADR002's declarative policy. Callers get fresh maps
// and slices; the source-only TS catalog consumer is staged in 0118.
func NavDescriptors() (map[string]registry.KindDescriptor, map[string]registry.RegionDescriptor) {
	kindRegions := map[string][]string{
		"nav.group": {"nav.rail"}, "nav.item": {"nav.rail"}, "page": {"page.routes"},
		"subnav.item": {"nav.subnav.left", "nav.subnav.top"},
		"menu.item":   {"menu.header", "menu.page", "menu.row", "menu.hamburger"},
	}
	kinds := map[string]registry.KindDescriptor{}
	regions := map[string]registry.RegionDescriptor{}
	for _, kind := range sortedNavKeys(kindRegions) {
		names := kindRegions[kind]
		kinds[kind] = registry.KindDescriptor{SchemaVersion: 1, MetadataSchema: json.RawMessage(`{}`), Representations: []registry.Representation{registry.Declarative}, Regions: names, RequiredCapabilities: []string{}}
		for _, name := range names {
			d, ok := regions[name]
			if !ok {
				ordering := "priority-ascending"
				if name == "page.routes" {
					ordering = "manifest"
				}
				d = registry.RegionDescriptor{Kinds: []string{}, Representations: []registry.Representation{registry.Declarative}, ContextSchema: json.RawMessage(`{}`), Ordering: ordering}
			}
			d.Kinds = append(d.Kinds, kind)
			regions[name] = d
		}
	}
	return kinds, regions
}

func projectionStatus(diags []contract.NavDiagnostic, revision uint64) *contract.NavProjection {
	p := &contract.NavProjection{Status: contract.NavProjectionOK, Revision: int64(revision)}
	for _, d := range diags {
		if d.Dropped {
			p.Dropped++
		}
	}
	if p.Dropped > 0 {
		p.Status = contract.NavProjectionDegraded
	}
	return p
}

// projectRegistry never grants capabilities or publishes executable page code.
// Structural validation happens before the document reaches either HTTP surface.
func projectRegistry(resp *RegistryResponse, resolved NavResolutionResult, logger *slog.Logger) {
	resp.Kinds, resp.Regions = NavDescriptors()
	resp.Diagnostics = resolved.Nav.Diagnostics
	resp.Notices = resolved.Nav.Notices
	resp.NavProjection = *projectionStatus(resp.Diagnostics, resp.Revision)
	var projectionErr error
	emit := func(kind, owner, id, binding string, metadata any, declarative any) {
		meta, err := json.Marshal(metadata)
		if err != nil {
			projectionErr = err
			return
		}
		payload, err := json.Marshal(declarative)
		if err != nil {
			projectionErr = err
			return
		}
		c := registry.Contribution{Status: registry.StatusAccepted, OwnerID: owner, OwnerGeneration: resp.Plugins[owner].OwnerGeneration, LocalKey: id, Kind: kind, SchemaVersion: 1, Required: false, Representation: registry.Declarative, PublicBinding: binding, Metadata: meta, Declarative: payload}
		if err := resp.Set(c); err != nil {
			projectionErr = err
		}
	}
	meta := func(kind, owner, id string, v any) map[string]any {
		raw, _ := json.Marshal(v)
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		delete(m, "plugin_id")
		delete(m, "requires_verb")
		delete(m, "group_ref")
		m["manifest_order"] = resolved.Orders[navOrderKey(kind, owner, id)]
		if kind != "page" {
			priority, ok := m["priority"].(float64)
			if !ok || priority == 0 {
				m["priority"] = 1000
			}
		}
		return m
	}
	for _, g := range resolved.Nav.Groups {
		if g.ID == "more" {
			continue
		}
		emit("nav.group", g.PluginID, g.ID, g.ID, meta("nav.group", g.PluginID, g.ID, g), map[string]any{})
	}
	for _, it := range resolved.Nav.Items {
		m := meta("nav.item", it.PluginID, it.ID, it)
		m["placement"] = resolved.Placements[it.ID]
		m["declared_group"] = resolved.DeclaredGroups[it.ID]
		m["requires_verbs"] = append([]string{}, it.RequiresVerbs...)
		emit("nav.item", it.PluginID, it.ID, it.ID, m, map[string]any{})
	}
	for _, p := range resolved.Nav.Pages {
		m := meta("page", p.PluginID, p.ID, p)
		delete(m, "view")
		m["requires_verbs"] = append([]string{}, p.RequiresVerbs...)
		emit("page", p.PluginID, p.ID, p.Route, m, map[string]any{"view": p.View})
	}
	for _, s := range resolved.Nav.Subnav {
		m := meta("subnav.item", s.PluginID, s.ID, s)
		m["requires_verbs"] = append([]string{}, s.RequiresVerbs...)
		emit("subnav.item", s.PluginID, s.ID, s.ID, m, map[string]any{})
	}
	for _, menu := range resolved.Nav.Menus {
		m := meta("menu.item", menu.PluginID, menu.ID, menu)
		delete(m, "action")
		m["requires_verbs"] = append([]string{}, menu.RequiresVerbs...)
		emit("menu.item", menu.PluginID, menu.ID, menu.ID, m, map[string]any{"action": menu.Action})
	}
	// Refusal identity must not collide with an accepted first occurrence or with
	// another drop of the same authored ID. Encode such rejection occurrences.
	used := map[string]bool{}
	for index, d := range resp.Diagnostics {
		if !d.Dropped {
			continue
		}
		kind := d.Kind
		if kind == "" {
			kind = "nav"
		}
		key := d.ID
		identity := navOrderKey(kind, d.PluginID, key)
		_, accepted := resp.Contributions[kind][registry.QualifiedKey(d.PluginID, key)]
		if key == "" || accepted || used[identity] {
			key = "rejected-" + hex.EncodeToString([]byte(fmt.Sprintf("%d:%s:%s", index, d.ID, d.Reason)))
		}
		// Hostile authored IDs can imitate the encoded rejection key. Probe
		// both namespaces until this rejection gets an unused identity.
		baseKey := key
		for suffix := 1; ; suffix++ {
			_, accepted := resp.Contributions[kind][registry.QualifiedKey(d.PluginID, key)]
			if !accepted && !used[navOrderKey(kind, d.PluginID, key)] {
				break
			}
			key = fmt.Sprintf("%s-%d", baseKey, suffix)
		}
		used[navOrderKey(kind, d.PluginID, key)] = true
		resp.Refusals = append(resp.Refusals, registry.Refusal{OwnerID: d.PluginID, OwnerGeneration: resp.Plugins[d.PluginID].OwnerGeneration, Kind: kind, LocalKey: key, Reason: d.Reason, Required: false})
	}
	if projectionErr == nil {
		projectionErr = resp.Response.Validate()
	}
	if projectionErr != nil {
		if logger != nil {
			logger.Error("navigation projection failed", "reason", "nav-projection-invalid", "error", projectionErr)
		}
		resp.Contributions = map[string]map[string]registry.Contribution{}
		resp.Refusals = []registry.Refusal{}
		resp.NavProjection = contract.NavProjection{Status: contract.NavProjectionFailed, Revision: int64(resp.Revision), Reason: "nav-projection-invalid"}
	}
}

func validNavMenu(m contract.NavMenu, owner string, pages map[string]contract.NavPage, routes map[string]string) bool {
	if m.Region != "header" && m.Region != "page" && m.Region != "row" && m.Region != "hamburger" {
		return false
	}
	if (m.Region == "page" && (m.Target == nil || m.Target.Page == "")) || (m.Region == "row" && (m.Target == nil || m.Target.Row == "")) {
		return false
	}
	if m.Target != nil && m.Target.Page != "" {
		p, ok := pages[m.Target.Page]
		if !ok || p.PluginID != owner {
			return false
		}
	}
	var action map[string]json.RawMessage
	if json.Unmarshal(m.Action, &action) != nil || action == nil {
		return false
	}
	var tag string
	if json.Unmarshal(action["type"], &tag) != nil {
		return false
	}
	object := func(raw json.RawMessage) bool {
		if len(raw) == 0 {
			return true
		}
		var v map[string]json.RawMessage
		return json.Unmarshal(raw, &v) == nil && v != nil
	}
	switch tag {
	case "navigate":
		var route string
		if json.Unmarshal(action["route"], &route) != nil {
			return false
		}
		_, ok := routes[route]
		return ok && object(action["parameters"])
	case "command":
		var command string
		if json.Unmarshal(action["command"], &command) != nil {
			return false
		}
		local, ok := strings.CutPrefix(command, owner+"/")
		return ok && local != "" && !strings.Contains(local, "/") && object(action["arguments"])
	default:
		return false
	}
}

// UnmarshalJSON preserves SDK exact-key and duplicate-key admission while
// decoding Tachyon's additive host signals. An embedded SDK unmarshaler alone
// would swallow nav_projection/diagnostics/retired_plugins on Go readback.
func (resp *RegistryResponse) UnmarshalJSON(raw []byte) error {
	var base registry.Response
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	var additions struct {
		RetiredPlugins []contract.SettingsTarget `json:"retired_plugins"`
		NavProjection  contract.NavProjection    `json:"nav_projection"`
		Diagnostics    []contract.NavDiagnostic  `json:"diagnostics"`
		Notices        []string                  `json:"notices"`
	}
	if err := json.Unmarshal(raw, &additions); err != nil {
		return err
	}
	*resp = RegistryResponse{Response: base, RetiredPlugins: additions.RetiredPlugins, NavProjection: additions.NavProjection, Diagnostics: additions.Diagnostics, Notices: additions.Notices}
	return nil
}
