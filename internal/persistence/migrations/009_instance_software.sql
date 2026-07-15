-- SPDX-License-Identifier: AGPL-3.0-only

-- Curated software install state per instance (VPS software catalog).
CREATE TABLE IF NOT EXISTS instance_software (
    instance_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    package_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('absent','pending','installed','failed')),
    version TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (instance_id, package_id),
    FOREIGN KEY(instance_id, owner_id) REFERENCES instances(id, owner_id)
);
CREATE INDEX IF NOT EXISTS instance_software_owner_instance ON instance_software(owner_id, instance_id);

-- Do not rebuild instances here. The table is referenced by routes, snapshots,
-- and other durable records, and SQLite cannot disable foreign-key enforcement
-- inside the transaction used by the migration runner. New instance creation
-- already rejects the retired devbox kind; retaining legacy rows is safe.
