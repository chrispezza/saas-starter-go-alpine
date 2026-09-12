package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"
)

// Pinger is the readiness dependency the detail probe checks. It is the
// consumer-side seam that keeps this package driver-free (ADR-003): the
// server passes its *pgxpool.Pool, which satisfies it; tests pass a fake.
type Pinger interface {
	Ping(ctx context.Context) error
}

var (
	startTime  time.Time
	startOnce  sync.Once
	healthDB   Pinger
	healthDBMu sync.RWMutex

	// buildVersion is the -X main.version stamp main hands over at boot;
	// "dev" until then, matching main's default.
	buildVersion   = "dev"
	buildVersionMu sync.RWMutex
)

// SetBuildVersion records the build's version string (main's ldflags stamp)
// so /health can name the build even where no VCS info is compiled in —
// Docker builds copy the tree without .git, so the revision alone reported
// "dev" on every deployed instance.
func SetBuildVersion(v string) {
	buildVersionMu.Lock()
	buildVersion = v
	buildVersionMu.Unlock()
}

// resolveVersion picks the most specific name for the running build: the
// stamped version when a release or deploy set one, else the short VCS
// revision of an unstamped local build, else "dev".
func resolveVersion(settings []debug.BuildSetting, build string) string {
	if build != "" && build != "dev" {
		return build
	}
	for _, s := range settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}

// InitHealth records the server start time and stores the dependency the
// detail probe pings. Pass nil when no database is configured — callers
// holding a typed nil pointer must convert it to a nil interface themselves,
// or the probe will ping a nil pool.
func InitHealth(db Pinger) {
	startOnce.Do(func() {
		startTime = time.Now()
	})
	healthDBMu.Lock()
	healthDB = db
	healthDBMu.Unlock()
}

// HealthHandler returns a simple liveness probe.
// Used by Dockerfile HEALTHCHECK — must return 200 with minimal overhead.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK")) // liveness probe; nothing actionable if the write fails
}

// HealthDetailHandler returns a detailed JSON health check with dependency status.
// Per ADR-013 Error Handling and Observability.
func HealthDetailHandler(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	dbStatus := "ok"

	healthDBMu.RLock()
	db := healthDB
	healthDBMu.RUnlock()

	if db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			dbStatus = "unreachable"
			status = "degraded"
		}
	} else {
		dbStatus = "not configured"
	}

	var settings []debug.BuildSetting
	if info, ok := debug.ReadBuildInfo(); ok {
		settings = info.Settings
	}
	buildVersionMu.RLock()
	build := buildVersion
	buildVersionMu.RUnlock()
	version := resolveVersion(settings, build)

	httpStatus := http.StatusOK
	if status != "ok" {
		httpStatus = http.StatusServiceUnavailable
	}

	resp := map[string]interface{}{
		"status":  status,
		"version": version,
		"uptime":  time.Since(startTime).String(),
		"checks": map[string]string{
			"database": dbStatus,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode health response", "error", err)
	}
}
