-- The one service identity trusted as the Tether MCP proxy. Only a request authenticated as this
-- identity may carry X-Tether-* actor headers; they are ignored from every other caller.
-- At most one row (id is fixed at 1). Applies to the identity's existing tokens at once.
CREATE TABLE service_proxy_pin (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 identity_id TEXT NOT NULL REFERENCES service_identities(id),
 pinned_at INTEGER NOT NULL
);
