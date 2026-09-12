package api

import (
	"testing"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/db"
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

func strptr(v string) *string { return &v }

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

// TestStatsWindowNormalisesANonUTCInstant guards dayOf's .UTC() call. Days are
// UTC days here because analytics.Recorder buckets them that way; a caller's
// location must not shift which calendar day a boundary lands on.
func TestStatsWindowNormalisesANonUTCInstant(t *testing.T) {
	// 00:30 on 12 September in UTC+2 is still 22:30 on 11 September in UTC.
	berlin := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 9, 12, 0, 30, 0, 0, berlin)

	start, end, err := statsWindow(time.Time{}, time.Time{}, now)

	require.NoError(t, err)
	require.Equal(t, "2026-09-11", end.Format(dayLayout), "the UTC day, not the caller's")
	require.Equal(t, "2026-08-13", start.Format(dayLayout))
	require.Equal(t, time.UTC, end.Location())
}

// TestBuildSeriesFillsEveryDayInTheWindow is why the API fills the gaps rather
// than the client: the table has no row for a day nobody clicked, and a chart
// wants a point per day. Making every consumer reimplement that fill —
// correctly, across a month boundary, in UTC — is how two clients end up
// disagreeing about February.
func TestBuildSeriesFillsEveryDayInTheWindow(t *testing.T) {
	rows := []db.GetLinkClickSeriesRow{
		{BucketStart: mustDay(t, "2026-08-31"), Clicks: 4, UniqueVisitors: 3, HumanClicks: 2, HumanUniqueVisitors: 1},
		{BucketStart: mustDay(t, "2026-09-02"), Clicks: 6, UniqueVisitors: 5, HumanClicks: 6, HumanUniqueVisitors: 5},
	}

	series, totals := buildSeries(rows, mustDay(t, "2026-08-30"), mustDay(t, "2026-09-02"))

	require.Len(t, series, 4, "30, 31, 1, 2 — the month boundary is not a gap")
	require.Equal(t, "2026-08-30", series[0].Date)
	require.EqualValues(t, 0, series[0].Clicks)
	require.Equal(t, "2026-08-31", series[1].Date)
	require.EqualValues(t, 4, series[1].Clicks)
	require.Equal(t, "2026-09-01", series[2].Date)
	require.EqualValues(t, 0, series[2].Clicks, "a day with no row is a zero day, not a missing one")
	require.Equal(t, "2026-09-02", series[3].Date)
	require.EqualValues(t, 6, series[3].Clicks)

	// Totals are summed from the series rather than queried, so they cannot
	// disagree with it — a guarantee a second aggregate could not give.
	require.EqualValues(t, 10, totals.Clicks)
	require.EqualValues(t, 8, totals.UniqueVisitors)
	require.EqualValues(t, 8, totals.HumanClicks)
	require.EqualValues(t, 6, totals.HumanUniqueVisitors)
}

// TestBuildSeriesHandlesASingleDayWindow covers the shape every clamped
// degenerate request collapses to.
func TestBuildSeriesHandlesASingleDayWindow(t *testing.T) {
	series, totals := buildSeries(nil, mustDay(t, "2026-09-11"), mustDay(t, "2026-09-11"))

	require.Len(t, series, 1)
	require.Equal(t, "2026-09-11", series[0].Date)
	require.EqualValues(t, 0, totals.Clicks)
}

// TestBuildBreakdownsCollapsesTheRemainder checks the arithmetic the SQL sets
// up: the dimension's full figures minus the rows actually returned.
func TestBuildBreakdownsCollapsesTheRemainder(t *testing.T) {
	rows := []db.GetLinkClickBreakdownsRow{
		{
			DimensionType: "referrer", DimensionValue: strptr("news.example"),
			Clicks: 30, UniqueVisitors: 20,
			DimensionClicks: 50, DimensionUniqueVisitors: 35, DimensionValues: 4,
		},
		{
			DimensionType: "referrer", DimensionValue: strptr("direct"),
			Clicks: 12, UniqueVisitors: 9,
			DimensionClicks: 50, DimensionUniqueVisitors: 35, DimensionValues: 4,
		},
	}

	got := buildBreakdowns(rows)

	require.Len(t, got.Referrer.Values, 2)
	require.Equal(t, "news.example", got.Referrer.Values[0].Value)
	require.EqualValues(t, 30, got.Referrer.Values[0].Clicks)
	require.EqualValues(t, 2, got.Referrer.OtherValues, "4 distinct values, 2 returned")
	require.EqualValues(t, 8, got.Referrer.OtherClicks, "50 total, 42 returned")
	require.EqualValues(t, 6, got.Referrer.OtherUniqueVisitors, "35 total, 29 returned")
}

// TestBuildBreakdownsAlwaysReturnsEveryDimension means a client never has to
// distinguish "no data" from "field absent", and never receives a null where
// it expects a list.
func TestBuildBreakdownsAlwaysReturnsEveryDimension(t *testing.T) {
	got := buildBreakdowns(nil)

	for name, breakdown := range map[string]StatBreakdown{
		"browser": got.Browser, "os": got.OS, "device": got.Device,
		"country": got.Country, "referrer": got.Referrer, "utm_source": got.UTMSource,
		"bot_status": got.BotStatus, "qr_vs_regular": got.QRVsRegular,
	} {
		require.NotNil(t, breakdown.Values, "%s must marshal as [] and never as null", name)
		require.Empty(t, breakdown.Values, name)
		require.EqualValues(t, 0, breakdown.OtherClicks, name)
	}
}

// TestBuildBreakdownsNamesANullValueUnknown keeps a row's clicks in the answer.
// The dimension_value column is nullable and only the 'total' rows are meant to
// use that, but no constraint enforces the pairing — so a null here is possible
// in principle, and dropping the row would silently lose its clicks. "unknown"
// is the spelling analytics/dimensions.go already uses for a value it could not
// determine.
func TestBuildBreakdownsNamesANullValueUnknown(t *testing.T) {
	rows := []db.GetLinkClickBreakdownsRow{{
		DimensionType: "browser", DimensionValue: nil,
		Clicks: 3, UniqueVisitors: 3,
		DimensionClicks: 3, DimensionUniqueVisitors: 3, DimensionValues: 1,
	}}

	got := buildBreakdowns(rows)

	require.Len(t, got.Browser.Values, 1)
	require.Equal(t, "unknown", got.Browser.Values[0].Value)
	require.EqualValues(t, 3, got.Browser.Values[0].Clicks)
}

// TestBuildBreakdownsSkipsAnUnrecognisedDimension covers the forward-compatible
// case: a value added to the table's check constraint later, before this
// endpoint knows what it means. Skipping is the honest answer — inventing a
// bucket for it would report a number under a name nobody chose.
func TestBuildBreakdownsSkipsAnUnrecognisedDimension(t *testing.T) {
	rows := []db.GetLinkClickBreakdownsRow{
		{
			DimensionType: "language", DimensionValue: strptr("de"),
			Clicks: 5, UniqueVisitors: 5,
			DimensionClicks: 5, DimensionUniqueVisitors: 5, DimensionValues: 1,
		},
		{
			DimensionType: "device", DimensionValue: strptr("mobile"),
			Clicks: 7, UniqueVisitors: 7,
			DimensionClicks: 7, DimensionUniqueVisitors: 7, DimensionValues: 1,
		},
	}

	got := buildBreakdowns(rows)

	require.Len(t, got.Device.Values, 1)
	require.Empty(t, got.Browser.Values)
}
