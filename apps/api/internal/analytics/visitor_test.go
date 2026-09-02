package analytics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

const (
	secret = "test-secret"
	ip     = "203.0.113.42"
	ua     = "Mozilla/5.0 (X11; Linux x86_64) Firefox/141.0"
)

func at(day int) time.Time {
	return time.Date(2026, 9, day, 13, 37, 0, 0, time.UTC)
}

func TestVisitorHashIsStableWithinADay(t *testing.T) {
	morning := time.Date(2026, 9, 2, 0, 0, 1, 0, time.UTC)
	evening := time.Date(2026, 9, 2, 23, 59, 59, 0, time.UTC)

	require.Equal(t,
		analytics.VisitorHash(secret, ip, ua, morning),
		analytics.VisitorHash(secret, ip, ua, evening),
	)
}

func TestVisitorHashRotatesDaily(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, ip, ua, at(3)),
		"yesterday's hash must not be correlatable with today's",
	)
}

func TestVisitorHashSeparatesDifferentVisitors(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, "198.51.100.7", ua, at(2)),
	)
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, ip, "curl/8.7.1", at(2)),
	)
}

func TestVisitorHashDoesNotAmbiguateFieldBoundaries(t *testing.T) {
	// Without a separator between the fields, "1.2.3" + "4Firefox" and
	// "1.2.34" + "Firefox" would hash identically.
	require.NotEqual(t,
		analytics.VisitorHash(secret, "1.2.3", "4Firefox", at(2)),
		analytics.VisitorHash(secret, "1.2.34", "Firefox", at(2)),
	)
}

func TestVisitorHashRevealsNothingAboutTheInput(t *testing.T) {
	hash := analytics.VisitorHash(secret, ip, ua, at(2))

	require.Len(t, hash, 32)
	require.NotContains(t, hash, ip)
	require.NotContains(t, strings.ToLower(hash), "firefox")
}

func TestVisitorHashChangesWithTheSecret(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash("another-secret", ip, ua, at(2)),
	)
}

func TestDayIsUTCDateOnly(t *testing.T) {
	// 00:30 in UTC+2 is still the previous day in UTC.
	berlin := time.FixedZone("CEST", 2*60*60)
	require.Equal(t, "2026-09-01",
		analytics.Day(time.Date(2026, 9, 2, 0, 30, 0, 0, berlin)))
}

func TestRateLimitKeyIsStableForTheSameIP(t *testing.T) {
	require.Equal(t,
		analytics.RateLimitKey(secret, ip),
		analytics.RateLimitKey(secret, ip),
	)
}

func TestRateLimitKeySeparatesDifferentIPs(t *testing.T) {
	require.NotEqual(t,
		analytics.RateLimitKey(secret, ip),
		analytics.RateLimitKey(secret, "198.51.100.7"),
	)
}

func TestRateLimitKeyRevealsNothingAboutTheIP(t *testing.T) {
	key := analytics.RateLimitKey(secret, ip)

	require.NotContains(t, key, ip)
}

func TestRateLimitKeyDoesNotRotateDaily(t *testing.T) {
	// Unlike VisitorHash, a rate-limit budget must not reset at midnight, or
	// it would be trivially gameable.
	require.Equal(t,
		analytics.RateLimitKey(secret, ip),
		analytics.RateLimitKey(secret, ip),
		"RateLimitKey takes no time input at all — this pins that contract",
	)
}
