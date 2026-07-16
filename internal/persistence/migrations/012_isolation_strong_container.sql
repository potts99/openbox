-- SPDX-License-Identifier: AGPL-3.0-only
-- Replace requested_isolation values best_available|standard|strong with strong|container.

PRAGMA foreign_keys=OFF;

CREATE TABLE instances_new (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    name TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('sandbox','vps','devbox')),
    image_id TEXT,
    requested_isolation TEXT NOT NULL CHECK(requested_isolation IN ('strong','container')),
    actual_isolation TEXT NOT NULL CHECK(actual_isolation IN ('unknown','container','virtual_machine')),
    desired_state TEXT NOT NULL CHECK(desired_state IN ('running','stopped','deleted')),
    observed_state TEXT NOT NULL CHECK(observed_state IN ('pending','creating','running','stopping','stopped','deleting','deleted','error')),
    vcpus INTEGER NOT NULL DEFAULT 0 CHECK(vcpus >= 0),
    memory_bytes INTEGER NOT NULL DEFAULT 0 CHECK(memory_bytes >= 0),
    disk_bytes INTEGER NOT NULL DEFAULT 0 CHECK(disk_bytes >= 0),
    expires_at TEXT, protected INTEGER NOT NULL DEFAULT 0 CHECK(protected IN (0,1)),
    runtime_ref TEXT NOT NULL DEFAULT '', error_code TEXT NOT NULL DEFAULT '',
    error_stage TEXT NOT NULL DEFAULT '', error_retryable INTEGER NOT NULL DEFAULT 0 CHECK(error_retryable IN (0,1)),
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT,
    clone_source_instance_id TEXT NOT NULL DEFAULT '',
    clone_source_snapshot_id TEXT NOT NULL DEFAULT '',
    clone_source_image_id TEXT NOT NULL DEFAULT '',
    egress_mode TEXT NOT NULL DEFAULT 'standard' CHECK(egress_mode IN ('standard','restricted')),
    egress_profile_id TEXT NOT NULL DEFAULT '',
    UNIQUE(owner_id, name), UNIQUE(id, owner_id),
    FOREIGN KEY(image_id, owner_id) REFERENCES images(id, owner_id),
    CHECK(kind != 'sandbox' OR expires_at IS NOT NULL)
);

INSERT INTO instances_new (
    id, owner_id, name, kind, image_id, requested_isolation, actual_isolation,
    desired_state, observed_state, vcpus, memory_bytes, disk_bytes, expires_at, protected,
    runtime_ref, error_code, error_stage, error_retryable, created_at, updated_at, deleted_at,
    clone_source_instance_id, clone_source_snapshot_id, clone_source_image_id,
    egress_mode, egress_profile_id
)
SELECT
    id, owner_id, name, kind, image_id,
    CASE requested_isolation
        WHEN 'standard' THEN 'container'
        WHEN 'best_available' THEN 'strong'
        ELSE requested_isolation
    END,
    actual_isolation,
    desired_state, observed_state, vcpus, memory_bytes, disk_bytes, expires_at, protected,
    runtime_ref, error_code, error_stage, error_retryable, created_at, updated_at, deleted_at,
    COALESCE(clone_source_instance_id, ''),
    COALESCE(clone_source_snapshot_id, ''),
    COALESCE(clone_source_image_id, ''),
    COALESCE(egress_mode, 'standard'),
    COALESCE(egress_profile_id, '')
FROM instances;

DROP TABLE instances;
ALTER TABLE instances_new RENAME TO instances;

CREATE INDEX idx_instances_owner ON instances(owner_id);

PRAGMA foreign_keys=ON;
