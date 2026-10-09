package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAThreatConfirmationRoundTripsUnderTheEnvironmentPrefix(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	require.NoError(t, client.ConfirmThreats(ctx, "link-1",
		[]string{"MALWARE", "SOCIAL_ENGINEERING"}, 10*time.Minute))

	threats, ok, err := client.ThreatConfirmation(ctx, "link-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"MALWARE", "SOCIAL_ENGINEERING"}, threats)

	ttl, err := client.Raw().TTL(ctx, client.Key("sb:confirmed:link-1")).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 9*time.Minute)

	_, err = client.Raw().Get(ctx, "sb:confirmed:link-1").Result()
	require.ErrorIs(t, err, redis.Nil, "a preview must not confirm a block in production's keyspace")
}

func TestAMissingConfirmationIsNotAnError(t *testing.T) {
	_, ok, err := newTestClient(t).ThreatConfirmation(context.Background(), "nobody")
	require.NoError(t, err)
	require.False(t, ok)
}

// go-redis reads a zero expiration as "keep forever", and a confirmation that
// never expires is a block on data Google's terms call stale after thirty
// minutes. A confirmation naming no threat would block with no reason to show.
func TestAConfirmationWithoutAPositiveTTLOrAThreatIsRefused(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	require.Error(t, client.ConfirmThreats(ctx, "link-2", []string{"MALWARE"}, 0))
	require.Error(t, client.ConfirmThreats(ctx, "link-2", []string{"MALWARE"}, -time.Minute))
	require.Error(t, client.ConfirmThreats(ctx, "link-2", nil, time.Minute))

	_, ok, err := client.ThreatConfirmation(ctx, "link-2")
	require.NoError(t, err)
	require.False(t, ok, "a refused confirmation must not have been written")
}

func TestClearingAConfirmationRemovesIt(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	require.NoError(t, client.ConfirmThreats(ctx, "link-3", []string{"MALWARE"}, time.Minute))

	require.NoError(t, client.ClearThreatConfirmation(ctx, "link-3"))

	_, ok, err := client.ThreatConfirmation(ctx, "link-3")
	require.NoError(t, err)
	require.False(t, ok)
}

// Only a hand-written key could look like this, and it must not block.
func TestAConfirmationNamingNoThreatDoesNotCount(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	require.NoError(t, client.Raw().Set(ctx, client.Key("sb:confirmed:link-4"), ",", time.Minute).Err())

	_, ok, err := client.ThreatConfirmation(ctx, "link-4")
	require.NoError(t, err)
	require.False(t, ok)
}
