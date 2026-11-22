package core

import (
	"context"

	"github.com/google/uuid"
)

type tenantContextKey struct{}

// WithWorkspaceOwner marks an authenticated coordinator's private workspace
// on the request context. Platform-admin calls intentionally leave it unset.
func WithWorkspaceOwner(ctx context.Context, owner uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, owner)
}

// WorkspaceOwner returns the coordinator ID that owns the current request's
// workspace. A missing value means a platform-admin or background operation.
func WorkspaceOwner(ctx context.Context) (uuid.UUID, bool) {
	owner, ok := ctx.Value(tenantContextKey{}).(uuid.UUID)
	return owner, ok && owner != uuid.Nil
}
