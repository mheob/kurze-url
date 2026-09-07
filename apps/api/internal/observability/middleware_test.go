package observability_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// bindFakeClient points the global hub at a collecting transport. The global
// hub is what both middlewares reach for — sentryhttp clones it per request,
// RedirectPanicMiddleware clones it only inside its recover — so this is what
// makes either of them observable from a test.
func bindFakeClient(t *testing.T) *fakeTransport {
	t.Helper()

	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)

	return transport
}

func panickingHandler() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
}

// Both middlewares are held to one contract, so the redirect surface's
// cheaper one cannot quietly stop doing the job the API surface's does.
//
// Repanic is half of that contract: it is what lets chi's Recoverer stay
// outermost and keep logging the stack trace. Without it a middleware would
// answer 500 itself and the stack trace would never reach the logs — this
// test fails if either one drops the re-panic.
func TestBothMiddlewaresCaptureAPanicAndRepanic(t *testing.T) {
	for name, middleware := range map[string]func(http.Handler) http.Handler{
		"api":      observability.APIMiddleware(),
		"redirect": observability.RedirectPanicMiddleware(),
	} {
		t.Run(name, func(t *testing.T) {
			transport := bindFakeClient(t)
			handler := middleware(panickingHandler())

			require.Panics(t, func() {
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			})
			require.Len(t, transport.events, 1)
		})
	}
}

// The redirect surface's middleware is cheaper than sentryhttp, not blinder:
// the three fields Scrub lets out of this process — method, path and
// User-Agent — must still be on the event. Attaching the request is one
// assignment on a path only a panic reaches.
func TestRedirectPanicMiddlewareKeepsTheRequestContext(t *testing.T) {
	transport := bindFakeClient(t)

	req := httptest.NewRequest(http.MethodGet, "/abc123?utm_source=newsletter", nil)
	req.Header.Set("User-Agent", "test-agent/1.0")

	handler := observability.RedirectPanicMiddleware()(panickingHandler())
	require.Panics(t, func() { handler.ServeHTTP(httptest.NewRecorder(), req) })

	require.Len(t, transport.events, 1)
	request := transport.events[0].Request
	require.NotNil(t, request)
	require.Equal(t, http.MethodGet, request.Method)
	require.Contains(t, request.URL, "/abc123")
	require.Equal(t, "test-agent/1.0", request.Headers["User-Agent"])
}

// A handler that does not panic must leave no trace: RedirectPanicMiddleware
// exists so that a successful redirect pays nothing, and an event per
// redirect would be both the cost and the budget problem all over again.
func TestRedirectPanicMiddlewarePassesASuccessThrough(t *testing.T) {
	transport := bindFakeClient(t)

	handler := observability.RedirectPanicMiddleware()(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusFound) },
	))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abc123", nil))

	require.Equal(t, http.StatusFound, rec.Code)
	require.Empty(t, transport.events)
}
