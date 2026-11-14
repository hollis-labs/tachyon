-- Tether event summaries and cells (CW-20261114-0023a, Tether event vocabulary CW-20261114-0019).
-- tether_summary is Tether's one-line, redacted event summary (at most 200 characters); cell is the
-- acting seat's team slug. Rows written before this are ''.
--
-- agents_events: additive. Its summary column stays Tachyon's derived key, which the timeline reads
-- to hide and group rows; Tether's line is stored beside it.
ALTER TABLE agents_events ADD COLUMN tether_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE agents_events ADD COLUMN cell TEXT NOT NULL DEFAULT '';

-- bus_events: rebuilt, because the type CHECK must admit mail ('message': message.sent) and seat
-- events ('seat': seat.*), which carry no session and were dropped. Every row and id is copied.
CREATE TABLE bus_events_055 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 tether_seq INTEGER NOT NULL,
 tether_event_id TEXT NOT NULL UNIQUE,
 at_ns INTEGER NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('deploy','restart','task','failure','host','workstream','lifecycle','card','message','seat')),
 subject TEXT NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 app TEXT NOT NULL DEFAULT '',
 severity TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warn','error')),
 refs_json TEXT NOT NULL DEFAULT '[]',
 card_json TEXT,
 tether_summary TEXT NOT NULL DEFAULT '',
 cell TEXT NOT NULL DEFAULT ''
);
INSERT INTO bus_events_055(id,tether_seq,tether_event_id,at_ns,type,subject,actor,app,severity,refs_json,card_json)
 SELECT id,tether_seq,tether_event_id,at_ns,type,subject,actor,app,severity,refs_json,card_json FROM bus_events;
-- Carry the AUTOINCREMENT high-water mark, so ids are never reused once retention deletes the newest rows.
-- sqlite_sequence has no unique key on name, so the copy's row is replaced by delete-then-insert; the rename
-- below renames it along with the table.
DELETE FROM sqlite_sequence WHERE name='bus_events_055';
INSERT INTO sqlite_sequence(name,seq) SELECT 'bus_events_055', MAX(
 COALESCE((SELECT seq FROM sqlite_sequence WHERE name='bus_events'),0),
 COALESCE((SELECT MAX(id) FROM bus_events),0));
DROP TABLE bus_events;
ALTER TABLE bus_events_055 RENAME TO bus_events;
CREATE INDEX bus_events_seq ON bus_events(tether_seq);
CREATE INDEX bus_events_type ON bus_events(type, tether_seq);
