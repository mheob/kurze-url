// Package analytics turns a redirect request into aggregate rollup counts.
// It never stores, logs or returns a full IP address.
package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// VisitorHash produces the daily-rotating, salted, non-reversible identifier
// used to count unique visitors. The date is part of the HMAC input, so a
// visitor's identifier changes every day and yesterday's cannot be correlated
// with today's. The raw IP never leaves this function.
func VisitorHash(secret, ip, userAgent string, at time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// A NUL separator keeps the field boundaries unambiguous, so no pair of
	// distinct (ip, userAgent) inputs can produce the same digest input.
	mac.Write([]byte(Day(at)))
	mac.Write([]byte{0})
	mac.Write([]byte(ip))
	mac.Write([]byte{0})
	mac.Write([]byte(userAgent))

	// 128 bits is far more than enough to keep collisions negligible within a
	// single link's daily visitor set, and halves the Redis memory each set
	// costs against the 256 MB free tier.
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// Day is the UTC date bucket a click is counted into. Both the Redis
// unique-visitor set and the Postgres rollup key off this, so they must agree.
func Day(at time.Time) string {
	return at.UTC().Format(time.DateOnly)
}

// RateLimitKey derives a non-reversible identifier for rate-limit bucketing.
//
// Unlike VisitorHash it deliberately does NOT rotate daily — a budget that
// reset at midnight would be trivially gameable — and deliberately does NOT
// include the User-Agent, or an attacker could reset their budget by changing
// it. The raw address never leaves this function.
func RateLimitKey(secret, ip string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("ratelimit"))
	mac.Write([]byte{0})
	mac.Write([]byte(ip))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}
