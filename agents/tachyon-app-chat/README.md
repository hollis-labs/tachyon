# Tachyon app chat agent

The Tachyon-owned definition of the in-app chat agent (ADR `adr_embedded_app_agents`).

- `agent.md`: agentdef v2. Portable; no host, provider, model or tool authority.
- `host.yaml`: how Nanite runs it today (native `api` loop, Anthropic `claude-opus-5-5`).

Tools are not authored here. They are granted by the Nanite operator against the
installed definition pin and host-settings revision: the five read-only
`tachyon_*` verbs (`tachyon_planner_list`, `tachyon_objects_list`,
`tachyon_objects_get`, `tachyon_objects_history`, `tachyon_objects_lookup_source`)
served by Tachyon's MCP at `/api/agent/v1/mcp`, registered in Nanite as the
`tachyon` server.

## Changing it

An installed definition is immutable: the same `definition_id` and `revision` with
different bytes is a 409. Bump `revision` in `agent.md` on every change, install,
then redo the grants (they belong to a host-settings revision and definition pin).

```sh
nanite agent validate agents/tachyon-app-chat/agent.md
nanite agent load agents/tachyon-app-chat --dry-run
```
