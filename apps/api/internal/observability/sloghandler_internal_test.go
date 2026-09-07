package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"
)

// collectingTransport is this file's own copy of the fake in
// sloghandler_test.go: that one lives in the external observability_test
// package, and these tests are internal because they need the unexported
// clock seam newSlogHandler exposes.
type collectingTransport struct{ events []*sentry.Event }

func (t *collectingTransport) Configure(sentry.ClientOptions)        {}
func (t *collectingTransport) SendEvent(event *sentry.Event)         { t.events = append(t.events, event) }
func (t *collectingTransport) Flush(time.Duration) bool              { return true }
func (t *collectingTransport) FlushWithContext(context.Context) bool { return true }
func (t *collectingTransport) Close()                                {}

// A fake clock rather than a shortened window: the window is the production
// constant, and a test that waited out a real minute would not be run.
func loggerWithClock(t *testing.T, now *time.Time) (*slog.Logger, context.Context, *collectingTransport) {
	t.Helper()

	transport := &collectingTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)

	ctx := sentry.SetHubOnContext(context.Background(), sentry.NewHub(client, sentry.NewScope()))
	handler := newSlogHandler(slog.NewJSONHandler(io.Discard, nil), func() time.Time { return *now })

	return slog.New(handler), ctx, transport
}

// The half of the throttle a black-box test cannot see: what was suppressed
// is counted, and the next event that does go out carries the count. Without
// it a reader cannot tell one Redis blip from a four-thousand-request outage,
// which is the only reason suppressing anything is acceptable.
func TestASuppressedCountRidesOnTheNextEvent(t *testing.T) {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now)

	failure := errors.New("connection refused")
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", failure)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", failure)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", failure)
	require.Len(t, transport.events, 1, "the window has not elapsed, so only the first goes out")
	require.NotContains(t, transport.events[0].Contexts[logContextKey], suppressedAttrKey,
		"nothing had been suppressed when the first event went out")

	now = now.Add(sentryThrottleWindow + time.Second)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", failure)

	require.Len(t, transport.events, 2)
	require.Equal(t, 2, transport.events[1].Contexts[logContextKey][suppressedAttrKey])
}

// The counter resets with each event that goes out, so a long outage reports
// the gap since the last report rather than a total that keeps growing.
func TestTheSuppressedCountResetsAfterEachEvent(t *testing.T) {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now)

	logger.ErrorContext(ctx, "flushing click stats failed")
	logger.ErrorContext(ctx, "flushing click stats failed")

	now = now.Add(sentryThrottleWindow + time.Second)
	logger.ErrorContext(ctx, "flushing click stats failed")

	now = now.Add(sentryThrottleWindow + time.Second)
	logger.ErrorContext(ctx, "flushing click stats failed")

	require.Len(t, transport.events, 3)
	require.Equal(t, 1, transport.events[1].Contexts[logContextKey][suppressedAttrKey])
	require.NotContains(t, transport.events[2].Contexts[logContextKey], suppressedAttrKey,
		"nothing was suppressed between the second and third events")
}

// A group is not something this codebase logs; the branch exists so that one
// arriving flattens onto dotted keys rather than dropping out of the event.
func TestGroupedAttributesFlattenOntoDottedKeys(t *testing.T) {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now)

	logger.ErrorContext(ctx, "domain verification failed",
		slog.Group("domain", slog.String("hostname", "go.example.test")))

	require.Len(t, transport.events, 1)
	require.Equal(t, "go.example.test",
		transport.events[0].Contexts[logContextKey]["domain.hostname"])
}
