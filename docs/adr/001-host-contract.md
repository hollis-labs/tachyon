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

Tachyon defines 8 canonical modules organized across 3 operational tiers:

| Tier | Module | Responsibility | Example Verbs |
|---|---|---|---|
| **Tier 1 (Core)** | `agent` | Agent entity lifecycle, identity, tool/skill grants | `agent_list`, `agent_get`, `agent_create`, `agent_update`, `agent_delete` |
| | `launch` | Execution instantiation and runtime bootstrapping | `launch_session`, `launch_quick` |
| | `session` | Active session lifecycle and state synchronization | `session_get`, `session_list`, `session_terminate` |
| **Tier 2 (Observability)** | `observe` | Telemetry, traces, event logs, and operational health | `observe_tail`, `observe_events`, `observe_metrics` |
| **Tier 3 (Integrations)** | `work` | Task management, issues, backlog sync, and tracking | `work_list`, `work_assign`, `work_sync` |
| | `service` | External integrations, connected service brokers | `service_connect`, `service_status`, `service_invoke` |
| | `scm` | Source control operations, repository and commit bindings | `scm_diff`, `scm_branch`, `scm_commit` |
| | `config` | Dynamic runtime settings, host/plugin configuration | `config_get`, `config_set`, `config_schema` |

### 7. Capability Declaration Format

Plugins declare their capabilities in the `plugin/init` response during host initialization. The initialization payload includes a `capabilities` object containing the declared `modules` array and `verbs` map:

```json
{
  "id": "agent-ops",
  "name": "Agent Operations",
  "version": "0.2.0",
  "capabilities": {
    "modules": [
      "agent",
      "launch"
    ],
    "verbs": {
      "agent_list": { "effect": "reads" },
      "agent_get": { "effect": "reads" },
      "agent_create": { "effect": "writes" },
      "agent_delete": { "effect": "destroys" },
      "launch_session": { "effect": "open_world" }
    }
  }
}
```

### 8. Registration-Time Capability Verification (D-47)

Capability validation occurs synchronously during plugin initialization (`LoadPlugin`). The host validates:

1. **Module Uniqueness**: No two loaded plugins may declare ownership of the same module.
2. **Verb Prefix Adherence**: Every verb declared in `verbs` must start with `<module>_` where `<module>` is present in the plugin's `modules` array.
3. **Effect Validity**: Each verb's `effect` must be one of `reads`, `writes`, `destroys`, or `open_world`.
4. **Identifier Formatting**: Module names and verb names must match `^[a-z0-9_]+$`.

**Enforcement Rule**: Any validation failure is treated as a **hard load error**. The plugin is terminated, and Tachyon refuses to start. Degraded catalog admissions or silent fallbacks are prohibited.

### 9. Wire Format and Migration Strategy

Tachyon introduces the `verb/invoke` JSON-RPC method (`contract.MethodVerbInvoke = "verb/invoke"`) for capability execution:

#### Request Params (`VerbInvokeParams`)
```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "verb/invoke",
  "params": {
    "verb": "agent_create",
    "payload": {
      "name": "ResearchAgent",
      "model": "claude-3-7-sonnet"
    }
  }
}
```

#### Response Envelope (`VerbInvokeResult` / `ResultEnvelope`)
```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "status": "ok",
    "data": {
      "id": "ag_01h8x...",
      "name": "ResearchAgent"
    }
  }
}
```

#### Migration
To enable non-breaking incremental migration of existing plugins:
* `verb/invoke` runs concurrently alongside legacy methods (`crud/create`, `crud/read`, `crud/update`, `crud/delete`, `crud/list`, and `command/execute`).
* Host route proxies will bridge HTTP endpoints to `verb/invoke` where capability declarations exist, falling back to legacy handlers for unmigrated plugins until the deprecation milestone is reached.

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
