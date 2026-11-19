# Approvals: batch resolve (CW-20261030-0133)

`POST /api/plugins/console/v1/approval-inbox/batch/resolve` (real; the stub of the same path stands down).
Shape: `docs/approvals-contract.md` and the Architect's comment on the task. Migration `036_approval_batches.sql`
(take the next free number at merge).

Body `{batch_id, decision:"approved"|"denied", note?, items:[{item_id, expected_revision, presented_projection_revision}]}`,
1 to 25 items, each once, `batch_id` 1 to 64 of `[A-Za-z0-9._:-]`, note up to 2000 bytes, unknown fields 400. Same-origin
and a session are required like any write; `assurance.ActionBatch` (`batch_resolve`) decides the level, 403
`assurance_insufficient` when short (the table row is the swap point).

Items are decided one at a time in request order through the same `Service.Respond` as a single resolve
(revision check, terminal and expiry handling, card answer validation); a failure never stops the next.
Each item is re-read and its eligibility recomputed from the record (`approvalsapi.Eligibility` through
`Project`); the client's projection is not trusted. Per-item `status`:

| status | when |
|---|---|
| `resolved` | decided; `decision`, new `revision` |
| `ineligible` | `blockers[]` from the canonical list (not attempted); a lone `expired` blocker reports `expired`. A Cerberus-sourced item reports `risk_not_low` |
| `stale` | expected or presented revision is not the current one; `revision` is the actual, `error.code` `stale_revision` |
| `expired` | past `expires_at` or already expired |
| `conflict` | already terminal; carries the existing `decision` and `revision` |
| `error` | not found, `reason_required` (deny without a note where the item asks), or an internal failure; the item stays in the queue |

Response `{batch_id, results[], counts{status:n}, completed_at}`. Request-level errors are only 400 `invalid`,
403 `assurance_insufficient`, 409 `batch_id_reuse` and the usual origin or session refusals.

Idempotency: `approval_batches(batch_id PK, request_digest, response, created_ms)`. The digest is over the parsed
request, so whitespace does not matter. The same `batch_id` and digest replays the stored answer with
`X-Tachyon-Batch-Replay: 1` and decides nothing again; a different digest is 409. Batches run under one lock, so
a retry that arrives mid-flight waits and then replays. Rows older than seven days are pruned on the next store.
If storing the answer fails the decisions stand and only the replay is lost.

Not done (by design): no extra decision-log rows (`batch_resolved` stays unwritten), no index or projection
changes (`batchable` and `batch_blockers` on `/inbox` already exist from A1), no new hardening.
