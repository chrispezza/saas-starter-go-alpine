package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/clownware/go-performance-starter/internal/auth"
	"github.com/clownware/go-performance-starter/internal/database"
)

// fakeUpgrader fakes the GoTrue upgrade + refresh calls.
type fakeUpgrader struct {
	upgradeErr    error
	confirmation  bool
	refreshErr    error
	upgradeSeen   bool
	refreshSeen   bool
	gotToken      string
	gotEmail      string
	gotPassword   string
	gotRefreshTok string
}

func (f *fakeUpgrader) UpgradeAnonymousUser(_ context.Context, accessToken, email, password string) (*auth.UpgradeResult, error) {
	f.upgradeSeen = true
	f.gotToken = accessToken
	f.gotEmail = email
	f.gotPassword = password
	if f.upgradeErr != nil {
		return nil, f.upgradeErr
	}
	return &auth.UpgradeResult{ConfirmationSent: f.confirmation}, nil
}

func (f *fakeUpgrader) RefreshSession(_ context.Context, refreshToken string) (*auth.AnonSession, error) {
	f.refreshSeen = true
	f.gotRefreshTok = refreshToken
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return &auth.AnonSession{AccessToken: "tok-new", RefreshToken: "ref-new", ExpiresIn: 3600}, nil
}

// SetAnonymous extends the shared fakeUserRepo for the upgrade flow.
func (f *fakeUserRepo) SetAnonymous(_ context.Context, id uuid.UUID, anonymous bool) error {
	if anonymous {
		return errors.New("upgrade flow must only promote, never demote")
	}
	f.anonymousFlipIDs = append(f.anonymousFlipIDs, id)
	return nil
}

// Update records the email sync that replaces the guest placeholder (#68).
func (f *fakeUserRepo) Update(_ context.Context, params database.UpdateUserParams) (*database.User, error) {
	f.updatedEmails = append(f.updatedEmails, params.Email)
	return &database.User{ID: params.ID, Email: params.Email}, nil
}

func upgradeRequest(t *testing.T, form string, user *database.User, repo *fakeUserRepo, withCookies bool) *http.Request {
	t.Helper()
	req := formRequest("/learn/upgrade", form)
	if user != nil {
		req = withDBUser(req, user, repo)
	}
	if withCookies {
		req.AddCookie(&http.Cookie{Name: "sb-access-token", Value: "tok-old"})
		req.AddCookie(&http.Cookie{Name: "sb-refresh-token", Value: "ref-old"})
	}
	return req
}

func TestUpgradePage(t *testing.T) {
	tests := []struct {
		name         string
		user         *database.User
		wantStatus   int
		wantLocation string
		wantContains string
	}{
		{name: "signed out redirects to auth", wantStatus: http.StatusSeeOther, wantLocation: "/auth/page"},
		{
			name:         "guest sees the upgrade form",
			user:         &database.User{ID: uuid.New(), IsAnonymous: true},
			wantStatus:   http.StatusOK,
			wantContains: `name="email"`,
		},
		{
			name:         "registered user sees the already-registered state",
			user:         &database.User{ID: uuid.New(), IsAnonymous: false},
			wantStatus:   http.StatusOK,
			wantContains: "already signed in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/learn/upgrade", nil)
			if tt.user != nil {
				req = withDBUser(req, tt.user, nil)
			}
			w := httptest.NewRecorder()

			UpgradePage(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantLocation != "" && w.Header().Get("Location") != tt.wantLocation {
				t.Errorf("Location = %q, want %q", w.Header().Get("Location"), tt.wantLocation)
			}
			if tt.wantContains != "" && !strings.Contains(w.Body.String(), tt.wantContains) {
				t.Errorf("body missing %q", tt.wantContains)
			}
		})
	}
}

func TestUpgradeSubmit(t *testing.T) {
	userID := uuid.New()
	guest := func() *database.User { return &database.User{ID: userID, IsAnonymous: true} }

	tests := []struct {
		name          string
		form          string
		user          *database.User
		withCookies   bool
		upgrader      *fakeUpgrader
		htmx          bool
		wantStatus    int
		wantLocation  string
		wantContains  string
		wantUpgraded  bool // GoTrue call must happen
		wantAnonFlip  bool // users row must be promoted
		wantNewCookie bool // refreshed session cookies must be set
	}{
		{
			name:         "signed out redirects to auth",
			form:         "email=a@b.com&password=hunter2hunter2",
			upgrader:     &fakeUpgrader{},
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/auth/page",
		},
		{
			name:         "registered user is not re-upgraded",
			form:         "email=a@b.com&password=hunter2hunter2",
			user:         &database.User{ID: userID, IsAnonymous: false},
			withCookies:  true,
			upgrader:     &fakeUpgrader{},
			wantStatus:   http.StatusOK,
			wantContains: "already signed in",
		},
		{
			name:         "invalid email re-renders with 422",
			form:         "email=not-an-email&password=hunter2hunter2",
			user:         guest(),
			withCookies:  true,
			upgrader:     &fakeUpgrader{},
			htmx:         true,
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "email",
		},
		{
			name:         "short password re-renders with 422",
			form:         "email=a@b.com&password=short",
			user:         guest(),
			withCookies:  true,
			upgrader:     &fakeUpgrader{},
			htmx:         true,
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "password",
		},
		{
			name:          "successful upgrade persists, promotes, and refreshes",
			form:          "email=ada@example.com&password=hunter2hunter2",
			user:          guest(),
			withCookies:   true,
			upgrader:      &fakeUpgrader{confirmation: true},
			htmx:          true,
			wantStatus:    http.StatusOK,
			wantContains:  "confirmation link",
			wantUpgraded:  true,
			wantAnonFlip:  true,
			wantNewCookie: true,
		},
		{
			name:         "email in use maps to a form error",
			form:         "email=taken@example.com&password=hunter2hunter2",
			user:         guest(),
			withCookies:  true,
			upgrader:     &fakeUpgrader{upgradeErr: auth.ErrEmailInUse},
			htmx:         true,
			wantStatus:   http.StatusUnprocessableEntity,
			wantContains: "already registered",
			wantUpgraded: true,
		},
		{
			name:        "gotrue failure returns 502-style error state",
			form:        "email=ada@example.com&password=hunter2hunter2",
			user:        guest(),
			withCookies: true,
			upgrader:    &fakeUpgrader{upgradeErr: errors.New("gotrue down")},
			htmx:        true,
			wantStatus:  http.StatusUnprocessableEntity,
			// generic, retryable message — no internal detail
			wantContains: "try again",
			wantUpgraded: true,
		},
		{
			name:          "refresh failure still succeeds with a warning logged",
			form:          "email=ada@example.com&password=hunter2hunter2",
			user:          guest(),
			withCookies:   true,
			upgrader:      &fakeUpgrader{refreshErr: errors.New("refresh down")},
			htmx:          true,
			wantStatus:    http.StatusOK,
			wantUpgraded:  true,
			wantAnonFlip:  true,
			wantNewCookie: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeUserRepo{}
			req := upgradeRequest(t, tt.form, tt.user, repo, tt.withCookies)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()

			UpgradeSubmit(tt.upgrader, false)(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantLocation != "" && w.Header().Get("Location") != tt.wantLocation {
				t.Errorf("Location = %q, want %q", w.Header().Get("Location"), tt.wantLocation)
			}
			if tt.wantContains != "" && !strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(tt.wantContains)) {
				t.Errorf("body missing %q", tt.wantContains)
			}
			if tt.upgrader.upgradeSeen != tt.wantUpgraded {
				t.Errorf("GoTrue upgrade called = %v, want %v", tt.upgrader.upgradeSeen, tt.wantUpgraded)
			}
			if tt.wantUpgraded && tt.upgrader.upgradeSeen && tt.upgrader.gotToken != "tok-old" {
				t.Errorf("upgrade used token %q, want the session cookie's token", tt.upgrader.gotToken)
			}
			gotFlip := len(repo.anonymousFlipIDs) == 1 && repo.anonymousFlipIDs[0] == userID
			if gotFlip != tt.wantAnonFlip {
				t.Errorf("row promotion = %v (%v), want %v", gotFlip, repo.anonymousFlipIDs, tt.wantAnonFlip)
			}
			if tt.wantAnonFlip {
				// Promotion must also replace the guest placeholder email.
				if len(repo.updatedEmails) != 1 || repo.updatedEmails[0] != tt.upgrader.gotEmail {
					t.Errorf("email sync = %v, want [%q]", repo.updatedEmails, tt.upgrader.gotEmail)
				}
			}
			var gotAccess string
			for _, c := range w.Result().Cookies() {
				if c.Name == "sb-access-token" {
					gotAccess = c.Value
				}
			}
			if tt.wantNewCookie && gotAccess != "tok-new" {
				t.Errorf("sb-access-token after upgrade = %q, want refreshed token", gotAccess)
			}
			if !tt.wantNewCookie && gotAccess != "" {
				t.Errorf("unexpected session cookie set: %q", gotAccess)
			}
		})
	}
}

// TestUpgradeRoutes_RateLimitTier pins the ADR-014 §4 promise UpgradeRoutes
// makes: the credential-setting POST sits behind the strict rate tier
// (burst 5), while the GET page stays unthrottled. All requests here are
// sessionless, so handlers answer with the login redirect — the assertion
// is about which requests the limiter lets through at all.
func TestUpgradeRoutes_RateLimitTier(t *testing.T) {
	router := chi.NewRouter()
	UpgradeRoutes(t.Context(), router, &fakeUpgrader{}, false)

	post := func() int {
		req := formRequest("/learn/upgrade", "email=a@b.co&password=12345678")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 1; i <= 5; i++ {
		if code := post(); code != http.StatusSeeOther {
			t.Fatalf("POST %d = %d, want 303 (within burst)", i, code)
		}
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Errorf("POST 6 = %d, want 429 — credential endpoint must be rate limited (ADR-014 §4)", code)
	}

	// The GET page shares the path but not the strict tier.
	for i := 1; i <= 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/learn/upgrade", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("GET %d = %d, want 303 (page load must not be throttled)", i, rec.Code)
		}
	}
}
