# Chat cards (T5a)

Answerable cards in a chat thread. The queue item (Approvals) stays the only record of the decision; this adds the thread binding.

- `card.ask` takes optional `thread_ref` (and `message_ref`). The card is bound in `chat_card_refs` (migration 037), appears as a `card` note
  in app threads, and a `card` stream event `{item_id, kind, state}` is published. A thread that does not exist fails the ask.
- `GET chat/threads/{ref}/cards?state=&since=` returns the chatapi Card projection (caption from the title, fields from `data.fields`
  or `data.options`, actions per kind, target from the first effect, `state` pending/resolved/expired/superseded, `result` once answered).
  Mounted explicitly behind the operator guard; `TACHYON_CHAT_STUBS=1` leaves it to the stub.
- Answers still go through the Approvals resolve route. When a bound card is resolved, the thread gets a `card` event and, in an app thread,
  an operator message to the agent ("Card answered: ... Decision: ... Answers: ..."). Expiry publishes the event only.
- The thread list's `blocked_on_user` counts bound cards still open (`Aggregator.CardBlocks`).
- Kinds `card.plan` (approved | changes_requested) and `card.dispatch` (approved | denied). Both are low class unless they carry effects.
- cardfx `create`: effect `{target:"torque:task/new", op:"create", value:<title or JSON {title, description, project_id}>}` opens a Torque task
  when the card is approved. Same switch as status changes: off unless `TACHYON_CARD_EFFECTS_TORQUE` is set. The receipt detail carries the new id.
- Approval rules (T5c): `chat_approval_rules` (same migration). `GET chat/threads/{ref}/approval-rules` lists rules in force,
  `DELETE .../{id}` revokes one. `Store.AddRule` takes the class of the approval just answered and refuses anything but low;
  `Store.Allows(thread, agent, tool, class, args)` is the matcher (tool and agent equal, every `match` key equal to the call's argument,
  never for medium or high). Rules only allow. Nothing calls them yet: the runtime `approval_request` translation that creates and
  consults them waits on the Nanite chatstream/v1 contract, which is not verified.
