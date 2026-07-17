// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	maxArtifactsPerInstance = 100
	maxArtifactBytes        = int64(256 << 20)
	maxInstanceArtifactSize = int64(1 << 30)
	maxOwnerArtifactSize    = int64(5 << 30)
)

func (s *Store) CheckArtifactUpload(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path string, size int64) error {
	if err := validateArtifactInput(ownerID, instanceID, path, size); err != nil {
		return err
	}
	return s.checkArtifactQuota(ctx, s.db, ownerID, instanceID, path, size)
}

func (s *Store) PutArtifact(ctx context.Context, artifact domain.Artifact, idempotencyKey string) (domain.Artifact, *domain.Artifact, bool, error) {
	if err := validateArtifactInput(artifact.OwnerID, artifact.InstanceID, artifact.Path, artifact.SizeBytes); err != nil {
		return domain.Artifact{}, nil, false, err
	}
	if len(idempotencyKey) > 255 {
		return domain.Artifact{}, nil, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "idempotency_key"}
	}
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Artifact{}, nil, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Artifact{}, nil, false, fmt.Errorf("begin artifact write: %w", err)
	}
	defer tx.Rollback()
	if idempotencyKey != "" {
		var replay domain.Artifact
		err := scanArtifact(tx.QueryRowContext(ctx, artifactUploadSelect+` WHERE owner_id=? AND idempotency_key=?`, artifact.OwnerID, idempotencyKey), &replay)
		if err == nil {
			if replay.SizeBytes != artifact.SizeBytes || replay.SHA256 != artifact.SHA256 {
				return domain.Artifact{}, nil, false, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
			}
			return replay, nil, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.Artifact{}, nil, false, err
		}
	}
	if err := s.checkArtifactQuota(ctx, tx, artifact.OwnerID, artifact.InstanceID, artifact.Path, artifact.SizeBytes); err != nil {
		return domain.Artifact{}, nil, false, err
	}
	var previous domain.Artifact
	previousFound := true
	if err := scanArtifact(tx.QueryRowContext(ctx, artifactSelect+` WHERE owner_id=? AND instance_id=? AND path=?`,
		artifact.OwnerID, artifact.InstanceID, artifact.Path), &previous); errors.Is(err, sql.ErrNoRows) {
		previousFound = false
	} else if err != nil {
		return domain.Artifact{}, nil, false, err
	}
	if previousFound {
		if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE id=?`, previous.ID); err != nil {
			return domain.Artifact{}, nil, false, mapWriteError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(id,owner_id,instance_id,path,size_bytes,content_type,sha256,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, artifact.ID, artifact.OwnerID, artifact.InstanceID, artifact.Path, artifact.SizeBytes,
		artifact.ContentType, artifact.SHA256, formatTime(artifact.CreatedAt), formatTime(artifact.UpdatedAt)); err != nil {
		return domain.Artifact{}, nil, false, mapWriteError(err)
	}
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_uploads(owner_id,idempotency_key,id,instance_id,path,size_bytes,content_type,sha256,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, artifact.OwnerID, idempotencyKey, artifact.ID, artifact.InstanceID, artifact.Path,
			artifact.SizeBytes, artifact.ContentType, artifact.SHA256, formatTime(artifact.CreatedAt), formatTime(artifact.UpdatedAt)); err != nil {
			return domain.Artifact{}, nil, false, mapWriteError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Artifact{}, nil, false, fmt.Errorf("commit artifact write: %w", err)
	}
	if previousFound {
		return artifact, &previous, false, nil
	}
	return artifact, nil, false, nil
}

func (s *Store) GetArtifact(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path string) (domain.Artifact, error) {
	var artifact domain.Artifact
	err := scanArtifact(s.db.QueryRowContext(ctx, artifactSelect+` WHERE owner_id=? AND instance_id=? AND path=?`, ownerID, instanceID, path), &artifact)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Artifact{}, &domain.Error{Code: domain.CodeNotFound, Field: "artifact"}
	}
	return artifact, err
}

func (s *Store) ListArtifacts(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, prefix string) ([]domain.Artifact, error) {
	query := artifactSelect + ` WHERE owner_id=? AND instance_id=?`
	args := []any{ownerID, instanceID}
	if prefix != "" {
		query += ` AND path LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(prefix)+"%")
	}
	query += ` ORDER BY path`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Artifact, 0)
	for rows.Next() {
		var artifact domain.Artifact
		if err := scanArtifact(rows, &artifact); err != nil {
			return nil, err
		}
		items = append(items, artifact)
	}
	return items, rows.Err()
}

func (s *Store) DeleteArtifact(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path string) (domain.Artifact, error) {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return domain.Artifact{}, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Artifact{}, err
	}
	defer tx.Rollback()
	var artifact domain.Artifact
	if err := scanArtifact(tx.QueryRowContext(ctx, artifactSelect+` WHERE owner_id=? AND instance_id=? AND path=?`, ownerID, instanceID, path), &artifact); errors.Is(err, sql.ErrNoRows) {
		return domain.Artifact{}, &domain.Error{Code: domain.CodeNotFound, Field: "artifact"}
	} else if err != nil {
		return domain.Artifact{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artifacts WHERE id=?`, artifact.ID); err != nil {
		return domain.Artifact{}, mapWriteError(err)
	}
	return artifact, tx.Commit()
}

func (s *Store) DeleteInstanceArtifacts(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) error {
	release, err := s.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE owner_id=? AND instance_id=?`, ownerID, instanceID)
	return mapWriteError(err)
}

const artifactSelect = `SELECT id,owner_id,instance_id,path,size_bytes,content_type,sha256,created_at,updated_at FROM artifacts`
const artifactUploadSelect = `SELECT id,owner_id,instance_id,path,size_bytes,content_type,sha256,created_at,updated_at FROM artifact_uploads`

func (s *Store) checkArtifactQuota(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ownerID domain.OwnerID, instanceID domain.InstanceID, path string, size int64) error {
	var count int
	var instanceBytes, ownerBytes int64
	if err := db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM artifacts
		WHERE owner_id=? AND instance_id=? AND NOT (path=?)`, ownerID, instanceID, path).Scan(&count, &instanceBytes); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(sum(size_bytes),0) FROM artifacts
		WHERE owner_id=? AND NOT (instance_id=? AND path=?)`, ownerID, instanceID, path).Scan(&ownerBytes); err != nil {
		return err
	}
	if count >= maxArtifactsPerInstance || instanceBytes+size > maxInstanceArtifactSize || ownerBytes+size > maxOwnerArtifactSize {
		return &domain.Error{Code: domain.CodeQuotaExceeded, Field: "artifacts"}
	}
	return nil
}

func validateArtifactInput(ownerID domain.OwnerID, instanceID domain.InstanceID, path string, size int64) error {
	if ownerID == "" || instanceID == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "instance"}
	}
	if err := domain.ValidateArtifactPath(path); err != nil {
		return err
	}
	if size < 0 || size > maxArtifactBytes {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "size_bytes"}
	}
	return nil
}

func scanArtifact(row rowScanner, artifact *domain.Artifact) error {
	var created, updated string
	if err := row.Scan(&artifact.ID, &artifact.OwnerID, &artifact.InstanceID, &artifact.Path, &artifact.SizeBytes, &artifact.ContentType, &artifact.SHA256, &created, &updated); err != nil {
		return err
	}
	var err error
	artifact.CreatedAt, err = parseTime(created)
	if err != nil {
		return err
	}
	artifact.UpdatedAt, err = parseTime(updated)
	return err
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
