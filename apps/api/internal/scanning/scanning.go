// Package scanning checks link destinations against Google Safe Browsing, the
// one package in this module that talks to it.
//
// It uses the v5 hashes.search method, never urls.search: only the first four
// bytes of the SHA-256 of each canonicalized URL expression leave this
// process, never a URL. A destination can carry personal data — a prefilled
// form, a member's own page — and Google's terms let it reuse and share URLs
// sent to urls.search, but not hash prefixes. The price is that this package
// canonicalizes, expands, hashes and compares full hashes itself, exactly as
// Google's "URLs and Hashing" rules describe.
package scanning

import (
	"context"
	"errors"
	"time"
)

// Result is one URL's verdict.
type Result struct {
	// ThreatTypes are the threat types Google reports for the URL, sorted and
	// de-duplicated. Empty means clean. A value this package does not know is
	// kept as Google sent it: Google's reference tells clients to tolerate new
	// ones, and dropping one would turn a report into "clean".
	ThreatTypes []string
	// ValidFor is how long the verdict may be relied on, read from the
	// response's cacheDuration. Zero when Google sent none, or one this
	// package could not read: a verdict that may not be relied on at all is
	// the safe reading of either.
	ValidFor time.Duration
}

// Checker is the seam api.Deps holds. It is provider-neutral on purpose:
// Google's Web Risk API, the documented fallback should the non-commercial
// reading of Safe Browsing's terms ever fail, would be a second
// implementation of it, not a change to its callers.
type Checker interface {
	// Check returns one Result per URL it could judge. A URL missing from the
	// map has no verdict; never read that as clean.
	Check(ctx context.Context, urls []string) (map[string]Result, error)
}

// ErrQuotaExceeded means Google refused a lookup because the project's quota
// is spent. It is distinct from every other failure so cmd/api can report it
// to Sentry once an hour rather than once a minute per message: the condition
// does not clear by itself, and every flagged redirect and every sweep would
// otherwise report it again.
var ErrQuotaExceeded = errors.New("scanning: safe browsing quota exceeded")

// QuotaExceeded reports whether err is, or wraps, ErrQuotaExceeded. It is the
// observability.CoalesceRule matcher cmd/api registers, shaped like
// cache.QuotaExceeded beside it.
func QuotaExceeded(err error) bool {
	return errors.Is(err, ErrQuotaExceeded)
}
