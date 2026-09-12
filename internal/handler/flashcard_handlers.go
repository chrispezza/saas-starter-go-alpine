package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/repository"
	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/pages"
	"github.com/clownware/go-performance-starter/internal/view/partials"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// Flashcards complete ADR-024 surface 3: user-owned rows created from wrong
// quiz answers (the quiz result card posts here), reviewed, marked known, and
// deleted. All access goes through repository.FlashcardRepository (ADR-003),
// RLS-scoped via inScope (ADR-004). Mounted in the same /learn group as the
// quiz (GuestSession → AuthMiddleware → UserLoader + stricter rate limit).
//
// Progressive enhancement (ADR-007/012): every mutation is a plain POST form
// that redirects back to the list without JS; with HTMX the card (or offer
// form) swaps in place and a toast fires via HX-Trigger. Mark-known carries
// the target state in the form because the repository API is
// SetKnown(id, bool), not a blind toggle. Delete is idempotent end-to-end —
// the repository deliberately doesn't distinguish missing rows.

// flashcardFieldMax caps the front/back text accepted at the boundary; quiz
// prompts and explanations fit comfortably, abuse doesn't.
const flashcardFieldMax = 500

// FlashcardRoutes registers the flashcard review flow backed by the
// RLS-scoped flashcard repository.
func FlashcardRoutes(r chi.Router, repo repository.FlashcardRepository) {
	r.Get("/learn/flashcards", flashcardsPage(repo))
	r.Post("/learn/flashcards", flashcardCreate(repo))
	r.Post("/learn/flashcards/{id}/known", flashcardSetKnown(repo))
	r.Post("/learn/flashcards/{id}/delete", flashcardDelete(repo))
	// RLS isolation check (ADR-034): the proof lives next to the rows it proves.
	r.Get("/learn/flashcards/isolation", flashcardIsolation(repo))
}

func flashcardsPage(repo repository.FlashcardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			// Browse-first: preview why flashcards need an identity instead
			// of bouncing to the login page.
			if view.IsHTMXRequest(r) {
				renderQuiz(w, r, http.StatusOK, partials.FlashcardsTeaser())
				return
			}
			props := pages.FlashcardsPageProps{BaseProps: view.NewBaseProps("Flashcards"), Teaser: true}
			renderQuiz(w, r, http.StatusOK, pages.FlashcardsPage(props))
			return
		}

		cards, err := repo.ListByUser(r.Context(), user.ID)
		if err != nil {
			slog.Error("Failed to list flashcards", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		listProps := partials.FlashcardListProps{Cards: flashcardProps(cards)}
		if view.IsHTMXRequest(r) {
			renderQuiz(w, r, http.StatusOK, partials.FlashcardList(listProps))
			return
		}

		// ?check=1 is the no-JavaScript path of the isolation check (ADR-034).
		isolation := isolationIdentity(r.Context(), user, len(cards))
		if r.URL.Query().Get("check") == "1" && isolation.HasCards {
			isolation, err = runIsolationCheck(r.Context(), repo, user)
			if err != nil {
				slog.Error("Isolation check failed", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}
		props := pages.FlashcardsPageProps{
			BaseProps:   view.NewBaseProps("Flashcards"),
			GuestBanner: user.IsAnonymous,
			Cards:       listProps.Cards,
			Isolation:   isolation,
		}
		renderQuiz(w, r, http.StatusOK, pages.FlashcardsPage(props))
	}
}

func flashcardCreate(repo repository.FlashcardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}

		front := strings.TrimSpace(r.PostFormValue("front"))
		back := strings.TrimSpace(r.PostFormValue("back"))
		if front == "" || back == "" || len(front) > flashcardFieldMax || len(back) > flashcardFieldMax {
			renderQuiz(w, r, http.StatusUnprocessableEntity,
				partials.FlashcardSaveError("A flashcard needs both a front and a back (500 characters max)."))
			return
		}

		if _, err := repo.Create(r.Context(), database.CreateFlashcardParams{
			UserID: user.ID,
			Front:  front,
			Back:   back,
		}); err != nil {
			slog.Error("Failed to create flashcard", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if view.IsHTMXRequest(r) {
			view.SetHXTrigger(w, "Flashcard saved.")
			renderQuiz(w, r, http.StatusOK, partials.FlashcardSaved())
			return
		}
		http.Redirect(w, r, "/learn/flashcards", http.StatusSeeOther)
	}
}

func flashcardSetKnown(repo repository.FlashcardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		known := r.PostFormValue("known") == "true"
		card, err := repo.SetKnown(r.Context(), id, known)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("Failed to set flashcard known state", "id", id, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if view.IsHTMXRequest(r) {
			renderQuiz(w, r, http.StatusOK, partials.FlashcardCard(oneFlashcardProps(*card)))
			return
		}
		http.Redirect(w, r, "/learn/flashcards", http.StatusSeeOther)
	}
}

func flashcardDelete(repo repository.FlashcardRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}

		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if err := repo.Delete(r.Context(), id, user.ID); err != nil {
			slog.Error("Failed to delete flashcard", "id", id, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if view.IsHTMXRequest(r) {
			// Empty 200 body: the card's outerHTML swap removes it.
			view.SetHXTrigger(w, "Flashcard deleted.")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/learn/flashcards", http.StatusSeeOther)
	}
}

// flashcardProps maps database rows to card props.
func flashcardProps(cards []database.Flashcard) []partials.FlashcardProps {
	props := make([]partials.FlashcardProps, 0, len(cards))
	for _, card := range cards {
		props = append(props, oneFlashcardProps(card))
	}
	return props
}

func oneFlashcardProps(card database.Flashcard) partials.FlashcardProps {
	return partials.FlashcardProps{
		ID:      card.ID.String(),
		Front:   card.Front,
		Back:    card.Back,
		IsKnown: card.IsKnown,
	}
}
