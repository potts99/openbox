-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE route_tokens (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    route_id TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash BLOB NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    revoked_at TEXT,
    UNIQUE(owner_id, route_id, name)
);

CREATE INDEX idx_route_tokens_route ON route_tokens(owner_id, route_id, created_at);
