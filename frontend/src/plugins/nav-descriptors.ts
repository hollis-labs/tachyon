import type { KindDescriptor, RegionDescriptor } from "@hollis-labs/plugin-registry"

// Source-only ADR002 descriptors. The current frontend router does not adopt
// this registry catalog; runtime kind validation/projection belongs to 0118.
export const navKinds: Record<string, KindDescriptor> = {
  "nav.group": {
    schema_version: 1,
    metadata_schema: {},
    representations: ["declarative"],
    regions: ["nav.rail"],
    required_capabilities: [],
  },
  "nav.item": {
    schema_version: 1,
    metadata_schema: {},
    representations: ["declarative"],
    regions: ["nav.rail"],
    required_capabilities: [],
  },
  page: {
    schema_version: 1,
    metadata_schema: {},
    representations: ["declarative"],
    regions: ["page.routes"],
    required_capabilities: [],
  },
  "subnav.item": {
    schema_version: 1,
    metadata_schema: {},
    representations: ["declarative"],
    regions: ["nav.subnav.left", "nav.subnav.top"],
    required_capabilities: [],
  },
  "menu.item": {
    schema_version: 1,
    metadata_schema: {},
    representations: ["declarative"],
    regions: ["menu.header", "menu.page", "menu.row", "menu.hamburger"],
    required_capabilities: [],
  },
}

export const navRegions: Record<string, RegionDescriptor> = {
  "nav.rail": {
    kinds: ["nav.group", "nav.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "page.routes": {
    kinds: ["page"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "manifest",
  },
  "nav.subnav.left": {
    kinds: ["subnav.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "nav.subnav.top": {
    kinds: ["subnav.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "menu.header": {
    kinds: ["menu.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "menu.page": {
    kinds: ["menu.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "menu.row": {
    kinds: ["menu.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
  "menu.hamburger": {
    kinds: ["menu.item"],
    representations: ["declarative"],
    context_schema: {},
    ordering: "priority-ascending",
  },
}
