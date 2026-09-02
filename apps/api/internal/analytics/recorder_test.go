package analytics_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

type flushSpy struct {
	mu    sync.Mutex
	calls [][]analytics.Row
	err   error
}

func (s *flushSpy) flush(_ context.Context, rows []analytics.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, rows)
	return s.err
}

func (s *flushSpy) rowsFor(dimType string) []analytics.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []analytics.Row
	for _, call := range s.calls {
		for _, row := range call {
			if row.DimType == dimType {
				out = append(out, row)
			}
		}
	}
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func desktopDimensions() analytics.Dimensions {
	return analytics.Dimensions{
		Browser: "Chrome", OS: "macOS", Device: "desktop", Country: "DE",
		Referrer: "direct", BotStatus: "human", Source: "regular",
	}
}

func TestRecorderAggregatesRepeatedClicksIntoOneRow(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	linkID := uuid.New()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	rec.Record(linkID, now, desktopDimensions(), true)
	rec.Record(linkID, now, desktopDimensions(), false)
	rec.Record(linkID, now, desktopDimensions(), false)

	require.NoError(t, rec.Flush(context.Background()))

	totals := spy.rowsFor("total")
	require.Len(t, totals, 1, "three clicks on one link on one day are one row")
	require.EqualValues(t, 3, totals[0].Clicks)
	require.EqualValues(t, 1, totals[0].Unique)
	require.Nil(t, totals[0].DimValue, "the total row's dimension_value is null")
}

func TestRecorderKeepsDifferentDaysApart(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	linkID := uuid.New()

	rec.Record(linkID, time.Date(2026, 9, 2, 23, 0, 0, 0, time.UTC), desktopDimensions(), true)
	rec.Record(linkID, time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC), desktopDimensions(), true)

	require.NoError(t, rec.Flush(context.Background()))

	require.Len(t, spy.rowsFor("total"), 2)
}

func TestRecorderTruncatesTheDayToAUTCDate(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	rec.Record(uuid.New(), time.Date(2026, 9, 2, 13, 37, 42, 99, time.UTC), desktopDimensions(), true)
	require.NoError(t, rec.Flush(context.Background()))

	day := spy.rowsFor("total")[0].Day
	require.Equal(t, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), day)
}

func TestFlushIsANoOpWhenNothingWasRecorded(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	require.NoError(t, rec.Flush(context.Background()))

	spy.mu.Lock()
	defer spy.mu.Unlock()
	require.Empty(t, spy.calls, "an empty buffer must not hit the database")
}

func TestFlushEmptiesTheBuffer(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	now := time.Now()

	rec.Record(uuid.New(), now, desktopDimensions(), true)
	require.NoError(t, rec.Flush(context.Background()))
	require.NoError(t, rec.Flush(context.Background()))

	require.Len(t, spy.rowsFor("total"), 1, "the second flush must not resend the first flush's rows")
}

func TestFlushReportsButDoesNotRetainRowsOnError(t *testing.T) {
	spy := &flushSpy{err: errors.New("database down")}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)

	require.Error(t, rec.Flush(context.Background()))

	spy.err = nil
	require.NoError(t, rec.Flush(context.Background()))
	require.Len(t, spy.rowsFor("total"), 1,
		"a failed flush drops its rows rather than growing the buffer without bound")
}

func TestRunFlushesOnContextCancellation(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	require.Len(t, spy.rowsFor("total"), 1, "buffered clicks must survive shutdown")
}

func TestRunFlushesWhenTheBufferFills(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 2, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	for range 5 {
		rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)
	}

	require.Eventually(t, func() bool {
		return len(spy.rowsFor("total")) >= 2
	}, 5*time.Second, 20*time.Millisecond,
		"exceeding maxBuffer must trigger a flush without waiting for the interval")
}
