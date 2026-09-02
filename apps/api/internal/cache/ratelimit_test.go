package cache_test

import (
	"context"
	"testing"
	"time"

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
