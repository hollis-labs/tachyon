-- Agents bus feed (A4). Additive. Normalised, derived events published by Tether apps
-- (Cerberus, Torque, Tachyon...) and Tether's own session lifecycle. No raw payloads.
CREATE TABLE bus_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 tether_seq INTEGER NOT NULL,
 tether_event_id TEXT NOT NULL UNIQUE,
 at_ns INTEGER NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('deploy','restart','task','failure','host','workstream','lifecycle','card')),
 subject TEXT NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 app TEXT NOT NULL DEFAULT '',
 severity TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warn','error')),
 refs_json TEXT NOT NULL DEFAULT '[]',
 card_json TEXT
);
CREATE INDEX bus_events_seq ON bus_events(tether_seq);
CREATE INDEX bus_events_type ON bus_events(type, tether_seq);
