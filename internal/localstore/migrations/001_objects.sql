-- Durable domain data: never evicted with api_cache. Definitions are typed at
-- the service boundary; common identity/history/links work across all providers.
CREATE TABLE objects (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('agent','team','assignment','workflow','project','machine','repository')),
 ownership TEXT NOT NULL CHECK(ownership IN ('tachyon','source')),
 name TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 local_json TEXT NOT NULL CHECK(json_valid(local_json) AND json_type(local_json)='object'),
 metadata_json TEXT NOT NULL CHECK(json_valid(metadata_json) AND json_type(metadata_json)='object'),
 deleted_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX objects_kind_name ON objects(kind,name,id);

-- One authoritative source per imported object in this slice. Multiple sources
-- get separate records until an explicit correlation/ownership operation exists.
CREATE TABLE object_sources (
 object_id TEXT PRIMARY KEY REFERENCES objects(id),
 provider TEXT NOT NULL,
 instance TEXT NOT NULL,
 collection TEXT NOT NULL,
 kind TEXT NOT NULL,
 external_id TEXT NOT NULL,
 version TEXT NOT NULL,
 observed_at TEXT NOT NULL,
 deleted_at TEXT,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND json_type(snapshot_json)='object'),
 UNIQUE(provider,instance,collection,kind,external_id)
);
CREATE TABLE object_revisions (
 object_id TEXT NOT NULL REFERENCES objects(id),
 revision INTEGER NOT NULL,
 operation TEXT NOT NULL,
 actor TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 record_json TEXT NOT NULL CHECK(json_valid(record_json)),
 PRIMARY KEY(object_id,revision)
);
CREATE TABLE object_links (
 from_id TEXT NOT NULL REFERENCES objects(id),
 relation TEXT NOT NULL,
 slot TEXT NOT NULL DEFAULT '',
 to_id TEXT NOT NULL REFERENCES objects(id),
 PRIMARY KEY(from_id,relation,slot,to_id)
);
CREATE INDEX object_links_target ON object_links(to_id,relation,from_id);
-- Successful complete scans are watermarks, including empty collections. A
-- late poll cannot resurrect something a newer complete scan removed.
CREATE TABLE object_sync_watermarks (
 provider TEXT NOT NULL, instance TEXT NOT NULL, collection TEXT NOT NULL,
 kind TEXT NOT NULL, observed_at TEXT NOT NULL,
 PRIMARY KEY(provider,instance,collection,kind)
);
