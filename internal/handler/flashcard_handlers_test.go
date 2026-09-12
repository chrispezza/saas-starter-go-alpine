package handler

import (
	"context"
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

// fakeFlashcardRepo is a hand-rolled repository.FlashcardRepository double:
// fixture cards, injectable errors, and a record of every mutation — the
// handler must never touch SQL directly (ADR-003), so this is the full seam.
type fakeFlashcardRepo struct {
	cards []database.Flashcard
	// scoped makes ListByUser behave like RLS: rows come back only when the
	// context claims name the owner (asFlashcardUser's "auth-<id>" sub).
	scoped bool

	createErr error
	listErr   error
	setErr    error
	deleteErr error

	created []database.CreateFlashcardParams
	known   []setKnownCall
	deleted []deleteCall
}

type setKnownCall struct {
	id    uuid.UUID
	known bool
}

type deleteCall struct {
	id     uuid.UUID
	userID uuid.UUID
}

var _ repository.FlashcardRepository = (*fakeFlashcardRepo)(nil)

func (f *fakeFlashcardRepo) Create(_ context.Context, params database.CreateFlashcardParams) (*database.Flashcard, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, params)
	return &database.Flashcard{
		ID:     uuid.New(),
		UserID: params.UserID,
		Front:  params.Front,
		Back:   params.Back,
	}, nil
}

func (f *fakeFlashcardRepo) Get(_ context.Context, id uuid.UUID) (*database.Flashcard, error) {
	for i := range f.cards {
		if f.cards[i].ID == id {
			return &f.cards[i], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeFlashcardRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]database.Flashcard, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.scoped {
		claims, ok := webutil.AuthClaimsFromContext(ctx)
		if !ok || claims.Sub != "auth-"+userID.String() {
			return nil, nil // RLS: the policy hides rows the identity does not own
		}
	}
	return f.cards, nil
}

func (f *fakeFlashcardRepo) SetKnown(_ context.Context, id uuid.UUID, known bool) (*database.Flashcard, error) {
	if f.setErr != nil {
		return nil, f.setErr
	}
	for i := range f.cards {
		if f.cards[i].ID == id {
			f.known = append(f.known, setKnownCall{id: id, known: known})
			card := f.cards[i]
			card.IsKnown = known
			return &card, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeFlashcardRepo) Delete(_ context.Context, id, userID uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	// Idempotent by design: no ErrNotFound for missing rows.
	f.deleted = append(f.deleted, deleteCall{id: id, userID: userID})
	return nil
}

func flashcardFixtures(userID uuid.UUID) []database.Flashcard {
	return []database.Flashcard{
		{
			ID:      uuid.New(),
			UserID:  userID,
			Front:   "Which router assembles the middleware stack?",
			Back:    "Chi composes the middleware stack in server.setupMiddleware.",
			IsKnown: false,
		},
		{
			ID:      uuid.New(),
			UserID:  userID,
			Front:   "What scopes every query to the requesting user?",
			Back:    "RLS policies evaluate auth.uid() inside the scoped transaction.",
			IsKnown: true,
		},
	}
}

func newFlashcardRouter(repo repository.FlashcardRepository) http.Handler {
	r := chi.NewRouter()
	FlashcardRoutes(r, repo)
	return r
}

// asFlashcardUser mirrors what the /learn identity chain provides.
func asFlashcardUser(r *http.Request, user *database.User) *http.Request {
	if user == nil {
		return r
	}
	ctx := webutil.WithUser(r.Context(), user)
	ctx = webutil.WithAuthClaims(ctx, webutil.AuthClaims{
		Sub:         "auth-" + user.ID.String(),
		Role:        webutil.RoleAuthenticated,
		IsAnonymous: true,
	})
	return r.WithContext(ctx)
}

func TestFlashcardsPage(t *testing.T) {
	user := &database.User{ID: uuid.New(), IsAnonymous: true}
	cards := flashcardFixtures(user.ID)

	tests := []struct {
		name         string
		repo         *fakeFlashcardRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:       "lists saved cards with their known state",
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				"<!doctype",
				`data-testid="flashcard"`,
				`data-testid="isolation-panel"`, // ADR-034: the RLS proof lives next to the rows it proves
				"Which router assembles the middleware stack?",
				"What scopes every query to the requesting user?",
				`data-known="true"`,
				`data-known="false"`,
			},
		},
		{
			name:       "empty collection renders the empty state",
			repo:       &fakeFlashcardRepo{},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="flashcards-empty"`,
			},
			wantAbsent: []string{
				`data-testid="flashcard"`,
			},
		},
		{
			name:       "HTMX request swaps just the list",
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="flashcard"`,
			},
			wantAbsent: []string{
				"<!doctype",
			},
		},
		{
			name:       "repository failure is a 500",
			repo:       &fakeFlashcardRepo{listErr: errFake},
			user:       user,
			wantStatus: http.StatusInternalServerError,
		},
		{
			// Signed-out browsing gets a preview that sells the sign-in
			// instead of a blind redirect (mutations still redirect).
			name:       "missing user sees the teaser with a sign-in call to action",
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       nil,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="flashcards-teaser"`,
				`href="/auth/page"`,
			},
			wantAbsent: []string{
				`data-testid="flashcard"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/learn/flashcards", nil)
			req = asFlashcardUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newFlashcardRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("GET /learn/flashcards status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("Location = %q, want %q", got, tt.wantLocation)
				}
			}
			body := strings.ToLower(w.Body.String())
			for _, want := range tt.wantContains {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(body, strings.ToLower(absent)) {
					t.Errorf("body unexpectedly contains %q", absent)
				}
			}
		})
	}
}

func TestFlashcardCreate(t *testing.T) {
	user := &database.User{ID: uuid.New(), IsAnonymous: true}

	tests := []struct {
		name         string
		form         url.Values
		repo         *fakeFlashcardRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantCreated  bool
		wantToast    bool
	}{
		{
			name:       "HTMX save swaps in the confirmation and fires a toast",
			form:       url.Values{"front": {"What renders the HTML?"}, "back": {"templ components with typed props."}},
			repo:       &fakeFlashcardRepo{},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="flashcard-saved"`,
			},
			wantCreated: true,
			wantToast:   true,
		},
		{
			name:         "non-JS save redirects to the flashcards list",
			form:         url.Values{"front": {"What renders the HTML?"}, "back": {"templ components with typed props."}},
			repo:         &fakeFlashcardRepo{},
			user:         user,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/learn/flashcards",
			wantCreated:  true,
		},
		{
			name:       "blank front is a 422 and creates nothing",
			form:       url.Values{"front": {"   "}, "back": {"something"}},
			repo:       &fakeFlashcardRepo{},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "blank back is a 422 and creates nothing",
			form:       url.Values{"front": {"something"}, "back": {""}},
			repo:       &fakeFlashcardRepo{},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "oversized field is a 422 and creates nothing",
			form:       url.Values{"front": {strings.Repeat("x", 501)}, "back": {"something"}},
			repo:       &fakeFlashcardRepo{},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "repository failure is a 500",
			form:       url.Values{"front": {"a"}, "back": {"b"}},
			repo:       &fakeFlashcardRepo{createErr: errFake},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:         "missing user redirects to auth and creates nothing",
			form:         url.Values{"front": {"a"}, "back": {"b"}},
			repo:         &fakeFlashcardRepo{},
			user:         nil,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/learn/flashcards", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = asFlashcardUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newFlashcardRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("POST /learn/flashcards status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("Location = %q, want %q", got, tt.wantLocation)
				}
			}
			body := strings.ToLower(w.Body.String())
			for _, want := range tt.wantContains {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("body missing %q", want)
				}
			}
			if tt.wantToast && w.Header().Get("HX-Trigger") == "" {
				t.Error("expected an HX-Trigger toast header on save")
			}

			if !tt.wantCreated {
				if len(tt.repo.created) != 0 {
					t.Fatalf("created %d card(s), want none", len(tt.repo.created))
				}
				return
			}
			if len(tt.repo.created) != 1 {
				t.Fatalf("created %d card(s), want exactly 1", len(tt.repo.created))
			}
			got := tt.repo.created[0]
			if got.UserID != user.ID {
				t.Errorf("created UserID = %s, want %s", got.UserID, user.ID)
			}
			if got.Front != strings.TrimSpace(tt.form.Get("front")) {
				t.Errorf("created Front = %q, want %q", got.Front, strings.TrimSpace(tt.form.Get("front")))
			}
			if got.Back != strings.TrimSpace(tt.form.Get("back")) {
				t.Errorf("created Back = %q, want %q", got.Back, strings.TrimSpace(tt.form.Get("back")))
			}
		})
	}
}

func TestFlashcardSetKnown(t *testing.T) {
	user := &database.User{ID: uuid.New(), IsAnonymous: true}
	cards := flashcardFixtures(user.ID)
	unknownCard := cards[0] // IsKnown: false

	tests := []struct {
		name         string
		id           string
		form         url.Values
		repo         *fakeFlashcardRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantSet      *setKnownCall
	}{
		{
			name:       "HTMX mark-known swaps the updated card",
			id:         unknownCard.ID.String(),
			form:       url.Values{"known": {"true"}},
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="flashcard"`,
				`data-known="true"`,
			},
			wantSet: &setKnownCall{id: unknownCard.ID, known: true},
		},
		{
			name:         "non-JS mark-known redirects back to the list",
			id:           unknownCard.ID.String(),
			form:         url.Values{"known": {"true"}},
			repo:         &fakeFlashcardRepo{cards: cards},
			user:         user,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/learn/flashcards",
			wantSet:      &setKnownCall{id: unknownCard.ID, known: true},
		},
		{
			name:       "unmarking works the same way",
			id:         cards[1].ID.String(),
			form:       url.Values{"known": {"false"}},
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-known="false"`,
			},
			wantSet: &setKnownCall{id: cards[1].ID, known: false},
		},
		{
			name:       "unknown card is a 404",
			id:         uuid.NewString(),
			form:       url.Values{"known": {"true"}},
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "malformed id is a 404",
			id:         "not-a-uuid",
			form:       url.Values{"known": {"true"}},
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "repository failure is a 500",
			id:         unknownCard.ID.String(),
			form:       url.Values{"known": {"true"}},
			repo:       &fakeFlashcardRepo{cards: cards, setErr: errFake},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:         "missing user redirects to auth",
			id:           unknownCard.ID.String(),
			form:         url.Values{"known": {"true"}},
			repo:         &fakeFlashcardRepo{cards: cards},
			user:         nil,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/learn/flashcards/" + tt.id + "/known"
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = asFlashcardUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newFlashcardRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("POST %s status = %d, want %d", target, w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("Location = %q, want %q", got, tt.wantLocation)
				}
			}
			body := strings.ToLower(w.Body.String())
			for _, want := range tt.wantContains {
				if !strings.Contains(body, strings.ToLower(want)) {
					t.Errorf("body missing %q", want)
				}
			}

			if tt.wantSet == nil {
				if len(tt.repo.known) != 0 {
					t.Fatalf("SetKnown called %d time(s), want none", len(tt.repo.known))
				}
				return
			}
			if len(tt.repo.known) != 1 {
				t.Fatalf("SetKnown called %d time(s), want exactly 1", len(tt.repo.known))
			}
			if got := tt.repo.known[0]; got != *tt.wantSet {
				t.Errorf("SetKnown call = %+v, want %+v", got, *tt.wantSet)
			}
		})
	}
}

func TestFlashcardDelete(t *testing.T) {
	user := &database.User{ID: uuid.New(), IsAnonymous: true}
	cards := flashcardFixtures(user.ID)

	tests := []struct {
		name         string
		id           string
		repo         *fakeFlashcardRepo
		user         *database.User
		htmx         bool
		wantStatus   int
		wantLocation string
		wantToast    bool
		wantEmpty    bool // HTMX delete must return an empty body so outerHTML swap removes the card
		wantDeleted  bool
	}{
		{
			name:        "HTMX delete removes the card and fires a toast",
			id:          cards[0].ID.String(),
			repo:        &fakeFlashcardRepo{cards: cards},
			user:        user,
			htmx:        true,
			wantStatus:  http.StatusOK,
			wantToast:   true,
			wantEmpty:   true,
			wantDeleted: true,
		},
		{
			name:         "non-JS delete redirects back to the list",
			id:           cards[0].ID.String(),
			repo:         &fakeFlashcardRepo{cards: cards},
			user:         user,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/learn/flashcards",
			wantDeleted:  true,
		},
		{
			name:        "deleting an already-deleted card is still a success (idempotent)",
			id:          uuid.NewString(),
			repo:        &fakeFlashcardRepo{cards: cards},
			user:        user,
			htmx:        true,
			wantStatus:  http.StatusOK,
			wantToast:   true,
			wantEmpty:   true,
			wantDeleted: true,
		},
		{
			name:       "malformed id is a 404",
			id:         "not-a-uuid",
			repo:       &fakeFlashcardRepo{cards: cards},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "repository failure is a 500",
			id:         cards[0].ID.String(),
			repo:       &fakeFlashcardRepo{cards: cards, deleteErr: errFake},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:         "missing user redirects to auth and deletes nothing",
			id:           cards[0].ID.String(),
			repo:         &fakeFlashcardRepo{cards: cards},
			user:         nil,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "/learn/flashcards/" + tt.id + "/delete"
			req := httptest.NewRequest(http.MethodPost, target, nil)
			req = asFlashcardUser(req, tt.user)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			newFlashcardRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("POST %s status = %d, want %d", target, w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" {
				if got := w.Header().Get("Location"); got != tt.wantLocation {
					t.Errorf("Location = %q, want %q", got, tt.wantLocation)
				}
			}
			if tt.wantToast && w.Header().Get("HX-Trigger") == "" {
				t.Error("expected an HX-Trigger toast header on delete")
			}
			if tt.wantEmpty && strings.TrimSpace(w.Body.String()) != "" {
				t.Errorf("HTMX delete body = %q, want empty (outerHTML swap removes the card)", w.Body.String())
			}

			if !tt.wantDeleted {
				if len(tt.repo.deleted) != 0 {
					t.Fatalf("Delete called %d time(s), want none", len(tt.repo.deleted))
				}
				return
			}
			if len(tt.repo.deleted) != 1 {
				t.Fatalf("Delete called %d time(s), want exactly 1", len(tt.repo.deleted))
			}
			got := tt.repo.deleted[0]
			if got.id.String() != tt.id {
				t.Errorf("deleted id = %s, want %s", got.id, tt.id)
			}
			if got.userID != user.ID {
				t.Errorf("deleted userID = %s, want %s (owner scoping)", got.userID, user.ID)
			}
		})
	}
}
