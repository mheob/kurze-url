package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// errorAttrKey is the attribute key this codebase logs its failures under.
const errorAttrKey = "error"

// sentryThrottleWindow is the shortest gap allowed between two Sentry events
// carrying the same log message.
//
// One dependency outage otherwise empties the month's budget. GET /<slug>
// logs at error level up to twice per redirect while Redis is unreachable,
// and analytics/recorder.go logs on every five-second flush while Postgres
// is: at the traffic ceiling this project documents for itself that is
// thousands of events an hour against a 5,000-events-a-month free tier.
// Sentry answers 429 once it is gone, and the Go SDK's transport then backs
// off and drops events locally — so the *next* incident, the one nobody is
// already watching, is the one that goes unreported.
const sentryThrottleWindow = time.Minute

// suppressedAttrKey carries, on the next event that does go out for a
// message, how many occurrences of it the throttle dropped since the last
// one. Without it "this happened once" and "this happened four thousand
// times" look identical in Sentry.
const suppressedAttrKey = "suppressed_since_last_event"

// logContextKey names the Sentry context the record's attributes land in.
//
// A context, not "extras": sentry-go v0.49 removed Event.Extra and
// Scope.SetExtra altogether (`grep -r Extra` over the module finds only
// ExtractStacktrace). A custom context is what replaced them — structured
// data rendered as its own card on the event, and, like extras before it,
// no part of what Sentry groups on.
const logContextKey = "log"

// slogHandler forwards error-level records to Sentry and passes every record
// on to the handler it wraps.
//
// A handler rather than CaptureException calls at each site: this codebase
// already logs its failures consistently, so every existing Log.Error becomes
// an event with no new call site — and, more importantly, with no second
// list of "places that report" to drift out of step with the first.
type slogHandler struct {
	inner slog.Handler
	// Shared by every handler derived through WithAttrs/WithGroup, so that
	// the rate a message is reported at is a property of the process rather
	// than of whichever derived logger happened to be at hand.
	throttle *messageThrottle
}

// NewSlogHandler wraps inner so that records at slog.LevelError also reach
// Sentry. Capturing is a non-blocking enqueue onto the transport's queue, so
// this is safe on the redirect hot path: nothing here waits for delivery.
// The events still in that queue when an instance retires are lost, which is
// the deliberate price of not charging every successful redirect for the
// possibility of an error.
//
// At most one event per distinct record.Message per sentryThrottleWindow
// reaches Sentry; the inner handler still receives every record, so the
// throttle never costs a log line.
//
// The "error" attribute is read from the record's own attributes only, so
// Error(msg, "error", err) produces a Sentry exception with a stack trace —
// but an error carried in via Logger.With("error", err) arrives as a plain
// message instead, because slog keeps With-attributes on the handler rather
// than threading them onto the Record, so this handler never sees them.
func NewSlogHandler(inner slog.Handler) slog.Handler {
	return newSlogHandler(inner, time.Now)
}

// newSlogHandler takes the clock the throttle reads, so a test can advance
// past sentryThrottleWindow without sleeping for a minute.
func newSlogHandler(inner slog.Handler, now func() time.Time) slog.Handler {
	return &slogHandler{
		inner: inner,
		throttle: &messageThrottle{
			now:    now,
			window: sentryThrottleWindow,
			seen:   make(map[string]*throttleEntry),
		},
	}
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	// Only LevelError. Warn is used here for expected conditions — an unset
	// SUPABASE_JWKS_URL, disabled invitations, a Redis write that failed
	// after the lookup already reported the same outage — and reporting
	// those would spend the monthly event budget on notes.
	if record.Level >= slog.LevelError {
		if admitted, suppressed := h.throttle.admit(record.Message); admitted {
			capture(ctx, record, suppressed)
		}
	}

	// Unconditional, and outside the throttle on purpose: the throttle gates
	// what reaches Sentry, never what reaches the logs.
	return h.inner.Handle(ctx, record)
}

// WithAttrs and WithGroup delegate, carrying the throttle across so a derived
// logger shares one budget with the logger it came from.
//
// Attributes passed here reach the inner handler's output but not Sentry: as
// NewSlogHandler's comment says, slog keeps With-attributes on the handler
// and never threads them onto the Record, which is the only thing the Sentry
// side can read. Attributes passed to the logging call itself do travel —
// see capture.
func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{inner: h.inner.WithAttrs(attrs), throttle: h.throttle}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	return &slogHandler{inner: h.inner.WithGroup(name), throttle: h.throttle}
}

// capture sends one record to Sentry, with the record's own attributes
// attached.
//
// The hub is cloned before the attributes are attached. A scope is what keeps
// one record's attributes off the next event, and pushing one onto a shared
// hub — sentry.CurrentHub() is process-global, and the redirect surface
// deliberately no longer clones a hub per request — would let two concurrent
// captures see each other's scope. Cloning costs a scope copy, on a path that
// is already throttled to one event per message per minute.
func capture(ctx context.Context, record slog.Record, suppressed int) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub = hub.Clone()

	if fields := logFields(record, suppressed); len(fields) > 0 {
		hub.Scope().SetContext(logContextKey, fields)
	}

	if err := errorAttr(record); err != nil {
		// Wrapped rather than reported separately so the log message,
		// which is what a human recognises, participates in Sentry's
		// grouping alongside the underlying error.
		hub.CaptureException(fmt.Errorf("%s: %w", record.Message, err))
		return
	}

	hub.CaptureMessage(record.Message)
}

// logFields renders the record's attributes into the event's "log" context.
//
// Grouping is unaffected by this: Sentry groups on the exception or the
// message and on fingerprint, never on a context. Dropping the attributes was
// costing real information — health.go logs the same message for a Postgres
// and a Redis failure, and "dependency" is the only thing that tells them
// apart.
func logFields(record slog.Record, suppressed int) sentry.Context {
	fields := make(sentry.Context, record.NumAttrs())

	record.Attrs(func(attr slog.Attr) bool {
		// "error" is already the event's exception; repeating it here would
		// only say the same thing twice.
		if attr.Key != errorAttrKey {
			addField(fields, "", attr)
		}
		return true
	})

	if suppressed > 0 {
		fields[suppressedAttrKey] = suppressed
	}

	return fields
}

// addField writes one attribute into fields.
//
// Nothing in this codebase logs a slog.Group — the attributes are flat pairs
// (team_id, link_id, hostname, slug, attempt, dependency) — so the group
// branch is a safety net, not the main path: a group's members are written
// under dotted keys ("outer.inner") rather than as a nested object, which
// keeps the context a flat map whatever arrives.
//
// KindAny values are rendered the way slog itself would render them. JSON is
// what a context is serialised as, and an arbitrary Go value can marshal to
// nothing useful there — an error becomes "{}" — whereas every other kind
// (string, number, bool, duration, time) marshals faithfully as itself.
func addField(fields sentry.Context, prefix string, attr slog.Attr) {
	value := attr.Value.Resolve()

	key := attr.Key
	switch {
	case key == "":
		key = prefix
	case prefix != "":
		key = prefix + "." + key
	}

	switch value.Kind() {
	case slog.KindGroup:
		for _, member := range value.Group() {
			addField(fields, key, member)
		}
	case slog.KindAny:
		fields[key] = value.String()
	default:
		fields[key] = value.Any()
	}
}

// errorAttr finds the conventional "error" attribute this codebase logs its
// failures under, so Sentry gets an exception with a stack trace rather than
// a bare message.
func errorAttr(record slog.Record) error {
	var found error

	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != errorAttrKey {
			return true
		}
		if err, ok := attr.Value.Any().(error); ok {
			found = err
			return false
		}
		found = errors.New(attr.Value.String())
		return false
	})

	return found
}

// messageThrottle holds, per distinct log message, when it was last reported
// and how many occurrences have been dropped since.
//
// The map is keyed by record.Message. Every Error call site in this codebase
// passes a fixed string literal and puts the varying part in attributes —
// never fmt.Sprintf into the message — so the key space is the number of such
// call sites (a few dozen) and does not grow with traffic. A message built by
// formatting would break that, which is the reason to keep writing them as
// literals.
type messageThrottle struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	seen   map[string]*throttleEntry
}

type throttleEntry struct {
	lastSent   time.Time
	suppressed int
}

// admit reports whether this message may become a Sentry event now and, when
// it may, how many occurrences of it were suppressed since the last one that
// did.
func (t *messageThrottle) admit(message string) (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()

	entry, seen := t.seen[message]
	if !seen {
		t.seen[message] = &throttleEntry{lastSent: now}
		return true, 0
	}

	if now.Sub(entry.lastSent) < t.window {
		entry.suppressed++
		return false, 0
	}

	suppressed := entry.suppressed
	entry.lastSent, entry.suppressed = now, 0

	return true, suppressed
}
