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

Tachyon is a local operator tool. **Its HTTP API has no authentication and no TLS.** Anyone who can reach the listener can list, create, edit and delete agents, grant tools and skills, attach MCP servers and create reflexes, all as whatever identity the backing provider (Nanite) extends to Tachyon.

The default listen address is `:8093`, which binds every interface. Set `TACHYON_ADDR=127.0.0.1:8093` to restrict it to the local machine, and do so unless you have placed the listener behind something that provides the missing boundary: a VPN, an SSH tunnel, or an authenticating, TLS-terminating reverse proxy. Do not expose the port to an untrusted network.

## Plugins

Plugins are local subprocesses spawned by path, running with the same privileges as Tachyon. They are not sandboxed and the host does not verify signatures or digests. Run only plugin binaries you built or trust; the `agent-ops` and `hello` plugins ship in this repository and are built by `make build-plugins`.

The `agent-ops` plugin forwards requests to a Nanite API, by default `http://localhost:8090` over plaintext HTTP. Keep that hop on loopback or a trusted network.

## Data at rest

Tachyon keeps no database of its own and stores no credentials; agent data lives in the backing provider. The server logs JSON lines to stdout, which can include request and plugin error detail, so protect those logs as you would the provider's.

## Current security limitations

- no authentication or authorization on the HTTP API
- no built-in TLS
- default listener binds all interfaces
- plugins are unsandboxed and unverified
- pre-1.0 contracts

These are deployment constraints, not hidden roadmap promises. Operate within them or place Tachyon behind controls that provide the missing boundary.
