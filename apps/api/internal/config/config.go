// Package config loads the API's runtime settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every runtime setting the API needs. It is loaded once at
// startup and passed by value; nothing mutates it afterwards.
type Config struct {
	Port        string
	DatabaseURL string
	RedisURL    string

	// APIHostname is the single hostname that serves /v1. Every other Host
	// header is treated as a short-link domain and routed to the redirect
	// surface.
	APIHostname string

	JWKSURL     string
	JWTIssuer   string
	JWTAudience string

	// VisitorSalt is the secret keying the daily-rotating visitor hash. It is
	// never logged and never leaves the process.
	VisitorSalt string

	RedirectRateLimitPerMin   int
	PasswordRateLimitPerMin   int
	LinkCreateRateLimitPerMin int

	LinkCacheTTL     time.Duration
	NotFoundCacheTTL time.Duration
	UniqueVisitorTTL time.Duration
}

// Load reads the environment and validates that the required settings are
// present. JWKS settings are optional here: the API starts without them and
// only /v1 operations that declare bearerAuth fail, which keeps the redirect
// surface runnable in local development with no Supabase project.
func Load() (Config, error) {
	cfg := Config{
		Port:        env("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		APIHostname: env("API_HOSTNAME", "localhost"),
		JWKSURL:     os.Getenv("SUPABASE_JWKS_URL"),
		JWTIssuer:   os.Getenv("SUPABASE_JWT_ISSUER"),
		JWTAudience: env("SUPABASE_JWT_AUDIENCE", "authenticated"),
		VisitorSalt: os.Getenv("VISITOR_SALT"),

		LinkCacheTTL:     time.Hour,
		NotFoundCacheTTL: time.Minute,
		UniqueVisitorTTL: 25 * time.Hour,
	}

	for _, required := range []struct {
		name  string
		value string
	}{
		{"DATABASE_URL", cfg.DatabaseURL},
		{"REDIS_URL", cfg.RedisURL},
		{"VISITOR_SALT", cfg.VisitorSalt},
	} {
		if required.value == "" {
			return Config{}, fmt.Errorf("config: %s is required", required.name)
		}
	}

	var err error
	if cfg.RedirectRateLimitPerMin, err = envInt("RATE_LIMIT_REDIRECT_PER_MIN", 60); err != nil {
		return Config{}, err
	}
	if cfg.PasswordRateLimitPerMin, err = envInt("RATE_LIMIT_PASSWORD_PER_MIN", 5); err != nil {
		return Config{}, err
	}
	if cfg.LinkCreateRateLimitPerMin, err = envInt("RATE_LIMIT_LINK_CREATE_PER_MIN", 20); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", name, err)
	}
	return v, nil
}
