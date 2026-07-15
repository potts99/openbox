-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE instances ADD COLUMN egress_mode TEXT NOT NULL DEFAULT 'standard'
    CHECK(egress_mode IN ('standard','restricted'));

UPDATE instances SET egress_mode = 'restricted' WHERE kind = 'sandbox';
