# Security policy

## Supported versions

Tachyon is pre-1.0 software with no tagged releases. Security fixes are made on `main`. There are no backports.

## Report a vulnerability

Do not include an exploit, token, credential or other sensitive material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's Security tab offers it. If it is unavailable, contact a repository maintainer privately through a contact channel published on the Hollis Labs organization or maintainer profile. Include:

- the affected commit and operating system
- how the server was started, and whether the listener was reachable beyond the local machine
- reproduction steps and the security impact
- whether credentials or data may have been exposed
- a safe way to contact you about coordination

Public, non-sensitive hardening suggestions can go in an ordinary issue. Maintainers will acknowledge a private report, investigate it, and coordinate disclosure; response times are best effort.

## Deployment boundary

Tachyon is a local operator tool. **Its HTTP API has no authentication and no TLS.** Anyone who can reach the listener can invoke exposed operations as the backing providers permit: agent/grant/reflex writes and deletes, Torque work mutations, Tether launch/session actions, local settings changes, and plugin restart. Skill/MCP catalogs and durable agents are read-only; that does not make the rest of the API read-only. Effect declarations are descriptive and do not authenticate callers or enforce approval.

The default listen address is `:8093`, which binds every interface. Set `TACHYON_ADDR=127.0.0.1:8093` to restrict it to the local machine, and do so unless you have placed the listener behind something that provides the missing boundary: a VPN, an SSH tunnel, or an authenticating, TLS-terminating reverse proxy. Do not expose the port to an untrusted network.

The reference deployment is an OS service supervised by systemd, with a loopback listener set through `TACHYON_ADDR` and an HTTPS reverse proxy such as Caddy on a private interface. HTTPS termination and network reachability controls do not add authentication to Tachyon. The code default remains unchanged. See [host operations](docs/host-operations.md) for deployment and the source/build/live revision distinction.

## Plugins

Plugins are local subprocesses spawned by path, running with the same privileges as Tachyon. They are not sandboxed and the host does not verify signatures or digests. Run only plugin binaries you built or trust; eight module plugins plus the optional `hello` example ship here and are built by `make build-plugins`. Startup validates declarations before listening; that validates the contract, not binary provenance. Watchdogs isolate a broken subprocess, but killing it does not guarantee cancellation of an upstream write. No ambiguous plugin write is automatically retried.

The `agent-ops` plugin forwards requests to a Nanite API, by default `http://localhost:8090` over plaintext HTTP. Keep that hop on loopback or a trusted network.

## Data at rest

Provider-owned agent/work/session state stays upstream. Tachyon nevertheless has local state: config-ops writes plaintext declared settings to `<data root>/config-ops/settings.json`; launch-ops uses a local SQLite checkpoint store in its plugin data directory. The root is `TACHYON_DATA_DIR`, otherwise absolute `XDG_DATA_HOME/tachyon/plugins`, otherwise `$HOME/.local/share/tachyon/plugins`. Protect permissions and backups; configured values may be sensitive. There is no host credential vault or encryption guarantee.

Retired plugin metadata and bounded HITL correlations are in memory. Observe activity is ephemeral, safe metadata only: no payloads, prompts, config values, credentials or upstream error bodies. General server/plugin stdout/stderr logs can contain diagnostics; protect those logs and do not treat the feed's redaction policy as a blanket logging guarantee.

## HITL and private activity ingestion

`TACHYON_TANGENT_MCP_URL` is unrestricted operator configuration, disabled when unset or whitespace-only. Treat enabling it as an outbound connection/data disclosure choice. A whitespace-only `TACHYON_TANGENT_ITEM_BASE` likewise counts as unset and exposes no item link. Configured asks send prompt/impact information and opaque host correlation references to Tangent; options/context remain in the browser ask and are not copied into the enqueue request. An approved item-link base restricts browser links independently; the browser never supplies the MCP endpoint. Startup logs only MCP scheme/host. Read-only status exposes a safe projection, validates exact host/process correlation and rejects explicit foreign browser origins; missing Origin permits local clients and is not authentication.

Approval never resumes or replays a verb. Continuation is unavailable and its design is tracked as a follow-up; enqueue failure, correlation loss, timeout and malformed outcomes fail closed. An ambiguous enqueue may retry only the same frozen request/key, not the plugin operation. See [HITL](docs/hitl-host.md) for the bounds and safe outcome rules.

The Observe ingestion command is absent from public capabilities and blocked by all HTTP command/verb paths. Its per-process marker travels only in init stdin; it is a trusted-subprocess boundary, not a sandbox against a malicious local process with the same privileges. [Observe feed](docs/observe-host-feed.md) documents this boundary, recursion exclusion and lossy queues.

## Current security limitations

- no authentication or authorization on the HTTP API
- no built-in TLS
- default listener binds all interfaces
- plugins are unsandboxed and unverified
- pre-1.0 contracts

These are deployment constraints, not hidden roadmap promises. Operate within them or place Tachyon behind controls that provide the missing boundary.
