These golden bodies are unmodified HTTP output from disposable Torque builds:

- legacy: `47aae8175a259c6ac0025fb6c609b92faa6c4cda`
- current: `0f5a22f09981257958818ea9424f55750b96556e` (merged Torque PR #192)

Each `.golden` file has a provenance comment followed by the actual response
body. Both binaries ran against new SQLite databases, isolated HOME/XDG/data
paths, on automatically selected unused `127.0.0.1` ports. Scheduler and stuck
watcher were disabled; tasks were explicitly manual. Processes were terminated
and their temporary data directories removed after capture.

Capture setup used `PUT /api/v1/settings/features.projects` with
`{"value":"true"}`, then created `golden-project` with a temporary existing
`repo_path`. Four tasks were POSTed with title `golden-match-0` through
`golden-match-3`, description `fixture description N`, priority `N+1`,
manual=true, tags `["golden","team"]`, and metadata
`{"owner":"fixture","nested":{"flag":true},"count":N}`. The first three
belonged to the project; the last had null project_id. The exact GET paths
appear in each header. Current captures include responses both with and without
include_total; legacy list already includes total and legacy search has none.

Committed tests only replay these bodies from fake HTTP servers. Regeneration
requires explicit disposable builds; tests never start Torque or use live data.
