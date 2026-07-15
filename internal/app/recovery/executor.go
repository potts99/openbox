// SPDX-License-Identifier: AGPL-3.0-only

// Package recovery connects durable operation claims to lifecycle recovery.
package recovery

import (
	"context"
	"errors"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/operations"
)

type InstanceRecoverer interface {
	RecoverOperation(context.Context, domain.Operation) error
}

type Executor struct{ Instances InstanceRecoverer }

func (e Executor) Execute(ctx context.Context, operation domain.Operation) error {
	err := e.Instances.RecoverOperation(ctx, operation)
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
