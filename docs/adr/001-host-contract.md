# ADR 001: Tachyon Host Contract Specification

**Status:** Accepted  
**Date:** 2026-10-01  
**Deciders:** Chrispian  

## Context

Tachyon serves as the operational host and management UI for agent runtime environments within the agent-fabric ecosystem. Historically, Tachyon plugins exposed operations via ad-hoc abstractions: generic CRUD resource endpoints (`crud/*`) and unstructured command invocations (`command/execute`). While functional for basic prototypes, this interface lacked:

1. A unified response envelope conveying execution status and human-in-the-loop (HITL) intervention requirements.
2. Formal semantic classification of side effects (e.g., read vs. write vs. destructive actions vs. open-world execution).
3. Early verification and strict ownership of plugin capabilities at registration time, leading to silent collisions and runtime routing ambiguities.
4. Consistent verb naming schemas across multi-plugin installations.

During the agent-fabric vNext architecture planning, decisions **D-44** through **D-49** established the foundation for Tachyon's revised host contract. This document formalizes these architectural decisions and defines the specification governing Tachyon and its subprocess plugins.

---

## Decision

Tachyon adopts a capability-driven host contract implementing decisions **D-44** through **D-49**.

### 1. Result Envelope (D-44)

Every verb invocation returns a structured `ResultEnvelope` containing four canonical fields:

```json
{
  "status": "ok | error | ask",
  "data": { ... },
  "ask": { ... },
  "error": { ... }
}
```

* **`status`** (string, required): One of `ok`, `error`, or `ask`.
* **`data`** (json, optional): The successful response payload when `status == "ok"`.
* **`ask`** (object, optional): Operator decision metadata populated when `status == "ask"`. Maps directly to HITL gates (D-39):
  * `prompt` (string, required): Clarification or authorization prompt for the human operator.
  * `options` (array of strings, optional): Predefined choices or acceptable inputs (e.g., `["approve", "reject"]`).
  * `context` (map, optional): Supporting context required for operator evaluation.
* **`error`** (object, optional): Structured diagnostic information when `status == "error"`:
  * `code` (string, required): Machine-readable error code.
  * `message` (string, required): Human-readable error description.
  * `detail` (json, optional): Supplementary error context or stack/validation traces.

Consumers switch strictly on `status` to determine which field is active.

### 2. Policy Middle-State (D-45)

The **only** permitted policy middle-state across Tachyon and its plugins is `ask`.

Synonyms such as `confirm`, `needs_approval`, `pending`, `prompt`, or `decision_required` are strictly forbidden. When policy evaluation or execution gating pauses an operation for operator intervention, the verb must return `status: "ask"` with an `AskDetail` payload.

### 3. Effect Classification (D-46)

Verbs declare side effects using the `go-permission` `Behavior` taxonomy:

* **`reads`**: Pure observation. Does not mutate internal or external state. Safe to retry, cache, and execute without interactive operator confirmation.
* **`writes`**: Mutates local or managed state within Tachyon's direct boundaries (e.g., updating agent metadata, modifying reflex definitions).
* **`destroys`**: Permanently removes or deletes state (e.g., deleting agents, tearing down workspaces). Carries high blast radius.
* **`open_world`**: Causes side effects beyond Tachyon's containment boundary (e.g., launching execution sessions, calling third-party APIs, triggering CI/CD pipelines, remote network actions). Implies `writes`.

Tachyon does not adopt `go-workflow`'s `graph.Effect` model. To avoid unnecessary framework couplings, Tachyon defines its own internal `Effect` type (`internal/contract/effect.go`) while maintaining exact semantic and textual parity with `go-permission`. Inventing alternative taxonomies is explicitly disallowed under D-46.

### 4. Terminology (D-48)

The system standardizes on the term **`capabilities`**, not `features`.

All host APIs, manifest definitions, registration payloads, and inspection models use `capabilities` when referring to the modules, verbs, and behavioral declarations exposed by plugins.

### 5. Verb Naming and Namespace Rules (D-49)

Verbs must conform to the following identifier rules:

* **Naming Pattern**: `<module>_<verb>` (e.g., `agent_list`, `agent_create`, `session_launch`).
* **Format Regex**: `^[a-z0-9_]+$` (strict lowercase alphanumeric and underscores).
* **Prefix Requirement**: Every verb must begin with `<module>_` where `<module>` is one of the modules explicitly declared by the plugin.
* **Collision Policy**: Collisions on module or verb registrations constitute a **fatal startup error**. If two plugins claim the same module namespace or declare identical verbs, the host aborts startup immediately.

### 6. Module Taxonomy

The examples below name current declarations, not every operation originally
envisioned for a module. Services and SCM currently expose read-only adapters;
external streams/history, plugin installation and automatic HITL continuation
are not supplied by this taxonomy.

Tachyon defines 8 canonical modules organized across 3 operational tiers:

| Tier | Module | Responsibility | Example Verbs |
|---|---|---|---|
| **Tier 1 (Core)** | `agent` | Agent entity lifecycle, identity, tool/skill grants | `agent_list`, `agent_read`, `agent_create`, `agent_update`, `agent_delete` |
| | `launch` | Execution instantiation and runtime bootstrapping | `launch_prepare`, `launch_execute`, `launch_status` |
| | `session` | Active session lifecycle and state synchronization | `session_read`, `session_list`, `session_stop` |
| **Tier 2 (Observability)** | `observe` | Telemetry, traces, event logs, and operational health | `observe_activity`, `observe_events`, `observe_metrics` |
| **Tier 3 (Integrations)** | `work` | Task management, issues, backlog sync, and tracking | `work_list`, `work_assign`, `work_transition` |
| | `service` | External integrations, connected service brokers | `service_list`, `service_status`, `service_health` |
| | `scm` | Source control operations, repository and commit bindings | `scm_list`, `scm_status`, `scm_diff` |
| | `config` | Dynamic runtime settings, host/plugin configuration | `config_get`, `config_set`, `config_schema` |

### 7. Capability Declaration Format

Plugins embed `capabilities.json` and return its declaration through the reserved
`plugin_capabilities` command after `plugin/init` and `plugin/load`. The host
calls `command/execute` with `name: "plugin_capabilities"`; the response has
`action: "message"` and `content` containing this JSON as a string:

```json
{
  "modules": ["agent"],
  "verbs": {
    "agent_list": { "effect": "reads" },
    "agent_create": { "effect": "writes" },
    "agent_delete": { "effect": "destroys" }
  },
  "nav": {
    "groups": [{ "id": "agents", "label": "Agents", "priority": 100 }],
    "items": [{
      "id": "agent_list", "label": "Agents", "group": "agents",
      "route": "/agents", "requires_verb": "agent_list", "priority": 100
    }]
  },
  "settings": {
    "fields": [{
      "key": "provider_url", "type": "string", "label": "Provider URL",
      "default": "http://localhost:8090", "required": true
    }]
  }
}
```

Plugins implement `pluginkit.VerbPlugin` (`Capabilities` and `HandleVerb`) and
call `pluginkit.Dispatch` first in `Command`. When `handled` is false, they
continue their legacy command dispatch.

`nav` and `settings` are optional and survive the capability carrier. Existing
`GET /api/verbs` responses remain keyed by module, with these fields added to
each plugin declaration. `GET /api/nav` exposes the merged navigation view as
`{ "groups": [...], "items": [...] }`.

Each nav item references a group declared by the same plugin. Cross-plugin
references are rejected so validation never depends on load order. Plugins
may declare the same group ID: the first successfully loaded plugin's group
metadata wins, the loser is logged, and later distinct items still contribute
to that group. Duplicate item IDs across plugins also use first-loaded wins
and log the loser. Within one declaration, duplicate group/item IDs are hard
errors. Group/item IDs match `^[a-z0-9][a-z0-9_-]*$`, and a non-empty
`requires_verb` must name a verb declared by that plugin. Merged navigation
sorts by priority (zero means the default 1000), then by ID to break ties.

Settings keys are unique within a declaration and match
`^[a-z0-9][a-z0-9_.-]*$`. Types are `string`, `boolean`, `number`, or `select`.
Select fields require non-empty, unique option values. Supplied defaults must
match the field type; select defaults must name an allowed option. A missing
or null default is valid even for required fields, meaning the operator must
supply a value. Required string defaults cannot be blank; boolean false and
numeric zero are valid. These declarations describe schemas only: the
declaration alone does not persist values. The host/config-ops integration now
persists validated overrides and applies them at spawn/restart; see
[host operations](../host-operations.md). Frontend controls have their own
implementation and are not implied by a declaration.

### 8. Registration-Time Capability Verification (D-47)

Capability validation occurs synchronously during plugin initialization (`LoadPlugin`). The host validates:

1. **Module Uniqueness**: No two loaded plugins may declare ownership of the same module.
2. **Verb Prefix Adherence**: Every verb declared in `verbs` must start with `<module>_` where `<module>` is present in the plugin's `modules` array.
3. **Effect Validity**: Each verb's `effect` must be one of `reads`, `writes`, `destroys`, or `open_world`.
4. **Identifier Formatting**: Module names and verb names must match `^[a-z0-9_]+$`.
5. **Navigation and Settings**: Local references, unique declaration IDs/keys, field types/options and provided defaults follow section 7. Invalid declarations are hard load errors; cross-plugin nav metadata collisions use the documented first-loaded rule.

**Enforcement Rule**: Any validation failure is treated as a **hard load error**. The plugin is terminated, and Tachyon refuses to start. Degraded catalog admissions or silent fallbacks are prohibited.

Startup admission policy:

| Condition | Startup decision |
| --- | --- |
| Missing binary | Warn; optional absence |
| Unbuilt/non-executable binary | Warn with build guidance; optional absence |
| Init/handshake timeout | Refuse startup; 120s host I/O ceiling |
| Discovery RPC error or command `action: "error"` | Warn; admit legacy without module claims |
| Malformed declaration or RPC/command shape | Refuse startup |
| Module/verb ownership collision | Refuse startup |
| Invalid nav/settings declaration | Refuse startup |
| Hello/no-capabilities | Missing hello is optional; explicit discovery rejection admits legacy |

* Discovery is ordered by directory name. A missing plugin directory, directories
  without manifests, and missing/non-executable binaries are optional absences:
  the host logs build guidance and continues. The manifest-free `hello` binary
  follows the same optional-absence rule and loads first when available.
* Once an executable is present, any init/load/discovery, settings-read, or
  declaration-validation failure aborts startup, including duplicate module
  ownership. Filesystem errors other than absence also abort discovery.
* Only an explicit capability-discovery RPC error or command `action: "error"`
  admits a legacy plugin without module claims. Malformed RPC/command responses,
  unexpected command actions, and malformed declarations are hard errors.
* HTTP starts only after admission completes. Refusal exits non-zero with one
  structured error line naming `plugin_id`, `path`, and `reason_class`.
  `LoadPlugin` force-stops and reaps the rejected child; startup rollback unloads all earlier children with a shared
  five-second grace, then force-stops/reaps them. An expired startup context does
  not skip rollback. Init/load/discovery retain the host's 120-second I/O ceiling.
* Cross-plugin nav IDs retain first-loaded precedence. A later explicit restart
  uses ordinary per-plugin admission; its failure unloads only that plugin and
  does not trigger startup rollback.

### 9. Wire Format and Migration Strategy

Tachyon carries declared verbs over `command/execute`. plugin-sdk v0.4.0
`Serve` cannot route `verb/invoke`, and its `InitResult` cannot carry a
capability declaration. The command carrier preserves the contract semantics
without requiring an unavailable SDK release. Native `verb/invoke` routing
remains a future plugin-sdk change.

#### Request

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "command/execute",
  "params": {
    "name": "agent_create",
    "args": "{\"name\":\"ResearchAgent\",\"model\":\"claude-3-7-sonnet\"}"
  }
}
```

`name` is the declared verb; `args` is the JSON payload string. Empty `args`
means no payload.

#### Response

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "action": "message",
    "content": "{\"status\":\"ok\",\"data\":{\"id\":\"ag_01h8x...\",\"name\":\"ResearchAgent\"}}"
  }
}
```

The host unwraps `CommandExecResult.content` and returns the `ResultEnvelope`
JSON to HTTP callers, preserving `ok`, `error`, and `ask` outcomes.

#### Migration

Legacy `crud/*` and commands (including `launch` and provider `capabilities`)
continue to work. A discovery RPC error (including method-not-found or unknown
command), or a command response with `action: "error"`, admits a legacy plugin
without capabilities and logs a warning. A returned declaration must validate;
malformed declarations and module collisions remain hard load errors under
D-47. HTTP verb routes dispatch only declared verbs through the command carrier.

### 10. Implemented host lifecycle and optional bridges

The serial wire uses request IDs without concurrent multiplexing. Ordinary calls
have a cancellable queue wait capped at 120s, then a fresh 120s active-I/O budget;
request cancellation no longer interrupts active work. Lifecycle owners retain
context cancellation. Watchdogs close/kill only the captured process, retire
routing claims and retain recovery metadata. Explicit
`POST /api/plugins/{id}/restart` recovers retired plugins; a busy lifecycle lock
returns 503/status `unchanged` (`ErrRestartBusy`), not a false unload report.
Settings targets and optional registry `retired_plugins` expose safe retirement
identity/reason/time, clearing on successful load or shutdown.

SIGINT/SIGTERM drains HTTP for 5s, then gives each plugin a fresh 5s unload grace,
force-stopping/reaping leftovers. The deployed systemd stop limit is 90s;
lifecycle contention is separately bounded and can exceed that outer limit.
[Host operations](../host-operations.md) gives the per-operation budget audit,
admission/exit policy, deployment and known limitations.

Configured Tangent HITL adds bounded correlation and read-only safe status;
unconfigured asks pass through unchanged. Approval is a terminal decision, not
permission to replay an operation: continuation is unavailable pending
CW-20261001-0532. See [HITL](../hitl-host.md). Private bounded Observe ingestion
adds safe metadata and explicit loss counters without recursive recording or
changing operation-error health semantics; see [Observe feed](../observe-host-feed.md).

---

## Consequences

### Positive
* **Deterministic Contract**: The host and frontend have an unambiguous, machine-readable declaration of all available operations at startup.
* **HITL Integration**: Standardizing on `status: "ask"` ensures direct mapping into the agent-fabric gate and authorization architecture without bespoke translation layers.
* **Granular Safety & Policies**: Categorizing effects into `reads`, `writes`, `destroys`, and `open_world` allows the host and security policies (e.g. Cerberus) to restrict or prompt for operations based on side-effect profiles.
* **Safe Coexistence & Fail-Fast Guarantees**: Registration-time capability verification eliminates namespace conflicts and ensures invalid plugins fail before accepting traffic.
* **Backward Compatibility**: Parallel wire protocol support allows progressive migration of plugin implementations.

### Negative & Trade-Offs
* **Strict Upfront Validation**: Plugins with missing prefixes, invalid effect labels, or overlapping modules will halt daemon initialization.
* **Migration Overhead**: Existing plugins and proxies must be updated to export capability manifests, adopt `<module>_<verb>` conventions, and wrap outputs in `ResultEnvelope`.
