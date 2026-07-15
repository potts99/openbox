// SPDX-License-Identifier: AGPL-3.0-only

package operations

import (
	"context"

	"github.com/openbox-dev/openbox/internal/domain"
)

type Claim struct {
	OwnerID     domain.OwnerID
	OperationID domain.OperationID
	WorkerID    string
	Token       string
}

type claimContextKey struct{}

func WithClaim(ctx context.Context, claim Claim) context.Context {
	return context.WithValue(ctx, claimContextKey{}, claim)
}

func ClaimFromContext(ctx context.Context) (Claim, bool) {
	claim, ok := ctx.Value(claimContextKey{}).(Claim)
	return claim, ok && claim.Token != ""
}
