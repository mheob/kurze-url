// Package link holds the link record shared between the cache and the HTTP
// handlers, and the key layout the redirect path depends on.
package link

import (
	"time"

	"github.com/google/uuid"
)

// UniqueSetPrefix is the Redis key prefix for a link's daily unique-visitor
// set. The redirect Lua script appends "<link id>:<day>" to it, so it must
// stay in sync with lua/redirect_lookup.lua.
const UniqueSetPrefix = "uniq:"

// NotFoundSentinel is the cached value meaning "no link resolves this key".
// Negative caching keeps a scanner walking random slugs off the database.
const NotFoundSentinel = "-"

// Cached is the link record the redirect path reads. It is deliberately
// minimal: everything needed to answer a redirect, nothing else. The password
// hash is never cached — only whether one exists.
type Cached struct {
	ID             uuid.UUID  `json:"id"`
	TeamID         uuid.UUID  `json:"team_id"`
	DestinationURL string     `json:"destination_url"`
	RedirectType   int        `json:"redirect_type"`
	State          string     `json:"state"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	HasPassword    bool       `json:"has_password"`
	// AnalyticsEnabled false means this link records no clicks at all.
	AnalyticsEnabled bool `json:"analytics_enabled"`
}

// CacheKey is the Redis key for a link, scoped by hostname because slugs are
// unique per domain, never globally.
func CacheKey(hostname, slug string) string {
	return "l:" + hostname + ":" + slug
}
