package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Row is one rollup increment ready for the database.
type Row struct {
	LinkID   uuid.UUID
	Day      time.Time
	DimType  string
	DimValue *string
	Clicks   int64
	Unique   int64
}

// FlushFunc writes a batch of rollup increments. It is the only place the
// recorder touches the outside world.
type FlushFunc func(ctx context.Context, rows []Row) error

type bufferKey struct {
	linkID   uuid.UUID
	day      time.Time
	dimType  string
	dimValue string
	isNull   bool
}

type bufferCounts struct {
	clicks int64
	unique int64
}

// Recorder aggregates clicks in memory and writes them to the database out of
// band, so the redirect response never waits on analytics. Buffered rows are
// lost if the process dies unflushed — an accepted trade for keeping the hot
// path free of a synchronous write, and bounded by the flush interval.
type Recorder struct {
	flush    FlushFunc
	interval time.Duration
	maxSize  int
	log      *slog.Logger

	mu     sync.Mutex
	buffer map[bufferKey]bufferCounts

	wake chan struct{}
}

// NewRecorder builds a recorder. maxBuffer bounds how many distinct rows may
// accumulate before a flush is triggered early, independent of interval.
func NewRecorder(flush FlushFunc, interval time.Duration, maxBuffer int, log *slog.Logger) *Recorder {
	return &Recorder{
		flush:    flush,
		interval: interval,
		maxSize:  maxBuffer,
		log:      log,
		buffer:   make(map[bufferKey]bufferCounts),
		wake:     make(chan struct{}, 1),
	}
}

// Record accumulates one click. It performs no I/O and never blocks on
// anything but a brief in-memory lock, which is what keeps it safe to call
// from the redirect handler.
func (r *Recorder) Record(linkID uuid.UUID, at time.Time, d Dimensions, unique bool) {
	day := at.UTC().Truncate(24 * time.Hour)

	var uniqueCount int64
	if unique {
		uniqueCount = 1
	}

	r.mu.Lock()
	for _, row := range d.Rows() {
		key := bufferKey{linkID: linkID, day: day, dimType: row.Type, isNull: row.Value == nil}
		if row.Value != nil {
			key.dimValue = *row.Value
		}
		counts := r.buffer[key]
		counts.clicks++
		counts.unique += uniqueCount
		r.buffer[key] = counts
	}
	full := len(r.buffer) >= r.maxSize
	r.mu.Unlock()

	if full {
		select {
		case r.wake <- struct{}{}:
		default: // a flush is already pending
		}
	}
}

// Run flushes on the interval and whenever the buffer fills, then flushes once
// more when ctx is cancelled so a graceful shutdown does not drop counts.
func (r *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := r.Flush(shutdownCtx); err != nil {
				r.log.Error("final click-stats flush failed", "error", err)
			}
			cancel()
			return
		case <-ticker.C:
			r.flushAndLog(ctx)
		case <-r.wake:
			r.flushAndLog(ctx)
		}
	}
}

func (r *Recorder) flushAndLog(ctx context.Context) {
	if err := r.Flush(ctx); err != nil {
		r.log.Error("click-stats flush failed", "error", err)
	}
}

// Flush writes everything buffered so far. On failure the rows are dropped
// rather than retained: analytics are a rollup, and an unbounded retry buffer
// on a serverless instance is a worse failure than a gap in the counts.
func (r *Recorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	if len(r.buffer) == 0 {
		r.mu.Unlock()
		return nil
	}
	pending := r.buffer
	r.buffer = make(map[bufferKey]bufferCounts)
	r.mu.Unlock()

	rows := make([]Row, 0, len(pending))
	for key, counts := range pending {
		row := Row{
			LinkID:  key.linkID,
			Day:     key.day,
			DimType: key.dimType,
			Clicks:  counts.clicks,
			Unique:  counts.unique,
		}
		if !key.isNull {
			value := key.dimValue
			row.DimValue = &value
		}
		rows = append(rows, row)
	}

	return r.flush(ctx, rows)
}
