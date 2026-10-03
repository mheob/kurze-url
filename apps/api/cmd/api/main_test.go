package main

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// redisReply stands in for go-redis's server-reply error, which lives in an
// internal package: anything with a RedisError method satisfies redis.Error,
// and that is all cache.QuotaExceeded looks for.
type redisReply string

func (e redisReply) Error() string { return string(e) }
func (redisReply) RedisError()     {}

// main builds these rules only when SENTRY_DSN is set, which no local run
// and no other test reaches, and NewSlogHandler panics on an unusable rule.
// Building them here is what makes a bad edit fail in CI rather than at a
// Preview or Production boot, where the panic would take the redirect
// surface down with it.
func TestTheSentryCoalesceRulesBuildAHandler(t *testing.T) {
	require.NotPanics(t, func() {
		observability.NewSlogHandler(slog.DiscardHandler, sentryCoalesceRules()...)
	})
}

// The rule exists for one refusal, so it has to recognise it as the cache
// hands it over — wrapped — and coalesce it under the hourly window.
func TestUpstashsQuotaRefusalIsCoalescedHourly(t *testing.T) {
	refusal := fmt.Errorf("cache: redirect lookup: %w",
		redisReply("ERR max requests limit exceeded. Limit: 500000, Usage: 500002"))

	var matched []observability.CoalesceRule
	for _, rule := range sentryCoalesceRules() {
		if rule.Match(refusal) {
			matched = append(matched, rule)
		}
	}

	require.Len(t, matched, 1)
	require.Equal(t, redisQuotaReportInterval, matched[0].Window)
}
