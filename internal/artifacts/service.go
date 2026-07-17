// SPDX-License-Identifier: AGPL-3.0-only

// Package artifacts stores owner-scoped, instance-attached artifact blobs.
package artifacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	MaxArtifactBytes = int64(256 << 20)
	MaxPrefixLength  = 256
)

type Repository interface {
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	CheckArtifactUpload(context.Context, domain.OwnerID, domain.InstanceID, string, int64) error
	PutArtifact(context.Context, domain.Artifact, string) (domain.Artifact, *domain.Artifact, bool, error)
	GetArtifact(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Artifact, error)
	ListArtifacts(context.Context, domain.OwnerID, domain.InstanceID, string) ([]domain.Artifact, error)
	DeleteArtifact(context.Context, domain.OwnerID, domain.InstanceID, string) (domain.Artifact, error)
	DeleteInstanceArtifacts(context.Context, domain.OwnerID, domain.InstanceID) error
}

type Service struct {
	repo  Repository
	root  string
	now   func() time.Time
	newID func() string
}

func New(repo Repository, root string) (*Service, error) {
	if repo == nil || strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact repository and storage root are required")
	}
	return &Service{repo: repo, root: root, now: time.Now, newID: randomID}, nil
}

func (s *Service) Put(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path, contentType string, size int64, idempotencyKey string, body io.Reader) (domain.Artifact, bool, error) {
	if err := s.checkWritable(ctx, ownerID, instanceID); err != nil {
		return domain.Artifact{}, false, err
	}
	if size < 0 || size > MaxArtifactBytes {
		return domain.Artifact{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "size_bytes"}
	}
	if err := domain.ValidateArtifactPath(path); err != nil {
		return domain.Artifact{}, false, err
	}
	if err := s.repo.CheckArtifactUpload(ctx, ownerID, instanceID, path, size); err != nil {
		return domain.Artifact{}, false, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	id := domain.ArtifactID(s.newID())
	temp, hash, err := s.writeBlob(ownerID, instanceID, id, body, size)
	if err != nil {
		return domain.Artifact{}, false, err
	}
	now := s.now().UTC()
	artifact := domain.Artifact{
		ID: id, OwnerID: ownerID, InstanceID: instanceID, Path: path, SizeBytes: size,
		ContentType: contentType, SHA256: hash, CreatedAt: now, UpdatedAt: now,
	}
	stored, previous, replay, err := s.repo.PutArtifact(ctx, artifact, idempotencyKey)
	if err != nil {
		_ = os.Remove(temp)
		return domain.Artifact{}, false, err
	}
	if replay {
		_ = os.Remove(temp)
		return stored, true, nil
	}
	if err := os.Rename(temp, s.blobPath(ownerID, instanceID, id)); err != nil {
		_, _ = s.repo.DeleteArtifact(context.Background(), ownerID, instanceID, path)
		return domain.Artifact{}, false, fmt.Errorf("publish artifact blob: %w", err)
	}
	if previous != nil {
		_ = os.Remove(s.blobPath(ownerID, instanceID, previous.ID))
	}
	return stored, previous != nil, nil
}

func (s *Service) List(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, prefix string) ([]domain.Artifact, error) {
	if len(prefix) > MaxPrefixLength {
		return nil, &domain.Error{Code: domain.CodeInvalidArgument, Field: "prefix"}
	}
	if _, err := s.repo.GetInstance(ctx, ownerID, instanceID); err != nil {
		return nil, err
	}
	return s.repo.ListArtifacts(ctx, ownerID, instanceID, prefix)
}

func (s *Service) Get(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path string) (domain.Artifact, io.ReadCloser, error) {
	if err := domain.ValidateArtifactPath(path); err != nil {
		return domain.Artifact{}, nil, err
	}
	artifact, err := s.repo.GetArtifact(ctx, ownerID, instanceID, path)
	if err != nil {
		return domain.Artifact{}, nil, err
	}
	file, err := os.Open(s.blobPath(ownerID, instanceID, artifact.ID))
	if errors.Is(err, os.ErrNotExist) {
		return domain.Artifact{}, nil, &domain.Error{Code: domain.CodeNotFound, Field: "artifact"}
	}
	if err != nil {
		return domain.Artifact{}, nil, fmt.Errorf("open artifact blob: %w", err)
	}
	return artifact, file, nil
}

func (s *Service) Delete(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID, path string) error {
	if err := s.checkWritable(ctx, ownerID, instanceID); err != nil {
		return err
	}
	if err := domain.ValidateArtifactPath(path); err != nil {
		return err
	}
	artifact, err := s.repo.DeleteArtifact(ctx, ownerID, instanceID, path)
	if err != nil {
		return err
	}
	if err := os.Remove(s.blobPath(ownerID, instanceID, artifact.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove artifact blob: %w", err)
	}
	return nil
}

// DeleteAll runs after runtime cleanup and before instance metadata finalization.
func (s *Service) DeleteAll(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) error {
	if err := os.RemoveAll(s.instanceDir(ownerID, instanceID)); err != nil {
		return fmt.Errorf("remove instance artifact blobs: %w", err)
	}
	return s.repo.DeleteInstanceArtifacts(ctx, ownerID, instanceID)
}

func (s *Service) checkWritable(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) error {
	instance, err := s.repo.GetInstance(ctx, ownerID, instanceID)
	if err != nil {
		return err
	}
	if instance.ObservedState == domain.ObservedDeleting || instance.ObservedState == domain.ObservedDeleted {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "instance"}
	}
	return nil
}

func (s *Service) writeBlob(ownerID domain.OwnerID, instanceID domain.InstanceID, id domain.ArtifactID, body io.Reader, size int64) (string, string, error) {
	dir := s.instanceDir(ownerID, instanceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create artifact directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", "", fmt.Errorf("create artifact blob: %w", err)
	}
	name := file.Name()
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, size+1))
	if err != nil || written != size {
		_ = os.Remove(name)
		if err != nil {
			return "", "", fmt.Errorf("write artifact blob: %w", err)
		}
		return "", "", &domain.Error{Code: domain.CodeInvalidArgument, Field: "size_bytes"}
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(name)
		return "", "", fmt.Errorf("sync artifact blob: %w", err)
	}
	return name, hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Service) instanceDir(ownerID domain.OwnerID, instanceID domain.InstanceID) string {
	return filepath.Join(s.root, storageName(string(ownerID)), storageName(string(instanceID)))
}

func (s *Service) blobPath(ownerID domain.OwnerID, instanceID domain.InstanceID, id domain.ArtifactID) string {
	return filepath.Join(s.instanceDir(ownerID, instanceID), string(id))
}

func randomID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("artifact-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func storageName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
