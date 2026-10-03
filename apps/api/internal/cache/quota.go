package cache

import (
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

// quotaRefusals are the replies Upstash gives every command once a database
// has spent its command allowance. The first is the current one: the free
// tier's 500K commands a month, after which it stops hard — no
// throttling, no warning, no automatic upgrade — and appends the limit and
// the usage to the text, as in "… exceeded. Limit: 500000, Usage: 500002".
// The second is what databases on the older 10K-a-day allowance answered,
// and is kept because nothing on Upstash's side says every such database
// has been migrated.
var quotaRefusals = []string{
	"ERR max requests limit exceeded",
	"ERR max daily request limit exceeded",
}

// QuotaExceeded reports whether err is Upstash refusing a command because
// the database's command quota is spent.
//
// It exists for observability.CoalesceRule. Once the quota is gone every
// Redis call fails until the month turns over or the plan changes, and the
// redirect path alone logs up to three different error-level messages per
// redirect for it — "redirect rate limit unavailable, failing open",
// "redirect cache lookup failed" and "negative cache write failed". The
// per-message throttle still lets each of those through once a minute,
// which spends Sentry's 5,000 events a month inside a day; matching the
// cause rather than the message lets main.go report all of them as one.
//
// Only a reply Redis itself sent counts: the chain is walked to a
// redis.Error, the interface go-redis's reply type implements, and only that
// reply's text is compared. A plain error whose text merely contains the
// phrase is not a refusal. Every Client method wraps with %w, so a caller
// passes the error it got back unchanged.
func QuotaExceeded(err error) bool {
	var reply redis.Error
	if !errors.As(err, &reply) {
		return false
	}

	text := reply.Error()
	for _, refusal := range quotaRefusals {
		if strings.HasPrefix(text, refusal) {
			return true
		}
	}
	return false
}
