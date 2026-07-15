-- SPDX-License-Identifier: AGPL-3.0-only

-- Immutable Pi profile version history for preview/rollback (slice 12).
CREATE TABLE pi_profile_versions (
    profile_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    settings_json BLOB NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY(profile_id, version),
    FOREIGN KEY(profile_id, owner_id) REFERENCES pi_profiles(id, owner_id)
);
CREATE INDEX pi_profile_versions_owner_profile ON pi_profile_versions(owner_id, profile_id);
