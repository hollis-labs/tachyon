# Approvals: running it, curl matrix, security notes (A9)

Shapes are in `internal/approvalsapi/openapi.yaml` and `docs/approvals-contract.md`; per-slice detail is in
`approvals-index.md`, `approvals-batch.md`, `approvals-defer-history.md`, `approvals-secrets.md` and
`approvals-assurance.md`. This page is the operator's view. Design: `tesseract:01M65BWBZAPZ63M64SSCZ7YW10`.

## What runs

| Part | Notes |
|---|---|
| Queue, resolve, present | `approval-inbox` prefix, cookie `tachyon_approvals`, same-origin, no `Authorization` header |
| Index, filters, search, ordering, expiry sweeper (A1) | derived `approval_item_index`; `Reindex` repairs it |
| Batch resolve (A2) | up to 25 items, not a transaction, idempotent by `batch_id` |
| Gate and destructive requests (A3a, A3b) | server-derived confirm phrase, `approvable` flag, digest-covered `affected_items`/`destructive`/`alternatives` |
| Defer, decision log, history, audit (A4) | overlay only; never extends expiry |
| SSE, `/access`, assurance Provider (A5) | `GET /events` with replayable ids, polling `/inbox` stays as fallback |
| Secret store (A6) | needs `TACHYON_SECRET_KEY_FILE`; without it `POST /secrets` is 503 `secrets_unavailable` |
| Evidence and `run_state` (A7) | `evidence[]` is digest-covered (max 16); `run_state` is joined from action runs and card receipts and exposes the state only |

Stubs: `TACHYON_APPROVALS_STUBS=1` serves fixtures for the routes that are not real.

## Curl matrix

Browser session only. Writes need a same-origin `Origin`; use the cookie jar (`-b`). Levels come from the assurance table
(`internal/approvals/assurance`): batch, medium resolve and secret submit need operator access today.

```sh
B=https://tachyon.example/api/plugins/console/v1/approval-inbox
O='Origin: https://tachyon.example'

curl -s -b jar "$B/inbox?limit=20"                               # queue with projection fields
curl -s -b jar "$B/items/$ID"                                    # detail: evidence, run_state, confirm, approvable
curl -s -b jar "$B/items/$ID/audit"                              # decision events for one item
curl -s -b jar "$B/history?status=denied&q=deploy"               # decision timeline
curl -s -b jar "$B/access"                                       # none|elevated, what each action needs
curl -sN -b jar "$B/events"                                      # SSE; add -H 'Last-Event-ID: 41' to resume
curl -s -b jar -H "$O" -X POST "$B/items/$ID/present"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' -X POST "$B/items/$ID/resolve" \
  -d '{"expected_revision":3,"presented_projection_revision":3,"response":{"kind":"approval","decision":"approved"}}'
curl -s -b jar -H "$O" -H 'Content-Type: application/json' -X POST "$B/items/$ID/defer" -d '{"expected_revision":3,"duration_s":3600}'
curl -s -b jar -H "$O" -X DELETE "$B/items/$ID/defer"
curl -s -b jar -H "$O" -H 'Content-Type: application/json' -X POST "$B/batch/resolve" \
  -d '{"batch_id":"b-1","decision":"approved","items":[{"item_id":"a","expected_revision":2,"presented_projection_revision":2}]}'
curl -s -b jar -H "$O" -H 'Idempotency-Key: k1' -H 'Content-Type: application/json' -X POST "$B/secrets" \
  -d '{"item_id":"a","field":"token","value":"..."}'             # returns secret_ref, never the value
```

Expected refusals: 403 `assurance_insufficient` (elevate first), 403 `provider_handoff_required` (class high or Cerberus source:
decide at the provider), 409 `stale_revision` / `conflict` (someone else decided), 409 `batch_id_reuse`, 422 `confirmation_required`
(destructive item without the exact phrase), 422 `secret_ref_invalid`.

## Security notes

- The queue and every helper table are additive Tachyon state; the hitl record stays the source of truth and the index can be rebuilt.
- Batch is N independent resolves through the single-item path. Nothing a batch does is weaker than the sum of its parts.
- `op_digest` covers kind, data, effects, `affected_items`, `destructive`, `alternatives` and `evidence`, so what the operator saw is what
  runs. Requests without the optional members keep their old digest.
- Secrets are stored AES-256-GCM, bound to item and field, never returned, logged, digested or put in events. The only consumers are
  Tachyon-run effects; no API returns plaintext.
- Actor identity is a hashed browser-session ref ("You" or "Operator session"); history cannot name a person.
- Known limits: batch is not atomic; reversibility is an effect-op whitelist; defer is an overlay and the item can still expire.
- Hardening beyond the above (step-up, audit expansion, rate limits) is deferred to CW-20261030-0082.
