-- SPDX-License-Identifier: AGPL-3.0-only
-- System-scoped egress profiles + instance binding.

DROP TABLE IF EXISTS egress_profiles;

CREATE TABLE egress_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mode TEXT NOT NULL CHECK(mode IN ('standard','restricted')),
    allowed_destinations_json BLOB NOT NULL,
    dns_policy TEXT NOT NULL,
    system INTEGER NOT NULL CHECK(system IN (0,1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO egress_profiles (
    id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
) VALUES
    ('egress-standard', 'standard', 'standard', '[]', 'host_resolve', 1, datetime('now'), datetime('now')),
    ('egress-restricted', 'restricted', 'restricted', '[]', 'host_resolve', 1, datetime('now'), datetime('now'));

ALTER TABLE instances ADD COLUMN egress_profile_id TEXT NOT NULL DEFAULT '';

UPDATE instances SET egress_profile_id = 'egress-restricted', egress_mode = 'restricted'
WHERE kind = 'sandbox';

UPDATE instances SET egress_profile_id = 'egress-standard', egress_mode = 'standard'
WHERE kind != 'sandbox';
