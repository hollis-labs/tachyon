-- Last good Transcripts snapshot (TR1): session metadata only (titles, projects, counts), no turn text.
-- Served with source_state "unavailable" when Fragments Engine cannot be read.
CREATE TABLE transcript_snapshot (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 body BLOB NOT NULL,
 observed_ms INTEGER NOT NULL
);
