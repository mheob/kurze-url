package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mustDay parses a YYYY-MM-DD literal into the UTC midnight these functions
// work in.
func mustDay(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(dayLayout, value)
	require.NoError(t, err)
	return parsed
}

// TestStatsWindow covers the resolution order the spec fixes, because two of
// its three steps give a different answer if they are swapped. "now" is an
// afternoon rather than a midnight on purpose: the resolver must reduce it to
// a date, and a bug that keeps the time of day would survive a midnight input.
func TestStatsWindow(t *testing.T) {
	now := time.Date(2026, 9, 11, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		name        string
		from, to    string // "" means the caller omitted the parameter
		wantFrom    string
		wantTo      string
		wantRefusal bool
	}{
		{
			name:     "neither parameter gives a 30-day window ending today",
			wantFrom: "2026-08-13", wantTo: "2026-09-11",
		},
		{
			name: "an explicit window inside the floor is left alone",
			from: "2026-09-01", to: "2026-09-05",
			wantFrom: "2026-09-01", wantTo: "2026-09-05",
		},
		{
			name: "a future to is clamped to today rather than refused",
			to:   "2026-12-24",
			// from is absent, so it is resolved from the *clamped* to.
			wantFrom: "2026-08-13", wantTo: "2026-09-11",
		},
		{
			name: "a from a year back is clamped to the retention floor",
			from: "2025-09-11", to: "2026-09-11",
			wantFrom: "2026-06-14", wantTo: "2026-09-11",
		},
		{
			name: "a window entirely in the future collapses to today",
			from: "2026-10-01", to: "2026-10-31",
			wantFrom: "2026-09-11", wantTo: "2026-09-11",
		},
		{
			name: "a window entirely before the floor collapses to the floor",
			from: "2024-01-01", to: "2024-02-01",
			wantFrom: "2026-06-14", wantTo: "2026-06-14",
		},
		{
			name: "an exactly 90-day request survives unclamped",
			from: "2026-06-14", to: "2026-09-11",
			wantFrom: "2026-06-14", wantTo: "2026-09-11",
		},
		{
			name: "only from, in the future, is clamped down to today",
			from: "2026-10-01",
			// Not a refusal: the caller supplied one parameter, so there is no
			// pair to contradict itself.
			wantFrom: "2026-09-11", wantTo: "2026-09-11",
		},
		{
			name: "from after to, both supplied, is the one refusal",
			from: "2026-09-05", to: "2026-09-01",
			wantRefusal: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var from, to time.Time
			if c.from != "" {
				from = mustDay(t, c.from)
			}
			if c.to != "" {
				to = mustDay(t, c.to)
			}

			start, end, err := statsWindow(from, to, now)

			if c.wantRefusal {
				require.ErrorIs(t, err, errFromAfterTo)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantFrom, start.Format(dayLayout))
			require.Equal(t, c.wantTo, end.Format(dayLayout))
		})
	}
}

// TestStatsWindowMeasuresTheFloorAgainstToday is the case that would pass
// under the wrong implementation and matter most. Clamping from to "to minus
// 89 days" instead of "today minus 89 days" lets a request for an old range
// walk the window backwards out of the retention period entirely.
func TestStatsWindowMeasuresTheFloorAgainstToday(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	floor := "2026-06-14"

	start, end, err := statsWindow(mustDay(t, "2024-01-01"), mustDay(t, "2026-07-01"), now)

	require.NoError(t, err)
	require.Equal(t, floor, start.Format(dayLayout),
		"the floor is today minus 89 days, never 'to' minus 89 days")
	require.Equal(t, "2026-07-01", end.Format(dayLayout))
}

// TestStatsWindowNeverExceedsTheRetentionLength is a property rather than an
// example: whatever the caller asks for, the window is at most 90 days and at
// least one, and its start is never before the floor.
func TestStatsWindowNeverExceedsTheRetentionLength(t *testing.T) {
	now := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	floor := mustDay(t, "2026-06-14")

	for _, c := range [][2]string{
		{"2020-01-01", "2030-01-01"},
		{"", ""},
		{"2026-09-11", "2026-09-11"},
		{"2026-05-01", ""},
		{"", "2026-06-14"},
	} {
		var from, to time.Time
		if c[0] != "" {
			from = mustDay(t, c[0])
		}
		if c[1] != "" {
			to = mustDay(t, c[1])
		}

		start, end, err := statsWindow(from, to, now)
		require.NoError(t, err)
		require.False(t, start.Before(floor), "start %s is before the floor", start)
		require.False(t, end.After(mustDay(t, "2026-09-11")), "end %s is after today", end)
		require.False(t, start.After(end), "start %s is after end %s", start, end)

		days := int(end.Sub(start).Hours()/24) + 1
		require.GreaterOrEqual(t, days, 1)
		require.LessOrEqual(t, days, RetentionDays)
	}
}
