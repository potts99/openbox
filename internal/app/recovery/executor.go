// SPDX-License-Identifier: AGPL-3.0-only

// Package recovery connects durable operation claims to lifecycle recovery.
package recovery

import (
	"context"
	"errors"
	"strings"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
)

type InstanceRecoverer interface {
	RecoverOperation(context.Context, domain.Operation) error
}

type SnapshotRecoverer interface {
	RecoverOperation(context.Context, domain.Operation) error
}

type CloneRecoverer interface {
	RecoverOperation(context.Context, domain.Operation) error
}

type Executor struct {
	Instances InstanceRecoverer
	Snapshots SnapshotRecoverer
	Clones    CloneRecoverer
}

func (e Executor) Execute(ctx context.Context, operation domain.Operation) error {
	var err error
	switch {
	case strings.HasPrefix(operation.Type, "snapshot."):
		if e.Snapshots == nil {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
		}
		err = e.Snapshots.RecoverOperation(ctx, operation)
	case operation.Type == "instance.copy":
		if e.Clones == nil {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
		}
		err = e.Clones.RecoverOperation(ctx, operation)
	default:
		if e.Instances == nil {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
		}
		err = e.Instances.RecoverOperation(ctx, operation)
	}
	if err == nil {
		return nil
	}
	var identity *instances.IdentityConflictError
	if errors.As(err, &identity) {
		return operations.IntegrityError(domain.CodeConflict, err)
	}
	var capability *instances.CapabilityError
	if errors.As(err, &capability) {
		return operations.CorrectableError(domain.CodeInvalidArgument, err)
	}
	return err
}
