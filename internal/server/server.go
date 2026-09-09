package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/clownware/go-performance-starter/internal/auth"
	"github.com/clownware/go-performance-starter/internal/config"
	"github.com/clownware/go-performance-starter/internal/database"
	"github.com/clownware/go-performance-starter/internal/handler"
	mw "github.com/clownware/go-performance-starter/internal/middleware"
	"github.com/clownware/go-performance-starter/internal/repository/postgres"
	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/pages"
)

// Server represents the main application server.
type Server struct {
	router     chi.Router
	cfg        *config.Config
	db         *pgxpool.Pool
	authClient *auth.AuthClient
	// stop cancels the lifecycle context handed to background goroutines the
	// router owns (rate-limiter eviction, #117); Close calls it.
	stop context.CancelFunc
}

// New creates a new Server instance.
func New(cfg *config.Config, db *pgxpool.Pool) (*Server, error) {
	// Initialize Supabase Auth Client (skip if credentials not provided)
	var authClient *auth.AuthClient
	if cfg.SupabaseURL != "" && cfg.SupabaseAnonKey != "" {
		var err error
		authClient, err = auth.NewAuthClient(cfg.SupabaseURL, cfg.SupabaseAnonKey)
		if err != nil {
			slog.Error("Failed to create Supabase auth client", "error", err)
			return nil, fmt.Errorf("failed to create auth client: %w", err)
		}
		if cfg.SupabaseServiceRoleKey != "" {
			authClient.WithServiceRoleKey(cfg.SupabaseServiceRoleKey)
		}
		slog.Info("Supabase auth client initialized")
	} else {
		slog.Warn("Supabase credentials not set — auth disabled")
	}

	r := chi.NewRouter()

	ctx, stop := context.WithCancel(context.Background())
	s := &Server{
		router:     r,
		cfg:        cfg,
		db:         db,
		authClient: authClient,
		stop:       stop,
	}

	// Initialize health check with DB pool for connectivity checks. The pool
	// is handed over as a handler.Pinger (ADR-003 keeps the handler
	// driver-free); a nil pool must become a nil interface, not a typed nil.
	var pinger handler.Pinger
	if db != nil {
		pinger = db
	}
	handler.InitHealth(pinger)

	s.setupMiddleware(ctx)
	s.setupRoutes(ctx)

	return s, nil
}

// Close stops the background goroutines the server started (#117). It does
// not affect in-flight requests — the HTTP server's Shutdown handles those —
// and is safe to call more than once.
func (s *Server) Close() {
	s.stop()
}

// fileServer conveniently sets up a http.FileServer handler to serve static files
// from a http.FileSystem with proper cache control headers
func fileServer(r chi.Router, path string, root http.FileSystem) {
	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", http.StatusMovedPermanently).ServeHTTP)
		path += "/"
	}
	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		// Define cache durations based on file type
		rctx := chi.RouteContext(r.Context())
		pathPrefix := rctx.RoutePattern()[:len(rctx.RoutePattern())-2] // strip the /*
		fs := http.StripPrefix(pathPrefix, cacheControlWrapper(http.FileServer(root)))
		fs.ServeHTTP(w, r)
	})
}

// cacheControlWrapper adds cache control headers based on file extension.
// The header is decided up front from the path, but committed only for
// non-error responses: a 404 for a missing asset used to ship the one-year
// header, letting an intermediary cache the miss and keep serving it after
// the file appeared (2026-08-19 found-work fix). Errors go out as no-store;
// 304 revalidations keep the extension header — that's the cache working.
func cacheControlWrapper(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		// CSS, JS, and images can be cached for longer periods
		case isFileType(path, ".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp"):
			w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
		// Fonts can be cached for a long time
		case isFileType(path, ".woff", ".woff2", ".ttf", ".otf", ".eot"):
			w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
		// HTML and other files should have shorter cache times
		default:
			w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour
		}

		h.ServeHTTP(&errorUncachedWriter{ResponseWriter: w}, r)
	})
}

// errorUncachedWriter downgrades Cache-Control to no-store the moment an
// error status is written, so failures are never cached under a success
// policy chosen from the URL alone.
type errorUncachedWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *errorUncachedWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if status >= http.StatusBadRequest {
			w.Header().Set("Cache-Control", "no-store")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *errorUncachedWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// isFileType checks if the file path has any of the specified extensions
func isFileType(filePath string, extensions ...string) bool {
	ext := path.Ext(filePath)
	for _, e := range extensions {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

func (s *Server) setupMiddleware(ctx context.Context) {
	isProd := s.cfg.IsProduction()

	// Basic middleware (order matters!)
	s.router.Use(mw.SecurityHeaders(isProd))                               // Security headers first (ADR-014; HSTS in prod per ADR-025)
	s.router.Use(mw.RequestID)                                             // Generate request ID
	s.router.Use(mw.RealIP(s.cfg.TrustedProxyCIDRs, s.cfg.ClientIPHeader)) // Resolve client IP via trusted proxies (ADR-027; before rate limiter)
	s.router.Use(mw.MaxBodyBytes(s.cfg.MaxRequestBodyBytes))               // Cap request body size (2026-07-06 audit)
	s.router.Use(mw.RateLimiter(ctx, 50, 10))                              // Global rate limit (ADR-014)
	s.router.Use(mw.Compress(5))                                           // gzip/deflate responses
	s.router.Use(mw.Metrics)                                               // Track metrics (uses RequestID)
	s.router.Use(mw.RequestLogger)                                         // Log requests with context
	s.router.Use(mw.Recoverer)                                             // Panic recovery
	s.router.Use(mw.Timeout(30 * time.Second))                             // Request timeout
	s.router.Use(mw.CSRF(isProd))                                          // CSRF double-submit cookie (ADR-014 §3)

	// Inject UserRepository into context for all routes (skip if DB not available)
	if s.db != nil {
		s.router.Use(mw.UserRepoMiddleware(postgres.NewUserRepo(s.db, database.New(s.db))))
	}

	// Static file server with cache control
	fileServer(s.router, "/static", http.Dir("./web/static"))
}

func (s *Server) setupRoutes(ctx context.Context) {
	r := s.router // Use the router from the Server struct

	// Static & Informational Pages
	r.Get("/terms", handler.TermsPage)
	r.Get("/privacy", handler.PrivacyPage)

	// Logout GET route for UX (renders confirm form)
	r.Get("/auth/logout", handler.LogoutPage)

	// Health check endpoints (ADR-013)
	s.router.Get("/healthz", handler.HealthHandler)      // Liveness probe (Dockerfile HEALTHCHECK)
	s.router.Get("/health", handler.HealthDetailHandler) // Detailed readiness check

	// Metrics endpoint for Prometheus — bearer-token gated; hidden in
	// production when METRICS_TOKEN is unset (2026-07-05 audit)
	s.router.With(mw.MetricsGuard(s.cfg.MetricsToken, s.cfg.IsProduction())).
		Handle("/metrics", promhttp.Handler())

	// Profile page (HTMX-enabled, requires auth). Profile and first-run both
	// need the DB users row loaded (and JIT-provisioned) from the
	// authenticated identity — profile writes the name to that row (#70).
	if s.authClient != nil && s.db != nil {
		r.Group(func(protectedRouter chi.Router) {
			protectedRouter.Use(mw.AuthMiddleware(s.authClient, s.cfg.IsProduction()))
			protectedRouter.Use(mw.UserLoader(postgres.NewUserRepo(s.db, database.New(s.db))))
			protectedRouter.Get("/profile", handler.ProfileView)
			protectedRouter.Post("/profile", handler.ProfileUpdate)
			// Change password (#97): the session token is the credential, so
			// it sits on the strict credential tier like /auth/reset.
			protectedRouter.With(mw.RateLimiter(ctx, 5.0/60.0, 5)).
				Post("/profile/password", handler.ProfilePasswordUpdate(s.authClient))
			handler.FirstRunHandlers(protectedRouter)
		})
	}

	// Pattern showcase (ADR-024 surface 2): public, stub data, no DB/auth.
	handler.PatternsRoutes(r)

	// Quiz + flashcards + dashboard (ADR-024 surface 3): RLS-scoped
	// persistence behind a browse-first identity chain. GuestSession issues
	// anonymous identities only when guest mode is enabled (config-gated:
	// requires anonymous sign-ins in Supabase). OptionalAuth/OptionalUserLoader
	// load a valid session exactly like the strict pair but let signed-out
	// GETs through — the handlers render a why-sign-in teaser for them and
	// guard every mutation (nil user → redirect to login).
	if s.authClient != nil && s.db != nil {
		r.Group(func(learn chi.Router) {
			if s.cfg.GuestModeEnabled {
				learn.Use(mw.GuestSession(s.authClient, s.cfg.IsProduction()))
			}
			learn.Use(mw.OptionalAuth(s.authClient, s.cfg.IsProduction()))
			learn.Use(mw.OptionalUserLoader(postgres.NewUserRepo(s.db, database.New(s.db))))
			// Anonymous-writable surface: stricter tier on top of the global
			// limiter (ADR-024 accompanying constraints).
			learn.Use(mw.RateLimiter(ctx, 30.0/60.0, 20))
			quizRepo := postgres.NewQuizRepo(s.db, database.New(s.db))
			cardRepo := postgres.NewFlashcardRepo(s.db, database.New(s.db))
			handler.QuizRoutes(learn, quizRepo)
			handler.FlashcardRoutes(learn, cardRepo)
			// Progress dashboard (#69): reads the same RLS-scoped rows the
			// quiz and flashcards write, so it rides the same identity chain.
			handler.DashboardRoutes(learn, quizRepo, cardRepo)
			// Guest → registered upgrade (#68): same identity chain as the
			// other /learn surfaces; the POST adds the strict credential tier.
			handler.UpgradeRoutes(ctx, learn, s.authClient, s.cfg.IsProduction())
		})
	} else {
		// No identity chain to mount under: keep the dashboard shell routed
		// (it renders the teaser) so the nav link never 404s.
		r.Get("/dashboard", handler.DashboardPage)
	}

	// --- Authentication Routes ---
	if s.authClient != nil {
		r.Route("/auth", func(authRouter chi.Router) {
			authRouter.Get("/page", handler.AuthPage)       // Show login/signup form
			authRouter.Get("/recover", handler.RecoverPage) // Request-reset-link form (#71)
			// Exchanges the email link's token_hash for a recovery session
			// (sets cookies) and shows the update-password form.
			authRouter.Get("/reset", handler.AuthResetPage(s.authClient, s.cfg.IsProduction()))
			// Credential endpoints get the strict tier: 5 attempts/min
			// per IP (ADR-014 §4), on top of the global limiter.
			authRouter.Group(func(strict chi.Router) {
				strict.Use(mw.RateLimiter(ctx, 5.0/60.0, 5))
				strict.Post("/login", handler.AuthLoginPost(s.authClient, s.cfg.IsProduction())) // Handle login
				strict.Post("/signup", handler.AuthSignupPost(s.authClient))                     // Handle signup
				strict.Post("/recover", handler.AuthRecoverPost(s.authClient))                   // Send reset email
				strict.Post("/reset", handler.AuthResetPost(s.authClient))                       // Set new password
			})
			authRouter.Post("/logout", handler.AuthLogoutPost(s.authClient, s.cfg.IsProduction())) // Handle logout
		})
	} else {
		r.Get("/auth/page", handler.AuthPage) // Show login form (non-functional without Supabase)
	}

	// Home page route
	r.Get("/", homeHandler) // Use the router variable 'r'
}

// ServeHTTP implements the http.Handler interface, making Server usable with http.ListenAndServe.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// AuthClient exposes the Supabase auth client (nil when auth is disabled)
// for background jobs that need admin operations (e.g. the guest reaper).
func (s *Server) AuthClient() *auth.AuthClient {
	return s.authClient
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	// The explainer spine and the live budget stats are built per request
	// from code and constants (ADR-024 surface 1, #67) — server-rendered
	// dynamic content is part of what the page demonstrates.
	props := pages.HomePageProps{
		BaseProps: view.NewBaseProps("Go Performance Starter"),
		Nodes:     handler.ExplainerNodes(),
		Stats:     handler.PerfBudgetStats(),
	}
	if err := view.Render(w, r, http.StatusOK, pages.HomePage(props)); err != nil {
		slog.Error("Failed to render home page", "error", err)
	}
}
