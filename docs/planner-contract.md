# Planner extension contract (PL0)

Contract and stub for the Planner backend extension: typed links, pin, area/horizon, assignments, Torque-backed task lanes, timeline, momentum and need-you. Design: Tesseract `design_tachyon_planner_backend_20261030` (sections 3 and 6). Code: `internal/plannerapi` (contract, `openapi.yaml`, fixtures, stub). Real work lands in PL1 (data model), PL2 (assignments), PL3 (task service), PL4 (events and timeline), PL5 (momentum and need-you).

## Using the stub

`TACHYON_PLANNER_STUBS=1` mounts the stub, route by route, under `/api/plugins/console/v1/planner` behind `operator.RequireOperator`. Each stub pattern is more specific than the real planner's subtree mount, so every route not listed in `plannerapi.Routes` still reaches the real planner. The stub keeps no state and never touches the planner store.

Routes the stub serves: the new ones (relations, links, task lanes, transition, meta, task-context, assignments, agent work, timelines, momentum, need-you), plus two existing routes it replaces while the flag is on: `POST /{db}/{id}/comments` (adds status, pin, mentions) and `POST /ideas/{id}/promote`.

The extended shapes of existing routes (list, get, board, patch) are not mounted. They are named fixtures at `GET /stub-fixtures/{name}`: `themes` shows `area`, `horizon`, `pinned`, `assignees`, `need_you`, `relations_count`, `momentum` for all four momentum labels. Every other fixture is also reachable by name. `GET /stub/stream` is a scripted SSE (ids 101-106, replay after `Last-Event-ID`, `resync` outside the ring, then the response ends).

Controls: `X-Stub-Status` forces an error status. `X-Stub-Scenario`: `conflict` (409 on any write), `cycle` (422 `cycle` on POST relations), `degraded` (`/momentum`), `empty` (timelines and agent work).

Fixture ids: `theme-agents-console` (rising, pinned, assigned, need-you review+decide), `theme-forge-gui` (steady), `theme-transcripts` (cooling, stale), `theme-settings-center` (stalled, blocked), `theme-unknown` (404), `theme-empty` or any id ending `-empty` (empty timeline). Task `CW-20261030-0900` answers a transition with 409; `CW-20261030-0999` has no theme in `task-context`.

## Rules the real writers enforce (the stub checks the shape of these)

- Relations: no self link, no duplicate (kind, to), `needs` within one database and acyclic (422 `cycle`), `delivers` theme to roadmap only, `mitigates` theme to risk only, 200 per item.
- Pin: `PUT /{db}/{id}/pin {pinned, expected_revision}` toggles `pinned`/`pinned_at` and needs no comment; a stale revision is 409 `revision_conflict` with `details.current` the current item. Item payloads name the revision `rev`; request bodies keep `expected_revision`. The real write is live as of PL1; the stub only answers the shape (area is the roadmap enum: hub, llm-gateway, mesh, gui, plugin-platform, fabric, nanite, harness, policy, tooling, process).
- Comment with status: text up to 16 KiB, `kind` comment or decision, `status` and `pin` applied in one revision write.
- Transition: `blocked` needs `reason`; same status is a no-op 200; a differing Torque status is 409 with the current status. Undo is the inverse transition; the client holds the 8 second window.
- Task meta: `done_summary` up to 600 characters, up to 20 PR cites.
- Assignments: exactly one of `agent_id` and `team_id`; target is `{db,id}` or `{kind:"torque.task",ref}`. Assignments never change Torque's own claim or executor. Ending an ended assignment is 409.
- Timeline: `facet` all|tasks|prs|decisions|blockers|comments|workstreams, `limit` 1-100 (default 30), `before_seq` pages backwards, `next_before_seq` null at the end.

## Security posture

No new guards, audit, step-up or scopes (direction from Chrispian via the PM). Real writes keep the existing planner write path; new writes call the assurance Provider requirements table so a later hardening is one swap (`planner.write`).

## Not in PL0

The real behaviour (PL1-PL5), the frontend wiring (PL6), and the curl matrix (PL7). Open items for the owners are listed in the design, sections 7 and 8.

## PL2: assignments (real)

`internal/plannerassign`, mounted by `register_plannerassign.go` (stands down when `TACHYON_PLANNER_STUBS=1`). Routes: `POST /assignments`, `GET /assignments`, `POST /assignments/{id}/complete|cancel`, `GET /agents/{agent_id}/work`, behind `RequireOperator`.

- An assignment is the existing `assignment` object (`state` active|completed|cancelled) with `assignee` and `target` links. `planner.list` already filters `assigned_to` through those links; PL2's test confirms it for themes (any assignment state, per `objects.PlannerFilter`).
- `agent_id` and `team_id` accept the catalog id (agents synced from Tether) or the object id; responses return the catalog id. An unknown assignee is 404. Planner item targets are `{db,id}` (object id or legacy id, resolved). Torque tasks are external targets (`provider torque`, collection `tasks`, instance = the configured Torque URL); nothing is written to Torque.
- Ending an ended assignment is 409; `primary` marks the earliest active assignment per target.
- `Service.Assignees(ctx, target)` is the derived `assignees[]` for PL1/PL3 to attach to item and task responses.
- Work list: only `why = assigned` is produced here. Themes and decisions come from the planner item; Torque task targets are listed with the id as the title until PL3 enriches them. `session` (needs the Agents collector) and `referenced` (needs decision links) and `needs_user` (PL5) are not produced yet; other targets (workstreams, risks...) are left out.
- Not in this PR: the `assigned_to=` query parameter on existing planner lists (it needs an edit to the existing list route), agent verbs `assign`/`unassign` and the access catalog entries, and `task-context` (needs the theme/task mapping from PL3).

## Real behaviour: PL1, PL3, PL4, PL5 (PL7 docs and curl matrix)

Everything below is served by `internal/planner` under `/api/plugins/console/v1/planner`, behind the existing planner write path (`operator.RequireOperator`). Nothing here needs a stub flag; leave `TACHYON_PLANNER_STUBS` unset on live (when set, the stub wins on the routes it lists). Migrations: `041_planner_relation_index`, `042_planner_task_meta`, `044_planner_events` (numbers are taken at merge).

| Slice | Routes | Notes |
|---|---|---|
| PL1 | `PUT /{db}/{id}/pin`, `POST`/`DELETE /{db}/{id}/relations`, `GET /{db}/{id}/links`, `POST /ideas/{id}/promote`; `PATCH` accepts `pinned`, theme `area`, `horizon`, `done_summary` | Items gain `pinned`, `pinned_at`, `relations_count`. Default list order is pinned first. `X-Planner-Actor: <slug>` labels the author (`operator:<slug>`), default `operator:browser`. |
| PL3 | `GET /themes/{id}/tasks`, `GET /workstreams/{id}/tasks` (adds `lanes`), `POST /tasks/{task}/transition`, `PUT /tasks/{task}/meta`, `POST /{db}/{id}/comments` (adds `status`, `pin`) | Transitions are real Torque status changes with Tachyon's Torque token. Comments answer `{comment, item, applied}`. |
| PL4 | `GET /themes/{id}/timeline`, `GET /workstreams/{id}/timeline`, `GET /events` (SSE) | Stream ids are `planner_stream` seqs; `Last-Event-ID` replays, an id outside the ring gets `resync`. |
| PL5 | `GET /momentum`, `GET /need-you`; lists take `?need_you=review\|decide\|blocked\|stale\|any` | Themes and decisions gain `need_you {reasons, since}`; themes gain `momentum`. |

Theme `area` is the roadmap enum: `hub, llm-gateway, mesh, gui, plugin-platform, fabric, nanite, harness, policy, tooling, process`. Idea status gains `promoted`.

### Data and workers

- Relations are stored on the source item (`properties.portfolio.relations[]`); `planner_relation_index` is rewritten in the same transaction as the item (see `objects.persist`), so inbound links cannot drift. Legacy id arrays are read, not migrated: `depends_on` reads as `needs`, `roadmap_ids` on a theme as `delivers`, the rest as `relates`. The cycle check follows typed `needs` only.
- The ingest worker (`register_plannerevents.go`, `TACHYON_PLANNER_EVENTS=0` turns it off) runs every 45 s: planner revisions, Torque snapshot diff, merged PRs from the alerts store. Torque moves between two passes are not seen individually; there is no backfill before the first pass. Each stream connection also tails revisions.
- Derivations (`derive.go`) are pure functions with spec tables in `derive_test.go`. Settings live in `app_settings`: `planner.stale_days` (default 7), `planner.timeline_retention_days` (default 180), `planner.momentum` (JSON window, weights, thresholds; placeholders).
- Review/blocked counts and the series come from the Torque snapshots, so they read 0 until a theme has been polled once.

### Curl matrix

Run against a scratch instance (own `TACHYON_DATA_DIR` and port), with an operator session. `B` is the base URL including the prefix; the cookie jar holds the browser session. No token goes on the command line.

```
B=http://127.0.0.1:PORT/api/plugins/console/v1/planner
H='-H Origin:http://127.0.0.1:PORT -H Content-Type:application/json'
# PL1
curl -b jar $H -X PUT  $B/themes/$T/pin        -d '{"pinned":true,"expected_revision":1}'      # 200 {item}; stale rev 409 revision_conflict
curl -b jar $H -X POST $B/themes/$T/relations  -d '{"kind":"needs","to":{"db":"themes","id":"'$U'"},"expected_revision":2}'
curl -b jar $H -X POST $B/themes/$U/relations  -d '{"kind":"needs","to":{"db":"themes","id":"'$T'"},"expected_revision":1}'   # 422 cycle
curl -b jar $B/themes/$T/links                                                           # outbound, inbound, external, legacy_mapped
curl -b jar $H -X POST $B/ideas/$I/promote     -d '{"to_db":"themes","expected_revision":1}' # receipt, idea (promoted), created
# PL3
curl -b jar $B/themes/$T/tasks                                                           # lanes + counts from Torque
curl -b jar $H -X POST $B/tasks/CW-1/transition -d '{"to":"review","expected_status":"doing"}'  # 409 with current status if stale
curl -b jar $H -X POST $B/tasks/CW-1/transition -d '{"to":"blocked"}'                    # 400 invalid, details.field=reason
curl -b jar $H -X PUT  $B/tasks/CW-1/meta       -d '{"done_summary":"Shipped.","pr_cites":["owner/repo#9"]}'
curl -b jar $H -X POST $B/themes/$T/comments    -d '{"text":"Ready","status":"ready","pin":true,"expected_revision":3}'
# PL4
curl -b jar "$B/themes/$T/timeline?facet=tasks&limit=10"                                 # events, milestones, momentum, next_before_seq
curl -N -b jar -H 'Last-Event-ID: 0' $B/events                                           # replay; unknown id -> event: resync
# PL5
curl -b jar $B/need-you ; curl -b jar $B/momentum ; curl -b jar "$B/themes?need_you=blocked"
```
