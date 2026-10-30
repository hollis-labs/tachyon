# Approvals secret store (A6)

The secure dialog posts a secret value once and gets a reference back; resolve accepts only references. Design: Tesseract `design_tachyon_approvals_backend_20261030` section 10.

## Endpoint

`POST /api/plugins/console/v1/approval-inbox/secrets`, same-origin, browser session with the `secret_submit` level from the assurance table (`internal/approvals/assurance`, today operator-token). Body is JSON `{item_id, field, value}` (value up to 16 KiB) or multipart `item_id`, `field`, `file` (up to 64 KiB). `Idempotency-Key` (up to 128 characters) replays the first answer for the same session, key and value.

201 `{secret_ref, created_at, expires_at, size, fingerprint}` where `secret_ref` is `secret://tachyon/<32 hex>` and `fingerprint` is `sha256:` plus 8 hex characters. The value is never returned, logged, put in the record, digest or events.

| Status | code | When |
|---|---|---|
| 400 | `invalid` | malformed body, empty value, bad item id, unsupported content type |
| 403 | `assurance_insufficient` | session below the `secret_submit` level (same copy as a medium resolve) |
| 404 | `not_found` | no such item |
| 409 | `item_not_pending` | item finished or expired |
| 409 | `secret_field_unknown` | the request has no secret field with that id |
| 409 | `idempotency_key_reuse` | same key, different value |
| 409 | `secret_limit` | 20 live secrets already on the item |
| 413 | `too_large` | value or file over the limit |
| 429 | `rate_limited` | more than 10 submissions a minute per session |
| 503 | `secrets_unavailable` | no usable key (see below) |

## Resolve

A card form answer for a secret field must be `{"secret_ref": "<ref>"}`. The reference must exist, belong to this item and field, be unexpired and unused, otherwise 422 `secret_ref_invalid` and nothing is recorded (503 `secrets_unavailable` if the store has no key). After the response is recorded the references are marked consumed. A free-typed string is still 400 from the card validator.

## Storage

Migration `034_approval_secrets.sql`. AES-256-GCM with a random 96-bit nonce per row; the row id, item and field are the additional authenticated data, so a ciphertext cannot be moved to another item or field. Key from `TACHYON_SECRET_KEY_FILE`: 32 raw bytes or 64 hex characters, owner-only permissions expected (not checked). Without a usable key the endpoint answers 503 and the store reports why only as a reason string with no key material. Idempotency compares an HMAC of the value, not the value.

Expiry is the item's expiry plus 24 hours (30 days when the item has none). The inbox maintenance loop (every 30 s) purges expired rows and unconsumed rows of finished items once they are five minutes old; consumed rows stay until they expire so a Tachyon-run consumer can still redeem them.

The audit trail gets a `secret_submitted` event with the field, reference and size only.

## Redemption (G4)

`Secrets.Redeem(ctx, itemID, field, ref)` is a Go method for consumers Tachyon runs itself (card effects, action-run environment declared in the digest-bound request). There is no route that returns a value and no agent-facing verb. No consumer calls it yet; wiring one is a separate task.

## Not in this change

Step-up on the 403 (the assurance interface stays the swap point), deletion on deny or withdraw at the moment it happens (the purge handles it within minutes), a redeeming consumer, and any hardening beyond the contract (no key-file permission check, no key rotation).
