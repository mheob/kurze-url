package observability_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/observability"
)

// fakeTransport collects events instead of sending them. If the pinned
// sentry-go version's Transport interface requires more methods than these,
// add them — the compiler names them precisely.
type fakeTransport struct{ events []*sentry.Event }

func (t *fakeTransport) Configure(sentry.ClientOptions)        {}
func (t *fakeTransport) SendEvent(event *sentry.Event)         { t.events = append(t.events, event) }
func (t *fakeTransport) Flush(time.Duration) bool              { return true }
func (t *fakeTransport) FlushWithContext(context.Context) bool { return true }
func (t *fakeTransport) Close()                                {}

// loggerWithFakeSentry returns a logger whose error records reach transport,
// via a hub carried on the returned context. A hub on the context rather
// than the global one keeps these tests independent of each other.
func loggerWithFakeSentry(t *testing.T) (*slog.Logger, context.Context, *fakeTransport) {
	t.Helper()

	transport := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)

	hub := sentry.NewHub(client, sentry.NewScope())
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	// io.Discard: this test is about what reaches Sentry, not about the JSON
	// the inner handler writes.
	logger := slog.New(observability.NewSlogHandler(slog.NewJSONHandler(io.Discard, nil)))

	return logger, ctx, transport
}

func TestErrorLogsBecomeSentryEvents(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.ErrorContext(ctx, "redis lookup failed", "error", errors.New("connection refused"))

	require.Len(t, transport.events, 1)
	require.Contains(t, transport.events[0].Message+eventException(transport.events[0]),
		"redis lookup failed")
}

// An error attached via Logger.With (rather than passed directly to the
// logging call) still reaches Sentry, because the handler stays wrapped
// through WithAttrs — but slog keeps With-attributes on the handler, not on
// the Record, so errorAttr never sees this one: it arrives as a plain
// message, not an exception. See NewSlogHandler's doc comment.
func TestErrorAttachedViaWithProducesAMessageNotException(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.With("error", errors.New("connection refused")).ErrorContext(ctx, "redis lookup failed")

	require.Len(t, transport.events, 1)
	event := transport.events[0]
	require.Empty(t, event.Exception, "error carried via .With() should not become an exception")
	require.Contains(t, event.Message, "redis lookup failed")
}

// The handler must stay wrapped through WithGroup too: an event still has to
// reach the transport for a logger built via .WithGroup(...). Without this,
// a future WithGroup that returned the inner handler unwrapped would drop
// Sentry reporting for every logger built through it, with every other test
// in this file still green.
func TestWithGroupKeepsReportingToSentry(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.WithGroup("redis").ErrorContext(ctx, "lookup failed")

	require.Len(t, transport.events, 1)
}

// Warn is deliberately not reported. The free tier allows 5,000 events a
// month, and this codebase warns about ordinary, expected conditions —
// an unset SUPABASE_JWKS_URL at startup, for one.
func TestWarnLogsProduceNoEvent(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.WarnContext(ctx, "supabase auth url is unset")

	require.Empty(t, transport.events)
}

func TestInfoLogsProduceNoEvent(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.InfoContext(ctx, "api listening", "port", "8080")

	require.Empty(t, transport.events)
}

// The wrapped handler must still be a working logger: swallowing records
// would trade one blind spot for another. The repeated message is the point
// as much as the two distinct ones — the throttle gates Sentry, never the
// log, so a suppressed occurrence still has to be written.
func TestTheInnerHandlerStillReceivesEveryRecord(t *testing.T) {
	var written []string
	logger := slog.New(observability.NewSlogHandler(&recordingHandler{lines: &written}))

	logger.Info("kept")
	logger.Error("also kept")
	logger.Error("also kept")

	require.Equal(t, []string{"kept", "also kept", "also kept"}, written)
}

// The budget finding, from the outside: one dependency outage logs the same
// failure on every request, and at the traffic ceiling this project documents
// that empties a 5,000-event month in under two hours — after which Sentry
// answers 429, the transport backs off, and the *next* incident is invisible.
func TestTheSameMessageIsReportedOncePerWindow(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	err := errors.New("connection refused")
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", err)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", err)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", err)

	require.Len(t, transport.events, 1)
}

// Throttled per message, not globally: a second, unrelated failure during the
// same minute is exactly the thing an incident needs to show, so it must not
// be swallowed by the first one's window.
func TestDifferentMessagesAreReportedSeparately(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.ErrorContext(ctx, "redirect cache lookup failed")
	logger.ErrorContext(ctx, "redirect database lookup failed")

	require.Len(t, transport.events, 2)
}

// The attributes this codebase logs are the difference between an actionable
// event and a mystery: when a message is shared across call sites (health.go
// used to send this exact message for both its Postgres and Redis pings),
// "dependency" is the only thing that says which one failed. Nothing about
// Sentry's grouping depended on dropping them — it groups on the exception or
// the message and on fingerprint, never on a context.
func TestRecordAttributesReachTheEvent(t *testing.T) {
	logger, ctx, transport := loggerWithFakeSentry(t)

	logger.ErrorContext(ctx, "deep health check failed",
		"dependency", "redis", "error", errors.New("connection refused"))

	require.Len(t, transport.events, 1)
	fields := transport.events[0].Contexts["log"]
	require.Equal(t, "redis", fields["dependency"])
	// "error" is the exception already; repeating it as data would say the
	// same thing twice.
	require.NotContains(t, fields, "error")
	require.Contains(t, eventException(transport.events[0]), "connection refused")
}

type recordingHandler struct{ lines *[]string }

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r.Message)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// eventException flattens an event's exception values so one assertion can
// cover both the CaptureMessage and CaptureException shapes.
func eventException(event *sentry.Event) string {
	out := ""
	for _, ex := range event.Exception {
		out += ex.Value
	}
	return out
}
