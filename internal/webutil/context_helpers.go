package webutil

import (
	"context"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/repository"
)

// context keys (unexported)
type contextKey string

const (
	userContextKey contextKey = "user"
	repoContextKey contextKey = "userRepo"
)

// WithUser stores the user in the context.
func WithUser(ctx context.Context, user *database.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// GetUserFromContext retrieves the user from the context.
func GetUserFromContext(ctx context.Context) *database.User {
	user, _ := ctx.Value(userContextKey).(*database.User)
	return user
}

// WithUserRepo stores the user repo in the context.
func WithUserRepo(ctx context.Context, repo repository.UserRepository) context.Context {
	return context.WithValue(ctx, repoContextKey, repo)
}

// GetUserRepoFromContext retrieves the user repo from the context.
func GetUserRepoFromContext(ctx context.Context) repository.UserRepository {
	repo, _ := ctx.Value(repoContextKey).(repository.UserRepository)
	return repo
}

const requestPathContextKey contextKey = "requestPath"

// WithRequestPath stores the request's URL path (no query string) so the
// layout can derive canonical URLs; view.Render sets it for every page.
func WithRequestPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, requestPathContextKey, path)
}

// RequestPathFromContext returns the stored request path, or "" when the
// component is rendered outside a request.
func RequestPathFromContext(ctx context.Context) string {
	path, _ := ctx.Value(requestPathContextKey).(string)
	return path
}
