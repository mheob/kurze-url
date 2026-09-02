package cache_test

import (
	"context"
	"testing"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/cache"
)

// newTestClient starts a throwaway Redis container and returns a client
// pointed at it. Skips when Docker is unavailable so the suite stays usable
// on a machine without it.
func newTestClient(t *testing.T) *cache.Client {
	t.Helper()

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	client, err := cache.New(url)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}
