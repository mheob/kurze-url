package observability

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// deliveryTimeout bounds the two places this process ever waits for Sentry:
// the flush at shutdown, and the flush after a panic. Both are already
// failure paths, which is the only reason waiting is permitted at all —
// golden rule 2 forbids it on the redirect path.
const deliveryTimeout = 2 * time.Second

// Init configures the Sentry client and returns the flush to run at shutdown.
// An empty dsn returns a no-op and configures nothing, so a local checkout
// needs no Sentry account.
//
// The error is returned rather than acted on here: whether an unusable DSN is
// fatal is the caller's decision, and cmd/api answers no — observability is
// an optional dependency like every other one in that file, and a typo in
// SENTRY_DSN must not take the redirect surface down. The returned flush is
// safe to call on the error path too.
//
// No tracing option is set. Tracing is off by default, and this project's
// problem is error visibility, not latency — performance data would spend
// the same 5,000 events a month that errors need.
func Init(dsn, environment, release string) (func(), error) {
	if dsn == "" {
		return func() {}, nil
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Environment: environment,
		Release:     release,
		BeforeSend:  Scrub,
	}); err != nil {
		return func() {}, err
	}

	return func() { sentry.Flush(deliveryTimeout) }, nil
}

// APIMiddleware is the /v1 surface's Sentry middleware: sentryhttp, which
// clones a hub per request and puts it on the context, so a handler's own
// error logs carry the request with them.
//
// It belongs INSIDE chi's middleware.Recoverer, with Repanic set: the two
// other arrangements each lose something concrete. Recoverer innermost
// swallows the panic and Sentry sees nothing; this middleware outermost
// without Repanic answers 500 itself and Recoverer never logs the stack
// trace.
//
// WaitForDelivery is one of the two flushes on a request path in this
// codebase, and it is reached exclusively by a panic — a response that is
// already 5xx.
func APIMiddleware() func(http.Handler) http.Handler {
	return sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: true,
		Timeout:         deliveryTimeout,
	}).Handle
}

// RedirectPanicMiddleware is the redirect surface's Sentry middleware. It
// captures panics and nothing else.
//
// Same observable behaviour as APIMiddleware — a panic still becomes an
// event, still flushes before the process can move on, and is still
// re-panicked so chi's Recoverer logs the stack trace and writes the 500 —
// but a successful redirect pays none of sentryhttp's per-request cost: no
// hub clone, no scope deep copy, no client-mutex acquisition, no two
// crypto/rand draws for a trace and span id, no *http.Request copy, no
// wrapped ResponseWriter, no deferred transaction finish. None of that ever
// waited on anything, so golden rule 2's letter held; the rule says every
// design choice is checked against GET /<slug>, and this is that check.
//
// The request context sentryhttp would have collected is almost entirely
// discarded by Scrub anyway — method, path and User-Agent are all that
// survive it — and the panic path here keeps those three, because attaching
// the request to the scope is one assignment and is only ever reached by a
// panic.
//
// The hub is cloned inside the recover, not per request: sentry.CurrentHub()
// is process-global, so mutating its scope directly would leak this panic's
// request onto whatever another goroutine captures next.
func RedirectPanicMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				hub := sentry.CurrentHub().Clone()
				hub.Scope().SetRequest(r)
				hub.Recover(recovered)
				hub.Flush(deliveryTimeout)

				// Repanic, for the same reason APIMiddleware sets
				// Repanic: true — chi's Recoverer is outside this and is
				// what logs the stack trace and writes the 500.
				panic(recovered)
			}()

			next.ServeHTTP(w, r)
		})
	}
}
