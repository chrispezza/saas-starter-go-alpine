package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear any env vars that might interfere
	envVars := []string{"ENV", "HTTP_PORT", "DATABASE_URL", "SUPABASE_URL", "SUPABASE_ANON_KEY", "SUPABASE_SERVICE_ROLE_KEY"}
	saved := make(map[string]string)
	for _, key := range envVars {
		saved[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for key, val := range saved {
			if val != "" {
				os.Setenv(key, val)
			}
		}
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Env != "development" {
		t.Errorf("Env = %q, want %q", cfg.Env, "development")
	}
	if cfg.HTTPPort != "4000" {
		t.Errorf("HTTPPort = %q, want %q", cfg.HTTPPort, "4000")
	}
}

func TestLoad_FromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		checkFn func(t *testing.T, cfg *Config)
	}{
		{
			name: "all fields from env",
			envVars: map[string]string{
				"ENV":                       "production",
				"HTTP_PORT":                 "8080",
				"DATABASE_URL":              "postgres://localhost:5432/test",
				"SUPABASE_URL":              "https://test.supabase.co",
				"SUPABASE_ANON_KEY":         "anon-key-123",
				"SUPABASE_SERVICE_ROLE_KEY": "service-role-key-456",
			},
			checkFn: func(t *testing.T, cfg *Config) {
				if cfg.Env != "production" {
					t.Errorf("Env = %q, want %q", cfg.Env, "production")
				}
				if cfg.HTTPPort != "8080" {
					t.Errorf("HTTPPort = %q, want %q", cfg.HTTPPort, "8080")
				}
				if cfg.DatabaseURL != "postgres://localhost:5432/test" {
					t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://localhost:5432/test")
				}
				if cfg.SupabaseURL != "https://test.supabase.co" {
					t.Errorf("SupabaseURL = %q, want %q", cfg.SupabaseURL, "https://test.supabase.co")
				}
				if cfg.SupabaseAnonKey != "anon-key-123" {
					t.Errorf("SupabaseAnonKey = %q, want %q", cfg.SupabaseAnonKey, "anon-key-123")
				}
				if cfg.SupabaseServiceRoleKey != "service-role-key-456" {
					t.Errorf("SupabaseServiceRoleKey = %q, want %q", cfg.SupabaseServiceRoleKey, "service-role-key-456")
				}
			},
		},
		{
			name: "partial env uses defaults",
			envVars: map[string]string{
				"SUPABASE_URL": "https://partial.supabase.co",
			},
			checkFn: func(t *testing.T, cfg *Config) {
				if cfg.Env != "development" {
					t.Errorf("Env = %q, want default %q", cfg.Env, "development")
				}
				if cfg.HTTPPort != "4000" {
					t.Errorf("HTTPPort = %q, want default %q", cfg.HTTPPort, "4000")
				}
				if cfg.SupabaseURL != "https://partial.supabase.co" {
					t.Errorf("SupabaseURL = %q, want %q", cfg.SupabaseURL, "https://partial.supabase.co")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and clear all config env vars
			allKeys := []string{"ENV", "HTTP_PORT", "DATABASE_URL", "SUPABASE_URL", "SUPABASE_ANON_KEY", "SUPABASE_SERVICE_ROLE_KEY"}
			saved := make(map[string]string)
			for _, key := range allKeys {
				saved[key] = os.Getenv(key)
				os.Unsetenv(key)
			}
			t.Cleanup(func() {
				for key, val := range saved {
					if val != "" {
						os.Setenv(key, val)
					} else {
						os.Unsetenv(key)
					}
				}
			})

			// Set test env vars
			for key, val := range tt.envVars {
				os.Setenv(key, val)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			tt.checkFn(t, cfg)
		})
	}
}

// TestValidate_PublicBaseURL pins the contract for the optional public
// origin: unset is fine (canonical links and the sitemap are simply
// omitted), a valid absolute http(s) origin is normalized without a trailing
// slash, and anything that is not an absolute origin fails fast at boot
// (ADR-015) instead of producing broken canonical URLs.
func TestValidate_PublicBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		wantErr bool
		want    string // normalized value after Validate
	}{
		{name: "unset is allowed", base: "", want: ""},
		{name: "https origin is kept", base: "https://demo.example.com", want: "https://demo.example.com"},
		{name: "trailing slash is trimmed", base: "https://demo.example.com/", want: "https://demo.example.com"},
		{name: "http origin is allowed for local previews", base: "http://localhost:4000", want: "http://localhost:4000"},
		{name: "path prefix is kept without trailing slash", base: "https://example.com/demo/", want: "https://example.com/demo"},
		{name: "scheme-less value is rejected", base: "demo.example.com", wantErr: true},
		{name: "non-http scheme is rejected", base: "ftp://demo.example.com", wantErr: true},
		{name: "query string is rejected", base: "https://demo.example.com/?x=1", wantErr: true},
		{name: "fragment is rejected", base: "https://demo.example.com/#top", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Env:                 "development",
				HTTPPort:            "4000",
				DatabaseURL:         "postgres://localhost:5432/test",
				DBMaxConns:          25,
				MaxRequestBodyBytes: 1,
				PublicBaseURL:       tt.base,
			}
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error for PUBLIC_BASE_URL %q", tt.base)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if cfg.PublicBaseURL != tt.want {
				t.Errorf("PublicBaseURL after Validate = %q, want %q", cfg.PublicBaseURL, tt.want)
			}
		})
	}
}
