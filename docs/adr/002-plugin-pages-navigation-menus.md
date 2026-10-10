# ADR-002 accepted owner disposition — 2026-10-10

Chrispian accepted all five remaining proposals through PM DEC-083, receipt 01a12409-f090-7b60-bf7c-70a1ed2acc03. The complete technically reviewed source draft follows verbatim; its DRAFT/PENDING owner labels are a historical snapshot superseded by this disposition. DEC-081 also confirms config-ops ownership of /settings[/...], with other plugin claims dropped and /dashboard plus /plugin-recovery host-only.

Accepted proposals: no operator overrides in v1; only per-browser layout state, remove the three unimplemented override comments during implementation; navigation-only palette first; curated Lucide allowlist with one deterministic fallback and diagnostics; wave-1 host-compiled pages with no Plugin JS until the 0104 package releases; additive nav_projection status ok/degraded/failed catalog signal.

Independent technical review by launch-worker-1 receipt01a12401-bb8f-748d-b034-f7ac00952267; source author task-tachyon-1. Review inspected pinned contracts, regenerated projection41475b8a40339eca4514a4e5e3c84ae930f65f69567ab271419937fbd1851bdd, exact example parity and replay source/hash. Author replay execution is separately attributed; manager did not execute the native browser or full toolchain replay.

Evidence workspace01M4HYCCKJQ5GM0PA6J2QFSC62/version01M4HZM7F004G6PH5TC81EWS86; replaya9a1bbf5399c294c31d1763c30b95bae4b299cb787e39e9b2093e3f39f3ff49e. Full59-file owned proof hash manifest was independently verified before author retirement. Original draft workspace01M4HY902BB72QKCSGAT8JZBX6/version01M4HZQZ2AZVYRJPQ93QD044C8 remains unchanged.

Implementation tasks named in the ADR may be queued subject to their live dependencies. 0122 needs a stated operator requirement. Source-approved 0104 and its trusted delivery contract do not authorize registry publication or production page adoption; real released runtime and consumer allocation remain gates. No product implementation, deployment, registry release or live effect is claimed by this ADR promotion.

---

# ADR 002 (proposed number): Plugin pages, navigation and menus on plugin-host-ui + registry v2

**Status:** DRAFT. Owner acceptance pending. Nothing here authorizes implementation, publication, a host endpoint, or adoption. Review-only: no PR, implementation or new task unless the manager routes one after review.
**Task:** CW-20261010-0111 (epic EP-20261010-0007). **Author:** task-tachyon-1 (Claude Code), 2026-10-10. **Revision:** v3 (manager review fixes). **Evidence + tested replay:** workspace item `01M4HYCCKJQ5GM0PA6J2QFSC62` (key `adr_002_evidence_appendix`).
**Would amend, on acceptance:** ADR 001 section 7 (nav part), README "Adding a page", AGENTS.md boundary on invalid declarations (nav contributions only).
**Labels:** **[AGREED]** owner decision given to this task. **[PROPOSED]** this ADR's recommendation. **[OPEN]** owner choice, with a default; all four defaults and the wave-1/checks choices are **pending PM**. **[PENDING]** depends on work not yet visible or released. **[VERIFIED]** read or run by the author on 2026-10-10.

---

## 0. Summary (read this if nothing else)

1. **One path [PROPOSED].** `capabilities.json` stays the only place a plugin authors nav, pages and menus. It rides the existing `plugin_capabilities` command (protocol 2, no bump; registry v2 is independent of the subprocess protocol, `types.ts:1`). The host (Go) validates and **projects** it into registry v2 contributions. `GET /api/plugins/registry` is the only browser-facing surface. `/api/nav` and `MergedNav` are retired in the same change that lands the frontend consumer. "Registry as the source" is rejected: registry v2 is a host-to-browser, host-authoritative wire; the SDK defines no plugin-to-host contribution carrier.
2. **Catalog [PROPOSED].** Host-defined kinds `nav.group`, `nav.item`, `page`, `subnav.item`, `menu.item` (all `declarative`, schema 1, `required:false`) and eight regions (section 3.3). No upstream change to plugin-host-ui is needed to adopt it (section 3.10).
3. **Pages [AGREED + PROPOSED].** A declared page always registers a route; `hidden` only removes nav/menu presentation. The eight current manifests keep working unchanged: each routed nav item synthesizes one page (8 groups, 15 items, 15 pages, verified).
4. **Placement [AGREED + PROPOSED].** Explicit group owner wins metadata; cross-plugin orphans go to a top-level **More** group; ties break by **plugin id**, not load order (today's first-loaded rule changes after a plugin restart, `restart.go:115-117`). The Go projector resolves all collisions **before** emitting, because a structural collision rejects the whole registry document (proved).
5. **Failure policy [AGREED + PROPOSED].** Nav-contribution problems warn-and-drop the single entry with a named reason (log + registry `refusals[]`); they never refuse startup. Non-nav declaration problems (modules, verbs, settings) stay fatal. Every nav contribution is `required:false`, or one bad entry would withhold the whole host snapshot (proved). A projector failure is signalled by an **explicit additive `nav_projection` field**, never inferred from an empty catalog (section 3.6).
6. **Pages are host-compiled in this epic [PROPOSED, pending PM].** Wave-1 `page` contributions bind a `view` id to a component compiled into Tachyon's frontend (all eight plugins' pages already are). Plugin-delivered page code (component representation, sandboxed opaque frame default) depends on the 0104 host-delivery contract **[PENDING]**.
7. **Hard gate [VERIFIED, dated snapshot].** `@hollis-labs/plugin-host-ui` is **not on npm** (E404). At design-kit 1dc83f9 the repo copy is `0.1.0`, `private: true`, with a large Unreleased block, and it depends on design-components/design-tokens `^0.4.0`, so adopting it lifts Tachyon's whole kit cohort off its 0.3.x lock. These are a pinned-source snapshot; the 0104 candidate (proposed 0.2, unpublished) will supersede them when its receipts exist. Backend work (0115, 0117) is not blocked; frontend adoption (0118) is, until a release exists.
8. **Nanite precedent [VERIFIED].** `nav-rail -> nav.item` is documented intent in the plugin-host-ui README (line 124), not shipped code. Nanite today projects nav-rail as registry kind `slot` (component representation) where a rail entry is also the page. Tachyon deliberately separates `page` from `nav.item`.
9. **Four owner choices [OPEN, pending PM]** with defaults: user nav overrides (none; kit layout store only), command palette scope (navigation only), reserved-route policy (host-owned + config-ops on `/settings`), icon vocabulary (lucide kebab-case via host allowlist + fallback).
10. **Design proof.** The eight real manifests projected into a registry v2 document validate under the pinned Go registry source and the published `plugin-registry@0.2.0`; the real plugin-host-ui runtime source (scratch copy, Node type stripping) orders them as the priorities dictate today; the section 4.1 example is cross-reference checked. One tested script (`replay.sh`, sha256 `a9a1bbf5...ff49e`, ~27 s, PASS) replays all of it from pinned commits in a fresh stage (evidence item).

---

## 1. Provenance (heads read; all fetched first)

| Repo | Ref | Notes |
|---|---|---|
| hollis-labs/tachyon | `origin/main` **7ac5345** (`7ac53456b2dc945392196ba32bdfb40db0d355f5`) | module `libs/plugin-mcp v0.1.1`; frontend lock: kit-dashboard 0.3.0, design-* 0.3.0, kit-observe 0.1.0, plugin-registry 0.2.0 (`frontend/package-lock.json`) |
| hollis-labs/design-kit | `origin/main` **1dc83f9** (`1dc83f98098a93ecf300dc5b3999bfe542efc7a7`) | plugin-host-ui 0.1.0 (private), kit-dashboard/design-* 0.4.0, kit-observe 0.1.1 |
| hollis-labs/libs | `origin/main` **f5a66c0**; annotated tags `plugin-mcp/v0.1.1` (commit **6244e46**, tag object af7a776) and `plugin-mcp/v0.2.0` (commit **84b5234**, tag object 60cfb97) | **plugin-registry source of truth**: `plugin-mcp/plugin-sdk/ts/packages/plugin-registry` + Go `plugin-mcp/plugin-sdk/registry`. `git diff v0.1.1..main` over the registry dirs and `docs/protocol/registry-v2.md` is empty. |
| hollis-labs/plugin-sdk | `49e69f2` (archived) | retired; README redirects to libs. npm metadata for `plugin-registry@0.2.0` still names this repo. Only URL/doc lines differ from the libs copy. |
| hollis-labs/nanite | `origin/main` **7616cf3** | precedent audit only |
| npm `@hollis-labs/plugin-registry@0.2.0` | shasum `f8f069fbec5b3ffd8d4b10bac81065a9409cd0b8` | tarball downloaded to scratch; shasum equals the registry's `dist.shasum` |

There is no repository named `plugin-registry`; the task's "plugin-registry origin/main" is libs main (above). Own worktrees: `~/dev/hollis-labs/worktrees/{tachyon,design-kit,libs,plugin-sdk,nanite}/CW-20261010-0111`. Nothing in any live checkout was touched. Torque ids in older Tachyon status files were not verified (out of scope).

---

## 2. What is true today (read from source; derived docs checked against it)

### 2.1 Two surfaces, one drives nav

```
Path A (drives nav)                                   Path B (proof-of-concept only)
plugins/<id>/capabilities.json  (go:embed)            Manager.BuildRegistry()
  -> p.Capabilities() = contract.PluginCapabilities     -> registry.NewResponse(...) + Plugins only
  -> pluginkit.Dispatch json.Marshal(caps)              -> GET /api/plugins/registry
  -> plugin_capabilities command/execute (protocol 2)   -> PluginLoader card on Dashboard (kinds {}, regions {})
  -> host json.Unmarshal + caps.Validate() (FATAL)      -> settings.ts reads retired_plugins from it
  -> m.navGroups/navItems first-loaded wins
  -> GET /api/nav (MergedNav)
  -> visibleNavigation + hand-edited pageRegistry
  -> App.tsx icon-only NavRail
```

### 2.2 Findings

| # | Finding | Evidence |
|---|---|---|
| F1 | Manifest nav = groups (id,label,icon,priority) + items (id,label,group,route,requires_verb,priority). No parent/hidden/page/sub-nav/menus/nav_schema. | `internal/contract/nav.go:7-60` |
| F2 | Validation is fatal: bad id, duplicate, undeclared same-plugin group, undeclared verb all fail `Validate()` and refuse plugin load, hence startup. A test pins this. | `declarations.go:15-50`; `capability.go:77`; `manager.go:609-611`; `declarations_test.go:63-79`; `plugins_discover.go:77-81`; AGENTS.md "Present but invalid plugins refuse startup" |
| F3 | Declarations are round-tripped through Go structs on **both** sides (plugin `Capabilities() contract.PluginCapabilities` then `json.Marshal`; host `json.Unmarshal`). Decoding is lenient (no `DisallowUnknownFields` on this path). A new manifest field not added to `internal/contract` is dropped silently, end to end. | `pluginkit/dispatch.go:16-19,24,42`; `manager.go:606`; `plugins/*/main.go` `Capabilities()`; `agent-ops/main.go:67-79` |
| F4 | Ownership is first-loaded wins, and "first loaded" is directory order at start but **respawn is a new load at the end of loadOrder**, so group-metadata winners can change after a restart. | `manager.go:645-659,664`; `restart.go:115-134`; `plugins_discover.go:26` |
| F5 | The "self-contained" rule forbids cross-plugin group references; sharing a group today means redeclaring it in both plugins (the test does this). | `declarations.go:38-42`; `nav.go:50-52`; `declarations_test.go:26-28,47-50` |
| F6 | Registry v2 is already served, with plugins only; no kinds, regions, contributions. A PoC card consumes it with empty descriptors. `retired_plugins` is an additive top-level field the TS parser and Go decoder accept (only mis-cased known keys are rejected); this is the precedent for D5's `nav_projection`. | `manager.go:237-265`; `main.go:422-433`; `frontend/src/plugins/loader.tsx:22-39`; `pages/dashboard.tsx:45`; `settings.ts:113-118`; plugin-registry `validation.ts:47-52`; replay check "additive keys" |
| F7 | Frontend: nav shown only if a `pageRegistry` entry exists for the route and the verb exists; flat icon-only rail; icon taken from the **group**; route is `useState("/agents")`, no URL routing. | `App.tsx:34,65-91,118-155`; `pages/registry.ts:20-37`; `api/navigation.ts:34-55`; no `location.hash`/history use in `frontend/src` |
| F8 | Icon bug: manifests use `users, settings, play, activity, git-branch, server, terminal, clipboard-list`; the map has keys `activity, git, play, radio, server, settings, terminal, users`, so `git-branch` and `clipboard-list` fall back to Activity. | `App.tsx:21-30` vs manifests (replay `audit` check) |
| F9 | `requires_verb` is effectively an existence check: validation forces the verb to be declared by the same plugin, and `/api/verbs` is built from the same declarations. It is not authorization. | `declarations.go:43-47`; `main.go:344-357`; AGENTS.md "Do not infer authorization..." |
| F10 | "User config can override label, priority, visibility" is written in three comments but implemented nowhere. | `nav.go:6,20,46`; repo-wide grep finds no nav override code |
| F11 | `/api/nav` has no consumer outside the embedded frontend (UI is embedded in the same binary), so it can be retired without a compatibility shim. Remaining mentions are docs (stale after retirement): README.md:86, CONTRIBUTING.md:40, AGENTS.md:22, ADR 001:139. | grep: `navigation.ts:21`, `main.go:359` |
| F12 | The eight manifests: 8 groups, 15 items, 15 distinct routes, no duplicate group/item/route, every `requires_verb` declared, every route has a page, `/dashboard` is the only extra registry page. No manifest relies on a shared group or an id collision. Per-plugin sha256 prefixes are in the evidence item. | replay `audit` check |
| F13 | Tachyon sends no Content-Security-Policy and no frame headers (grep of go/ts/html/yaml). | repo grep |
| F14 | design-kit `docs/admin-manifest-contract.md` section 1: "Tachyon's `/api/nav` remains product navigation, not a second authority over the admin sections." Nav must not reclassify Settings/Status/Diagnostics resources. | design-kit `docs/admin-manifest-contract.md` section 1 |

---

## 3. Decisions

### 3.1 Agreed (given to this task) [AGREED]

A1 Adopt plugin-host-ui + registry v2; no parallel delivery or registration. A2 Grouped sidebar / sub-nav / menu primitives are proven in Parallax, then promoted to the kit after owner review and a distinct second consumer. A3 Explicit group owner wins metadata; cross-plugin orphans fall to a top-level More group; never refuse startup. A4 Declarative menus and URL/hash routing. A5 New validations warn-and-drop; the present invalid-nav startup refusal is revised **for nav contribution handling only**; AGENTS.md's fatal rule for malformed non-nav declarations stands. A6 Sandboxed opaque frame is the default; main-origin only by explicit host authority. A7 A declared page registers a route even when hidden from navigation. A8 The eight manifests are preserved additively; `/dashboard`, `/settings`, `/plugin-recovery` are reserved.

### 3.2 D1: one authoritative path [PROPOSED]

**Decision.** Authoring and transport: `capabilities.json` over `plugin_capabilities` (unchanged). Admission and projection: host-side, in Go. Browser delivery: `GET /api/plugins/registry` only. `/api/nav`, `Manager.MergedNav`, `navGroups/navItems`, and the frontend `navigation.ts` are deleted when the frontend consumes the registry (UI ships in the same binary, F11).

**Why not "registry as source".** (a) `registry-v2.md` and the Go package doc define a *host-authoritative* catalog; `host_instance`, `owner_generation`, `status`, `kinds`, `regions` are host policy a plugin must not mint. (b) The SDK has no plugin-to-host contribution carrier for declarative contributions; Nanite's manifest `registers` is Nanite-specific. (c) `plugin_capabilities` is tested, carries nav today, and needs no protocol bump (`ProtocolVersion = 2`, plugin-mcp `subprocess/protocol.go:101`). (d) Two authoring paths would re-create the triple-maintained metadata (G17).

**Why not keep `/api/nav` as a derived view.** It would be a shim (RUN.md: clean breaks). Transitional rule: between 0117 and 0118 `MergedNav` may keep serving unchanged, with no new features, and is deleted in 0118.

**Consequence of F3.** New fields must be added to `internal/contract` first; plugin binaries must be rebuilt (`make build-plugins`; `make build` alone does not, AGENTS.md).

### 3.3 D2: Tachyon's catalog [PROPOSED]

All kinds: `schema_version 1`, `representations ["declarative"]`, `metadata_schema {}` (host validators own the rules, as Nanite does; JSON Schema in `metadata_schema` is a later upgrade), contributions `required:false`. Behavior lives in `declarative`, descriptive/placement data in `metadata`.

| Kind | Regions | metadata (host-validated; `*` = required) | declarative payload |
|---|---|---|---|
| `nav.group` | `nav.rail` | `id*`, `label*`, `icon`, `priority*`, `manifest_order*`, `footer`, `owner` | `{}` |
| `nav.item` | `nav.rail` | `id*`, `label*`, `group*` (resolved; `more` for orphans), `declared_group`, `placement` (`declared`/`group_ref`/`parent`/`orphan`), `parent`, `page`, `hidden`, `icon`, `priority*`, `manifest_order*`, `footer`, `requires_verbs[]` (normalized union, see 3.4.1) | `{}` |
| `page` | `page.routes` | `id*`, `route*`, `title*`, `hidden`, `synthesized`, `requires_verbs[]` | `{"view": "<view id>"}` (wave 1: host-compiled; v1 compat uses `legacy-route:<route>`) |
| `subnav.item` | `nav.subnav.left`, `nav.subnav.top` | `id*`, `label*`, `parent*` (nav.item id), `page*` (page id), `priority*`, `manifest_order*`, `requires_verbs[]` | `{}` |
| `menu.item` | `menu.header`, `menu.page`, `menu.row`, `menu.hamburger` | `id*`, `label*`, `icon`, `priority*`, `manifest_order*`, `target` (`{page?}` / `{row?}`), `requires_verbs[]` | `{"action": {typed intent}}` |
| `command` (0119) | none | verb effect class | handler `{id: <verb>}`, projected from `verbs` |

Registry local keys are the declared ids (they match `^[A-Za-z0-9_.-]+$`); `public_binding` carries the global id (groups, items, subnav, menus) or the route (pages), so registry uniqueness backs the projector's resolution (but see D5 on blast radius).

#### 3.3.1 Regions

| Region | Kinds | Ordering | Action policy | Surface |
|---|---|---|---|---|
| `nav.rail` | nav.group, nav.item | priority-ascending | none (navigation comes from `page`, not from an action) | left rail / grouped sidebar |
| `page.routes` | page | manifest | none | route table (not visual) |
| `nav.subnav.left` | subnav.item | priority-ascending | none | in-page left sub-nav |
| `nav.subnav.top` | subnav.item | priority-ascending | none | in-page top sub-nav (kit `TabStrip`) |
| `menu.header` | menu.item | priority-ascending | required; tags `command`, `navigate` | header actions |
| `menu.page` | menu.item | priority-ascending | same | page toolbar |
| `menu.row` | menu.item | priority-ascending | same | row context / overflow |
| `menu.hamburger` | menu.item | priority-ascending | same | hamburger (narrow) |

`modal` is left out of `allowedTags` until a modal widget region exists. The kit's `navigate` intent takes a **route id the host validates against its route registry** (README line 205), which is exactly the `page.routes` table (route id = the page's route path). `command` intents name `owner/local_key` of an accepted `command` entry (`handler` representation; owner = plugin id, local key = verb); Tachyon's `adapter.actions.command` would call the existing verb path (`POST /api/verb/{verb}`, `cmd/tachyon/hitl.go:33`) with its HITL bridge. Details are 0119's.

**Ordering the kit applies (so the projector must make everything explicit).** Default priority **10** (`catalog.ts:104`), ascending; ties by `manifest_order`, owner, key, kind, generation (`order.ts:10-21`). Tachyon's manifest default is **1000** (`nav.go`; `MergedNav`). The projector therefore always emits an explicit `priority` (absent/0 becomes 1000) and `manifest_order` = array index within the owning plugin.

**`reserved(ref)` limit.** The catalog's `reserved` predicate receives identity only (`{hostInstance, owner, generation, kind, key}`, `catalog.ts:33,115`), never metadata. Route reservation therefore cannot live there: the **Go projector is authoritative**, and each kind's `validate()` repeats the check as defense in depth (proved: a page on `/dashboard` is refused `invalid-metadata`). `reserved` is used only for owner `core` and for local-key based reservations.

### 3.4 D3: pages and routes [AGREED + PROPOSED]

- R1 A page that passes admission always registers its route in `page.routes`. `hidden` (on `nav.item`) and having no `nav.item` only remove nav/menu presentation; they never unregister the route.
- R2 **v1 compatibility.** A `nav.item` with `route` and no `page` synthesizes a page `{id=item.id, route=item.route, view="legacy-route:"+route, synthesized:true}`. All 15 current routes therefore auto-register with no manifest edit.
- R3 Route grammar: `^/[a-z0-9][a-z0-9/_-]*$`, no trailing slash, max 128 chars. Query strings belong to the page, never to route identity.
- R4 URL form: hash routing under `/sysop/`, e.g. `/sysop/#/work/board` (A4). Unknown or retired routes resolve to a *not-available* page (0116), not the dashboard.
- R5 `view` resolution is host-side in the browser. A registered page whose view has no compiled component still resolves (not-available page); its nav item is omitted with a diagnostic (matches today's `hasPage` behavior).
- R6 Active-state rule: a `nav.item` is active when the current route equals its page route **or** any of its `subnav.item` pages. This lets Work/Observe/Services/Source/Sessions keep a single rail entry with sibling pages as sub-nav (0121).

#### 3.4.1 Field precedence and rejection (when a manifest gives overlapping fields)

| Situation | Rule | Outcome |
|---|---|---|
| `route` and `page` both on an item | `page` is authoritative; `route` is only a consistency assertion | If the admitted page's route equals `route`: accepted, no page synthesized. If it differs: item dropped, `nav-route-page-mismatch`. |
| `route` only (no `page`) | R2: synthesize a page from the item | accepted (v1 and v2) |
| `page` only | route comes from the page | accepted; page not admitted: `nav-page-missing` |
| Neither `route` nor `page` | nothing to navigate to | item dropped, `nav-page-missing` |
| Synthesized page vs an explicit page on the same route inside one plugin | the explicit page wins; the item binds to it | accepted; no collision (cross-plugin collisions are O3) |
| `requires_verb` and `requires_verbs` both | **union**, singular first, de-duplicated; projected only as normalized `requires_verbs[]` | every verb must be declared by the owning plugin, else `nav-undeclared-verb` (drop); more requirements never loosen gating |
| More than one of `group`, `group_ref`, `parent` | exactly one is allowed | item dropped, `nav-placement-conflict` |
| None of `group`, `group_ref`, `parent` (v2) | no placement | orphan: More, `nav-orphan` |
| v2-only field in a `nav_schema` 1 (or absent) declaration | fields outside the declared schema are not honored | field ignored, `nav-field-ignored` (informational); the rest of the entry is kept |

### 3.5 D4: ownership, ordering, collisions, orphans [AGREED + PROPOSED]

Deterministic and restart-stable; evaluated by the projector on every revision, never at plugin init. No current manifest relies on a shared group or a collision (F12), so O1-O3 change no present behavior.

- O1 Tie-break order across plugins is **plugin id ascending (byte order)**. Load order no longer matters (fixes F4).
- O2 **Group metadata owner.** (`owner: true` is this ADR's concrete reading of "explicit group owner"; confirm.) Declarations with `"owner": true` rank above plain declarations; among equals the lexically smallest plugin id wins. Losers get `nav-group-redeclared`; their metadata is ignored but their items still appear in the winning group.
- O3 Item, page, subnav and menu ids are one global namespace per kind (as today). Collision: lexically smallest plugin keeps it; the other entry is dropped (`nav-id-collision`). Two pages claiming one route: same rule (`nav-route-collision`); nav items pointing at a dropped page are dropped (`nav-page-missing`).
- O4 **Placement fields.** `group` = a group this plugin declares (v1 rule retained); `group_ref` = a group owned elsewhere; `parent` = another nav.item (any plugin). Exclusivity and fallbacks: section 3.4.1. Depth: group, item, child item (max 2 below the group); deeper is dropped (`nav-depth-exceeded`); cycles dropped (`nav-parent-cycle`).
- O5 **Orphans.** Placement target not admitted (group absent, owner failed or not installed; parent missing; v1 undeclared group) puts the item in **More**: the Go projector sets `group: "more"`, `placement: "orphan"` and logs `nav-orphan`; the **browser synthesizes the More group view** (label, icon, last position), so no contribution is minted for it (the registry reserves owner `core`, and a host-minted owner would need a plugin entry). `more` is a reserved group id; each item keeps its `owner_id` attribution.
- O6 Sub-nav orphans (parent nav.item missing) are dropped (`nav-subnav-orphan`); their pages stay routable.
- O7 `footer: true` on a group/item pins it to the rail footer (kit `NavRailItem.footer` exists).

### 3.6 D5: failure policy, blast radius and the diagnostic signal [AGREED + PROPOSED]

- Every nav-contribution defect drops **that entry only**, logged at WARN (plugin id, kind, id, code) and listed in the registry's top-level `refusals[]` (`required:false`, the registry's only refusal authority). This is loud, not a silent fallback.
- **Why `required:false` always.** In plugin-host-ui a required catalog refusal withholds the entire host snapshot (`host.ts:251`, README line 118); in the registry a required refusal aborts the candidate. Proved (evidence item).
- **Why the projector must pre-resolve collisions.** A duplicate `public_binding`, an unknown owner, or any other structural violation rejects the *whole* document (Go `Validate`, TS `validateResponse`). One plugin's duplicate would blank all nav.
- **Projector fail-safe.** After building, the projector runs `Response.Validate()`. On failure it logs ERROR and serves the same document with empty `contributions` and `refusals` (plugins only), never exits, never 500s.
- **Explicit diagnostic signal (mechanism proposed; exact name and shape PENDING manager/owner confirmation).** Every registry response carries an additive top-level field:
  `"nav_projection": { "status": "ok" | "degraded" | "failed", "revision": <int>, "dropped": <int, degraded only>, "reason": "<code>", only for failed }`.
  `ok` = no entry dropped; `degraded` = at least one entry dropped (each is in `refusals[]`); `failed` = the fail-safe above was taken. **The failure banner is driven only by `status == "failed"`.** A missing field (older host) or `ok` shows no banner, and an empty catalog is never read as an error: a host with zero plugins is valid and reports `ok`. `degraded` may show a non-blocking diagnostics count.
  *Contract position.* This is not a documented extension point of registry v2 (`registry-v2.md` does not define one); it follows Tachyon's existing precedent, the additive `retired_plugins` field (`manager.go:240-243`, F6). The tested replay shows the published TS registry and the pinned Go decoder give identical results with `retired_plugins` and `nav_projection` present. The browser reads it from the same raw response used by `retiredConfigTargets` (`settings.ts:113`), separately from `runtime.sync`, which ignores it. If the manager prefers no new field, the alternative is a separate host diagnostics endpoint (also PENDING); inference from an empty catalog is rejected.
- Fatal stays fatal for: modules, verbs, effects, settings (AGENTS.md). Admission still calls `Validate()` for those; nav moves out of the fatal path into `NormalizeNav` (diagnostics, not errors).

Reason codes (stable strings; informational ones never drop):

| Code | Condition | Outcome |
|---|---|---|
| `nav-invalid-id` | id fails `^[a-z0-9][a-z0-9_-]*$` | drop entry |
| `nav-duplicate-id` | duplicate within the plugin | keep first, drop rest |
| `nav-id-collision` | id owned by lexically earlier plugin | drop |
| `nav-undeclared-verb` | `requires_verb(s)` not declared by owner | drop |
| `nav-route-invalid` / `nav-reserved-route` / `nav-route-collision` | grammar / reserved / taken | drop page |
| `nav-route-page-mismatch` | item `route` differs from its page's route | drop item |
| `nav-page-missing` | item names a page that was not admitted, or names none | drop item |
| `nav-placement-conflict` | more than one of `group`/`group_ref`/`parent` | drop item |
| `nav-orphan` | placement target absent or no placement | place in More (kept) |
| `nav-parent-cycle` / `nav-depth-exceeded` | topology | drop members / deeper entries |
| `nav-reserved-id` | declares group `more` | drop |
| `nav-group-redeclared` | lost the owner contest | ignore metadata (items kept) |
| `nav-subnav-orphan` | subnav parent absent | drop |
| `nav-menu-invalid-action` | action fails kit parse or region policy | drop |
| `nav-field-ignored` | v2-only field in a schema-1 declaration | informational; entry kept |
| `nav-schema-newer` | `nav_schema` value above host support | informational; fields projected as schema 2 |
| `nav-icon-unknown` | icon not in allowlist | informational (browser; fallback icon) |

### 3.7 D6: `nav_schema` [PROPOSED]

Top-level integer on the declaration (sibling of `nav`), absent = 1. The host supports 1 and 2.
- **1 (legacy profile):** today's fields only; every item with a route synthesizes a page; the reserved-route check honors the existing claim (config-ops on `/settings`); orphan fallback applies; v2-only fields are ignored with `nav-field-ignored`. All eight manifests are schema 1 and must project to the same visible order and labels as today's `MergedNav` (golden for the migration PR; see section 8, note on checks).
- **2:** adds `owner`, `footer`, `icon`, `group_ref`, `parent`, `page`, `hidden`, `requires_verbs`, `nav.pages`, `nav.subnav`, `nav.menus` and applies the new checks strictly.
- A declaration newer than the host: fields beyond the host's struct are already dropped by lenient decode (F3); where the field is known but the value exceeds 2, the host logs `nav-schema-newer` once per plugin.
- Per A5 the *outcome* of every nav check, old or new, is warn-and-drop; `nav_schema` scopes which checks a given declaration is held to, so legacy manifests are never newly dropped by rules they could not have anticipated.

### 3.8 D7: compatibility with the eight manifests [AGREED + PROPOSED]

No manifest edit is required. Projected counts: 8 `nav.group`, 15 `nav.item`, 15 synthesized `page`, 0 refusals. Group order today and after (priority): agents 100, execution 200, sessions 300, observability 400, work 500, services 600, source 700, settings 900; items by their priorities within each group. Priorities are distinct within every group, so the kit's `manifest_order` tie-break changes nothing for these manifests.

Reserved routes: see section 6, choice 3. `/settings` is currently *contributed by config-ops* and rendered by the host; `/dashboard` and `/plugin-recovery` are host surfaces that are not contributions. The host keeps rendering `/plugin-recovery` when retired plugins exist and no Settings route is contributed (`App.tsx:93-95`).

### 3.9 D8: trust and isolation [AGREED + PROPOSED + PENDING]

| Stage | What runs in the browser | Authority |
|---|---|---|
| This epic (declarative) | Host React renders validated JSON from the registry. No plugin JavaScript. `view` ids resolve only to components compiled into Tachyon. | Host validates (Go projector + TS catalog). Actions are typed intents the host gateway validates per invocation and fences by owner generation. `navigate` reaches only registered routes. |
| Later: plugin-delivered pages [PENDING 0104] | `component` representation, digest-bound (`bundle_version sha256:`) bundle, rendered in a **sandboxed opaque frame by default**. | Main-origin only if the host's explicit app isolation setting and `allowMainOrigin` say so; main-origin has ambient document authority (ISOLATION.md). |

Notes: declarative rendering is unaffected by the isolation mode (README line 96); the runtime adapter still requires an `isolation` store, so wave 1 supplies a constant `sandboxed-frame` (no component views are admitted). The TS registry's default importer uses an immutable `data:` URL and needs a host CSP that permits it; Tachyon sends no CSP (F13). **Do not adopt that importer or invent a CSP relaxation; component delivery waits for the 0104 digest-bound host-delivery contract** (not seen by this task; label PENDING; no endpoint, blob/data fallback or mutable-URL fallback is assumed). Isolation is not provenance or grant validity. This ADR gives no host endpoint, adoption or publish authorization.

### 3.10 D9: what plugin-host-ui needs upstream [PROPOSED]

**Nothing to adopt.** `createSlotCatalog` has no preset kinds and no group concept by design (`catalog.ts:76`; README line 94). `ViewProjection.props`/`data` carry arbitrary frozen JSON (`host.ts:78-89`), so group/parent/hidden live in host-defined kinds. Region ordering is a stable total order (`order.ts`), so a nav tree is a pure host-side grouping of ordered views. Optional later proposals, **not to be invented here and needing the second consumer first (A2):**
1. a pure `buildNavTree(views, {groupOf, parentOf, fallbackGroup})` helper (headless; plugin-host-ui or kit-dashboard);
2. visual primitives: grouped sidebar, left sub-nav, context menu (the kit has `NavRail` (flat), `TabStrip`, `OverlaySidebar`, `OverflowMenu`, `DropdownMenu`; no context menu).
Parallax (0113) proves them; promotion is 0114 after owner review.

### 3.11 Coverage of the planner's gaps G1-G18

| Gap | Where handled |
|---|---|
| G1 nesting; G2 sub-nav | O4 `parent`; `subnav.item` + regions (3.3), R6 |
| G3 hide flag / auto-register | D3 R1-R2, 3.4.1 |
| G4 cross-plugin placement | O2-O5 (`group_ref`, `parent`, More) |
| G5 menu hooks | `menu.item` + four menu regions; 0119 |
| G6 coarse gating | F9; `requires_verbs`; real authorization is out of scope, follow-up 0122 |
| G7 icon bug | F8; choice 4; fixed in 0112 |
| G8 ordering/overrides unimplemented | explicit priority + manifest order (3.3); choice 1 |
| G9 no URL routing | R4; 0116 |
| G10 silent collisions | O3, D5 (WARN log + `refusals[]` + `nav_projection`) |
| G11 owner attribution / reserved routes | registry `owner_id`; choice 3 |
| G12 badges | not designed here; needs a data source and a menu/item metadata slot; 0122 |
| G13 palette | choice 2 |
| G14 weak empty/error states | not-available page (R4), explicit `nav_projection` signal (D5); 0116/0120 |
| G15 a11y / narrow | hamburger region; behavior requirements for 0120 (no design here) |
| G16 no nav_schema | D6 |
| G17 triple-maintained metadata, no tests | D1 (single path) + golden/regression tests (section 8) |
| G18 stale docs | F11 list + section 8 layer 8 |

---

## 4. Specification notes

### 4.1 Manifest additions: complete example (all new fields optional; Go struct fields `omitempty`)

Two illustrative, complete manifests (not repo manifests; file name = plugin id; the replay cross-reference check confirms every named page, group, parent, verb and route below exists, placement is exclusive, and `route` agrees with its `page`). They show: `nav_schema`, explicit group `owner`, an item carrying both `route` and `page` (consistent) and both `requires_verb` and `requires_verbs` (union), a `hidden` item with a page, explicit `pages`, `subnav` (top), a `command` menu and a `navigate` menu, and a cross-plugin item using `group_ref`. If `work-ops` is absent or its group is not admitted, `session_timeline` lands in **More** (`nav-orphan`).

`work-ops`:
```json
{
  "modules": ["work"],
  "verbs": {
    "work_list": { "effect": "reads" },
    "work_assign": { "effect": "writes" }
  },
  "nav_schema": 2,
  "nav": {
    "groups": [
      { "id": "work", "label": "Work", "icon": "clipboard-list", "priority": 500, "owner": true }
    ],
    "items": [
      { "id": "work_list", "label": "Tasks", "group": "work", "page": "work_tasks", "route": "/work",
        "requires_verb": "work_list", "requires_verbs": ["work_list"], "priority": 100 },
      { "id": "work_archive", "label": "Archive", "group": "work", "page": "work_archive", "hidden": true, "priority": 900 }
    ],
    "pages": [
      { "id": "work_tasks", "route": "/work", "title": "Tasks", "view": "work", "requires_verbs": ["work_list"] },
      { "id": "work_board", "route": "/work/board", "title": "Board", "view": "work-board", "requires_verbs": ["work_list"] },
      { "id": "work_archive", "route": "/work/archive", "title": "Archive", "view": "work-archive", "requires_verbs": ["work_list"] }
    ],
    "subnav": [
      { "id": "work_tasks_tab", "label": "Tasks", "parent": "work_list", "page": "work_tasks", "placement": "top", "priority": 100 },
      { "id": "work_board_tab", "label": "Board", "parent": "work_list", "page": "work_board", "placement": "top", "priority": 200 }
    ],
    "menus": [
      { "id": "work_refresh", "label": "Refresh", "region": "page", "target": { "page": "work_tasks" }, "priority": 100,
        "requires_verbs": ["work_list"], "action": { "type": "command", "command": "work-ops/work_list", "arguments": {} } },
      { "id": "work_open_board", "label": "Open board", "region": "header", "priority": 200,
        "action": { "type": "navigate", "route": "/work/board", "parameters": {} } }
    ]
  }
}
```
`session-ops`:
```json
{
  "modules": ["session"],
  "verbs": {
    "session_list": { "effect": "reads" }
  },
  "nav_schema": 2,
  "nav": {
    "items": [
      { "id": "session_timeline", "label": "Timeline", "group_ref": "work", "page": "session_timeline",
        "requires_verbs": ["session_list"], "priority": 300 }
    ],
    "pages": [
      { "id": "session_timeline", "route": "/sessions/timeline", "title": "Timeline", "view": "session-timeline",
        "requires_verbs": ["session_list"] }
    ]
  }
}
```
Overlap rules (what the host does when fields disagree) are in section 3.4.1. In `command: "work-ops/work_list"` the owner is the plugin id and the local key is the verb (section 3.3.1). `navigate.route` is the page's route path.

### 4.2 Wire example (excerpt of the verified projection, plus the additive top-level fields)

```jsonc
{ "registry_version": 2, "host_instance": "<m.hostInstance>", "revision": 7,
  "plugins": { "work-ops": { "owner_generation": "<observeGeneration>" } },
  "kinds":   { "nav.item": { "schema_version": 1, "metadata_schema": {}, "representations": ["declarative"], "regions": ["nav.rail"], "required_capabilities": [] } },
  "regions": { "nav.rail": { "kinds": ["nav.group","nav.item"], "representations": ["declarative"], "context_schema": {}, "ordering": "priority-ascending" } },
  "contributions": { "nav.item": { "work-ops/work_list": {
      "status": "accepted", "owner_id": "work-ops", "owner_generation": "<gen>", "local_key": "work_list", "kind": "nav.item",
      "schema_version": 1, "required": false, "representation": "declarative", "public_binding": "work_list",
      "metadata": { "id": "work_list", "label": "Tasks", "group": "work", "page": "work_list", "hidden": false,
                    "requires_verbs": ["work_list"], "priority": 100, "manifest_order": 0 }, "declarative": {} } } },
  "refusals": [],
  "retired_plugins": [],
  "nav_projection": { "status": "ok", "revision": 7 } }
```
(The replay's projection uses the legacy single `requires_verb` string in `metadata` because it models the schema-1 path; the normalized `requires_verbs[]` form above is the proposed projector output.)

### 4.3 Frontend composition (for 0118, gated)

`createPluginRegistry({kinds, regions, stylesheets:false})` + `createSlotCatalog` (kinds' `validate`/`project` implement section 3.3) + `createPluginHostRuntime({scope:{appId:"tachyon", environmentId, clientId}, ...})`; `runtime.sync(rawText)` with the original response text (the registry needs raw JSON to detect duplicate keys). Tachyon's published descriptors (Go) and local descriptors (TS) must both admit a kind/region; a mismatch yields a named refusal, not silence. Whether to add a Go/TS descriptor parity test is a *new check asserting two sources agree* and is raised as a decision (section 8), not assumed. Re-fetch triggers: mount, after plugin restart, window focus (revision-guarded; unchanged data keeps object identity). `scope.environmentId`/`clientId` values are 0118's choice; `clientId` keys the kit layout store (A2/choice 1).

---

## 5. Nanite precedent audit (nanite 7616cf3)

| Question | Answer (primary source) |
|---|---|
| Is `nav-rail -> nav.item` shipped? | **No.** It appears only as intent in plugin-host-ui README line 124 ("Later Nanite adoption is a separate clean break ... Existing manifests are not translated; the live app remains unchanged"). |
| What does Nanite ship? | Slot name `nav-rail` (`pkg/plugin/ui.go:22`). Registry kinds are only `envelope, widget, slot, panel`, all `component` representation, region ordering `manifest` (`internal/api/plugins_registry.go:159-162`). A nav-rail entry becomes kind `slot`, local key `slot-<hex("nav-rail/<id>")>`, `public_binding` = `nav-rail/<id>` (`plugins_registry.go:281-289`); refused with `reviewed_component_bundle_unavailable` if no reviewed bundle (`:286`). |
| Is rail entry = page? | Yes. `NavRail.tsx:24,104-127` renders a button; `AppShell.tsx:124-135` renders `entry.component` for the page whose id is the current page. No group, parent, hidden flag or sub-nav. |
| Ordering | Hook comment says priority descending (`usePluginSlots.ts:7-13`) while the published region is `manifest`: Nanite is internally inconsistent. |
| Icons | 21-entry lucide kebab-case allowlist, `Puzzle` fallback (`ui/src/lib/icons.ts`). |
| Routing | `useHashRoute.ts`: `#chat`, `#settings/<section>`, a fixed page set (hash routing precedent, not plugin routes). |
| Registry state | Nanite is already on `plugin-registry` 0.2.0 (`ui/package.json:24`) and plugin-mcp v0.2.0 (`go.mod:97`); the registry CHANGELOG's "Nanite remains on 0.1.0" is stale. |

**Consequences.** Do not cite `nav-rail -> nav.item` as proven. Tachyon intentionally deviates: `page` is separate from `nav.item` (so hidden pages exist), representation is `declarative`, and group/parent/hidden are added. Reuse from Nanite: lucide-name allowlist with fallback, `public_binding` plus encoded local keys when ids contain separators, hash routing.

---

## 6. Open owner choices [OPEN, pending PM] (defaults chosen so work can proceed)

| # | Choice | Default | Rationale |
|---|---|---|---|
| 1 | **User nav overrides** (label/priority/visibility edits by an operator) | **None in v1.** Persist only per-browser order + visibility + selected item via the kit layout store (`createPluginLayoutStore`, scoped by app/env/client, localStorage, never plugin-visible, never server-side). Delete the three unimplemented "User config can override" comments (`nav.go:6,20,46`). | F10: the claim is unimplemented and no requirement is evidenced. The layout store is already built, tested, scoped and safe-failing; label/priority overrides need a server-side owner (config-ops) and an authorization story. Revisit on a stated operator need (0122). |
| 2 | **Command palette scope** | **Navigation only first:** visible `nav.item`s and unhidden pages (jump-to-page) plus host-owned commands. Plugin `menu.item`s join later through an explicit `palette` region. | A palette that executes plugin commands needs the HITL/argument UX that 0119 is still proving; a fifth menu region before the action gateway is proven widens blast radius. Nanite's `command-palette` slot (`ui.go:29`) shows the target shape, not a reason to rush. |
| 3 | **Reserved-route policy** | **Host-owned set** `/dashboard`, `/plugin-recovery` (no plugin may claim); `/settings` and `/settings/*` claimable **only by plugin id `config-ops`** (an explicit host allowlist). Violations warn-and-drop (`nav-reserved-route`), never refuse startup. Group id `more` and owner `core` also reserved (`core` is reserved by the registry itself). Enforced by the Go projector; repeated in kind `validate()` (see 3.3 `reserved(ref)` limit). | Matches today's behavior (config-ops contributes `/settings`; recovery appears only when it is absent, `App.tsx:93-95`) and the admin-manifest rule that nav cannot reclassify settings (F14). An allowlist is explicit and testable; a blanket reservation would break config-ops. |
| 4 | **Icon vocabulary** | **Lucide kebab-case names resolved through a host-curated allowlist map with one deterministic fallback icon** (and a diagnostic), as Nanite does. Manifests keep today's names. | Fixes F8 without touching manifests (`git-branch`, `clipboard-list` become real icons), bounds the bundle (no whole-lucide import), and needs no new vocabulary to learn. A fixed bespoke vocabulary adds a translation layer for no gain. Unknown names degrade visibly, not fatally. |

---

## 7. Versions, skew and gates (verified 2026-10-10; a dated pinned-source snapshot)

The plugin-host-ui package facts below (`0.1.0`, `private`, `vite ^7` peer, `^0.4.0` design deps) describe design-kit `1dc83f9`. The 0104 candidate (proposed 0.2, unpublished) and its dependency/Vite receipts will **supersede this section's release gate** when they exist; nothing here treats a local pack as a release.

| Item | Tachyon pin / lock | Published / source | Effect |
|---|---|---|---|
| `@hollis-labs/plugin-host-ui` | not a dependency | **not on npm (E404)**; repo `0.1.0`, `private:true`; Unreleased block (frames, bootstrap, typed actions) | **Blocks 0118** until a public release exists. Tachyon AGENTS.md: consume *published* design-kit packages. |
| plugin-host-ui deps (at 1dc83f9) | n/a | `design-components ^0.4.0`, `design-tokens ^0.4.0` (dependencies); `vite ^7` (optional peer); `kit-settings ^0.2.0` (optional) | Adoption forces design-* and kit-dashboard to 0.4.x and kit-observe to 0.1.1 (peer `kit-dashboard ^0.4.0`). The Vite range only matters if `./vite` importmap provisioning is used later; Tachyon pins 6.4.3 for advisories. |
| kit-dashboard | `^0.3.0` (locked 0.3.0) | 0.4.0 | `NavRail` source is byte-identical across tags 0.3.0 and 0.4.0 (last touched `232f825`), so the skew alone does **not** block nav work. |
| `@hollis-labs/plugin-registry` | `^0.2.0` (0.2.0) | 0.2.0 (npm), same source in libs | Aligned. |
| Go `libs/plugin-mcp` | v0.1.1 | v0.1.1, v0.2.0 tags | Registry Go package and docs identical; no bump needed. v0.2.0 adds GrantSet renewal only. |

Gate ledger (explicit, in order): (1) owner accepts this ADR; (2) 0115/0117 backend land (not blocked by anything below); (3) a plugin-host-ui public release exists (0104) with exact version availability verified on npm; (4) Tachyon lifts the kit cohort to 0.4.x in its own change; (5) 0118 adopts. No npm publish, tag, credential, deploy or consumer adoption is authorized by this ADR.

---

## 8. Change list per layer (one page; feeds the epic)

| Layer | Concrete changes | Tasks |
|---|---|---|
| **1 Contract** `internal/contract` | Add `nav_schema` and the section 4.1 fields (`omitempty`). Replace the fatal `validateNav` with `NormalizeNav(...) []NavDiag` implementing 3.4.1 and the reason codes; `Validate()` keeps modules/verbs/effects/settings fatal and no longer returns nav errors. Pure helpers: route grammar, reserved routes (+ config-ops allowlist), reserved ids, reason-code constants. Rewrite the unimplemented "User config can override" comments. Flip `declarations_test.go:63-79` nav subtest to warn-and-drop; keep the settings subtest fatal. Table tests per reason code and per 3.4.1 row. | 0115 (pure validators may land earlier in 0112) |
| **2 Plugins / manifests** | Eight manifests need **no** change (hashes in evidence). Rebuild plugins after the contract change (`make build-plugins`). Migration adds `pages`, `subnav`, `group_ref`, `hidden` for Work, Observe, Services, Source, Sessions, Agents. | 0121 |
| **3 Host** `internal/plugins` | New pure projector (e.g. `nav_projection.go`): plugins sorted by id, ownership/collision/orphan/depth rules, emits kinds, regions, contributions, `refusals[]`, WARN logs, the `nav_projection` field, then `Validate()` fail-safe. `BuildRegistry` calls it; keep `retired_plugins`. Delete `navGroups/navItems`, the `detachProcess` re-election block (`restart.go:115-134`), and `MergedNav` (in 0118). `registryRevision` already bumps on load/detach. Tests: golden of the eight manifests (15 pages), collision determinism across restart, fail-safe path sets `status:"failed"`. | 0117 |
| **4 HTTP** `cmd/tachyon` | Route `/api/plugins/registry` unchanged. Retire `GET /api/nav` in 0118. Optional later: ETag by revision. Note that `/api/verbs`' embedded `nav` is not authoritative. | 0117, 0118 |
| **5 Frontend runtime** | After the gates: add plugin-host-ui; lift kit cohort; `createSlotCatalog` (kinds from 3.3), registry, host runtime, constant `sandboxed-frame` isolation store, shared registry fetch also feeding `retiredConfigTargets` (`settings.ts:113`) and the `nav_projection` banner. Remove `api/navigation.ts`; repurpose or remove the Dashboard PoC card (`pages/dashboard.tsx:45`). Re-key the host page table from route to **view id**. | 0118 (gated) |
| **6 Frontend UI** | Hash router + not-available page + breadcrumbs (group, item, page); grouped sidebar, More group, footer pinning, active rule R6; sub-nav via `TabStrip` (top) and a left list; icon allowlist with kebab-case keys and fallback (the F8 fix); menus via `DropdownMenu`/`OverflowMenu`/`OverlaySidebar` and the typed action gateway; keyboard/a11y/narrow behavior. | 0116, 0119, 0120 |
| **7 Kit / Parallax** | Parallax example with fixtures and tokens only; then promote proven primitives after owner review and a distinct second consumer; consider the headless `buildNavTree` helper. | 0113, then 0114 |
| **8 Docs / checks** | README "Adding a page" (lines 82-89), CONTRIBUTING.md:40, AGENTS.md:22, ADR 001 section 7, docs/host-operations nav policy, plugin-author guide, and the **AGENTS.md boundary sentence** (add: "malformed *navigation contributions* are warned and dropped; all other malformed declarations still refuse startup"). Gating, ordering, badges, overrides, palette follow-ups. | 0123, 0122 |

**Notes for the PM.**
- *0112 overlap.* Its "owner plugin_id in /api/nav" extends a surface this ADR retires; the registry already carries `owner_id`. Suggest 0112 keep to the icon-map fix, reserved-route and duplicate handling written as pure functions (reused by 0115), and not grow the `/api/nav` JSON.
- *New checks are decisions.* A "manifest vs registry vs icon" CI check, a Go/TS descriptor parity test, and the schema-1 golden (old `MergedNav` vs new projection) each assert that two sources agree. AGENTS.md says that is a decision to raise, not a step to take. Recommendation: the golden is a *temporary* migration assertion in the 0117 PR; the other two need the owner's yes.
- *Sequence.* 0112 and 0113 parallel; 0115 then 0117; gate; 0118; 0116 can start after 0117 against the registry doc; 0120 owner visual checkpoint; then 0121, 0119, 0122, 0123; 0114 after owner review of 0113.

---

## 9. Verification performed, and limits

Done (replay and outputs in the evidence item): fetched all origins first; read primary source for Tachyon, design-kit, libs, plugin-sdk (archived), Nanite; verified npm availability and versions; downloaded the published `plugin-registry@0.2.0` tarball into scratch and matched its shasum to npm's; projected the eight real manifests into a registry v2 document (sha256 `41475b8a...851bdd`, independently regenerated byte-exact by the reviewer) and validated it with (a) the pinned Go `registry` source (`Validate` ok; `Plan`: 38 listed, 38 accepted, 0 refusals), (b) the published TS package (38 accepted); ran the proposed catalog on the real plugin-host-ui runtime source (scratch copy under Node 24 type stripping): 8 groups in priority order, 15 items in their priorities, 15 routes, all `available`; confirmed the TS registry and Go decoder give identical results when `retired_plugins` and `nav_projection` are present at top level; cross-reference checked the section 4.1 example manifests. All of this replays from pinned commits in a fresh stage with one script (~27 s, PASS); the replay's first run caught that the libs tags are annotated (tag object vs commit), now corrected in section 1.

Negative cases observed. Registry layer: reserved owner `core` and an unknown optional kind give named refusals while the 38 admitted entries stay; a duplicate `public_binding`, an unknown owner, or a required entry with an unsupported schema reject the **whole** document (0 contributions). Host runtime layer: a required invalid entry publishes 0 views (planning.accepted false); the same entry optional is refused alone (38 views listed, 37 available, the refused one shown as a generic unavailable view with `invalid-metadata`); a page claiming `/dashboard` is refused the same way.

**Not covered.** No Tachyon code was changed, built or tested (docs-only; no broad Tachyon or Chromium gate was run, as the review asked). plugin-host-ui's own test suite and Chromium harness were not run; its source was exercised only through the scratch harness. The 0104 host-delivery draft was not available; component delivery, CSP, frame hosting and endpoints are PENDING. Parallax and the second-consumer candidates (Tangent ops shell, Nil) were not audited. The projector, router, menus, `command` projection and the `nav_projection` field are designed, not implemented or measured. Keyboard/a11y/narrow behavior is specified only as a requirement. No live service, config, credential or account was touched.

---

## 10. Consolidated sheet for the manager/PM (agreed vs proposed vs open)

**Agreed (owner):** adopt plugin-host-ui + registry v2, no parallel mechanism; primitives proven in Parallax then promoted (owner review + second consumer); explicit group owner wins; orphans to top-level More; never refuse startup for nav; declarative menus + URL/hash routing; warn-and-drop for nav only; sandboxed-frame default; a declared page registers a route even when hidden; eight manifests preserved additively; `/dashboard` `/settings` `/plugin-recovery` reserved.
**Proposed (this ADR; please confirm or redirect):** D1 single path via Go projector and `/api/plugins/registry`, retire `/api/nav` with the frontend consumer; D2 five declarative kinds + eight regions, `required:false`; D3 page rules R1-R6 incl. v1 page synthesis and the 3.4.1 overlap rules; D4 plugin-id tie-break, `owner:true` tier, `group_ref`/`parent`, depth 2, More fallback synthesized in the browser; D5 reason-code table, projector fail-safe and the explicit `nav_projection` signal (name/shape pending); D6 `nav_schema` 1/2 semantics; D8 wave-1 pages are host-compiled `view` bindings, component delivery pending 0104; D9 no upstream plugin-host-ui change now.
**Open (owner; defaults in section 6; pending PM):** (1) user nav overrides: none, kit layout store only; (2) palette: navigation only first; (3) reserved routes: host set + config-ops on `/settings`; (4) icons: lucide kebab-case allowlist + fallback.
**Pending PM (decisions, not assumed):** (a) the three "two sources agree" checks (section 8); (b) confirm wave-1 pages are host-compiled view bindings (plugin-delivered page bundles wait for 0104); (c) confirm or rename the `nav_projection` diagnostic field; (d) whether to open a docs-only ADR PR (not opened; the manager owns that request).
**Critical-path warning (dated snapshot):** plugin-host-ui is unpublished; frontend adoption (0118) cannot start under "consume published packages" until a release exists and the kit cohort is lifted.

---

## Implementation allocation disposition — DEC085 (2026-10-10)

The canonical body above is retained verbatim, including its historical
DRAFT/PENDING labels. The owner acceptance at its beginning supersedes those
labels; dated source/package observations remain dated observations.

PM allocation receipt `01a12455-38b2-796b-b3d8-90d3a8b074a6` assigns
CW-20261010-0115 to Codex and authorizes this separate docs-only PR.
DEC085 approves the schema-1 golden and Go/TS descriptor parity checks, and
declines a manifest/icon/page-registry agreement check. The actual published Go
kind/region versus local TS catalog descriptor parity is staged with 0117/0118;
the old-MergedNav/new-registry projection golden is staged with 0117. Local
normalization compatibility controls and a declaration wire fixture in 0115
are scaffolding, not completion of those projector/catalog checks. This stage
ownership is awaiting the allocation clarification escalated by the manager
(receipt `01a12460-27dc-792b-8b41-3f22f17918fd`). The additive field name
is `nav_projection`, with `ok`, `degraded`, and `failed` status declarations.
Projection itself belongs to CW-20261010-0117. Wave-1 pages remain host-compiled;
source-prepared 0104 code is not registry publication, a released runtime, or
allocated consumer custody.

The historical 0115 task description mentions `requires_any` and badges.
The accepted contract instead uses the singular/plural required-verb union in
section 3.4.1 (all declared requirements apply). There is no any-of weakening or
badge API in this contract; G12 remains deferred to the held 0122 scope.

The routing task's named-parameter proposal (`/work/:id`) differs from the
static route identity grammar in R3. That proposal has been escalated for an
owner disposition. This document does not claim colon syntax was accepted;
R3 remains the contract until an actual disposition supersedes it.

This PR records decisions and provenance. It does not implement the projector,
frontend adoption, routing, menus, or deployment. Independent manager source
and receipt review is separate from the implementation author's test execution.
