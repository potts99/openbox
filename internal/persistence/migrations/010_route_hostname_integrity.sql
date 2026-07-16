-- SPDX-License-Identifier: AGPL-3.0-only

-- Gateway and certificate lookups are case-insensitive, so route ownership
-- must be globally unambiguous. Canonicalise historical values first, then
-- drop case-only duplicates (keep the oldest row) before the unique index.
UPDATE routes SET hostname=lower(trim(hostname));
DELETE FROM routes
 WHERE rowid NOT IN (
   SELECT MIN(rowid) FROM routes GROUP BY hostname COLLATE NOCASE
 );
CREATE UNIQUE INDEX idx_routes_hostname_nocase ON routes(hostname COLLATE NOCASE);
