# Messaging: backend needs (M1)

The GUI codes to `MessagingClient` (`types.ts`). The mock (`mock.ts`) backs dev and tests. The
mail adapter (`adapter.ts`, `createMailMessagingClient`) already maps onto the existing
`/api/v1/messages` routes through `ApiClient`; the items below are what is missing or unconfirmed
for it to be correct and complete.

## Routes the adapter uses today

| Client method | Route | Notes |
|---|---|---|
| `send` | `POST /api/v1/messages` | Body is `SendMessageRequest`. Intent maps to `kind`: message and nudge -> `notice`, update_request -> `request`. |
| `getDelivery` | `GET /api/v1/messages/{id}` + `GET /api/v1/messages/thread/{thread_id}` | Live client also reads the Tether delivery trace; polls every 5s. |
| `recentThread` | `GET /api/v1/messages/thread/{thread_id}` | Only by thread id. |

## Needed

1. **Per-recipient history.** `recentThread({with})` has no route: threads are fetched by id only,
   and many envelopes have an empty `thread_id`. Need `GET /messages?peer=<urn>&limit=` (messages
   between the operator and a peer, both directions, oldest/newest ordering stated).
2. **Delivery evidence (CW-20261020-0049).** Live delivery polling now supplements envelope
   timestamps with `GET /api/plugins/console/v1/tether/messages/{id}/trace`. Host acceptance
   alone stays queued; turn submission confirms delivery, consumption confirms acknowledgment.
   Replies require an explicit `in_reply_to` from the recipient; cross-thread replies remain a gap.
3. **Shared status polling (CW-20261020-0049).** A bounded 5s metadata event poll updates visible
   mail badges by exact message id. This is recent evidence, not complete history; older messages
   still depend on envelope timestamps or their individual trace. No new SSE connection is used.
4. **Recipient resolution.** `resolveRecipient` only parses the URN today. Need
   `GET /messages/recipients/resolve?ref=` (agent slug or name -> URN, kind, label, broadcast flag)
   so an agent without a registered URN is reported unresolvable server-side too, and group
   membership size is available for the broadcast confirmation text.
5. **Operator identity.** `from` defaults to `msg://user/local/operator`. Need the signed-in
   user's URN (from the auth workstream) so sends are attributed and replies route back.
6. **Server-side guard rails.** The GUI rate-limits nudges (one per agent per 2 minutes, in
   memory) and requires a confirmed broadcast. These must be enforced again server-side:
   nudge rate limit keyed by (operator, recipient), broadcast confirmation flag, body size cap
   (GUI caps 4000 characters).
7. **Metadata.** Sends carry `console.intent`, `console.template`, `console.expected_response` in
   `metadata`. Confirm the store keeps arbitrary string metadata and returns it on read; the
   recipient agent should be told about `expected_response` (render it in the inbox projection).
8. **Idempotency.** A retry after a timeout can double-send. Accept a client `Idempotency-Key`
   (the GUI can supply `console-<uuid>`).
9. **Unread counts (Q1).** The Agents tab MailBadge falls back to `MessagingClient.unreadCounts(addresses)`
   when the fleet feed has no `unread_messages`. `GET /messages/inbox` is destructive (marks delivered),
   so the live adapter does not implement it and the badge stays "not reported". Need a non-destructive
   batch read, e.g. `GET /messages/unread?to=<urn>&to=<urn>` returning `{ "<urn>": n }`. The GUI polls it
   once per 30s for all visible agents, only while the Agents tab is open and the Console is enabled. The
   address it asks for is `msg://agent/<provider>/<fleet agent id>`; confirm this matches registered URNs.
10. **Bare-slug recipients (Q1).** Alert reply resolves the event's sender (`agent-alpha`, a fleet slug) via
    `resolveRecipient`. The mock accepts any slug; the mail adapter only accepts full `msg://` URNs, so in
    `?console=live` an A2 reply to a slug-only sender says "No messageable sender". Need an agent-registry
    lookup (slug -> URN), the same one item 4 asks for.

## Open questions

- Should nudge be a distinct `kind` (e.g. `nudge`) rather than `notice` + metadata, so agents can
  prioritise it? `MessageKind` is a closed list in `lib/types.ts` today.
- Do agents without a live session still receive mail (queued), and should the composer say so?
- Does `read` mean the agent consumed it (`consumed_at`) or a human marked it read?
