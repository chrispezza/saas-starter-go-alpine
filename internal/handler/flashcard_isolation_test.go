package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/webutil"
)

// TestFlashcardIsolationCheck pins the RLS proof surface (ADR-034 TC-4): the
// visitor's own ListByUser runs twice through the same repository interface
// — once as the requester, once as a freshly minted stranger — and the page
// shows both counts. The fake repository enforces the scoping the way RLS
// does (rows only for the owning identity), so a handler that forgot to
// swap the claims, or that bypassed the repository, fails here.
func TestFlashcardIsolationCheck(t *testing.T) {
	user := &database.User{ID: uuid.New(), IsAnonymous: true}
	cards := flashcardFixtures(user.ID)

	tests := []struct {
		name         string
		target       string
		repo         *fakeFlashcardRepo
		user         *database.User
		noClaims     bool // user present but no auth claims in the context
		htmx         bool
		wantStatus   int
		wantLocation string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:       "page offers the check next to the cards without running it",
			target:     "/learn/flashcards",
			repo:       &fakeFlashcardRepo{cards: cards, scoped: true},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="isolation-panel"`,
				`href="/learn/flashcards?check=1"`,
				`hx-get="/learn/flashcards/isolation"`,
				"flashcards_self_access",
			},
			wantAbsent: []string{`data-testid="isolation-result"`},
		},
		{
			name:       "page with ?check=1 runs both reads and reports the stranger's zero rows",
			target:     "/learn/flashcards?check=1",
			repo:       &fakeFlashcardRepo{cards: cards, scoped: true},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="isolation-result"`,
				`data-own-rows="2"`,
				`data-stranger-rows="0"`,
				"refus",
			},
		},
		{
			name:       "no cards yet: the check asks for a card first instead of proving nothing",
			target:     "/learn/flashcards?check=1",
			repo:       &fakeFlashcardRepo{scoped: true},
			user:       user,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="isolation-panel"`,
				"save",
			},
			wantAbsent: []string{`data-stranger-rows="0"`, "refus"},
		},
		{
			name:       "HTMX fragment endpoint returns only the result",
			target:     "/learn/flashcards/isolation",
			repo:       &fakeFlashcardRepo{cards: cards, scoped: true},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusOK,
			wantContains: []string{
				`data-testid="isolation-result"`,
				`data-own-rows="2"`,
				`data-stranger-rows="0"`,
			},
			wantAbsent: []string{"<!doctype", `data-testid="isolation-panel"`},
		},
		{
			name:         "non-HTMX request to the fragment endpoint lands on the page with the check run",
			target:       "/learn/flashcards/isolation",
			repo:         &fakeFlashcardRepo{cards: cards, scoped: true},
			user:         user,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/learn/flashcards?check=1",
		},
		{
			name:         "signed-out visitors are sent to sign in",
			target:       "/learn/flashcards/isolation",
			repo:         &fakeFlashcardRepo{cards: cards, scoped: true},
			user:         nil,
			htmx:         true,
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
		{
			name:       "a request without identity claims cannot be proved and is a 500, not a false pass",
			target:     "/learn/flashcards/isolation",
			repo:       &fakeFlashcardRepo{cards: cards, scoped: true},
			user:       user,
			noClaims:   true,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "repository failure is a 500",
			target:     "/learn/flashcards/isolation",
			repo:       &fakeFlashcardRepo{listErr: errFake},
			user:       user,
			htmx:       true,
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.noClaims {
				req = req.WithContext(webutil.WithUser(req.Context(), tt.user))
			} else {
				req = asFlashcardUser(req, tt.user)
			}
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()
			newFlashcardRouter(tt.repo).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d\n%s", tt.target, w.Code, tt.wantStatus, w.Body.String())
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

// TestTruncateID pins the display form of identities on the proof panel:
// enough to recognise, never the whole subject.
func TestTruncateID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"3f9a2c1e-7b5d-4e3a-9c1f-2a6b8d4e0f12", "3f9a2c1e…"},
		{"auth-abc", "auth-abc"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := truncateID(tt.in); got != tt.want {
			t.Errorf("truncateID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
