package observability_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

func TestMiddlewareCapturesAPanicAndRepanics(t *testing.T) {
	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	handler := observability.Middleware()(panicking)

	// Repanic: true is what lets chi's Recoverer stay outermost and keep
	// logging the stack trace. Without it this middleware would answer 500
	// itself and the stack trace would never reach the logs.
	require.Panics(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
	require.Len(t, transport.events, 1)
}
