package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/repository"
	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/pages"
	"github.com/clownware/go-performance-starter/internal/view/partials"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// The quiz is ADR-024 surface 3: the persisted, authenticated CRUD demo. All
// data access goes through repository.QuizRepository (ADR-003), whose postgres
// implementation runs every call inside the RLS-scoped inScope transaction
// (ADR-004) — the handler never sees SQL and never bypasses the scope.
//
// Mounted in server.setupRoutes under /learn behind GuestSession (when guest
// mode is enabled) → AuthMiddleware → UserLoader, so a cookie-less visitor
// gets an anonymous identity and the users row is JIT-provisioned before any
// handler runs; the group carries a stricter rate-limit tier because this is
// a public anonymous-writable surface (ADR-024).
//
// Flow (progressive enhancement, ADR-007/012): GET renders a full page with
// the question card; the answer form POSTs and works without JS (full result
// page), while HTMX swaps just the card. A wrong answer offers "save as
// flashcard" — the offer markup ships here, its endpoint ships in Slice B.
//
// Per-topic entry (#118): the landing explainer hands off with ?topic=<slug>.
// A known topic narrows the run to that topic's questions (landing on one the
// user has not attempted yet) and every link carries the scope forward; an
// unknown topic is ignored so a stray query never changes behaviour. The
// running score stays whole-quiz — it counts rows, not runs.

// QuizRoutes registers the quiz flow backed by the RLS-scoped quiz repository.
func QuizRoutes(r chi.Router, repo repository.QuizRepository) {
	r.Get("/learn/quiz", quizPage(repo))
	r.Post("/learn/quiz/{slug}/answer", quizAnswer(repo))
}

func quizPage(repo repository.QuizRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			// Browse-first: preview what the quiz is and why it needs an
			// identity instead of bouncing to the login page.
			if view.IsHTMXRequest(r) {
				renderQuiz(w, r, http.StatusOK, partials.QuizTeaser())
				return
			}
			props := pages.QuizPageProps{BaseProps: view.NewBaseProps("Architecture Quiz"), Teaser: true}
			renderQuiz(w, r, http.StatusOK, pages.QuizPage(props))
			return
		}

		questions, err := repo.ListQuestions(r.Context())
		if err != nil {
			slog.Error("Failed to list quiz questions", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if len(questions) == 0 {
			if view.IsHTMXRequest(r) {
				renderQuiz(w, r, http.StatusOK, partials.QuizEmptyState())
				return
			}
			props := pages.QuizPageProps{BaseProps: view.NewBaseProps("Architecture Quiz"), Empty: true}
			renderQuiz(w, r, http.StatusOK, pages.QuizPage(props))
			return
		}

		topic := resolveQuizTopic(questions, r.URL.Query().Get("topic"))
		run := quizRun(questions, topic)

		idx := 0
		if slug := r.URL.Query().Get("q"); slug != "" {
			var ok bool
			if idx, ok = questionIndexBySlug(run, slug); !ok {
				http.NotFound(w, r)
				return
			}
		} else if topic != "" {
			attempts, err := repo.ListAttemptsByUser(r.Context(), user.ID, quizTopicAttemptWindow, 0)
			if err != nil {
				slog.Error("Failed to list quiz attempts for topic entry", "topic", topic, "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			idx = firstUnattempted(run, attempts)
		}
		question := run[idx]

		choices, err := decodeQuizChoices(question.Choices)
		if err != nil {
			slog.Error("Malformed quiz choices", "slug", question.Slug, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		score, err := quizScore(r, repo, user, len(questions))
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		qProps := quizQuestionProps(question, choices, topic)
		if view.IsHTMXRequest(r) {
			renderQuiz(w, r, http.StatusOK, partials.QuizQuestionCard(qProps))
			return
		}
		props := pages.QuizPageProps{
			BaseProps:   view.NewBaseProps("Architecture Quiz"),
			GuestBanner: user.IsAnonymous,
			Question:    &qProps,
			Score:       score,
		}
		renderQuiz(w, r, http.StatusOK, pages.QuizPage(props))
	}
}

func quizAnswer(repo repository.QuizRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := webutil.GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/page", http.StatusSeeOther)
			return
		}

		questions, err := repo.ListQuestions(r.Context())
		if err != nil {
			slog.Error("Failed to list quiz questions", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		topic := resolveQuizTopic(questions, r.URL.Query().Get("topic"))
		run := quizRun(questions, topic)

		slug := chi.URLParam(r, "slug")
		idx, ok := questionIndexBySlug(run, slug)
		if !ok {
			http.NotFound(w, r)
			return
		}
		question := run[idx]

		choices, err := decodeQuizChoices(question.Choices)
		if err != nil {
			slog.Error("Malformed quiz choices", "slug", question.Slug, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		selected, err := strconv.Atoi(r.PostFormValue("choice"))
		if err != nil || selected < 0 || selected >= len(choices) {
			qProps := quizQuestionProps(question, choices, topic)
			qProps.Error = "Pick one of the answers before checking."
			if view.IsHTMXRequest(r) {
				renderQuiz(w, r, http.StatusUnprocessableEntity, partials.QuizQuestionCard(qProps))
				return
			}
			props := pages.QuizPageProps{
				BaseProps: view.NewBaseProps("Architecture Quiz"),
				Question:  &qProps,
				Score:     view.QuizScore{Total: len(questions)},
			}
			renderQuiz(w, r, http.StatusUnprocessableEntity, pages.QuizPage(props))
			return
		}

		isCorrect := int32(selected) == question.CorrectIndex
		if _, err := repo.RecordAttempt(r.Context(), database.CreateQuizAttemptParams{
			UserID:        user.ID,
			QuestionID:    question.ID,
			SelectedIndex: int32(selected),
			IsCorrect:     isCorrect,
		}); err != nil {
			slog.Error("Failed to record quiz attempt", "slug", question.Slug, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		score, err := quizScore(r, repo, user, len(questions))
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		resProps := partials.QuizResultProps{
			Correct:        isCorrect,
			Explanation:    question.Explanation,
			Done:           idx == len(run)-1,
			RunTopic:       topic,
			OfferFlashcard: !isCorrect,
			FlashcardFront: question.Prompt,
			FlashcardBack:  question.Explanation,
			Score:          score,
		}
		if !resProps.Done {
			resProps.NextSlug = run[idx+1].Slug
		}

		if view.IsHTMXRequest(r) {
			renderQuiz(w, r, http.StatusOK, partials.QuizResult(resProps))
			return
		}
		props := pages.QuizPageProps{
			BaseProps: view.NewBaseProps("Architecture Quiz"),
			Result:    &resProps,
			Score:     score,
		}
		renderQuiz(w, r, http.StatusOK, pages.QuizPage(props))
	}
}

// quizTopicAttemptWindow bounds the attempt history read on topic entry:
// far more than the seeded question count, without an unbounded scan.
const quizTopicAttemptWindow = 200

// resolveQuizTopic validates a requested topic against the question set.
// Unknown or empty topics resolve to "" — the full sequence — so a stray
// ?topic= neither changes behaviour nor gets echoed into links.
func resolveQuizTopic(questions []database.QuizQuestion, topic string) string {
	for i := range questions {
		if questions[i].Topic == topic {
			return topic
		}
	}
	return ""
}

// quizRun narrows the display order to one topic; "" keeps the whole sequence.
func quizRun(questions []database.QuizQuestion, topic string) []database.QuizQuestion {
	if topic == "" {
		return questions
	}
	run := make([]database.QuizQuestion, 0, len(questions))
	for _, q := range questions {
		if q.Topic == topic {
			run = append(run, q)
		}
	}
	return run
}

// firstUnattempted picks the topic-entry question: the first in the run the
// user has never attempted, or the first in the run once all are attempted.
func firstUnattempted(run []database.QuizQuestion, attempts []database.QuizAttempt) int {
	attempted := make(map[uuid.UUID]struct{}, len(attempts))
	for _, a := range attempts {
		attempted[a.QuestionID] = struct{}{}
	}
	for i := range run {
		if _, ok := attempted[run[i].ID]; !ok {
			return i
		}
	}
	return 0
}

// questionIndexBySlug finds a question's position in display order.
func questionIndexBySlug(questions []database.QuizQuestion, slug string) (int, bool) {
	for i := range questions {
		if questions[i].Slug == slug {
			return i, true
		}
	}
	return 0, false
}

// decodeQuizChoices unmarshals the jsonb choices column.
func decodeQuizChoices(raw []byte) ([]string, error) {
	var choices []string
	err := json.Unmarshal(raw, &choices)
	return choices, err
}

// quizScore computes the running score shown on every quiz surface.
func quizScore(r *http.Request, repo repository.QuizRepository, user *database.User, total int) (view.QuizScore, error) {
	correct, err := repo.CountCorrectByUser(r.Context(), user.ID)
	if err != nil {
		slog.Error("Failed to count correct attempts", "error", err)
		return view.QuizScore{}, err
	}
	return view.QuizScore{Correct: int(correct), Total: total}, nil
}

// quizQuestionProps maps a database question to its card props; runTopic is
// the topic scope the card's links must carry forward ("" for the full run).
func quizQuestionProps(q database.QuizQuestion, choices []string, runTopic string) partials.QuizQuestionProps {
	return partials.QuizQuestionProps{
		Slug:     q.Slug,
		Topic:    q.Topic,
		Prompt:   q.Prompt,
		Choices:  choices,
		RunTopic: runTopic,
	}
}

// renderQuiz renders a quiz component, logging render failures.
func renderQuiz(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	if err := view.Render(w, r, status, c); err != nil {
		slog.Error("Failed to render quiz view", "error", err)
	}
}
