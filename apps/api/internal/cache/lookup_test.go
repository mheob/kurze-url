package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

func sample() link.Cached {
	return link.Cached{
		ID:               uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TeamID:           uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		DestinationURL:   "https://example.org/a|b?c=d",
		RedirectType:     302,
		State:            "active",
		HasPassword:      false,
		AnalyticsEnabled: true,
	}
}

func TestLookupReportsMissForAnUncachedKey(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	got, err := client.LookupForRedirect(ctx, link.CacheKey("short.test", "nope"), "v1", "2026-09-02", time.Hour)

	require.NoError(t, err)
	require.False(t, got.Found)
	require.False(t, got.NegativelyCached)
}

func TestLookupReturnsThePutLinkAndCountsTheFirstVisitAsUnique(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Hour))

	first, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, first.Found)
	require.False(t, first.NegativelyCached)
	require.Equal(t, sample(), first.Link, "a destination containing a pipe must survive the round trip")
	require.True(t, first.UniqueVisit)

	second, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, second.Found)
	require.False(t, second.UniqueVisit, "the same visitor on the same day is not unique again")

	other, err := client.LookupForRedirect(ctx, key, "visitor-b", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, other.UniqueVisit, "a different visitor is unique")

	nextDay, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-03", time.Hour)
	require.NoError(t, err)
	require.True(t, nextDay.UniqueVisit, "the same visitor on a new day is unique again")
}

func TestLookupReportsNegativeCache(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "gone")

	require.NoError(t, client.PutNotFound(ctx, key, time.Minute))

	got, err := client.LookupForRedirect(ctx, key, "v1", "2026-09-02", time.Hour)

	require.NoError(t, err)
	require.False(t, got.Found)
	require.True(t, got.NegativelyCached)
}

func TestInvalidateRemovesTheCachedLink(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Hour))
	require.NoError(t, client.InvalidateLink(ctx, key))

	got, err := client.LookupForRedirect(ctx, key, "v1", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.False(t, got.Found)
}
