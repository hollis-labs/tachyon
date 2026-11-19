-- Each seat's latest declared status (CW-20261114-0023c/d), from Tether's seat.status events (typed messages,
-- CW-20261114-0013): available, working, waiting-decision, waiting-agent, blocked, paused or offline, with an
-- optional one-line reason, ref, owner (@handle) and expiry. One row per agent; a later event replaces it. Read by
-- the needs-you view (statuses owned by @chrispian) and the seat status cards. Additive.
CREATE TABLE seat_status (
 agent_urn TEXT PRIMARY KEY,
 handle TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 ref TEXT NOT NULL DEFAULT '',
 owner TEXT NOT NULL DEFAULT '',
 expires_at_ns INTEGER,
 set_by TEXT NOT NULL DEFAULT '',
 at_ns INTEGER NOT NULL,
 tether_seq INTEGER NOT NULL
);
CREATE INDEX seat_status_owner ON seat_status(owner, state);
