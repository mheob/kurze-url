// Package config loads the API's runtime settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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

	// Only these user IDs may create teams. Empty means nobody can: this is a
	// shared instance, and an open POST /v1/teams is the classic URL-shortener
	// abuse vector. A misconfigured deployment must close team creation, not
	// open it, so this is deliberately not a required variable.
	MaintainerUserIDs []uuid.UUID

	// Supabase's auth base URL and service-role key, used for exactly one
	// call: POST {SupabaseAuthURL}/invite. Both empty means invitations are
	// unavailable and the members endpoint refuses the new-address branch.
	SupabaseAuthURL        string
	SupabaseServiceRoleKey string

	RedirectRateLimitPerMin   int
	PasswordRateLimitPerMin   int
	LinkCreateRateLimitPerMin int
	InviteRateLimitPerHour    int

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

	cfg.SupabaseAuthURL = env("SUPABASE_AUTH_URL", cfg.JWTIssuer)
	cfg.SupabaseServiceRoleKey = os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	maintainers, err := envUUIDs("MAINTAINER_USER_IDS")
	if err != nil {
		return Config{}, err
	}
	cfg.MaintainerUserIDs = maintainers

	if cfg.RedirectRateLimitPerMin, err = envInt("RATE_LIMIT_REDIRECT_PER_MIN", 60); err != nil {
		return Config{}, err
	}
	if cfg.PasswordRateLimitPerMin, err = envInt("RATE_LIMIT_PASSWORD_PER_MIN", 5); err != nil {
		return Config{}, err
	}
	if cfg.LinkCreateRateLimitPerMin, err = envInt("RATE_LIMIT_LINK_CREATE_PER_MIN", 20); err != nil {
		return Config{}, err
	}
	if cfg.InviteRateLimitPerHour, err = envInt("RATE_LIMIT_INVITE_PER_HOUR", 20); err != nil {
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

// envUUIDs parses a comma-separated list of UUIDs. A malformed entry fails
// startup: silently dropping it would quietly change who may create teams.
func envUUIDs(name string) ([]uuid.UUID, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}

	var ids []uuid.UUID
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			return nil, fmt.Errorf("config: %s contains an invalid uuid %q: %w", name, part, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// IsMaintainer reports whether the user may create teams.
func (c Config) IsMaintainer(id uuid.UUID) bool {
	for _, allowed := range c.MaintainerUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}
