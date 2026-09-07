package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/getsentry/sentry-go"
)

// slogHandler forwards error-level records to Sentry and passes every record
// on to the handler it wraps.
//
// A handler rather than CaptureException calls at each site: this codebase
// already logs its failures consistently, so every existing Log.Error becomes
// an event with no new call site — and, more importantly, with no second
// list of "places that report" to drift out of step with the first.
type slogHandler struct{ inner slog.Handler }

// NewSlogHandler wraps inner so that records at slog.LevelError also reach
// Sentry. Capturing is a non-blocking enqueue onto the transport's queue, so
// this is safe on the redirect hot path: nothing here waits for delivery.
// The events still in that queue when an instance retires are lost, which is
// the deliberate price of not charging every successful redirect for the
// possibility of an error.
//
// The "error" attribute is read from the record's own attributes only, so
// Error(msg, "error", err) produces a Sentry exception with a stack trace —
// but an error carried in via Logger.With("error", err) arrives as a plain
// message instead, because slog keeps With-attributes on the handler rather
// than threading them onto the Record, so this handler never sees them.
func NewSlogHandler(inner slog.Handler) slog.Handler {
	return &slogHandler{inner: inner}
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	// Only LevelError. Warn is used here for expected conditions — an unset
	// SUPABASE_JWKS_URL, disabled invitations — and reporting those would
	// spend the monthly event budget on configuration notes.
	if record.Level >= slog.LevelError {
		hub := sentry.GetHubFromContext(ctx)
		if hub == nil {
			hub = sentry.CurrentHub()
		}

		if err := errorAttr(record); err != nil {
			// Wrapped rather than reported separately so the log message,
			// which is what a human recognises, participates in Sentry's
			// grouping alongside the underlying error.
			hub.CaptureException(fmt.Errorf("%s: %w", record.Message, err))
		} else {
			hub.CaptureMessage(record.Message)
		}
	}

	return h.inner.Handle(ctx, record)
}

// WithAttrs and WithGroup delegate. The attributes reach the inner handler's
// output; the Sentry side reads only the record, which keeps grouping stable
// rather than letting a request-scoped attribute split one failure into many
// issues.
func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &slogHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	return &slogHandler{inner: h.inner.WithGroup(name)}
}

// errorAttr finds the conventional "error" attribute this codebase logs its
// failures under, so Sentry gets an exception with a stack trace rather than
// a bare message.
func errorAttr(record slog.Record) error {
	var found error

	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != "error" {
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
