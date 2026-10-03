package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The floor is the start of a UTC day two calendar years back, whatever time
// of day the job happens to run: an entry's survival must not depend on
// whether the cron fired at 03:43 or was queued until noon.
func TestAuditRetentionFloorIsTwoCalendarYearsBackAtUTCMidnight(t *testing.T) {
	floor := auditRetentionFloor(time.Date(2026, 10, 3, 15, 4, 5, 0, time.UTC))

	require.Equal(t, time.Date(2024, 10, 3, 0, 0, 0, 0, time.UTC), floor)
}

// The same UTC day dayOf gives the click rollup, not the caller's local one.
func TestAuditRetentionFloorReducesToAUTCDay(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)

	floor := auditRetentionFloor(time.Date(2026, 10, 3, 0, 30, 0, 0, berlin))

	require.Equal(t, "2024-10-02", floor.Format(dayLayout),
		"00:30 on 3 October in UTC+2 is 22:30 on the 2nd in UTC")
	require.Equal(t, time.UTC, floor.Location())
}

// TestAuditRetentionFloorOnALeapDay pins AddDate's normalisation rather than
// working around it. 29 February 2028 minus two years is 29 February 2026,
// which does not exist, so Go rolls it forward to 1 March. That is acceptable:
// on that one day the job keeps one day less than two years, never one more,
// and a deletion floor erring towards deleting is the direction the privacy
// policy can state. Clamping it back to 28 February instead would need a
// second piece of date arithmetic that only matters once every four years.
func TestAuditRetentionFloorOnALeapDay(t *testing.T) {
	floor := auditRetentionFloor(time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC))

	require.Equal(t, "2026-03-01", floor.Format(dayLayout))
}
