package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/repository"
	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/partials"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// The isolation check is ADR-034's RLS proof surface. It runs the visitor's
// own ListByUser twice through the same repository interface — once with the
// request's claims, once with a freshly minted identity nobody owns — so the
// visitor watches Postgres return their rows and then refuse the identical
// query (same SQL, same user_id parameter) to a stranger. No other visitor's
// data is read or counted: the stranger is synthetic and the rows are the
// visitor's own.

// errNoClaims marks a request whose context carries a user but no identity
// claims — nothing to prove against, so the check refuses rather than
// reporting a hollow pass.
var errNoClaims = errors.New("isolation check: no auth claims on request")

// isolationIdentity describes the requester for the panel without running
// the check.
func isolationIdentity(ctx context.Context, user *database.User, cardCount int) partials.IsolationProps {
	props := partials.IsolationProps{
		UserID:   truncateID(user.ID.String()),
		HasCards: cardCount > 0,
		OwnRows:  cardCount,
	}
	if claims, ok := webutil.AuthClaimsFromContext(ctx); ok {
		props.Sub = truncateID(claims.Sub)
		props.Role = claims.Role
		props.IsAnonymous = claims.IsAnonymous
	}
	return props
}

// runIsolationCheck performs both reads and returns the counts.
func runIsolationCheck(ctx context.Context, repo repository.FlashcardRepository, user *database.User) (partials.IsolationProps, error) {
	if _, ok := webutil.AuthClaimsFromContext(ctx); !ok {
		return partials.IsolationProps{}, errNoClaims
	}
	own, err := repo.ListByUser(ctx, user.ID)
	if err != nil {
		return partials.IsolationProps{}, fmt.Errorf("isolation check: list as requester: %w", err)
	}

	stranger := webutil.AuthClaims{Sub: uuid.NewString(), Role: webutil.RoleAuthenticated, IsAnonymous: true}
	foreign, err := repo.ListByUser(webutil.WithAuthClaims(ctx, stranger), user.ID)
	if err != nil {
		return partials.IsolationProps{}, fmt.Errorf("isolation check: list as stranger: %w", err)
	}

	props := isolationIdentity(ctx, user, len(own))
	props.Ran = true
	props.StrangerSub = truncateID(stranger.Sub)
	props.StrangerRows = len(foreign)
	return props, nil
}

// flashcardIsolation serves the HTMX fragment; a plain navigation is sent to
// the page with the check run, so the surface works without JavaScript.
func flashcardIsolation(repo repository.FlashcardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}
		if !view.IsHTMXRequest(r) {
			http.Redirect(w, r, "/learn/flashcards?check=1", http.StatusSeeOther)
			return
		}
		props, err := runIsolationCheck(r.Context(), repo, user)
		if err != nil {
			slog.Error("Isolation check failed", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		renderQuiz(w, r, http.StatusOK, partials.IsolationResult(props))
	}
}

// truncateID shortens a UUID-shaped identifier to its first group for
// display: recognisable, never the whole subject.
func truncateID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
