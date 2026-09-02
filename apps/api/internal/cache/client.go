// Package cache owns every Redis interaction: the link cache that fronts the
// redirect path, unique-visitor deduplication, and rate limiting. No other
// package issues Redis commands.
package cache

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/ratelimit.lua
var rateLimitSource string

var rateLimitScript = redis.NewScript(rateLimitSource)

// Client wraps the Redis connection with the small set of operations the
// redirect path needs. Every method costs a known number of Redis commands —
// the free tier's 500K/month ceiling is the binding constraint on this project,
// so that cost is part of each method's contract.
type Client struct {
	rdb *redis.Client
}

// New dials Redis from a redis:// or rediss:// URL.
func New(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse redis url: %w", err)
	}
	return &Client{rdb: redis.NewClient(opts)}, nil
}

// Close releases the underlying Redis connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Allow applies a sliding-window rate limit to key. Costs one Redis command
// (a single EVAL, regardless of how many operations the script performs).
func (c *Client) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	res, err := rateLimitScript.Run(ctx, c.rdb,
		[]string{key},
		limit,
		int(window.Seconds()),
		time.Now().UnixMilli(),
	).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("cache: rate limit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("cache: rate limit: unexpected reply length %d", len(res))
	}
	return res[0] == 1, int(res[1]), nil
}
