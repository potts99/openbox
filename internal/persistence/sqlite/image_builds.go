// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// CreateImageBuild atomically records a build target and its durable operation.
func (s *Store) CreateImageBuild(ctx context.Context, build domain.ImageBuild, operation domain.Operation) (domain.Operation, bool, error) {
	if build.ID == "" || build.OwnerID == "" || build.Architecture == "" || build.Runtime == "" || build.Alias == "" || build.BuilderRef == "" {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "image_build"}
	}
	if operation.OwnerID != build.OwnerID || operation.Type != "image.build" || operation.TargetType != "image" || operation.TargetID != build.ID {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.target"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer tx.Rollback()
	existing, found, err := findOperationByIdempotency(ctx, tx, operation.OwnerID, operation.IdempotencyKey)
	if err != nil {
		return domain.Operation{}, false, err
	}
	if found {
		if existing.Type != operation.Type || existing.RequestHash != operation.RequestHash {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, true, nil
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE owner_id=? AND type='image.build' AND status IN (?,?))`,
		build.OwnerID, domain.OperationPending, domain.OperationRunning).Scan(&active); err != nil {
		return domain.Operation{}, false, err
	}
	if active != 0 {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeBusy, Field: "image.build"}
	}
	if err := insertOperation(ctx, tx, operation); err != nil {
		return domain.Operation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO image_builds(id,owner_id,architecture,runtime,alias,builder_ref,digest,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		build.ID, build.OwnerID, build.Architecture, build.Runtime, build.Alias, build.BuilderRef, build.Digest, formatTime(build.CreatedAt), formatTime(build.UpdatedAt)); err != nil {
		return domain.Operation{}, false, mapWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit image build: %w", err)
	}
	return operation, false, nil
}

func (s *Store) GetImageBuild(ctx context.Context, ownerID domain.OwnerID, id string) (domain.ImageBuild, error) {
	var build domain.ImageBuild
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,owner_id,architecture,runtime,alias,builder_ref,digest,created_at,updated_at FROM image_builds WHERE owner_id=? AND id=?`, ownerID, id).
		Scan(&build.ID, &build.OwnerID, &build.Architecture, &build.Runtime, &build.Alias, &build.BuilderRef, &build.Digest, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ImageBuild{}, &domain.Error{Code: domain.CodeNotFound, Field: "image_build"}
	}
	if err != nil {
		return domain.ImageBuild{}, err
	}
	if build.CreatedAt, err = parseTime(created); err != nil {
		return domain.ImageBuild{}, err
	}
	build.UpdatedAt, err = parseTime(updated)
	return build, err
}

// PublishImageBuild records a successful immutable image without changing the
// old image record that existing instances already pin.
func (s *Store) PublishImageBuild(ctx context.Context, ownerID domain.OwnerID, id, digest string, now time.Time) error {
	if digest == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "digest"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var build domain.ImageBuild
	var created, updated string
	if err := tx.QueryRowContext(ctx, `SELECT id,owner_id,architecture,runtime,alias,builder_ref,digest,created_at,updated_at FROM image_builds WHERE owner_id=? AND id=?`, ownerID, id).
		Scan(&build.ID, &build.OwnerID, &build.Architecture, &build.Runtime, &build.Alias, &build.BuilderRef, &build.Digest, &created, &updated); err != nil {
		return err
	}
	if build.Digest != "" && build.Digest != digest {
		return &domain.Error{Code: domain.CodeConflict, Field: "image_build.digest"}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE image_builds SET digest=?,updated_at=? WHERE owner_id=? AND id=?`, digest, formatTime(now), ownerID, id); err != nil {
		return mapWriteError(err)
	}
	image := domain.Image{ID: domain.ImageID(digest), OwnerID: ownerID, Alias: digest, Source: "incus:" + build.Alias, Digest: digest, Architecture: build.Architecture, Compatibility: build.Runtime, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO images(id,owner_id,alias,source,digest,architecture,compatibility,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		image.ID, image.OwnerID, image.Alias, image.Source, image.Digest, image.Architecture, image.Compatibility, formatTime(image.CreatedAt), formatTime(image.UpdatedAt)); err != nil {
		return mapWriteError(err)
	}
	return tx.Commit()
}

// AppendOperationEvent appends log output to the durable SSE event stream.
func (s *Store) AppendOperationEvent(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, stage, message string, metadata []byte, now time.Time) error {
	if stage == "" || len(message) > 16<<10 {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.event"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status domain.OperationStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM operations WHERE owner_id=? AND id=?`, ownerID, id).Scan(&status); err != nil {
		return err
	}
	if status != domain.OperationRunning {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.status"}
	}
	if err := appendOperationEventTx(ctx, tx, ownerID, id, stage, status, "", "", message, metadata, now); err != nil {
		return err
	}
	return tx.Commit()
}
