-- Explicit binding of a service identity to the agents it may act for (chat plan verbs).
-- Rows apply to every token of the identity, including ones already issued.
CREATE TABLE service_identity_agents (
 identity_id TEXT NOT NULL REFERENCES service_identities(id),
 agent_key TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 PRIMARY KEY(identity_id, agent_key)
);
