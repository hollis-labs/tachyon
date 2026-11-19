# Exact chat continuation association

The supported operator API is `POST /api/plugins/console/v1/chat/continuations`.
It is for an Agent Ops caller that has already
obtained a replacement session from Tether. The caller sends the exact source
and replacement session UUIDs, the logical agent ID, and the deterministic key
`continuation:<source UUID>:<replacement UUID>`. The route uses Tachyon's
explicit operator authorization; an unrelated bearer does not authorize it.

```json
{
  "source_session_id": "11111111-1111-4111-8111-111111111111",
  "new_session_id": "22222222-2222-4222-8222-222222222222",
  "logical_agent_id": "agent-id",
  "idempotency_key": "continuation:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222"
}
```

The successful response repeats the exact pair, agent and key and includes
`replayed` and `plan_moved` booleans. A retry of the same verified pair reports
`replayed: true`; it cannot move a plan or increment its revision again. The
replacement's parent and agent are immutable, while that replacement may be
the source of a later continuation. A pair conflict returns 409.

The server verifies both Tether session rows, the exact parent, same agent,
and a server-owned successful **native admission** receipt before committing
the association. A client assertion, generic `intent=resume`, or `state=running`
is insufficient. Missing proof returns `native_proof_unavailable` or
`native_continuation_unverified`, and no plan or history link is committed.
The receipt field is being added to the Tether native continuation contract;
the verifier rejects older Tether rows with no receipt. The field shape remains
subject to final exact-head contract confirmation before activation.

Worker UI Restart with preserved context is **not delivered** by this slice:
Tachyon lacks the exact source generation, current policy digest and independent
catalog authority required by Tether's worker native-only resume. It refuses
before stopping or creating anything, with no fresh fallback and no grant
widening. An explicitly chosen clean start remains a separate Tether launch.
The existing hub logical-agent resume call is retained when Tether accepts it.
Its operation result reports `continuity_mode: "checkpoint"`. A typed Tether
checkpoint-resume refusal retains the legacy hub lineage relaunch with
`continuity=relaunch` and reports `continuity_mode: "lineage"`; other resume
failures do not trigger a fresh relaunch.

Hub Restart now verifies the exact source/replacement IDs, matching logical
agent, terminal source, running replacement, explicit resume parent, and hub
placement on both rows before committing local plan/history association. This
internal completion path proves UI lineage, not native model context; the
public native association API still rejects hub rows without native proof.
Restart reports `done` only after association commits. If association fails,
the control reports `failed` at `associating` and retains the replacement ID.
Retry the same Restart request ID to retry only association of that exact pair;
no second stop, resume or relaunch occurs. Missing association configuration
refuses Restart before stopping the source.

After success, the replacement's inherited plan and messages, when allowed by
the source's Continue choice and clear marker, are available immediately through
the ordinary thread GET routes before any replacement agent tool call. Agent Ops
must use this API after native readiness; it must not
relink a production database directly. A refused or uncertain native launch
does not authorize a fresh relaunch or a chat association.

## Restarts outside the UI

With the existing Tether control client configured, ordinary thread, history and
plan GETs reconcile a running replacement before returning its contents. The
thread list discovers candidates through Tether's canonical paginated sessions
API; it does not wait for agent output or a proxied tool call. Only explicit
`intent=resume` rows are candidates. A fresh spawned child may have a parent, but
that parent is not a continuation.

The reconciler verifies every missing edge, up to 16 hops, before committing the
whole chain in one SQLite transaction. It requires exact session IDs, explicit
parents and the same logical agent throughout. Ordinary hub edges require a
terminal source. Retained-team recovery additionally supports orphaned sources
through Tether's committed `replacement_lineage` receipt, whose actor and agent
are bound by Tether to both stored plans. Worker edges require either the native
admission receipt or the separate `worker_lineage` receipt from an authenticated
successful launch acknowledgement. A running worker row alone is insufficient.

A one-pair receipt does not prove that older ancestors were observed. Only a
completed chain reconciliation creates the separate local completion marker.
For example, if an unseen A-to-B restart is followed by a recorded B-to-C UI
restart, the first read repairs A-to-B and carries through the existing B-to-C
pair. Public one-pair retries retain their usual no-op behavior.

The transaction preserves Continue choices, clear markers, existing destination
plans and immutable association pairs. Concurrent reads and retries cannot move
a plan twice. Original history and reply IDs remain unchanged. Missing or
unavailable proof returns 409 or 503 instead of a successful empty replacement;
a later read retries. Already associated and historical threads remain readable.

These receipts establish UI history lineage only. They do not grant native
context, custody or execution authority, and never bypass the native receipt
required by the public `POST /continuations` route, including on replay.
