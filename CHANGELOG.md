# Changelog

All notable changes to Tachyon are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Tachyon is pre-1.0 and has not cut a tagged release: there is no stability promise, and breaking changes can land in any commit. This file is backfilled from the git log on a good-faith basis, not exhaustively.

## [Unreleased]

### Added

- **Sessions page for durable agents**, and an Enabled toggle on agents.
- **Agent Ops CRUD proxy over the full Nanite surface**: agents, tool grants, skill assignment and grants, MCP server attach/detach, reflexes, and read-only views of durable agents, their events and their sessions. `GET /api/capabilities` reports what the active provider supports so the UI can gate actions.
- **Agent detail view, edit flow, dynamic status filters** and a New Agent dialog in the Agent Ops dashboard.
- **Agent Ops plugin** (`plugins/agent-ops`) with a Nanite HTTP adapter and CRUD operations, and an Agent Operations dashboard UI.
- **Plugin host**: Tachyon spawns `plugin-sdk` subprocess plugins, serves their registry at `GET /api/plugins/registry`, and loads them in the browser.
- Project descriptor for running Tachyon under Cerberus.

### Changed

- Scaffolded from Folio's `sysop-ui` preset: a Go server embedding a Vite + React Sysop UI served under `/sysop/`.

### Fixed

- The theme attribute is set synchronously before first paint, removing a flash of the wrong theme.
- Agent status vocabulary corrected to match the provider, and the frontend typecheck fixed.
