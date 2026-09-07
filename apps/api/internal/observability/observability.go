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

// Init configures the Sentry client and returns the flush to defer at
// shutdown. An empty dsn returns a no-op and configures nothing, so a local
// checkout needs no Sentry account.
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

// Middleware captures panics. It belongs INSIDE chi's middleware.Recoverer,
// with Repanic set: the two other arrangements each lose something concrete.
// Recoverer innermost swallows the panic and Sentry sees nothing; this
// middleware outermost without Repanic answers 500 itself and Recoverer
// never logs the stack trace.
//
// WaitForDelivery is the only flush on a request path in this codebase, and
// it is reached exclusively by a panic — a response that is already 5xx. A
// successful redirect never enters this code.
func Middleware() func(http.Handler) http.Handler {
	return sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: true,
		Timeout:         deliveryTimeout,
	}).Handle
}
