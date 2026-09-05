package config_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestLoadParsesTheMaintainerAllowlist(t *testing.T) {
	first := uuid.New()
	second := uuid.New()

	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", first.String()+", "+second.String())

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{first, second}, cfg.MaintainerUserIDs)
	require.True(t, cfg.IsMaintainer(first))
	require.True(t, cfg.IsMaintainer(second))
	require.False(t, cfg.IsMaintainer(uuid.New()))
}

func TestLoadTreatsAnUnsetAllowlistAsNobody(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", "")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Empty(t, cfg.MaintainerUserIDs)
	require.False(t, cfg.IsMaintainer(uuid.New()),
		"an unset allowlist must close team creation, never open it")
}

func TestLoadRejectsAMalformedMaintainerID(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", "not-a-uuid")

	_, err := config.Load()

	require.Error(t, err, "a typo in the allowlist must fail startup, not silently drop an entry")
	require.Contains(t, err.Error(), "MAINTAINER_USER_IDS")
}

func TestSupabaseAuthURLDefaultsToTheJWTIssuer(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("SUPABASE_JWT_ISSUER", "https://project.supabase.co/auth/v1")
	t.Setenv("SUPABASE_AUTH_URL", "")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "https://project.supabase.co/auth/v1", cfg.SupabaseAuthURL,
		"the issuer is the auth base URL for a Supabase project; do not make operators set both")
}

func TestInviteRateLimitDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("RATE_LIMIT_INVITE_PER_HOUR", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 20, cfg.InviteRateLimitPerHour)

	t.Setenv("RATE_LIMIT_INVITE_PER_HOUR", "5")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.Equal(t, 5, cfg.InviteRateLimitPerHour)
}

func TestSharedDomainHostnameDefaultsToLocalhost(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "localhost", cfg.SharedDomainHostname)
	require.Equal(t, "http", cfg.ShortURLScheme,
		"a local checkout must produce http:// short URLs, not https://")
}

func TestSharedDomainHostnameComesFromTheEnvironment(t *testing.T) {
	setRequired(t)
	t.Setenv("SHARED_DOMAIN_HOSTNAME", "kurze.url")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "kurze.url", cfg.SharedDomainHostname)
	require.Equal(t, "https", cfg.ShortURLScheme)
}

func TestShortURLSchemeCanBeOverridden(t *testing.T) {
	setRequired(t)
	t.Setenv("SHARED_DOMAIN_HOSTNAME", "kurze.url")
	t.Setenv("SHORT_URL_SCHEME", "http")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "http", cfg.ShortURLScheme)
}

func TestShortURLSchemeRejectsAnythingElse(t *testing.T) {
	setRequired(t)
	t.Setenv("SHORT_URL_SCHEME", "javascript")

	_, err := config.Load()
	require.Error(t, err)
}

func TestAPIHostnameFallsBackToVercelURL(t *testing.T) {
	// Vercel mints a new hostname for every preview deployment, so no fixed
	// API_HOSTNAME can match one. Without this fallback a preview matches
	// nothing, every /v1 request falls through to the redirect surface, and
	// the health probe reports the API unreachable even when it is fine.
	setRequired(t)
	t.Setenv("VERCEL_URL", "kurze-url-abc123-mheobs-projects.vercel.app")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "kurze-url-abc123-mheobs-projects.vercel.app", cfg.APIHostname)
}

func TestExplicitAPIHostnameBeatsVercelURL(t *testing.T) {
	// Production sets API_HOSTNAME to a stable hostname. VERCEL_URL is present
	// there too, and must not win.
	setRequired(t)
	t.Setenv("VERCEL_URL", "kurze-url-abc123-mheobs-projects.vercel.app")
	t.Setenv("API_HOSTNAME", "api.kurze.url")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "api.kurze.url", cfg.APIHostname)
}

func TestBranchURLBeatsVercelURL(t *testing.T) {
	// Both name this deployment, but the web app reaches the API through
	// @vercel/related-projects, which hands it the *branch* alias. Matching
	// VERCEL_URL instead left the API answering /v1 on a hostname its own
	// frontend never calls: requests arrived on the branch alias, missed the
	// hostname gate, and fell through to the redirect surface as a slug.
	setRequired(t)
	t.Setenv("VERCEL_URL", "kurze-url-abc123-mheobs-projects.vercel.app")
	t.Setenv("VERCEL_BRANCH_URL", "kurze-url-api-git-some-branch-mheobs-projects.vercel.app")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "kurze-url-api-git-some-branch-mheobs-projects.vercel.app", cfg.APIHostname)
}

func TestExplicitAPIHostnameBeatsBranchURL(t *testing.T) {
	setRequired(t)
	t.Setenv("VERCEL_BRANCH_URL", "kurze-url-api-git-some-branch-mheobs-projects.vercel.app")
	t.Setenv("API_HOSTNAME", "api.kurze.url")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "api.kurze.url", cfg.APIHostname)
}

func TestAPIHostnameDefaultsToLocalhostOffVercel(t *testing.T) {
	// Local development has neither variable.
	setRequired(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "localhost", cfg.APIHostname)
}
