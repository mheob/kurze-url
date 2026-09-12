package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRetentionCutoffIsTheStatsEndpointsFloor is what makes the two halves of
// the retention promise one thing rather than two that happen to agree. The
// stats endpoint serves from its floor forward; this job deletes everything
// before its cutoff. If they ever differ, one of two silent failures follows:
// rows the promise says are gone stay readable, or a Verein's statistics
// vanish from inside a window the API still offers.
//
// Both sides are computed here from one clock, so the test cannot pass by two
// literals happening to match. Asking statsWindow for a window starting in
// 1999 forces it to clamp, which makes the start it returns the floor itself.
func TestRetentionCutoffIsTheStatsEndpointsFloor(t *testing.T) {
	now := time.Date(2026, 9, 12, 14, 30, 0, 0, time.UTC)

	floor, _, err := statsWindow(mustDay(t, "1999-01-01"), time.Time{}, now)
	require.NoError(t, err)

	require.Equal(t, floor.Format(dayLayout), retentionCutoff(now).Format(dayLayout))
	require.Equal(t, "2026-06-15", retentionCutoff(now).Format(dayLayout),
		"2026-09-12 minus 89 days")
}

// The cutoff is a UTC calendar day, not an instant: a caller's time of day
// must not shift which day survives.
func TestRetentionCutoffReducesToAUTCDay(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)

	cutoff := retentionCutoff(time.Date(2026, 9, 12, 0, 30, 0, 0, berlin))

	require.Equal(t, "2026-06-14", cutoff.Format(dayLayout),
		"00:30 on 12 September in UTC+2 is 22:30 on the 11th in UTC")
	require.Equal(t, time.UTC, cutoff.Location())
}
