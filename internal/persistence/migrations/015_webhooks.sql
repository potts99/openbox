-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE webhook_subscriptions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    description TEXT NOT NULL,
    secret TEXT NOT NULL,
    events_json TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX webhook_subscriptions_owner ON webhook_subscriptions(owner_id, created_at);

CREATE TABLE webhook_deliveries (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','running','delivered','failed','dead','canceled')),
    attempt INTEGER NOT NULL CHECK(attempt >= 1),
    http_status INTEGER,
    error_class TEXT NOT NULL DEFAULT '',
    first_attempt_at TEXT,
    next_attempt_at TEXT,
    claimed_by TEXT NOT NULL DEFAULT '',
    claim_expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX webhook_deliveries_due ON webhook_deliveries(status, next_attempt_at, claim_expires_at);
CREATE INDEX webhook_deliveries_owner ON webhook_deliveries(owner_id, created_at DESC);
CREATE INDEX webhook_deliveries_subscription ON webhook_deliveries(subscription_id, status);
