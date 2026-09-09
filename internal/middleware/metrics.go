package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/clownware/go-performance-starter/internal/performance"
)

var (
	// HTTP metrics
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"method", "path", "status"},
	)

	httpRequestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_size_bytes",
			Help:    "HTTP request size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "path"},
	)

	httpResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "path"},
	)

	// Performance budget violations
	budgetViolations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "performance_budget_violations_total",
			Help: "Total number of performance budget violations",
		},
		[]string{"budget_type", "path"},
	)

	// Active connections
	activeConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_active_connections",
			Help: "Number of active HTTP connections",
		},
	)

	// Memory metrics
	memoryUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_memory_usage_bytes",
			Help: "Memory usage by type",
		},
		[]string{"type"},
	)
)

// responseWriter wraps http.ResponseWriter to capture status code and bytes
// written, and to stamp Server-Timing the moment headers are committed —
// the handler's time to first byte, visible in any browser's devtools
// (ADR-034). Headers cannot change once the body streams, so the stamp
// rides the first WriteHeader or Write, whichever comes first.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
	start        time.Time
	wroteHeader  bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader = true
	rw.statusCode = code
	if !rw.start.IsZero() {
		ms := float64(time.Since(rw.start).Microseconds()) / 1000
		rw.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.1f", ms))
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// Metrics middleware tracks HTTP metrics using Prometheus
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		activeConnections.Inc()
		defer activeConnections.Dec()

		// Wrap response writer
		ww := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			start:          start,
		}

		// Get route pattern (e.g., /api/posts/{id})
		routePattern := getRoutePattern(r)

		// Track request size
		if r.ContentLength > 0 {
			httpRequestSize.WithLabelValues(r.Method, routePattern).Observe(float64(r.ContentLength))
		}

		// Process request
		next.ServeHTTP(ww, r)

		// Calculate duration
		duration := time.Since(start)
		durationSeconds := duration.Seconds()
		status := strconv.Itoa(ww.statusCode)

		// Record metrics
		httpRequestsTotal.WithLabelValues(r.Method, routePattern, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, routePattern, status).Observe(durationSeconds)
		httpResponseSize.WithLabelValues(r.Method, routePattern).Observe(float64(ww.bytesWritten))

		// Feed the process observer (landing page observed column, ADR-034)
		performance.Default.RecordLatency(duration)

		// Check performance budgets
		checkPerformanceBudget(duration, routePattern, r.Method)

		// Log slow requests
		if duration > performance.MaxP95ResponseTime {
			slog.Warn("slow request detected",
				"method", r.Method,
				"path", r.URL.Path,
				"route", routePattern,
				"duration_ms", duration.Milliseconds(),
				"budget_ms", performance.MaxP95ResponseTime.Milliseconds(),
				"status", ww.statusCode,
			)
		}
	})
}

// checkPerformanceBudget verifies response time against performance budgets
func checkPerformanceBudget(duration time.Duration, path, method string) {
	if duration > performance.MaxP50ResponseTime {
		budgetViolations.WithLabelValues("p50_response_time", path).Inc()
	}
	if duration > performance.MaxP95ResponseTime {
		budgetViolations.WithLabelValues("p95_response_time", path).Inc()
	}
	if duration > performance.MaxP99ResponseTime {
		budgetViolations.WithLabelValues("p99_response_time", path).Inc()
	}
}

// getRoutePattern extracts the route pattern from the request
// e.g., /api/posts/123 -> /api/posts/{id}
func getRoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil && rctx.RoutePattern() != "" {
		return rctx.RoutePattern()
	}
	return r.URL.Path
}

// UpdateMemoryMetrics updates memory usage metrics
func UpdateMemoryMetrics() {
	performance.Default.ObserveMemory() // advance the peak high-water mark (ADR-034)
	stats := performance.GetMemoryStats()

	if allocMB, ok := stats["alloc_mb"].(float64); ok {
		memoryUsage.WithLabelValues("alloc").Set(allocMB * 1024 * 1024)
	}
	if sysMB, ok := stats["sys_mb"].(float64); ok {
		memoryUsage.WithLabelValues("sys").Set(sysMB * 1024 * 1024)
	}
	if stackMB, ok := stats["stack_in_use_mb"].(float64); ok {
		memoryUsage.WithLabelValues("stack").Set(stackMB * 1024 * 1024)
	}
}

// StartMemoryMetricsCollector starts a goroutine to periodically update memory metrics
func StartMemoryMetricsCollector(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			UpdateMemoryMetrics()
		}
	}()
}

// RequestLogger logs each completed request with the context fields required
// by ADR-013 (request_id, duration, status).
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		slog.Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"request_id", middleware.GetReqID(r.Context()),
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
