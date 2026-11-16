# Changelog

## Unreleased

### Added

- Experimental, default-off `gate_native_v1` approvals in the existing inbox:
  exact advertised choices scoped Once to a single operation.
- Explicit mapped-service grants for `gate.enqueue`, `gate.get`, `gate.await`,
  `gate.withdraw` and the read-only `gate.assess-response` API. Human responses
  remain owner controlled; no producer Respond verb is exposed.
- A separately credentialed Gatecrasher authorization callback using a direct
  API key, with fresh current-user authority and producer-mapping checks.
- Private same-record commit confirmation. Missing, late or uncertain evidence
  remains quarantined across reads, replay, history, events, copy and restart.
- Migration `00005_native_commit_confirmation.sql` for the reader-protocol
  ledger fence, preserving the baseline table/column schema.

### Fixed

- Bound native response authorization through writer contention and the actual
  commit observation, without claiming physical rollback after an uncertain
  commit acknowledgement.
- Recover confirmed native index projections at the same interaction revision
  after a transient derived-index failure. Recovery preserves the immutable
  choice, witness, revision and native identifiers, and progresses past
  quarantined rows without hiding unrelated legacy items.

### Compatibility and operational limits

- The native profile is experimental and disabled by default. It requires a
  compatible Gatecrasher host and independently validated provider runtime.
  Existing card profiles, default grants and ordinary error envelopes remain
  unchanged.
- Commit uncertainty or a crash between decision persistence and confirmation
  can permanently lose availability for that response. Confirmation is not a
  second decision store, a transferable grant or a cached admission approval.
- Upgrades require a pre-upgrade backup and an exclusive stop/drain of every
  owner process and offline tool. Mixed versions are unsupported. Older
  executables refuse unknown applied migration versions at startup; already
  running old processes are not fenced by that check.
- Rollback requires an explicit, consistent pre-upgrade backup restore, with its
  possible loss of later writes. Deleting ledger entries, stripping evidence,
  reconstructing a witness or grandfathering old native outcomes is unsupported.

See the [module README](README.md) and
[native transport contract](docs/gate-native-transport.md) before activation.
