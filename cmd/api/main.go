package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/clownware/go-performance-starter/internal/config"
	"github.com/clownware/go-performance-starter/internal/jobs"
	"github.com/clownware/go-performance-starter/internal/middleware"
	"github.com/clownware/go-performance-starter/internal/performance"
	"github.com/clownware/go-performance-starter/internal/repository/postgres"
	"github.com/clownware/go-performance-starter/internal/server"
	"github.com/clownware/go-performance-starter/internal/view"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

// processStart anchors the ADR-000 startup budget: process start → listening
// socket, recorded into the observer once the listener is bound (ADR-034).
var processStart = time.Now()

func main() {
	// Manually load .env and set environment variables (before logger setup
	// so LOG_LEVEL/ENV from .env apply)
	envMap, err := godotenv.Read() // Read .env into a map
	if err != nil {
		slog.Warn("Error reading .env file, relying on existing environment variables", "error", err)
	} else {
		for key, value := range envMap {
			if os.Getenv(key) == "" { // Only set if not already set in the OS environment
				err := os.Setenv(key, value)
				if err != nil {
					// Log setting error, but continue
					slog.Warn("Error setting env variable from .env", "key", key, "error", err)
				}
			}
		}
	}

	// Structured logging per ADR-026: JSON in production, text otherwise,
	// level from LOG_LEVEL (default info)
	setupLogger(os.Getenv("ENV"), os.Getenv("LOG_LEVEL"))
	slog.Info("Starting Go Performance Starter", "version", version)

	// Stamp static asset URLs with the build version so each release busts
	// the 1-year asset cache (ADR-016); dev builds keep a process-start stamp.
	view.SetAssetVersion(version)

	// Create context that listens for termination signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Shutdown signal received")
		cancel()
	}()

	// Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Fail fast on misconfiguration (ADR-015) instead of at first use
	if err := cfg.Validate(); err != nil {
		slog.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}

	// Connect to the database with tuned pool settings (ADR-025)
	slog.Info("Connecting to database...")
	poolCfg, err := cfg.PoolConfig()
	if err != nil {
		slog.Error("Failed to build database pool config", "error", err)
		os.Exit(1)
	}
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("Database connection established successfully")

	// Wire the router: middleware stack, repositories, and routes live in
	// internal/server; main only injects the pool.
	srv, err := server.New(cfg, db)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	// Start memory metrics collector (updates every 30 seconds)
	middleware.StartMemoryMetricsCollector(30 * time.Second)
	slog.Info("Memory metrics collector started")

	// Guest mode: reap expired anonymous accounts (ADR-024). Auth-side
	// cleanup — deleting reaped guests' GoTrue twins and sweeping orphaned
	// anonymous identities that never got a public row (#82) — runs only
	// when the service role key is configured.
	if cfg.GuestModeEnabled {
		var deleteAuthUser jobs.AuthUserDeleter
		var listAuthUsers jobs.AuthUserLister
		if ac := srv.AuthClient(); ac != nil && ac.HasServiceRoleKey() {
			deleteAuthUser = ac.AdminDeleteUser
			listAuthUsers = ac.AdminListAnonymousUsers
		}
		reaper := jobs.NewReaper(postgres.NewReaperRepo(db), deleteAuthUser, cfg.GuestTTL, cfg.ReaperInterval).
			WithAuthLister(listAuthUsers)
		reaper.Start(ctx)
		slog.Info("Anonymous-user reaper started", "ttl", cfg.GuestTTL, "interval", cfg.ReaperInterval,
			"auth_side_cleanup", deleteAuthUser != nil)
	}

	// Set up the HTTP server
	addr := fmt.Sprintf(":%s", cfg.HTTPPort)
	httpServer := newHTTPServer(addr, srv)

	// Bind first so "startup" means what ADR-000 says — process start to a
	// listening socket — then serve on the bound listener.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("Error binding listener", "addr", addr, "error", err)
		os.Exit(1)
	}
	startup := time.Since(processStart)
	performance.Default.RecordStartup(startup)
	if err := performance.CheckStartupTime(startup); err != nil {
		slog.Warn("startup budget exceeded", "error", err)
	}
	slog.Info("Server listening on "+addr, "startup_ms", startup.Milliseconds())

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("Error starting server", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for termination signal
	<-ctx.Done()

	// Graceful shutdown
	slog.Info("Shutting down server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown failed", "error", err)
		os.Exit(1)
	}
	srv.Close() // stop router-owned goroutines once no requests remain (#117)

	slog.Info("Server stopped gracefully")
}

// setupLogger installs the process-wide slog logger (ADR-026): JSON handler
// in production for log aggregation, text handler elsewhere for readability.
func setupLogger(env, level string) {
	opts := &slog.HandlerOptions{Level: parseLogLevel(level)}
	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

// parseLogLevel maps LOG_LEVEL to a slog.Level, defaulting to info on empty
// or unrecognized values (fail-open to the safe default rather than crashing).
func parseLogLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return level
}

// newHTTPServer builds the http.Server with connection timeouts. Without
// these, slow or idle clients hold connections indefinitely (connection
// exhaustion). WriteTimeout intentionally exceeds the 30s request timeout
// middleware so handler deadlines fire before the connection is cut.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
