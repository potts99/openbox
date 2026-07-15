-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE operations ADD COLUMN payload_json BLOB NOT NULL DEFAULT '{}';
ALTER TABLE operations ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0);
ALTER TABLE operations ADD COLUMN next_attempt_at TEXT;
ALTER TABLE operations ADD COLUMN claimed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN claim_token TEXT NOT NULL DEFAULT '';
ALTER TABLE operations ADD COLUMN claim_expires_at TEXT;
ALTER TABLE operations ADD COLUMN error_class TEXT NOT NULL DEFAULT '';

CREATE TABLE operation_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_id TEXT NOT NULL REFERENCES owners(id),
    operation_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK(sequence > 0),
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    error_class TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    metadata_json BLOB NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    UNIQUE(operation_id, sequence),
    FOREIGN KEY(operation_id, owner_id) REFERENCES operations(id, owner_id)
);

CREATE INDEX idx_operations_claimable ON operations(status, next_attempt_at, claim_expires_at, created_at);
CREATE INDEX idx_operation_events_operation ON operation_events(operation_id, sequence);
