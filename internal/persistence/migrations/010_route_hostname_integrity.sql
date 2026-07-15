-- SPDX-License-Identifier: AGPL-3.0-only

-- Gateway and certificate lookups are case-insensitive, so route ownership
-- must be globally unambiguous. Canonicalise historical values first.
UPDATE routes SET hostname=lower(trim(hostname));
CREATE UNIQUE INDEX idx_routes_hostname_nocase ON routes(hostname COLLATE NOCASE);
