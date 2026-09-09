package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAllowPermitsUpToTheLimitThenRejects(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for i := range 5 {
		allowed, remaining, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
		require.NoError(t, err)
		require.True(t, allowed, "request %d of 5 must be allowed", i+1)
		require.Equal(t, 4-i, remaining)
	}

	allowed, remaining, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed, "the sixth request must be rejected")
	require.Zero(t, remaining)
}

func TestAllowIsScopedPerKey(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for range 5 {
		_, _, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
		require.NoError(t, err)
	}

	allowed, _, err := client.Allow(ctx, "rl:test:b", 5, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed, "a different key must have its own budget")
}

func TestAllowRecoversAfterTheWindowPasses(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for range 2 {
		_, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
		require.NoError(t, err)
	}

	blocked, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
	require.NoError(t, err)
	require.False(t, blocked)

	// Two full windows clears both the current and the previous counter the
	// sliding-window estimate reads.
	time.Sleep(2100 * time.Millisecond)

	allowed, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
	require.NoError(t, err)
	require.True(t, allowed, "the budget must recover once the window rolls over")
}

// TestWithinLimitAndIncrementShareAWindowSlot is the one test that matters for
// this pair. They are two scripts doing half a job each, and if their slot
// arithmetic ever disagrees — a different divisor, a different rounding — the
// increment lands in a slot the peek never reads, the limit silently never
// fires, and nothing else in the suite notices.
func TestWithinLimitAndIncrementShareAWindowSlot(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	key := "test:peek:" + uuid.NewString()

	within, err := client.WithinLimit(ctx, key, 3, time.Hour)
	require.NoError(t, err)
	require.True(t, within, "a fresh key must start inside the limit")

	for range 3 {
		require.NoError(t, client.Increment(ctx, key, time.Hour))
	}

	within, err = client.WithinLimit(ctx, key, 3, time.Hour)
	require.NoError(t, err)
	require.False(t, within,
		"three increments against a limit of three must close the window; "+
			"a peek that still reports true means the two scripts disagree about the slot")
}

// TestWithinLimitDoesNotConsumeTheBudget pins the property the whole split
// exists for: a check is free, so a visitor who knows the password never
// spends a failure budget by arriving.
func TestWithinLimitDoesNotConsumeTheBudget(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	key := "test:peek-free:" + uuid.NewString()

	for range 10 {
		within, err := client.WithinLimit(ctx, key, 1, time.Hour)
		require.NoError(t, err)
		require.True(t, within)
	}
}
