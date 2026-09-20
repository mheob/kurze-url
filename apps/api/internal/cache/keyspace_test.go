package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

// Preview and production share one Upstash database, because the free tier
// allows exactly one. The key prefix is therefore the only thing keeping an
// unreviewed preview deployment out of the keyspace production serves
// redirects from. These tests are what hold that apart.

const day = "2026-09-20"

func TestProductionUsesTheBareKeyspace(t *testing.T) {
	ctx := context.Background()
	client := newClientAt(t, newTestRedis(t), "production")
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Minute))

	require.Equal(t, key, client.Key(key))
	_, err := client.Raw().Get(ctx, key).Result()
	require.NoError(t, err, "production must keep the unprefixed keyspace it already has")
}

func TestANonProductionEnvironmentScopesEveryKey(t *testing.T) {
	ctx := context.Background()
	client := newClientAt(t, newTestRedis(t), "preview")
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Minute))

	_, err := client.Raw().Get(ctx, key).Result()
	require.ErrorIs(t, err, redis.Nil, "a preview write must not land on the bare key")

	_, err = client.Raw().Get(ctx, "preview:"+key).Result()
	require.NoError(t, err)
}

// The environment comes from VERCEL_ENV, which is absent unless the Vercel
// project exposes system environment variables — a dashboard setting nothing
// in CI verifies. Absent must not mean production.
func TestAnUnsetEnvironmentIsNotTreatedAsProduction(t *testing.T) {
	ctx := context.Background()
	client := newClientAt(t, newTestRedis(t), "")
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Minute))

	_, err := client.Raw().Get(ctx, key).Result()
	require.ErrorIs(t, err, redis.Nil, "an unset environment must fail closed, not open")
}

func TestTwoEnvironmentsDoNotSeeEachOthersLinks(t *testing.T) {
	ctx := context.Background()
	url := newTestRedis(t)
	production := newClientAt(t, url, "production")
	preview := newClientAt(t, url, "preview")
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, production.PutLink(ctx, key, sample(), time.Minute))

	got, err := preview.LookupForRedirect(ctx, key, "visitor-a", day, time.Hour)
	require.NoError(t, err)
	require.False(t, got.Found, "preview read production's cached link")
}

// The unique-visitor set key is built twice — here in Go for the cache-miss
// path, and inside redirect_lookup.lua for the cache-hit path. Prefixing one
// and not the other produces no error at all: the two paths simply stop
// agreeing on which set a visitor is in, and every returning visitor is
// counted as new. This test drives both against one link.
func TestBothUniqueVisitorPathsBuildTheSameKey(t *testing.T) {
	ctx := context.Background()
	client := newClientAt(t, newTestRedis(t), "preview")
	key := link.CacheKey("short.test", "hello")
	cached := sample()

	require.NoError(t, client.PutLink(ctx, key, cached, time.Minute))

	first, err := client.MarkUniqueVisit(ctx, cached.ID.String(), day, "visitor-a", time.Hour)
	require.NoError(t, err)
	require.True(t, first)

	got, err := client.LookupForRedirect(ctx, key, "visitor-a", day, time.Hour)
	require.NoError(t, err)
	require.True(t, got.Found)
	require.False(t, got.UniqueVisit,
		"the Lua path looked in a different set than MarkUniqueVisit wrote to")

	// A visitor neither path has seen must still count as unique, or the
	// assertion above would also pass with a set nothing can reach.
	got, err = client.LookupForRedirect(ctx, key, "visitor-b", day, time.Hour)
	require.NoError(t, err)
	require.True(t, got.UniqueVisit)
}
