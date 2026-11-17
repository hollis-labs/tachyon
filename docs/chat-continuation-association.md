# Exact chat continuation association

The supported operator API is `POST /api/plugins/console/v1/chat/continuations`.
It is for a Tachyon Restart completion or an Agent Ops caller that has already
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
Its operation result reports `continuity_mode: "checkpoint"`. The established
plan carry still occurs when the replacement first uses the pinned proxy; this
legacy path has no immediate history/plan guarantee. Its generic response is
not a Codex worker native-admission receipt, and the native association API
rejects it. Eager hub association and history remain open work on 0084.

After success, the replacement's inherited plan and messages, when allowed by
the source's Continue choice and clear marker, are available immediately through
the ordinary thread GET routes before any replacement agent tool call. Agent Ops
must use this API after native readiness; it must not
relink a production database directly. A refused or uncertain native launch
does not authorize a fresh relaunch or a chat association.
