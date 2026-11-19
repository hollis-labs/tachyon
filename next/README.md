# Tachyon on Substrate

Tachyon is a control plane for agent operations. This directory is its separate
Substrate-based Go module and web application on the `substrate-port` branch.
The repository root also contains the earlier implementation; build and run this
module from `next/` with a dedicated data directory.

## Build

Use the Go version declared in [go.mod](go.mod) and Node.js 24 with npm.
Build the web application before the Go binary so its assets are embedded:

```sh
cd next
(cd web && npm ci && npm run build)
go build -o tachyon ./cmd/tachyon
```

Set `TACHYON_DATA_DIR` to a dedicated directory, then create an owner offline
before starting the server. The following reads an operator-supplied password
from `PASSWORD`; do not place a credential value in committed configuration:

```sh
printf '%s\n' "$PASSWORD" | ./tachyon identity setup --username owner --display-name Owner
unset PASSWORD
./tachyon serve
```

See [architecture and configuration](docs/architecture.md) for the authentication
model, database configuration, secrets references and data-directory rules.

## Experimental native approvals

The `gate_native_v1` profile adds exact native choices to the existing approval
inbox. It is **experimental and disabled by default** through
`tachyon.native_gate.enabled: false`.

- Human operators review one advertised option for one operation. This profile
  supports Once scopes only; existing cards, defaults and grant presets remain
  unchanged.
- A mapped producer can enqueue, get, await and withdraw its scoped interactions.
  The separate read-only assessment observes the recorded participant's current
  authority. Producers cannot submit human responses.
- Current user roles, disabled state, producer mapping and request/option expiry
  are checked. Assessments are short-lived observations, not transferable grants
  or cached admission decisions; the host performs its own fresh admission.
- A native outcome is usable only with a durable witness of an observed on-time
  commit. Late or uncertain commits can remain physically recorded but
  quarantined. A crash before confirmation may permanently sacrifice that
  response's availability; a later read never reconstructs its witness.

Enabling requires a compatible Gatecrasher host, explicit identities/namespaces
and separately provisioned credentials in each direction. Callback authentication
uses a direct Gatecrasher API key; a derived JWT is refused. Validate the paired
host and intended provider runtime before activation. See the
[native transport and configuration contract](docs/gate-native-transport.md).

## Upgrade and rollback

Migration `00005_native_commit_confirmation.sql` advances the reader protocol,
including when the native feature remains disabled. It preserves the baseline
table/column schema; private confirmation metadata stays in the existing owner
record.

1. Back up the database and required application data before starting an upgraded
   owner. Plan the recovery window and the consequences of restoring that backup.
2. Stop and drain **all** owner processes and offline database tools before the
   migration. Mixed-version serving is unsupported.
3. Apply the configured Substrate migration workflow. Auto and verify startup
   modes reject an applied version unknown to an older executable. This restart
   fence does not protect against an old process that is still running.
4. Rollback requires restoring a pre-upgrade backup consistently. Never delete
   the migration ledger entry, strip confirmation metadata, or run an older
   binary against the upgraded database. Restoration can discard later writes;
   it is an explicit operator recovery action.

Older native outcomes without a valid witness remain unavailable. There is no
automatic grandfathering, observation reconstruction or decision retry.

## Development checks

Run checks from this module:

```sh
go vet ./...
go test -race ./...
golangci-lint run ./...
go tool substrate pins check --app .
(cd web && npm run typecheck && npm run lint && npm test && npm run build)
```

The database suite supports isolated PostgreSQL schemas through
`SUBSTRATE_TEST_POSTGRES_DSN`; set `SUBSTRATE_REQUIRE_POSTGRES=1` for that lane.
Use an isolated test database. On macOS, a canonical temporary directory avoids
credential-file symlink restrictions. With Node.js 26, the existing jsdom tests
need `NODE_OPTIONS=--no-experimental-webstorage`; CI uses Node.js 24.

## Documentation and license

- [API contract](docs/api-contract.md)
- [Native gate transport](docs/gate-native-transport.md)
- [Changelog](CHANGELOG.md)
- [MIT license](../LICENSE)

Report reproducible issues through the repository's public issue tracker. Keep
credentials, private hostnames and sensitive request payloads out of public
reports.
