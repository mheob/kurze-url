package observability

import (
	"context"
	"errors"
	"fmt"
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
func loggerWithClock(
	t *testing.T, now *time.Time, rules ...CoalesceRule,
) (*slog.Logger, context.Context, *collectingTransport) {
	t.Helper()

	transport := &collectingTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://key@example.test/1",
		Transport: transport,
	})
	require.NoError(t, err)

	ctx := sentry.SetHubOnContext(context.Background(), sentry.NewHub(client, sentry.NewScope()))
	handler := newSlogHandler(slog.NewJSONHandler(io.Discard, nil), func() time.Time { return *now }, rules...)

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

// errQuotaSpent stands in for Upstash's quota refusal. Recognising the real
// one is cache.QuotaExceeded's job and is tested there; these tests only need
// an error a rule can tell apart from every other.
var errQuotaSpent = errors.New("ERR max requests limit exceeded. Limit: 500000, Usage: 500002")

// quotaRule mirrors the one main.go wires, with a matcher that needs no
// Redis.
func quotaRule() CoalesceRule {
	return CoalesceRule{
		Key:    "redis quota exhausted",
		Match:  func(err error) bool { return errors.Is(err, errQuotaSpent) },
		Window: time.Hour,
	}
}

// exceptionText flattens an event's exception chain, as eventException does
// in the external test file.
func exceptionText(event *sentry.Event) string {
	out := ""
	for _, exception := range event.Exception {
		out += exception.Value
	}
	return out
}

// What a rule is for: one cause logged under several messages becomes one
// event per window, not one per message per minute. The later records arrive
// well past the minute window, so only the rule's hour can be holding them
// back.
func TestRecordsARuleMatchesShareOneEventAcrossMessages(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now, quotaRule())

	logger.ErrorContext(ctx, "redirect cache lookup failed",
		"error", fmt.Errorf("cache: redirect lookup: %w", errQuotaSpent))
	now = now.Add(10 * time.Minute)
	logger.ErrorContext(ctx, "redirect rate limit unavailable, failing open",
		"error", fmt.Errorf("cache: rate limit: %w", errQuotaSpent))
	now = now.Add(40 * time.Minute)
	logger.ErrorContext(ctx, "negative cache write failed",
		"error", fmt.Errorf("cache: put not-found: %w", errQuotaSpent))

	require.Len(t, transport.events, 1)
	// The one that goes out is the event it would have been without a rule:
	// its own message, wrapping the error whose text carries the usage.
	text := exceptionText(transport.events[0])
	require.Contains(t, text, "redirect cache lookup failed")
	require.Contains(t, text, "Usage: 500002")
}

// After the rule's window the next record goes out, and carries the count of
// everything the slot dropped — across every message it covers, which is why
// the event also names the rule: without it, a count that includes other
// messages' records would read as this message's own.
func TestARuleReportsAgainAfterItsWindowWithTheCountItDropped(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	rule := quotaRule()
	logger, ctx, transport := loggerWithClock(t, &now, rule)

	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", errQuotaSpent)
	logger.ErrorContext(ctx, "redirect rate limit unavailable, failing open", "error", errQuotaSpent)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", errQuotaSpent)
	require.Len(t, transport.events, 1)

	now = now.Add(rule.Window + time.Second)
	logger.ErrorContext(ctx, "negative cache write failed", "error", errQuotaSpent)

	require.Len(t, transport.events, 2)
	fields := transport.events[1].Contexts[logContextKey]
	require.Equal(t, 2, fields[suppressedAttrKey])
	require.Equal(t, rule.Key, fields[coalescedAttrKey])
	require.Contains(t, exceptionText(transport.events[1]), "negative cache write failed")
}

// A rule claims records by their error, not their message, so a message
// whose quota records are coalesced still reports any other failure — and
// under its own per-minute slot, which the rule's records never touched.
// Anything else would let the quota outage hide a second, unrelated one on
// the same line for an hour.
func TestARuleLeavesTheMessagesOwnSlotAlone(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now, quotaRule())
	refused := errors.New("connection refused")

	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", errQuotaSpent)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", refused)
	require.Len(t, transport.events, 2, "the quota record did not spend the message's slot")
	require.Contains(t, transport.events[0].Contexts[logContextKey], coalescedAttrKey)
	require.NotContains(t, transport.events[1].Contexts[logContextKey], coalescedAttrKey)

	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", refused)
	require.Len(t, transport.events, 2, "the message's own minute still applies")

	now = now.Add(sentryThrottleWindow + time.Second)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", refused)
	logger.ErrorContext(ctx, "redirect cache lookup failed", "error", errQuotaSpent)

	require.Len(t, transport.events, 3, "the minute is up for the message, not for the rule")
	require.Equal(t, 1, transport.events[2].Contexts[logContextKey][suppressedAttrKey])
	require.Contains(t, exceptionText(transport.events[2]), "connection refused")
}

// A rule matches an error, so a record without one is never offered to it —
// not even to a rule that would match anything.
func TestARecordWithoutAnErrorIsNeverMatched(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	matchesAnything := CoalesceRule{
		Key:    "anything",
		Match:  func(error) bool { return true },
		Window: time.Hour,
	}
	logger, ctx, transport := loggerWithClock(t, &now, matchesAnything)

	logger.ErrorContext(ctx, "flushing click stats failed")
	logger.ErrorContext(ctx, "domain verification failed")

	require.Len(t, transport.events, 2, "each kept its own message's slot")
	require.NotContains(t, transport.events[1].Contexts[logContextKey], coalescedAttrKey)
}

// Derived loggers share the rule's slot as they share every other: the
// handlers built by With and WithGroup reach the same throttle.
func TestDerivedLoggersShareARulesSlot(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	logger, ctx, transport := loggerWithClock(t, &now, quotaRule())

	logger.With("hostname", "go.example.test").
		ErrorContext(ctx, "redirect cache lookup failed", "error", errQuotaSpent)
	logger.WithGroup("redirect").
		ErrorContext(ctx, "redirect rate limit unavailable, failing open", "error", errQuotaSpent)

	require.Len(t, transport.events, 1)
}
