package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kelseyhightower/envconfig"
)

// Config holds the application configuration.
type Config struct {
	Env                    string `envconfig:"ENV" default:"development"`
	HTTPPort               string `envconfig:"HTTP_PORT" default:"4000"`
	DatabaseURL            string `envconfig:"DATABASE_URL"`
	SupabaseURL            string `envconfig:"SUPABASE_URL"`
	SupabaseAnonKey        string `envconfig:"SUPABASE_ANON_KEY"`
	SupabaseServiceRoleKey string `envconfig:"SUPABASE_SERVICE_ROLE_KEY"`

	// MetricsToken gates /metrics: required as a bearer token when set;
	// with no token, /metrics is open in dev and hidden in production.
	MetricsToken string `envconfig:"METRICS_TOKEN"`

	// ClientIPHeader names the edge proxy's authoritative client-IP header
	// (Fly-Client-IP, CF-Connecting-IP), consulted before X-Forwarded-For —
	// and still only when the direct peer is a trusted proxy (ADR-027,
	// amended 2026-07-12). Empty (default) resolves via X-Forwarded-For.
	ClientIPHeader string `envconfig:"CLIENT_IP_HEADER"`

	// TrustedProxyCIDRs lists the proxy networks whose X-Forwarded-For /
	// X-Real-IP headers are honored for client-IP resolution (ADR-027).
	// Empty (default) trusts no forwarded headers — the direct peer IP is
	// used, which fails closed toward more rate limiting rather than
	// trusting a spoofable header. Set to the edge proxy's egress ranges
	// (e.g. Cloudflare) in production.
	TrustedProxyCIDRs []string `envconfig:"TRUSTED_PROXY_CIDRS"`

	// MaxRequestBodyBytes caps the request body size accepted before a
	// handler reads it, bounding memory use on form/upload endpoints
	// (2026-07-06 audit). Default 1 MiB.
	MaxRequestBodyBytes int64 `envconfig:"MAX_REQUEST_BODY_BYTES" default:"1048576"`

	// PublicBaseURL is the origin the app is reachable at from the internet
	// (scheme + host, optional path prefix, no trailing slash). It feeds the
	// canonical link, og:url/og:image and the sitemap; unset omits them.
	PublicBaseURL string `envconfig:"PUBLIC_BASE_URL"`

	// Database pool tuning (ADR-025; pgxpool defaults are too small for
	// production — MaxConns defaults to max(4, CPUs)).
	DBMaxConns        int32         `envconfig:"DB_MAX_CONNS" default:"25"`
	DBMinConns        int32         `envconfig:"DB_MIN_CONNS" default:"2"`
	DBMaxConnLifetime time.Duration `envconfig:"DB_MAX_CONN_LIFETIME" default:"30m"`
	DBMaxConnIdleTime time.Duration `envconfig:"DB_MAX_CONN_IDLE_TIME" default:"5m"`

	// Guest mode (ADR-024): anonymous Supabase identities for visitors.
	// Requires "anonymous sign-ins" enabled in the Supabase project.
	GuestModeEnabled bool `envconfig:"GUEST_MODE_ENABLED" default:"false"`
	// GuestTTL is how long an unupgraded anonymous account lives before the
	// reaper removes it (measured from creation).
	GuestTTL time.Duration `envconfig:"GUEST_TTL" default:"720h"`
	// ReaperInterval is how often the reaper pass runs.
	ReaperInterval time.Duration `envconfig:"REAPER_INTERVAL" default:"1h"`
}

// Load loads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		slog.Error("Failed to process envconfig", "error", err)
		return nil, err
	}

	slog.Info("Configuration loaded successfully")
	return &cfg, nil
}

var validEnvs = map[string]bool{
	"development": true,
	"test":        true,
	"staging":     true,
	"production":  true,
}

// Validate fails fast on misconfiguration at boot instead of at first use
// (ADR-015; 2026-07-05 audit: a missing DATABASE_URL previously crashed at
// pool creation, an ENV typo silently disabled production behavior).
func (c *Config) Validate() error {
	if !validEnvs[c.Env] {
		return fmt.Errorf("ENV %q invalid: must be development, test, staging, or production", c.Env)
	}
	if port, err := strconv.Atoi(c.HTTPPort); err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("HTTP_PORT %q invalid: must be a number between 1 and 65535", c.HTTPPort)
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if (c.SupabaseURL == "") != (c.SupabaseAnonKey == "") {
		return fmt.Errorf("SUPABASE_URL and SUPABASE_ANON_KEY must be set together (auth is disabled when both are empty)")
	}
	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS %d invalid: must be at least 1", c.DBMaxConns)
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS %d invalid: must be between 0 and DB_MAX_CONNS (%d)", c.DBMinConns, c.DBMaxConns)
	}
	if c.MaxRequestBodyBytes < 1 {
		return fmt.Errorf("MAX_REQUEST_BODY_BYTES %d invalid: must be at least 1", c.MaxRequestBodyBytes)
	}
	if c.PublicBaseURL != "" {
		base, err := normalizePublicBaseURL(c.PublicBaseURL)
		if err != nil {
			return fmt.Errorf("PUBLIC_BASE_URL %q invalid: %w", c.PublicBaseURL, err)
		}
		c.PublicBaseURL = base
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("TRUSTED_PROXY_CIDRS entry %q invalid: %w", cidr, err)
		}
	}
	if c.GuestModeEnabled {
		if c.SupabaseURL == "" {
			return fmt.Errorf("GUEST_MODE_ENABLED requires SUPABASE_URL and SUPABASE_ANON_KEY (guests are real Supabase identities)")
		}
		if c.GuestTTL <= 0 {
			return fmt.Errorf("GUEST_TTL %v invalid: must be positive", c.GuestTTL)
		}
		if c.ReaperInterval <= 0 {
			return fmt.Errorf("REAPER_INTERVAL %v invalid: must be positive", c.ReaperInterval)
		}
	}
	return nil
}

// IsProduction reports whether the app runs with production behavior
// (HSTS, secure cookies, hidden unauthenticated /metrics).
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// PoolConfig builds the pgxpool configuration from DATABASE_URL plus the
// pool-tuning fields above.
func (c *Config) PoolConfig() (*pgxpool.Config, error) {
	pc, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pc.MaxConns = c.DBMaxConns
	pc.MinConns = c.DBMinConns
	pc.MaxConnLifetime = c.DBMaxConnLifetime
	pc.MaxConnIdleTime = c.DBMaxConnIdleTime
	return pc, nil
}

// normalizePublicBaseURL accepts an absolute http(s) origin with an optional
// path prefix and returns it without a trailing slash, so callers can join
// request paths onto it verbatim. Query strings and fragments are rejected:
// they can never be part of a canonical origin.
func normalizePublicBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must not carry a query string or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
