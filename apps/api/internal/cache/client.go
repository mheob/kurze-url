// Package cache owns every Redis interaction: the link cache that fronts the
// redirect path, unique-visitor deduplication, and rate limiting. No other
// package issues Redis commands.
package cache

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

//go:embed lua/ratelimit.lua
var rateLimitSource string

var rateLimitScript = redis.NewScript(rateLimitSource)

//go:embed lua/redirect_lookup.lua
var redirectLookupSource string

var redirectLookupScript = redis.NewScript(redirectLookupSource)

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

// Lookup is the result of a redirect-path cache read.
type Lookup struct {
	// Found reports whether a live link record was cached.
	Found bool
	// NegativelyCached reports that a recent lookup already established there
	// is no such link — the caller must not hit Postgres again.
	NegativelyCached bool
	Link             link.Cached
	// UniqueVisit is true when this visitor hash had not been seen for this
	// link today. Meaningless unless Found is true.
	UniqueVisit bool
}

// LookupForRedirect reads the cached link and deduplicates the visitor in one
// Redis command. visitorHash must be the daily-rotating hash from
// analytics.VisitorHash — a raw IP must never reach this method.
func (c *Client) LookupForRedirect(
	ctx context.Context,
	cacheKey, visitorHash, day string,
	uniqueTTL time.Duration,
) (Lookup, error) {
	res, err := redirectLookupScript.Run(ctx, c.rdb,
		[]string{cacheKey},
		link.UniqueSetPrefix,
		visitorHash,
		day,
		int(uniqueTTL.Seconds()),
	).Slice()
	if err != nil {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: %w", err)
	}
	if len(res) != 2 {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: unexpected reply length %d", len(res))
	}

	raw, ok := res[0].(string)
	if !ok {
		return Lookup{}, nil // nil reply: cache miss
	}
	if raw == link.NotFoundSentinel {
		return Lookup{NegativelyCached: true}, nil
	}

	_, payload, found := strings.Cut(raw, "|")
	if !found {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: malformed cached value")
	}

	var cached link.Cached
	if err := json.Unmarshal([]byte(payload), &cached); err != nil {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: decode: %w", err)
	}

	unique, _ := res[1].(int64)
	return Lookup{Found: true, Link: cached, UniqueVisit: unique == 1}, nil
}

// PutLink caches a link record. The stored value is "<link id>|<json>" so the
// lookup script can extract the id without parsing JSON.
func (c *Client) PutLink(ctx context.Context, cacheKey string, l link.Cached, ttl time.Duration) error {
	payload, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("cache: encode link: %w", err)
	}
	value := l.ID.String() + "|" + string(payload)
	if err := c.rdb.Set(ctx, cacheKey, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put link: %w", err)
	}
	return nil
}

// PutNotFound negatively caches an unresolvable key for a short TTL.
func (c *Client) PutNotFound(ctx context.Context, cacheKey string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, cacheKey, link.NotFoundSentinel, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put not-found: %w", err)
	}
	return nil
}

// MarkUniqueVisit records a visitor against a link's daily set, for the
// cache-miss path where LookupForRedirect had no link id to work with.
func (c *Client) MarkUniqueVisit(
	ctx context.Context,
	linkID, day, visitorHash string,
	ttl time.Duration,
) (bool, error) {
	key := link.UniqueSetPrefix + linkID + ":" + day
	added, err := c.rdb.SAdd(ctx, key, visitorHash).Result()
	if err != nil {
		return false, fmt.Errorf("cache: mark unique visit: %w", err)
	}
	if added == 1 {
		if err := c.rdb.Expire(ctx, key, ttl).Err(); err != nil {
			return false, fmt.Errorf("cache: mark unique visit: expire: %w", err)
		}
	}
	return added == 1, nil
}

// InvalidateLink drops a cached link. Every mutation of a link in plan 2 must
// call this, or a 302's "destination changes take effect immediately" promise
// is only true after LinkCacheTTL elapses.
func (c *Client) InvalidateLink(ctx context.Context, cacheKey string) error {
	if err := c.rdb.Del(ctx, cacheKey).Err(); err != nil {
		return fmt.Errorf("cache: invalidate link: %w", err)
	}
	return nil
}
