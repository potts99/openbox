-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE owners (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE ssh_keys (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    fingerprint TEXT NOT NULL, public_key TEXT NOT NULL, label TEXT NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, fingerprint)
);

CREATE TABLE images (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    alias TEXT NOT NULL, source TEXT NOT NULL, digest TEXT NOT NULL,
    architecture TEXT NOT NULL, compatibility TEXT NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, alias), UNIQUE(id, owner_id)
);

CREATE TABLE instances (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    name TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('sandbox','vps','devbox')),
    image_id TEXT,
    requested_isolation TEXT NOT NULL CHECK(requested_isolation IN ('best_available','standard','strong')),
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
    UNIQUE(owner_id, name), UNIQUE(id, owner_id),
    FOREIGN KEY(image_id, owner_id) REFERENCES images(id, owner_id),
    CHECK(kind != 'sandbox' OR expires_at IS NOT NULL)
);

CREATE TABLE snapshots (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    instance_id TEXT NOT NULL, name TEXT NOT NULL,
    runtime_ref TEXT NOT NULL, created_at TEXT NOT NULL,
    UNIQUE(instance_id, name),
    FOREIGN KEY(instance_id, owner_id) REFERENCES instances(id, owner_id)
);

CREATE TABLE routes (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    instance_id TEXT NOT NULL, hostname TEXT NOT NULL,
    target_port INTEGER NOT NULL CHECK(target_port BETWEEN 1 AND 65535),
    visibility TEXT NOT NULL CHECK(visibility IN ('private','public')),
    tls_state TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, hostname),
    FOREIGN KEY(instance_id, owner_id) REFERENCES instances(id, owner_id)
);

CREATE TABLE pi_profiles (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id), name TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0), settings_json BLOB NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, name), UNIQUE(id, owner_id)
);

CREATE TABLE credential_profiles (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id), name TEXT NOT NULL,
    provider TEXT NOT NULL, gateway_store_ref TEXT NOT NULL, auth_mode TEXT NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, name), UNIQUE(id, owner_id)
);

CREATE TABLE gateway_grants (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    instance_id TEXT NOT NULL,
    credential_profile_id TEXT NOT NULL,
    provider TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL,
    FOREIGN KEY(instance_id, owner_id) REFERENCES instances(id, owner_id),
    FOREIGN KEY(credential_profile_id, owner_id) REFERENCES credential_profiles(id, owner_id)
);

CREATE TABLE egress_profiles (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id), name TEXT NOT NULL,
    mode TEXT NOT NULL CHECK(mode IN ('standard','restricted')),
    allowed_destinations_json BLOB NOT NULL, dns_policy TEXT NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, name)
);

CREATE TABLE operations (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    type TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','running','succeeded','failed')),
    stage TEXT NOT NULL, progress INTEGER NOT NULL CHECK(progress BETWEEN 0 AND 100),
    error_code TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
    UNIQUE(owner_id, idempotency_key), UNIQUE(id, owner_id)
);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    actor TEXT NOT NULL, action TEXT NOT NULL, target_type TEXT NOT NULL,
    target_id TEXT NOT NULL, outcome TEXT NOT NULL, metadata_json BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE instance_tombstones (
    instance_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES owners(id),
    name TEXT NOT NULL, operation_id TEXT NOT NULL,
    deleted_at TEXT NOT NULL,
    FOREIGN KEY(operation_id, owner_id) REFERENCES operations(id, owner_id)
);

CREATE INDEX idx_instances_owner ON instances(owner_id);
CREATE INDEX idx_operations_target ON operations(owner_id, target_type, target_id);
CREATE INDEX idx_audit_target ON audit_events(owner_id, target_type, target_id);
