-- SPDX-License-Identifier: AGPL-3.0-only

-- owners remains the organization boundary for this compatibility slice. Resource
-- owner_id columns intentionally continue to refer to this table.
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE org_memberships (
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('admin', 'member')),
    created_at TEXT NOT NULL,
    PRIMARY KEY(owner_id, user_id)
);

CREATE TABLE user_credentials (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Preserve the historical single-owner password as one admin user in its owner
-- organization. owner_credentials remains for rollback compatibility only.
INSERT INTO users(id, username, display_name, created_at, updated_at)
SELECT 'usr_' || owner_id, owner_id, 'Owner', updated_at, updated_at
FROM owner_credentials;

INSERT INTO org_memberships(owner_id, user_id, role, created_at)
SELECT owner_id, 'usr_' || owner_id, 'admin', updated_at
FROM owner_credentials;

INSERT INTO user_credentials(user_id, password_hash, updated_at)
SELECT 'usr_' || owner_id, password_hash, updated_at
FROM owner_credentials;

ALTER TABLE auth_sessions ADD COLUMN user_id TEXT REFERENCES users(id);

UPDATE auth_sessions
SET user_id = 'usr_' || owner_id
WHERE user_id IS NULL;

CREATE INDEX idx_org_memberships_user ON org_memberships(user_id, owner_id);
CREATE INDEX idx_auth_sessions_user ON auth_sessions(user_id, expires_at);
