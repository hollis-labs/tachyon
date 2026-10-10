package contract

import (
	"encoding/json"
	"reflect"
	"strings"
)

type navDecodeIssue struct {
	kind, id, field string
	index           int
	raw             json.RawMessage
}
type navFieldPresence struct {
	kind, id, field string
	raw             json.RawMessage
	index           int
}

// UnmarshalJSON keeps non-nav decoding strict about types, while isolating nav
// type errors. Unknown future keys retain encoding/json's lenient semantics.
// Malformed known nav fields are retained on re-encoding so the plugin-side
// capabilities carrier cannot erase defects before host normalization.
func (pc *PluginCapabilities) UnmarshalJSON(data []byte) error {
	type plain PluginCapabilities
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawNav := fields["nav"]
	rawSchema := fields["nav_schema"]
	delete(fields, "nav")
	delete(fields, "nav_schema")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var decoded plain
	if err := json.Unmarshal(rest, &decoded); err != nil {
		return err
	}
	*pc = PluginCapabilities(decoded)
	if len(rawSchema) > 0 {
		if err := json.Unmarshal(rawSchema, &pc.NavSchema); err != nil || string(rawSchema) == "null" {
			pc.navSchemaMalformed = append(json.RawMessage(nil), rawSchema...)
		}
	}
	if len(rawNav) > 0 && string(rawNav) != "null" {
		pc.Nav = &NavDeclaration{}
		return json.Unmarshal(rawNav, pc.Nav)
	}
	return nil
}

func (pc PluginCapabilities) MarshalJSON() ([]byte, error) {
	type plain PluginCapabilities
	raw, err := json.Marshal(plain(pc))
	if err != nil || len(pc.navSchemaMalformed) == 0 {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["nav_schema"] = pc.navSchemaMalformed
	return json.Marshal(fields)
}

func (nav *NavDeclaration) UnmarshalJSON(data []byte) error {
	*nav = NavDeclaration{}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		nav.malformed = append(json.RawMessage(nil), data...)
		return nil
	}
	// Decode output diagnostics for HTTP clients too. NormalizeNav never copies
	// source-supplied diagnostics or notices into host-produced output.
	_ = json.Unmarshal(fields["diagnostics"], &nav.Diagnostics)
	_ = json.Unmarshal(fields["notices"], &nav.Notices)
	sections := []struct {
		field, kind string
		dst         any
	}{
		{"groups", "nav.group", &nav.Groups}, {"items", "nav.item", &nav.Items},
		{"pages", "page", &nav.Pages}, {"subnav", "subnav.item", &nav.Subnav}, {"menus", "menu.item", &nav.Menus},
	}
	for _, section := range sections {
		raw, ok := fields[section.field]
		if !ok {
			continue
		}
		nav.presence = append(nav.presence, navFieldPresence{kind: "nav", field: section.field, raw: raw, index: -1})
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil || string(raw) == "null" {
			nav.decodeIssues = append(nav.decodeIssues, navDecodeIssue{section.kind, "", section.field, -1, raw})
			continue
		}
		slice := reflect.ValueOf(section.dst).Elem()
		for index, entry := range entries {
			value := reflect.New(slice.Type().Elem()).Elem()
			var object map[string]json.RawMessage
			if err := json.Unmarshal(entry, &object); err != nil || object == nil {
				nav.decodeIssues = append(nav.decodeIssues, navDecodeIssue{section.kind, "", "entry", index, entry})
				slice.Set(reflect.Append(slice, value))
				continue
			}
			var id string
			_ = json.Unmarshal(object["id"], &id)
			typ := value.Type()
			for j := 0; j < value.NumField(); j++ {
				key := strings.Split(typ.Field(j).Tag.Get("json"), ",")[0]
				fieldRaw, ok := object[key]
				if !ok {
					continue
				}
				nav.presence = append(nav.presence, navFieldPresence{kind: section.kind, id: id, field: key, raw: fieldRaw, index: index})
				// Null scalar fields are structural errors, not absent false/zero values.
				if string(fieldRaw) == "null" || json.Unmarshal(fieldRaw, value.Field(j).Addr().Interface()) != nil {
					nav.decodeIssues = append(nav.decodeIssues, navDecodeIssue{section.kind, id, key, index, fieldRaw})
				}
			}
			slice.Set(reflect.Append(slice, value))
		}
	}
	return nil
}

func (nav NavDeclaration) MarshalJSON() ([]byte, error) {
	if len(nav.malformed) > 0 {
		return append([]byte(nil), nav.malformed...), nil
	}
	type plain NavDeclaration
	raw, err := json.Marshal(plain(nav))
	if err != nil || (len(nav.decodeIssues) == 0 && len(nav.presence) == 0) {
		return raw, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	// Preserve explicitly present known zero values through the carrier; their
	// schema-1 ignored-field diagnostics must not vanish due to omitempty.
	for _, presence := range nav.presence {
		if presence.kind == "nav" {
			continue
		}
		field := navSectionField(presence.kind)
		var entries []json.RawMessage
		if err := json.Unmarshal(object[field], &entries); err != nil {
			return nil, err
		}
		if presence.index >= len(entries) {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(entries[presence.index], &entry); err != nil {
			return nil, err
		}
		if _, exists := entry[presence.field]; !exists {
			entry[presence.field] = presence.raw
		}
		entries[presence.index], err = json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		object[field], err = json.Marshal(entries)
		if err != nil {
			return nil, err
		}
	}
	for _, issue := range nav.decodeIssues {
		field := navSectionField(issue.kind)
		if issue.index < 0 {
			object[field] = issue.raw
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(object[field], &entries); err != nil {
			return nil, err
		}
		if issue.field == "entry" {
			entries[issue.index] = issue.raw
		} else {
			var entry map[string]json.RawMessage
			if err := json.Unmarshal(entries[issue.index], &entry); err != nil {
				return nil, err
			}
			entry[issue.field] = issue.raw
			entries[issue.index], err = json.Marshal(entry)
			if err != nil {
				return nil, err
			}
		}
		object[field], err = json.Marshal(entries)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(object)
}

func navSectionField(kind string) string {
	switch kind {
	case "nav.group":
		return "groups"
	case "nav.item":
		return "items"
	case "page":
		return "pages"
	case "subnav.item":
		return "subnav"
	case "menu.item":
		return "menus"
	}
	return ""
}
func isV2NavField(kind, field string) bool {
	switch kind {
	case "nav":
		return field == "pages" || field == "subnav" || field == "menus"
	case "nav.group":
		return field == "parent" || field == "kind" || field == "collapsed" || field == "footer" || field == "owner"
	case "nav.item":
		return field == "parent" || field == "group_ref" || field == "page" || field == "hidden" || field == "icon" || field == "footer" || field == "requires_verbs"
	case "page", "subnav.item", "menu.item":
		return true
	}
	return false
}
