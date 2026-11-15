-- Synthetic team-operations rows for the TACHYON_E2E_BUILDER=backend run. e2e/serve.sh fills in the two
-- placeholders (the fake Tether's URL as the binding target, and the binding identity the binary derives from it)
-- and applies the result to the run's throwaway database once the schema exists, before serving.
--
-- Two Tachyon-owned teams (Delivery, Docs), each at revision 1, and one confirmed formation of Delivery whose
-- run (run-e2e-1, as e2e/tether-fake.ts TEAM_RUN) has a saved roster with agt_e2e_lead and agt_e2e_reviewer.
-- The prompt "Review etiquette" is the Library's one local part. Ids are fixed UUIDs; times are the seed's now.

CREATE TEMP TABLE t AS SELECT
  strftime('%Y-%m-%dT%H:%M:%S.000000000Z', 'now') AS stamp,
  strftime('%Y-%m-%dT%H:%M:%fZ', 'now') AS at,
  CAST(strftime('%s', 'now') AS INTEGER) * 1000000000 AS ns;

CREATE TEMP TABLE team_defs (id TEXT, name TEXT, definition TEXT);
INSERT INTO team_defs VALUES
  ('e2e00000-0000-4000-8000-0000000000d1', 'Delivery',
   '{"name":"Delivery","coordinator":"lead","slots":[{"name":"lead","role":"lead","min":0,"max":1},{"name":"reviewer","role":"reviewer","min":1,"max":1}]}'),
  ('e2e00000-0000-4000-8000-0000000000d2', 'Docs',
   '{"name":"Docs","coordinator":"writer","slots":[{"name":"writer","role":"writer","min":1,"max":1}]}');

INSERT INTO objects (id, kind, ownership, name, revision, local_json, metadata_json, deleted_at, created_at, updated_at)
SELECT d.id, 'team', 'tachyon', d.name, 1, d.definition, '{}', NULL, t.stamp, t.stamp FROM team_defs d, t;

-- The reviewed revision a run projection reads (objects.Record as JSON).
INSERT INTO object_revisions (object_id, revision, operation, actor, recorded_at, record_json)
SELECT d.id, 1, 'create', 'owner', t.stamp,
  json_object('id', d.id, 'kind', 'team', 'ownership', 'tachyon', 'name', d.name, 'revision', 1,
    'local', json(d.definition), 'metadata', json('{}'), 'definition', json(d.definition),
    'created_at', t.stamp, 'updated_at', t.stamp)
FROM team_defs d, t;

-- The confirmed formation: target and credential identity are the binding the binary makes to the fake. Payload and
-- result are BLOBs, as the binary writes them (provideractions scans them into json.RawMessage).
INSERT INTO provider_actions (id, object_id, object_revision, provider, target, credential_identity, verb, payload,
  digest, input_digest, state, attempts, lease_until, result, problem, actor, created_at, updated_at)
SELECT 'e2e00000-0000-4000-8000-0000000000f1', 'e2e00000-0000-4000-8000-0000000000d1', 1, 'tether',
  '@TETHER_TARGET@', '@TETHER_IDENTITY@', 'teams.form',
  CAST('{"team":{"id":"tachyon-e2e00000-0000-4000-8000-0000000000d1-e2e00000-0000-4000-8000-0000000000f1","name":"Delivery","version":1,"slots":[{"name":"lead","role":"lead","min":0,"max":1},{"name":"reviewer","role":"reviewer","min":1,"max":1,"workspace":{"mission":"Ship the release notes."}}]}}' AS BLOB),
  'sha256:bc3ef9a94728da9bc4e3d1a833d10dd87a7efa5444649825f6fea42614a2ab67',
  'sha256:' || lower(hex(randomblob(32))), 'succeeded', 1, 0,
  CAST('{"run":{"id":"run-e2e-1","team_id":"tachyon-e2e00000-0000-4000-8000-0000000000d1-e2e00000-0000-4000-8000-0000000000f1","team_version":1,"channel":"team/run-e2e-1","status":"working"},"members":[]}' AS BLOB),
  '', 'owner', t.at, t.at
FROM t;

-- The saved roster (a team's summary reads it without reaching Tether).
INSERT INTO provider_roster_cache (identity, selection, body, observed_at)
SELECT '@TETHER_IDENTITY@', 'e2e00000-0000-4000-8000-0000000000f1',
  CAST('[{"run":{"id":"run-e2e-1","team_id":"tachyon-e2e00000-0000-4000-8000-0000000000d1-e2e00000-0000-4000-8000-0000000000f1","team_version":1,"channel":"team/run-e2e-1","status":"working"},"roster_version":1,"members":[{"id":"mem-e2e-lead","slot":"lead","actor":"agt_e2e_lead","status":"active","kind":"agent"},{"id":"mem-e2e-reviewer","slot":"reviewer","actor":"agt_e2e_reviewer","status":"active","kind":"agent"}]}]' AS BLOB),
  t.at
FROM t;

INSERT INTO library_parts (kind, id, name, summary, body_json, revision, archived, created_ns, updated_ns)
SELECT 'prompt', 'review-etiquette', 'Review etiquette', 'How to word review comments.',
  '{"text":"Name the line, say why, suggest a fix."}', 3, 0, t.ns, t.ns FROM t;
