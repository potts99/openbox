-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE image_builds (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    architecture TEXT NOT NULL CHECK(architecture IN ('x86_64','aarch64')),
    runtime TEXT NOT NULL CHECK(runtime IN ('container','virtual-machine')),
    alias TEXT NOT NULL,
    builder_ref TEXT NOT NULL,
    digest TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_id, id)
);

CREATE INDEX idx_image_builds_owner_active ON image_builds(owner_id, digest);
