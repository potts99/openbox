-- SPDX-License-Identifier: AGPL-3.0-only

-- Record clone provenance without foreign keys so deleting a source cannot
-- invalidate completed clones (source ids are historical labels only).
ALTER TABLE instances ADD COLUMN clone_source_instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN clone_source_snapshot_id TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN clone_source_image_id TEXT NOT NULL DEFAULT '';
