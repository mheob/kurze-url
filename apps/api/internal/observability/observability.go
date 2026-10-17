package observability

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// deliveryTimeout bounds every place this process waits for Sentry: the flush
// at shutdown, the flush after a panic, and DeliveryMiddleware's flush after a
// request that captured an event. All three are failure paths, which is the
// only reason waiting is permitted at all — golden rule 2 forbids it on a
// redirect that went well.
const deliveryTimeout = 2 * time.Second

// captured counts the events capture has handed to Sentry, so
// DeliveryMiddleware can tell whether one was captured while its request ran.
var captured atomic.Uint64

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
// WaitForDelivery is reached exclusively by a panic — a response that is
// already 5xx. The other flushes on a request path are
// RedirectPanicMiddleware's, after a panic too, and DeliveryMiddleware's.
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

// DeliveryMiddleware waits, once the handler has returned, for any Sentry
// event captured while the request ran, at most deliveryTimeout. Sentry's Go
// SDK sends events from a goroutine of its own, and Vercel freezes a Go
// function between requests — there is no waitUntil for Go — so an event
// still in flight when a request ends waits for the instance to wake again,
// which an instance that served a lone request may never do; the
// post-response Safe Browsing check timed out exactly that way on
// 2026-10-17. A request that captured nothing, which is every redirect that
// went well, reads one counter twice and never waits.
//
// It sits on the root router, around both surfaces and the internal
// endpoints, outside every Recoverer: a 500 Recoverer wrote after a panic has
// already been flushed by the panic middlewares, which do not go through
// capture, so it is not flushed twice. An event captured by a request running
// alongside this one is flushed by whichever of the two ends first. One
// captured on a goroutine after its request ended is still at the mercy of
// the freeze.
func DeliveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			before := captured.Load()
			defer func() {
				if captured.Load() != before {
					sentry.Flush(deliveryTimeout)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
