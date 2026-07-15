-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE owner_credentials (
    owner_id TEXT PRIMARY KEY REFERENCES owners(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE bootstrap_challenges (
    id TEXT PRIMARY KEY,
    secret_hash BLOB NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT
);

CREATE TABLE auth_sessions (
    id_hash BLOB PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    csrf_hash BLOB NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash BLOB NOT NULL UNIQUE,
    scopes TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT,
    revoked_at TEXT,
    last_used_at TEXT,
    UNIQUE(owner_id, name)
);

CREATE INDEX idx_auth_sessions_owner ON auth_sessions(owner_id, expires_at);
CREATE INDEX idx_api_tokens_owner ON api_tokens(owner_id, created_at);
