# Chat cards (T5a)

Answerable cards in a chat thread. The queue item (Approvals) stays the only record of the decision; this adds the thread binding.

- `card.ask` takes optional `thread_ref` (and `message_ref`). The card is bound in `chat_card_refs` (migration 036), appears as a `card` note
  in app threads, and a `card` stream event `{item_id, kind, state}` is published. A thread that does not exist fails the ask.
- `GET chat/threads/{ref}/cards?state=&since=` returns the chatapi Card projection (caption from the title, fields from `data.fields`
  or `data.options`, actions per kind, target from the first effect, `state` pending/resolved/expired/superseded, `result` once answered).
  Mounted explicitly behind the operator guard; `TACHYON_CHAT_STUBS=1` leaves it to the stub.
- Answers still go through the Approvals resolve route. When a bound card is resolved, the thread gets a `card` event and, in an app thread,
  an operator message to the agent ("Card answered: ... Decision: ... Answers: ..."). Expiry publishes the event only.
- The thread list's `blocked_on_user` counts bound cards still open (`Aggregator.CardBlocks`).
- Not here yet: `card.plan` / `card.dispatch` kinds, the cardfx `create` op, "Always in this chat" rules, and runtime `approval_request`
  translation (waits on the Nanite chatstream contract).
