-- Planner typed item relations (PL1). Derived and rebuildable: the source of truth is the
-- relations[] array embedded in each portfolio item (properties.portfolio.relations). objects.persist
-- rewrites an item's rows in the same transaction as the item write, so the index cannot drift.
-- Additive.
CREATE TABLE planner_relation_index (
 from_id TEXT NOT NULL REFERENCES objects(id),
 kind TEXT NOT NULL CHECK(kind IN ('needs','delivers','mitigates','relates')),
 to_id TEXT NOT NULL,
 from_db TEXT NOT NULL,
 to_db TEXT NOT NULL,
 PRIMARY KEY(from_id, kind, to_id)
);
CREATE INDEX planner_relation_index_to ON planner_relation_index(to_id, kind);
