package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/config"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost:54322/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "test-salt")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "8080", cfg.Port)
	require.Equal(t, "localhost", cfg.APIHostname)
	require.Equal(t, 60, cfg.RedirectRateLimitPerMin)
	require.Equal(t, 5, cfg.PasswordRateLimitPerMin)
	require.Equal(t, 20, cfg.LinkCreateRateLimitPerMin)
	require.Equal(t, time.Hour, cfg.LinkCacheTTL)
	require.Equal(t, time.Minute, cfg.NotFoundCacheTTL)
	require.Equal(t, 25*time.Hour, cfg.UniqueVisitorTTL)
}

func TestLoadOverridesFromEnv(t *testing.T) {
	setRequired(t)
	t.Setenv("PORT", "3000")
	t.Setenv("API_HOSTNAME", "api.kurze.url")
	t.Setenv("RATE_LIMIT_REDIRECT_PER_MIN", "120")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "3000", cfg.Port)
	require.Equal(t, "api.kurze.url", cfg.APIHostname)
	require.Equal(t, 120, cfg.RedirectRateLimitPerMin)
}

func TestLoadRejectsMissingRequiredVar(t *testing.T) {
	setRequired(t)
	t.Setenv("VISITOR_SALT", "")

	_, err := config.Load()

	require.ErrorContains(t, err, "VISITOR_SALT")
}

func TestLoadRejectsNonNumericRateLimit(t *testing.T) {
	setRequired(t)
	t.Setenv("RATE_LIMIT_REDIRECT_PER_MIN", "many")

	_, err := config.Load()

	require.ErrorContains(t, err, "RATE_LIMIT_REDIRECT_PER_MIN")
}
