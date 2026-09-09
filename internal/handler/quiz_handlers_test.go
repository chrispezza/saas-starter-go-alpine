package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/repository"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// fakeQuizRepo is a hand-rolled repository.QuizRepository double: fixture
// questions, injectable errors, and a record of every attempt written — the
// handler must never touch SQL directly (ADR-003), so this is the full seam.
type fakeQuizRepo struct {
	questions    []database.QuizQuestion
	correctCount int64
	// history is the read-side attempt log (most recent first) served by
	// ListAttemptsByUser; attempts below records the write side.
	history []database.QuizAttempt

	listErr          error
	getErr           error
	recordErr        error
	countErr         error
	listAttemptsErr  error
	countAttemptsErr error

	attempts []database.CreateQuizAttemptParams
}

var _ repository.QuizRepository = (*fakeQuizRepo)(nil)

func (f *fakeQuizRepo) ListQuestions(_ context.Context) ([]database.QuizQuestion, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.questions, nil
}

func (f *fakeQuizRepo) GetQuestion(_ context.Context, id uuid.UUID) (*database.QuizQuestion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.questions {
		if f.questions[i].ID == id {
			return &f.questions[i], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeQuizRepo) GetQuestionBySlug(_ context.Context, slug string) (*database.QuizQuestion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.questions {
		if f.questions[i].Slug == slug {
			return &f.questions[i], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeQuizRepo) RecordAttempt(_ context.Context, params database.CreateQuizAttemptParams) (*database.QuizAttempt, error) {
	if f.recordErr != nil {
		return nil, f.recordErr
	}
	f.attempts = append(f.attempts, params)
	return &database.QuizAttempt{
		ID:            uuid.New(),
		UserID:        params.UserID,
		QuestionID:    params.QuestionID,
		SelectedIndex: params.SelectedIndex,
		IsCorrect:     params.IsCorrect,
	}, nil
}

func (f *fakeQuizRepo) ListAttemptsByUser(_ context.Context, _ uuid.UUID, limit, offset int32) ([]database.QuizAttempt, error) {
	if f.listAttemptsErr != nil {
		return nil, f.listAttemptsErr
	}
	start := min(int(offset), len(f.history))
	end := min(start+int(limit), len(f.history))
	return f.history[start:end], nil
}

func (f *fakeQuizRepo) CountCorrectByUser(_ context.Context, _ uuid.UUID) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.correctCount, nil
}

func (f *fakeQuizRepo) CountAttemptsByUser(_ context.Context, _ uuid.UUID) (int64, error) {
	if f.countAttemptsErr != nil {
		return 0, f.countAttemptsErr
	}
	return int64(len(f.history)), nil
}

// quizFixtures returns three seeded questions in display order, mirroring the
// explainer topics (demo-design-brief.md content map).
func quizFixtures() []database.QuizQuestion {
	return []database.QuizQuestion{
		{
			ID:           uuid.New(),
			Slug:         "middleware-stack",
			Topic:        "routing",
			Prompt:       "Which router assembles the middleware stack?",
			Choices:      []byte(`["Chi","Gin","Echo","net/http mux"]`),
			CorrectIndex: 0,
			Explanation:  "Chi composes the middleware stack in server.setupMiddleware.",
		},
		{
			ID:           uuid.New(),
			Slug:         "rls-scoping",
			Topic:        "database",
			Prompt:       "What scopes every query to the requesting user?",
			Choices:      []byte(`["Row Level Security","WHERE clauses in handlers","An ORM hook"]`),
			CorrectIndex: 0,
			Explanation:  "RLS policies evaluate auth.uid() inside the scoped transaction.",
		},
		{
			ID:           uuid.New(),
			Slug:         "performance-budgets",
			Topic:        "performance",
			Prompt:       "Where are the performance budgets enforced?",
			Choices:      []byte(`["CI via task ci","A wiki page","Nowhere"]`),
			CorrectIndex: 0,
			Explanation:  "task ci fails the build when a budget is exceeded.",
		},
	}
}

// quizTopicFixtures extends quizFixtures with a second "database" question so
// the per-topic run (#118) has an order to walk and an unanswered one to prefer.
func quizTopicFixtures() []database.QuizQuestion {
	return append(quizFixtures(), database.QuizQuestion{
		ID:           uuid.New(),
		Slug:         "rls-policies",
		Topic:        "database",
		Prompt:       "Where do the RLS policies live?",
		Choices:      []byte(`["In migrations","In handlers","In templ"]`),
		CorrectIndex: 0,
		Explanation:  "Policies are versioned SQL in migrations/.",
	})
}

func newQuizRouter(repo repository.QuizRepository) http.Handler {
	r := chi.NewRouter()
	QuizRoutes(r, repo)
	return r
}

// asQuizUser attaches the loaded users row and validated claims, exactly what
// the GuestSession → AuthMiddleware → UserLoader chain provides in production.
func asQuizUser(r *http.Request, user *database.User) *http.Request {
	if user == nil {
		return r
	}
	ctx := webutil.WithUser(r.Context(), user)
	ctx = webutil.WithAuthClaims(ctx, webutil.AuthClaims{
		Sub:         "auth-" + user.ID.String(),
		Role:        webutil.RoleAuthenticated,
		IsAnonymous: true, // guests are the default audience (ADR-024)
	})
	return r.WithContext(ctx)
}

func TestQuizPage(t *testing.T) {
	questions := quizFixtures()
	user := &database.User{ID: uuid.New(), IsAnonymous: true}
	topicQuestions := quizTopicFixtures()
	// answeredFirst has attempted the first database question only.
	answeredFirst := []database.QuizAttempt{{QuestionID: topicQuestions[1].ID, UserID: user.ID}}
	// answeredAll has attempted both database questions.
	answeredAll := []database.QuizAttempt{
		{QuestionID: topicQuestions[3].ID, UserID: user.ID},
		{QuestionID: topicQuestions[1].ID, UserID: user.ID},
	}

	tests := []struct {
		name         string
		target       string
		repo         *fakeQuizRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:       "defaults to the first question with a running score",
			target:     "/learn/quiz",
			repo:       &fakeQuizRepo{questions: questions, correctCount: 1},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-question"`,
				"Which router assembles the middleware stack?",
				"Chi", "Gin", "Echo", "net/http mux",
				`data-testid="quiz-score"`,
				"1 of 3",
			},
		},
		{
			name:       "selects a question by slug",
			target:     "/learn/quiz?q=rls-scoping",
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"What scopes every query to the requesting user?",
			},
			wantAbsent: []string{
				"Which router assembles the middleware stack?",
			},
		},
		{
			name:       "full page render for direct navigation",
			target:     "/learn/quiz",
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"<!doctype",
			},
		},
		{
			name:       "HTMX request swaps just the question card",
			target:     "/learn/quiz?q=rls-scoping",
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-question"`,
			},
			wantAbsent: []string{
				"<!doctype",
			},
		},
		{
			name:       "unknown slug is a 404",
			target:     "/learn/quiz?q=no-such-question",
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty question set renders the empty state",
			target:     "/learn/quiz",
			repo:       &fakeQuizRepo{},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-empty"`,
			},
		},
		{
			name:       "repository failure is a 500",
			target:     "/learn/quiz",
			repo:       &fakeQuizRepo{listErr: errFake},
			user:       user,
			wantStatus: http.StatusInternalServerError,
		},
		{
			// Signed-out browsing gets a preview that sells the sign-in —
			// what the quiz is and why progress needs an identity — instead
			// of a blind redirect (mutations still redirect; see answer test).
			name:       "missing user sees the teaser with a sign-in call to action",
			target:     "/learn/quiz",
			repo:       &fakeQuizRepo{questions: questions},
			user:       nil,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-teaser"`,
				`href="/auth/page"`,
			},
			wantAbsent: []string{
				`data-testid="quiz-question"`,
			},
		},
		// Per-topic entry (#118): the explainer hands off with ?topic=<slug> and
		// the quiz lands on that topic instead of the generic first question.
		{
			name:       "topic entry lands on the first question of that topic",
			target:     "/learn/quiz?topic=database",
			repo:       &fakeQuizRepo{questions: topicQuestions},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"What scopes every query to the requesting user?",
				`action="/learn/quiz/rls-scoping/answer?topic=database"`,
			},
			wantAbsent: []string{
				"Which router assembles the middleware stack?",
			},
		},
		{
			name:       "topic entry prefers a question the user has not attempted",
			target:     "/learn/quiz?topic=database",
			repo:       &fakeQuizRepo{questions: topicQuestions, history: answeredFirst},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"Where do the RLS policies live?",
				`action="/learn/quiz/rls-policies/answer?topic=database"`,
			},
			wantAbsent: []string{
				"What scopes every query to the requesting user?",
			},
		},
		{
			name:       "topic entry with every question attempted restarts the topic",
			target:     "/learn/quiz?topic=database",
			repo:       &fakeQuizRepo{questions: topicQuestions, history: answeredAll},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"What scopes every query to the requesting user?",
			},
		},
		{
			name:       "explicit slug inside the topic keeps the topic scope",
			target:     "/learn/quiz?topic=database&q=rls-policies",
			repo:       &fakeQuizRepo{questions: topicQuestions},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"Where do the RLS policies live?",
				`action="/learn/quiz/rls-policies/answer?topic=database"`,
			},
		},
		{
			name:       "slug outside the topic is a 404",
			target:     "/learn/quiz?topic=database&q=middleware-stack",
			repo:       &fakeQuizRepo{questions: topicQuestions},
			user:       user,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown topic falls back to the full sequence",
			target:     "/learn/quiz?topic=no-such-topic",
			repo:       &fakeQuizRepo{questions: topicQuestions},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"Which router assembles the middleware stack?",
				`action="/learn/quiz/middleware-stack/answer"`,
			},
			wantAbsent: []string{
				"topic=",
			},
		},
		{
			name:       "attempt history failure on topic entry is a 500",
			target:     "/learn/quiz?topic=database",
			repo:       &fakeQuizRepo{questions: topicQuestions, listAttemptsErr: errFake},
			user:       user,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			req = asQuizUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newQuizRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", tt.target, w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("GET %s Location = %q, want %q", tt.target, got, tt.wantLocation)
				}
			}
			body := strings.ToLower(w.Body.String())
			for _, want := range tt.wantContains {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("GET %s body missing %q", tt.target, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(body, strings.ToLower(absent)) {
					t.Errorf("GET %s body unexpectedly contains %q", tt.target, absent)
				}
			}
		})
	}
}

func TestQuizAnswer(t *testing.T) {
	questions := quizFixtures()
	user := &database.User{ID: uuid.New(), IsAnonymous: true}

	tests := []struct {
		name         string
		slug         string
		query        string // raw query string appended to the answer URL (topic scope)
		form         url.Values
		repo         *fakeQuizRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantAbsent   []string
		wantAttempt  *database.CreateQuizAttemptParams // nil = no attempt recorded
	}{
		{
			name:       "correct answer records the attempt and explains",
			slug:       "middleware-stack",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-result"`,
				"Chi composes the middleware stack in server.setupMiddleware.",
				"/learn/quiz?q=rls-scoping", // link to the next question
			},
			wantAbsent: []string{
				`data-testid="save-flashcard-offer"`,
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "wrong answer offers to save a flashcard",
			slug:       "rls-scoping",
			form:       url.Values{"choice": {"2"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-result"`,
				"RLS policies evaluate auth.uid() inside the scoped transaction.",
				`data-testid="save-flashcard-offer"`,
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 2,
				IsCorrect:     false,
			},
		},
		{
			name:       "last question marks the quiz done",
			slug:       "performance-budgets",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-done"`,
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "non-HTMX submit falls back to a full result page",
			slug:       "middleware-stack",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       false,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"<!doctype",
				`data-testid="quiz-result"`,
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "unknown question slug is a 404 and records nothing",
			slug:       "no-such-question",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing choice re-renders with 422 and records nothing",
			slug:       "middleware-stack",
			form:       url.Values{},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "non-numeric choice is a 422 and records nothing",
			slug:       "middleware-stack",
			form:       url.Values{"choice": {"first"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "out-of-range choice is a 422 and records nothing",
			slug:       "middleware-stack",
			form:       url.Values{"choice": {"9"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "record failure is a 500",
			slug:       "middleware-stack",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions, recordErr: errFake},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:         "missing user redirects to auth and records nothing",
			slug:         "middleware-stack",
			form:         url.Values{"choice": {"0"}},
			repo:         &fakeQuizRepo{questions: questions},
			user:         nil,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
		// Topic-scoped runs (#118): next/done are computed within the topic
		// and every link carries the scope forward.
		{
			name:       "topic-scoped answer links to the next question in the topic",
			slug:       "rls-scoping",
			query:      "?topic=database",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: quizTopicFixtures()},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-result"`,
				"/learn/quiz?q=rls-policies&amp;topic=database",
				"0 of 4", // the score stays whole-quiz, not per topic
			},
			wantAbsent: []string{
				`data-testid="quiz-done"`,
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "last question of a topic marks the topic run done",
			slug:       "rls-scoping",
			query:      "?topic=database",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="quiz-done"`,
				"last database question",
				`href="/learn/quiz"`, // continue with the whole sequence
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "unknown topic on answer falls back to the full sequence",
			slug:       "middleware-stack",
			query:      "?topic=no-such-topic",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"/learn/quiz?q=rls-scoping",
			},
			wantAbsent: []string{
				"topic=",
			},
			wantAttempt: &database.CreateQuizAttemptParams{
				UserID:        user.ID,
				SelectedIndex: 0,
				IsCorrect:     true,
			},
		},
		{
			name:       "slug outside the topic is a 404 and records nothing",
			slug:       "middleware-stack",
			query:      "?topic=database",
			form:       url.Values{"choice": {"0"}},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid choice re-render keeps the topic scope",
			slug:       "rls-scoping",
			query:      "?topic=database",
			form:       url.Values{},
			repo:       &fakeQuizRepo{questions: questions},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
			wantContains: []string{
				`action="/learn/quiz/rls-scoping/answer?topic=database"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/learn/quiz/" + tt.slug + "/answer" + tt.query
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = asQuizUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newQuizRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("POST %s status = %d, want %d", target, w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("POST %s Location = %q, want %q", target, got, tt.wantLocation)
				}
			}

			body := strings.ToLower(w.Body.String())
			for _, want := range tt.wantContains {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("POST %s body missing %q", target, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(body, strings.ToLower(absent)) {
					t.Errorf("POST %s body unexpectedly contains %q", target, absent)
				}
			}

			if tt.wantAttempt == nil {
				if len(tt.repo.attempts) != 0 {
					t.Fatalf("POST %s recorded %d attempt(s), want none", target, len(tt.repo.attempts))
				}
				return
			}
			if len(tt.repo.attempts) != 1 {
				t.Fatalf("POST %s recorded %d attempt(s), want exactly 1", target, len(tt.repo.attempts))
			}
			got := tt.repo.attempts[0]
			wantQuestion, err := tt.repo.GetQuestionBySlug(context.Background(), tt.slug)
			if err != nil {
				t.Fatalf("fixture question %q missing: %v", tt.slug, err)
			}
			if got.QuestionID != wantQuestion.ID {
				t.Errorf("attempt QuestionID = %s, want %s", got.QuestionID, wantQuestion.ID)
			}
			if got.UserID != tt.wantAttempt.UserID {
				t.Errorf("attempt UserID = %s, want %s", got.UserID, tt.wantAttempt.UserID)
			}
			if got.SelectedIndex != tt.wantAttempt.SelectedIndex {
				t.Errorf("attempt SelectedIndex = %d, want %d", got.SelectedIndex, tt.wantAttempt.SelectedIndex)
			}
			if got.IsCorrect != tt.wantAttempt.IsCorrect {
				t.Errorf("attempt IsCorrect = %v, want %v", got.IsCorrect, tt.wantAttempt.IsCorrect)
			}
		})
	}
}

// errFake is a sentinel for injected repository failures.
var errFake = errors.New("boom")
