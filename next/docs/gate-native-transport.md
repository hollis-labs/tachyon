# Native gate transport v1

The experimental native gate profile extends the existing approval inbox and is
disabled by default. The existing HITL Store and first-terminal-wins CAS own
every decision. The profile is
`gate_native_v1`, exact advertised option ID in `response.decision`, Once with
max_uses=1 and scope.target=operation_id only. `gate_request` is a JSON string
containing serialized mesh/core/gate.Request. It is retained without adding old
card title/class fields or coercing native string/number IDs.

## Producer to Tachyon

The existing `/api/agent/v1/data/providers/tachyon/verbs/` routes authenticate
Tachyon-issued service keys or derived service tokens. Current access mapping,
key scope `tachyon.<verb>`, and configured producer identity must all agree on
every call. Caller application namespace is pinned independently. No Respond.

| Verb | HTTP | Input | data in success wrapper |
|---|---|---|---|
| gate.enqueue | POST | hitl.EnqueueRequest | hitl.Handle |
| gate.get | GET input query | hitl.GetCommand | hitl.RetrievalResult |
| gate.await | GET input query | hitl.AwaitCommand | hitl.RetrievalResult |
| gate.withdraw | POST | hitl.WithdrawCommand | hitl.Outcome |
| gate.assess-response | GET input query, read only | AssessResponseCommand below | ResponseAssessment below |

`gate.assess-response` is **GET only**, with the distinct explicit
`tachyon.gate.assess-response` scope and mapped verb grant for the pinned
producer. Like the other GET verbs, it accepts one URL-encoded `input` query
parameter, no body and no other query parameters. POST is refused with 405.

Success is `{version:1,provider:"tachyon",verb:<exact>,data:<typed value>}`.
The host validates all wrapper discriminants. Get/Await retain computed expiry;
a wait deadline does not withdraw. Only gate-family errors use exact flat
hitl.EncodeError/DecodeError: contract_version, code, message and all typed
revision/conflict/outcome fields. 409 stale/idempotency/terminal; 404 scoped
notfound; 403 unauthorized; 400 validation. Unavailable evidence is 503.
Old verb errors are unchanged. Old grant presets gain no gate permissions.

The exact assessment input Go type is the scoped native get command plus the
expected terminal revision:

```go
type AssessResponseCommand struct {
    hitl.GetCommand
    ExpectedTerminalRevision int64 `json:"expected_terminal_revision"`
}
```

Its JSON before URL encoding is:

```json
{"contract_version":"1.0","item_id":"<item-id>","caller":{"application_id":"<pinned-namespace>"},"expected_terminal_revision":2}
```

The version is `hitl.ContractVersion`. There are no assessment input members
`request_digest`, `operation_digest` or `option_id`; extra decision facts are
refused. The native caller assertion does not establish responder authority.
The item must be this profile in the pinned namespace, resolved at that exact
terminal revision. Exact option and participant come from the immutable actual
`hitl.Resolved` outcome; request/operation digests, native ID and binding come
from the retained native snapshot. Current scopes and disabled state are read
through `identity.Service.User(ctx, id)`, never from a JWT scope snapshot.
Operator/resolve grants, producer mapping and expiries are checked anew.
Expired, missing or unavailable evidence refuses.

ResponseAssessment:
`{contract_version:"1",owner_ref,item_id,idempotency_key,request_digest,operation_digest,binding,native_request_id,option_id,terminal_revision,participant,assessed_at,expires_at}`.
The observation and callback proposal/authorization use `gate.ContractVersion`
(`"1"`); the GET input uses `hitl.ContractVersion` (`"1.0"`). These are separate
version boundaries, not a shared or normalized discriminator. Wrong versions
are refused at their receiving boundary.
Expiry is the minimum of assessment time +5 seconds, request and option expiry.
Assessment reads the retained Store record directly, without materializing expiry.
This is a point-in-time observation, not a transferable grant, cached approval,
or zero-lag distributed revocation. It neither writes a decision nor admits an
effect. Hosts compare every binding and perform their own live admission.

## Tachyon to Gatecrasher

POST `/api/gate/v1/authorize-response` accepts a separately Gatecrasher-issued
**direct API key only**, mapped/pinned there to this Tachyon workload with scope
`gatecrasher.gate.authorize-response`. Gatecrasher re-verifies it with its own
identity verifier: verified service kind, audience `gatecrasher`, required scope,
the configured pinned `key:<credential-ID>` subject, and direct-key
`TokenID == <credential-ID>` correspondence must all agree. Direct-key
verification reloads the active credential on every call. Derived JWTs are
refused even when they remain valid until expiry after parent-key revocation.
Tachyon sends the direct key as the bearer without a JWT exchange or fallback.
No credential is supplied by a browser. The opposite producer direction keeps
its separate Tachyon-issued mapped credential and existing supported semantics.

ProposedResponse is flat:
`{contract_version:"1",owner_ref,idempotency_key,item_id,request_digest,operation_digest,binding,native_request_id,expected_revision,presented_projection_revision,option_id,participant,actor_scopes,assessed_at,expires_at}`.
Participant is server derived; actor_scopes are current owner-checked operator
and resolve scopes. Native ID and binding are the immutable gate.Request values.
The two revisions equal the reviewed owner revision. TTL is bounded as above.
The response is the exact proposal echoed, plus `authorized:true`; mismatch,
expiry, denial, unavailable callback or missing fields refuses. It is an
assessment for the immediate existing owner CAS, not a decision/grant. Roles
and producer mapping are checked again after callback before CAS.
The echoed expiry bounds writer acquisition, retries and the authoritative CAS.
The original queued-writer case still rolls back without a terminal outcome.
A COMMIT acknowledgement can instead arrive late or be lost: SQLite's pinned
COMMIT uses a background context, and even context-aware COMMIT can return a
cancellation error after durable persistence. The owner does not claim physical
rollback at that boundary.

Native resolutions therefore use private `native_commit` metadata inside the
same `approval_items.record` envelope. Phase one atomically stores the sole
immutable HITL choice plus an unconfirmed descriptor: protocol, random attempt,
canonical record hash and the exact authorized proposal. No terminal event or
usable response is published. Only that committing process may capture a witness
when phase one returns successfully, the caller remains valid, and both the wall
authorization expiry and the live monotonic budget still permit it. Phase two
CAS persists that historical witness against the exact phase-one bytes, revision
and attempt, without changing the immutable choice or its revision.
Phase two may complete later; it does not extend the authorization or attest that
its own COMMIT was on time. No subsequent read invents a lost observation.

Every native resolved-record read validates the full proposal/record binding and
on-time witness. Missing, late, malformed or mismatched evidence returns unavailable
without an outcome, including Get/Await, assessment, enqueue replay and conflicts.
Simultaneous native browser submissions queue locally through both phases, then
reread authority, preserving the ordinary confirmed winner/stale409 behavior.
This transient scheduling queue stores no decision or approval and does not
make an unconfirmed response usable to any reader.
Browser lists exclude usable quarantined choices and report `native_unavailable`
identifiers for reconciliation; history, facets, backfill and events cannot turn
unconfirmed bytes into authorization. Index recovery checks confirmation at the
same interaction revision and advances raw cursors across quarantines, so a
transient B-only index failure can be repaired without changing the choice or
witness. Unrelated old cards remain accessible.
The raw late resolution may remain for diagnosis. It is not approval evidence.

A crash after phase one but before durable confirmation deliberately sacrifices
that response's availability, even if the physical choice committed on time.
After restart, absent evidence remains unavailable; there is no reconstruction,
choice retry or grandfathering of older native resolutions. If phase-two
acknowledgement is uncertain, only an already persisted exact witness can establish
the earlier on-time observation. Current identity, mapping, request/option expiry
and host admission checks still apply; this receipt is not a transferable grant.

Paired migration `00005_native_commit_confirmation.sql` records the reader protocol
in the migration ledger without altering the baseline table schema needed by
`migrate-from`. Pinned Substrate database startup rejects unknown applied migration
versions in both auto and verify modes. Upgrade requires an exclusive stop and
drain of **all** owner processes and offline tools: that startup fence does not
stop an already-running old process. Mixed versions are unsupported. Rollback
requires restoring a pre-upgrade backup, never deleting the ledger version or
stripping evidence. Restoring a backup can discard later writes; operators must
plan recovery and restore the required application data consistently. Migration
and credential provisioning are operator-controlled deployment steps.

Browser native choice has only kind and exact decision ID. No old alternatives,
notes, card payloads, snooze, batch, undo or automatic fallback applies. A stale
choice is refreshed for review, never retried onto a newer projection.

## Configuration

`tachyon.native_gate.enabled` defaults to false. Enabling requires explicit
`owner_ref`, `producer_identity` (Tachyon's mapped service identity ID),
`caller_scope`, `callback_url` (the exact HTTPS callback endpoint) and
`callback_token` (a secrets reference to the separately Gatecrasher-issued
direct API key only). `allow_loopback_http` is false unless explicitly enabled
for an isolated loopback test. The callback resolver reads its secret on each
call, refuses redirects, and limits both timeout and response size. No existing
grant preset, old card profile, or default outbound loop is widened.

A disabled configuration example, using placeholders rather than credentials:

```yaml
secrets:
  env: true
tachyon:
  native_gate:
    enabled: false
    owner_ref: "<stable-owner-reference>"
    producer_identity: "<mapped-service-identity-id>"
    caller_scope: "<producer-application-id>"
    callback_url: "https://gatecrasher.example/api/gate/v1/authorize-response"
    callback_token: "env://TACHYON_GATE_CALLBACK_KEY"
    allow_loopback_http: false
```

Place these overrides in the file selected by `SUBSTRATE_CONFIG`. The
`secrets.env` opt-in permits the illustrated environment reference; it does not
contain the key itself. Supply the separately provisioned direct key through
that reference, or use another supported secrets backend.

The integration requires a compatible Gatecrasher host and separately provisioned
credentials in both directions. Validate the paired host and intended native
provider runtime before enabling it. The callback authorization and fresh
assessment remain observations; admission and Once enforcement belong to the
host. Existing card profiles and grant defaults are unchanged.
