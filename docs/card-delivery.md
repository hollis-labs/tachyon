# Card delivery (push to the asker)

Askers can always pull an outcome with `card.await` / `card.get`. Push delivery
is optional and off until `TACHYON_CARD_DELIVERY_FILE` points at a file like
this one. The `profile` is the libs `mesh/core/hitlrouting` rule file; `sinks`
says what each sink id means on this host. A card cannot name an address.

```json
{
  "from": "msg://service/tachyon/cards",
  "sinks": {
    "planner-inbox": {"kind": "tether_message", "to": "msg://agent/agent-mux/<asker-id>"}
  },
  "profile": {
    "version": "2026-10-23.1",
    "rules": [
      {"id": "planner-approvals", "match": {"kind": "card.approval", "source": "service:*"},
       "sink": "planner-inbox", "completion": "wait", "delivery": "durable_message"}
    ],
    "catch_all": {"sink": "tachyon", "completion": "async", "delivery": "await"}
  }
}
```

- `tachyon` is built in: the outcome stays here for the asker to pull.
- `delivery: durable_message` sends one Tether notice per (card, revision) with
  the outcome as a go-envelopes response; `completion: notify` stores it without
  waking the recipient. Retries reuse `metadata.card_delivery_key`.
- Matchable fields today: `kind` and `source` (the asker's application id).
  `tool` and `namespace` are not populated yet.
- The file is checked at boot: a profile naming an undefined sink leaves
  delivery off and logs why.

Enabling: set `TACHYON_CARD_DELIVERY_FILE`, plus the Tether address/token the
chat bridge already uses, then restart. Disabling: unset it.
