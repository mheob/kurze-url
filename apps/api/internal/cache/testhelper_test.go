package cache_test

import (
	"context"
	"testing"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/cache"
)

// newTestRedis starts a throwaway Redis container and returns its URL. Skips
// when Docker is unavailable so the suite stays usable on a machine without
// it. Separate from newTestClient below because the keyspace tests need two
// clients on one container — that is the whole point of what they assert.
func newTestRedis(t *testing.T) string {
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

	return url
}

// newClientAt points a client at an already-running Redis, in the named
// environment.
func newClientAt(t *testing.T, url, environment string) *cache.Client {
	t.Helper()

	client, err := cache.New(url, environment)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// newTestClient starts a throwaway Redis container and returns a client
// pointed at it. The environment is deliberately not "production": every test
// that uses this helper then runs through the prefixing path rather than
// around it.
func newTestClient(t *testing.T) *cache.Client {
	t.Helper()
	return newClientAt(t, newTestRedis(t), "test")
}
