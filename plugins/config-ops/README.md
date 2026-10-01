# Config Ops

Schema-backed, non-secret plugin settings. The plugin is discovered from its
manifest and executable. Before each `config_*` invocation, Tachyon sends the
settings schemas of all loaded plugins through the internal `config_schemas`
command. Target IDs are runtime plugin IDs, including plugins with no fields.
Schemas are declarations; configuration of portfolio apps is not supported
until an adapter exposes their schemas and values.

| Verb | Payload | Result |
| --- | --- | --- |
| `config_list` | none | Loaded plugin targets and settings schemas |
| `config_schema` | `plugin` | One target and its schema |
| `config_get` | `plugin` | Effective values and validation status |
| `config_set` | `plugin`, `values` object | Persisted config, `plugin`, `restart_required: true` |
| `config_reset` | `plugin` | Defaults, validation status, `plugin`, `restart_required: true` |

Invoke through `POST /api/verb/<verb>`. Set is a patch merged with existing
overrides and declared defaults. Unknown targets/keys, wrong types, disallowed
select options, null values, and missing required values are rejected. Required
strings must be nonblank; false and zero are valid. There is no secret field
type: do not store credentials here. Only declared scalar fields are accepted;
rejected values are never included in validation errors or logs.

Config-ops alone writes `<DataDir>/settings.json`, using a mode 0600 temp file,
file sync, and atomic rename. Invalid writes leave the existing file and
in-memory values intact. Corrupt existing storage fails initialization and is
not overwritten. Reset removes overrides; required fields without defaults
then appear in the returned validation errors until the operator supplies them.
Schema changes preserve stored overrides until reset.

The host only reads the values file when spawning a plugin. It passes defaults
and persisted values from the authored `capabilities.json` settings fields in
`InitParams.Config`; unknown stored keys are not forwarded. Values apply on
the next plugin spawn, with no live `config/changed` RPC. Every set/reset returns
`restart_required: true`. Restart a loaded plugin with `POST /api/plugins/<id>/restart` after saving.
The endpoint returns `{id, status: "loaded"}` on success, or
`{id, status: "unloaded", error}` on failure. A failed respawn removes its
module/nav registration; the host logs the failure. Other plugins keep running.
Restart waits for an in-flight serial command to finish. The respawn uses the
manager lifetime context, not the HTTP request context. This plugin does not
restart processes or services itself.

`TACHYON_DATA_DIR` overrides the host's plugin data root. Otherwise it uses
`$XDG_DATA_HOME/tachyon/plugins`, or `~/.local/share/tachyon/plugins` when no
absolute XDG data path is set. Each plugin receives its own `<root>/<plugin-id>`
DataDir. Config-ops requires that explicit directory; it has no disk fallback.
Tests and HTTP smoke checks use temporary directories only.

Navigation declarations are included; the React Settings page remains separate
work. `config_update`, `config_read`, and `config_validate` from the initial
handoff are superseded by the reviewed schema/get/list/set/reset vocabulary.
