package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/clownware/go-performance-starter/internal/auth"
	"github.com/clownware/go-performance-starter/internal/database"
	mw "github.com/clownware/go-performance-starter/internal/middleware"
	"github.com/clownware/go-performance-starter/internal/validate"
	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/pages"
	"github.com/clownware/go-performance-starter/internal/view/partials"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// anonUpgrader is the slice of AuthClient the upgrade flow needs; an
// interface so tests can fake the GoTrue calls (same seam as GuestSession).
type anonUpgrader interface {
	UpgradeAnonymousUser(ctx context.Context, accessToken, email, password string) (*auth.UpgradeResult, error)
	RefreshSession(ctx context.Context, refreshToken string) (*auth.AnonSession, error)
}

// UpgradeRoutes registers the guest → registered upgrade flow (ADR-024, #68)
// on the /learn group. The credential-setting POST gets the strict rate tier
// like the other credential endpoints (ADR-014 §4). ctx bounds the tier's
// background eviction (#117); the server passes its lifecycle context.
func UpgradeRoutes(ctx context.Context, r chi.Router, upgrader anonUpgrader, secureCookie bool) {
	r.Get("/learn/upgrade", UpgradePage)
	r.Group(func(strict chi.Router) {
		strict.Use(mw.RateLimiter(ctx, 5.0/60.0, 5))
		strict.Post("/learn/upgrade", UpgradeSubmit(upgrader, secureCookie))
	})
}

// UpgradePage renders the upgrade form for guests, or the already-registered
// state for accounts that have completed the upgrade.
func UpgradePage(w http.ResponseWriter, r *http.Request) {
	user := webutil.GetUserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
		return
	}
	props := pages.UpgradePageProps{
		BaseProps:  view.NewBaseProps("Keep your progress"),
		Registered: !user.IsAnonymous,
	}
	renderUpgrade(w, r, http.StatusOK, pages.UpgradePage(props))
}

// UpgradeSubmit attaches an email/password identity to the current anonymous
// session. The auth.uid() is unchanged, so every RLS-scoped row survives —
// that is the promise the guest banner makes.
func UpgradeSubmit(upgrader anonUpgrader, secureCookie bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			JSONError(w, http.StatusBadRequest, err)
			return
		}
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}
		if !user.IsAnonymous {
			props := pages.UpgradePageProps{
				BaseProps:  view.NewBaseProps("Keep your progress"),
				Registered: true,
			}
			renderUpgrade(w, r, http.StatusOK, pages.UpgradePage(props))
			return
		}

		email := strings.TrimSpace(r.PostFormValue("email"))
		password := r.PostFormValue("password")
		formErrors := make(map[string]string)
		if err := validate.Email(email); err != nil {
			formErrors["email"] = "That doesn't look like an email address."
		}
		if len(password) < 8 {
			formErrors["password"] = "Password must be at least 8 characters."
		}
		if len(formErrors) > 0 {
			renderUpgradeForm(w, r, http.StatusUnprocessableEntity,
				partials.UpgradeFormProps{Email: email, Errors: formErrors})
			return
		}

		accessCookie, err := r.Cookie("sb-access-token")
		if err != nil || accessCookie.Value == "" {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}

		result, err := upgrader.UpgradeAnonymousUser(r.Context(), accessCookie.Value, email, password)
		if err != nil {
			if errors.Is(err, auth.ErrEmailInUse) {
				formErrors["email"] = "That email address is already registered — sign in to that account instead."
			} else {
				slog.Error("Guest upgrade failed", "user_id", user.ID, "error", err)
				formErrors["form"] = "Something went wrong saving your account — please try again."
			}
			renderUpgradeForm(w, r, http.StatusUnprocessableEntity,
				partials.UpgradeFormProps{Email: email, Errors: formErrors})
			return
		}

		// Promote the row so the reaper can't touch it, and replace the
		// guest placeholder email with the real one. Best-effort: the
		// loader's claims-sync heals a missed promotion on the next request,
		// after the session below is refreshed to non-anonymous claims.
		if repo := webutil.GetUserRepoFromContext(r.Context()); repo != nil {
			if err := repo.SetAnonymous(r.Context(), user.ID, false); err != nil {
				slog.Error("Failed to promote upgraded users row (loader will heal)", "user_id", user.ID, "error", err)
			}
			if _, err := repo.Update(r.Context(), database.UpdateUserParams{ID: user.ID, Email: email}); err != nil {
				slog.Error("Failed to sync upgraded email to users row", "user_id", user.ID, "error", err)
			}
		}

		// Refresh so the session's is_anonymous claim reflects the upgrade
		// now rather than at token expiry. Non-fatal: the old token remains
		// valid and the loader heals on the next natural refresh.
		if refreshCookie, err := r.Cookie("sb-refresh-token"); err == nil && refreshCookie.Value != "" {
			if session, err := upgrader.RefreshSession(r.Context(), refreshCookie.Value); err == nil {
				setSessionCookies(w, session, secureCookie)
			} else {
				slog.Warn("Post-upgrade session refresh failed", "user_id", user.ID, "error", err)
			}
		}

		slog.Info("Guest upgraded to registered account", "user_id", user.ID, "confirmation_sent", result.ConfirmationSent)
		formProps := partials.UpgradeFormProps{
			Email:            email,
			Success:          true,
			ConfirmationSent: result.ConfirmationSent,
		}
		if view.IsHTMXRequest(r) {
			view.SetHXTrigger(w, "Your progress is saved.")
			renderUpgrade(w, r, http.StatusOK, partials.UpgradeForm(formProps))
			return
		}
		props := pages.UpgradePageProps{
			BaseProps: view.NewBaseProps("Keep your progress"),
			Form:      formProps,
		}
		renderUpgrade(w, r, http.StatusOK, pages.UpgradePage(props))
	}
}

// renderUpgradeForm picks partial vs full page for form re-renders.
func renderUpgradeForm(w http.ResponseWriter, r *http.Request, status int, formProps partials.UpgradeFormProps) {
	if view.IsHTMXRequest(r) {
		renderUpgrade(w, r, status, partials.UpgradeForm(formProps))
		return
	}
	props := pages.UpgradePageProps{
		BaseProps: view.NewBaseProps("Keep your progress"),
		Form:      formProps,
	}
	renderUpgrade(w, r, status, pages.UpgradePage(props))
}

// setSessionCookies issues the httpOnly session pair with the same attributes
// as the guest-session and login flows, so every cookie mutation shares one
// security posture.
func setSessionCookies(w http.ResponseWriter, session *auth.AnonSession, secureCookie bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sb-access-token",
		Value:    session.AccessToken,
		Path:     "/",
		MaxAge:   session.ExpiresIn,
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "sb-refresh-token",
		Value:    session.RefreshToken,
		Path:     "/",
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
}

func renderUpgrade(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	if err := view.Render(w, r, status, c); err != nil {
		slog.Error("Failed to render upgrade view", "error", err)
	}
}
