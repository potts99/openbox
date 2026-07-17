-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    instance_id TEXT NOT NULL,
    path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
    content_type TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_id, instance_id, path)
);

CREATE INDEX artifacts_owner_instance_path ON artifacts(owner_id, instance_id, path);
CREATE INDEX artifacts_owner_size ON artifacts(owner_id, size_bytes);

CREATE TABLE artifact_uploads (
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content_type TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(owner_id, idempotency_key)
);
