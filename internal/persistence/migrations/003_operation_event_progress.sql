-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE operation_events ADD COLUMN progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100);
