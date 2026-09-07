// Package observability holds everything this API sends to Sentry: the client
// options, the request-context scrubber, and the slog handler that turns an
// error log into an event. No other package imports sentry-go, so the policy
// about what may leave this process has exactly one home.
package observability

import (
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
)

// allowedHeaders is the complete set of request headers permitted to leave
// this process inside a Sentry event.
//
// An allowlist rather than a denylist, and that is the point: the hazards are
// X-Forwarded-For, X-Vercel-Forwarded-For, X-Real-Ip, Cookie and
// Authorization, and a denylist only protects against the ones somebody
// remembered to write down. A new proxy header added by the platform next
// year is excluded here by construction.
//
// User-Agent earns its place because redirect defects are routinely
// device-specific, and a User-Agent is not an IP address.
var allowedHeaders = map[string]struct{}{
	"User-Agent": {},
}

// Scrub is the value of sentry.ClientOptions.BeforeSend. It is an ordinary
// function of an event rather than middleware so that it can be tested
// directly — which is the whole reason the policy lives here.
func Scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	// Golden rule 5: never store a full IP address, ever.
	event.User.IPAddress = ""

	if event.Request == nil {
		return event
	}

	// Mandatory rather than cautious. POST /{slug}/verify carries a link's
	// password in cleartext, so without this line the first panic on the
	// password interstitial ships that password to a third party — the same
	// class of mistake audit.go's checkMetadata exists to prevent on the
	// audit path.
	event.Request.Data = ""

	event.Request.Cookies = ""
	event.Request.QueryString = ""
	// Env carries REMOTE_ADDR under sentry-go's HTTP integration.
	event.Request.Env = nil
	event.Request.URL = withoutQuery(event.Request.URL)

	headers := make(map[string]string, len(allowedHeaders))
	for name, value := range event.Request.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if _, ok := allowedHeaders[canonical]; ok {
			headers[canonical] = value
		}
	}
	event.Request.Headers = headers

	return event
}

func withoutQuery(rawURL string) string {
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}
