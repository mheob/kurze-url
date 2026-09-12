package api

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// dayLayout is how every date in this endpoint is written and read: the bucket
// column is a date, so an RFC 3339 timestamp would imply a precision the data
// does not have.
const dayLayout = "2006-01-02"

// RetentionDays is the one definition of the retention window promised in
// docs/planning/01-architecture.md. The stats endpoint derives its floor
// through statsWindow; the retention job derives its deletion cutoff through
// retentionCutoff. Both read this constant, making them complements: the
// endpoint serves rows from the floor forward; the job deletes rows before it.
// Keeping them one constant is not optional — two definitions would drift
// silently, and the drift shows only as rows the promise says are deleted
// staying readable, or a Verein's statistics vanishing from a window the API
// still offers. TestRetentionCutoffIsTheStatsEndpointsFloor holds them together.
// Inclusive: the floor is today minus 89 days, which makes a 90-day window.
const RetentionDays = 90

// DefaultWindowDays is the window a caller who names no dates receives.
// Inclusive, like RetentionDays.
const DefaultWindowDays = 30

// TopValuesPerDimension caps how many values each breakdown reports. Six of
// the eight dimensions have small closed value sets and will never reach it;
// the cap exists for country, referrer and utm_source, whose distinct values
// are supplied by whoever clicks the link and are therefore unbounded.
const TopValuesPerDimension = 10

// errFromAfterTo is the one way a caller can be refused outright.
var errFromAfterTo = errors.New("from is later than to")

// statsWindow resolves the requested dates into the window the queries run
// over. A zero from or to means the caller omitted that parameter.
//
// The order of the three steps is part of the endpoint's specification, not an
// implementation detail:
//
//  1. A self-contradictory *pair* is refused, judged on what the caller sent.
//     Clamping first could turn a coherent request into a contradiction the
//     caller never made — a window wholly in the future would become
//     from > to purely because to had been pulled back to today.
//  2. to is resolved and clamped into [floor, today].
//  3. from is defaulted from the *resolved* to, then clamped into [floor, to].
//
// The floor is measured against today, never against to. Against to, a request
// for two months of 2024 would walk the window backwards out of the retention
// period entirely.
func statsWindow(from, to, now time.Time) (start, end time.Time, err error) {
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return time.Time{}, time.Time{}, errFromAfterTo
	}

	today := dayOf(now)
	floor := today.AddDate(0, 0, -(RetentionDays - 1))

	end = today
	if !to.IsZero() {
		end = clampDay(dayOf(to), floor, today)
	}

	start = end.AddDate(0, 0, -(DefaultWindowDays - 1))
	if !from.IsZero() {
		start = dayOf(from)
	}
	start = clampDay(start, floor, end)

	return start, end, nil
}

// dayOf reduces an instant to the UTC calendar day it falls on. Days are UTC
// everywhere here because analytics.Recorder buckets with
// at.UTC().Truncate(24 * time.Hour) — a click at 01:30 Central European Summer
// Time belongs to the previous day's bucket, and no later choice can move it.
func dayOf(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func clampDay(value, low, high time.Time) time.Time {
	if value.Before(low) {
		return low
	}
	if value.After(high) {
		return high
	}
	return value
}

// LinkStats is the whole answer of GET /v1/links/{link_id}/stats: one document
// per link, rather than one request per dimension. A dashboard would otherwise
// make nine calls, each repeating the same authorization resolve and the same
// index scan over the same rows.
type LinkStats struct {
	LinkID uuid.UUID `json:"link_id"`
	// From and To are the window that was actually used, which is not always
	// the one that was asked for: both are clamped into the retention window,
	// silently, and echoing them here is the only way a caller can see that.
	From             string              `json:"from" doc:"First day included, as YYYY-MM-DD in UTC. This is the window actually used, which may be narrower than the one requested."`
	To               string              `json:"to" doc:"Last day included, as YYYY-MM-DD in UTC. This is the window actually used, which may be narrower than the one requested."`
	AnalyticsEnabled bool                `json:"analytics_enabled" doc:"False when this link's click counting is switched off. The redirect path then records nothing, so an empty document means 'not counted' rather than 'not clicked'."`
	Totals           StatCounts          `json:"totals"`
	Series           []StatDay           `json:"series" doc:"One entry per day of the window, including days with no clicks. At most 90 entries."`
	Breakdowns       LinkStatsBreakdowns `json:"breakdowns"`
}

// StatCounts is the four numbers every level of this response reports.
//
// Both a total and a human figure appear, because either alone misleads. A
// link in a Verein's newsletter is fetched by every mail-provider link scanner
// it passes, so the total overstates reach; but the human figure alone cannot
// be reconciled with any breakdown, and the difference would then be invisible
// rather than explained.
type StatCounts struct {
	Clicks              int64 `json:"clicks"`
	UniqueVisitors      int64 `json:"unique_visitors" doc:"Distinct visitors on this day. Summed over several days this counts a returning person once per day."`
	HumanClicks         int64 `json:"human_clicks" doc:"Clicks whose User-Agent was not recognised as a bot. Available here and in totals only — no breakdown can be filtered this way."`
	HumanUniqueVisitors int64 `json:"human_unique_visitors"`
}

// StatDay is one calendar day, as YYYY-MM-DD in UTC.
//
// StatCounts is embedded without a JSON name, so Huma inlines its four fields
// into this object rather than nesting them (schema.go collects an anonymous
// field with no explicit JSON name and splices that struct's fields into the
// parent's, matching encoding/json).
type StatDay struct {
	Date string `json:"date"`
	StatCounts
}

// StatValue is one value of one dimension.
type StatValue struct {
	Value          string `json:"value"`
	Clicks         int64  `json:"clicks"`
	UniqueVisitors int64  `json:"unique_visitors"`
}

// StatBreakdown is one dimension's top values plus what they leave out. The
// Other* fields are not padding: without them a capped list would silently
// misrepresent a dimension's total.
type StatBreakdown struct {
	Values              []StatValue `json:"values"`
	OtherValues         int64       `json:"other_values" doc:"How many further values exist beyond the ten reported here."`
	OtherClicks         int64       `json:"other_clicks" doc:"Clicks belonging to those further values, so this dimension's reported figures still sum to its true total."`
	OtherUniqueVisitors int64       `json:"other_unique_visitors"`
}

// LinkStatsBreakdowns names every dimension as its own field rather than
// keying a map. A map generates as additionalProperties and reaches TypeScript
// as an index signature, where every access is a possible undefined; named
// fields reach it as eight typed properties, and "all eight are always
// present" becomes a property of the type instead of a runtime promise.
type LinkStatsBreakdowns struct {
	Browser     StatBreakdown `json:"browser"`
	OS          StatBreakdown `json:"os"`
	Device      StatBreakdown `json:"device"`
	Country     StatBreakdown `json:"country"`
	Referrer    StatBreakdown `json:"referrer"`
	UTMSource   StatBreakdown `json:"utm_source"`
	BotStatus   StatBreakdown `json:"bot_status"`
	QRVsRegular StatBreakdown `json:"qr_vs_regular"`
}

// slot returns the field a dimension_type belongs in, or nil for a type this
// build does not know. Nil is deliberate: a value added to the table's check
// constraint later should be skipped, not funnelled into a bucket nobody named.
func (b *LinkStatsBreakdowns) slot(dimensionType string) *StatBreakdown {
	switch dimensionType {
	case "browser":
		return &b.Browser
	case "os":
		return &b.OS
	case "device":
		return &b.Device
	case "country":
		return &b.Country
	case "referrer":
		return &b.Referrer
	case "utm_source":
		return &b.UTMSource
	case "bot_status":
		return &b.BotStatus
	case "qr_vs_regular":
		return &b.QRVsRegular
	default:
		return nil
	}
}

// buildSeries expands the query's sparse rows into one entry per day of the
// window and sums them into the totals.
//
// It returns both because the totals are the series' sum by construction:
// querying them separately would allow the headline figure and the chart under
// it to disagree, which is the kind of defect nobody reports and everybody
// distrusts.
func buildSeries(
	rows []db.GetLinkClickSeriesRow, start, end time.Time,
) ([]StatDay, StatCounts) {
	// Keyed by the formatted day rather than by time.Time: the date column
	// arrives as a time.Time whose location and precision are the driver's
	// business, and two instants that mean the same calendar day must not miss
	// each other in a map.
	byDay := make(map[string]StatCounts, len(rows))
	for _, row := range rows {
		byDay[row.BucketStart.UTC().Format(dayLayout)] = StatCounts{
			Clicks:              row.Clicks,
			UniqueVisitors:      row.UniqueVisitors,
			HumanClicks:         row.HumanClicks,
			HumanUniqueVisitors: row.HumanUniqueVisitors,
		}
	}

	var totals StatCounts
	series := make([]StatDay, 0, RetentionDays)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format(dayLayout)
		counts := byDay[key]

		totals.Clicks += counts.Clicks
		totals.UniqueVisitors += counts.UniqueVisitors
		totals.HumanClicks += counts.HumanClicks
		totals.HumanUniqueVisitors += counts.HumanUniqueVisitors

		series = append(series, StatDay{Date: key, StatCounts: counts})
	}

	return series, totals
}

// buildBreakdowns groups the ranked rows by dimension and turns each
// dimension's full figures into the remainder its returned values leave out.
func buildBreakdowns(rows []db.GetLinkClickBreakdownsRow) LinkStatsBreakdowns {
	var out LinkStatsBreakdowns

	// Every dimension answers, empty ones included, so a client never has to
	// distinguish "no data" from "field absent". An empty slice rather than
	// nil, so the field marshals as [] and never as null.
	for _, slot := range []*StatBreakdown{
		&out.Browser, &out.OS, &out.Device, &out.Country,
		&out.Referrer, &out.UTMSource, &out.BotStatus, &out.QRVsRegular,
	} {
		slot.Values = []StatValue{}
	}

	for _, row := range rows {
		slot := out.slot(row.DimensionType)
		if slot == nil {
			continue
		}

		value := "unknown"
		if row.DimensionValue != nil {
			value = *row.DimensionValue
		}
		slot.Values = append(slot.Values, StatValue{
			Value:          value,
			Clicks:         row.Clicks,
			UniqueVisitors: row.UniqueVisitors,
		})

		// Each row repeats its dimension's full figures, so the remainder is
		// recomputed rather than accumulated — the last row of a dimension
		// leaves the correct answer behind.
		slot.OtherValues = row.DimensionValues - int64(len(slot.Values))
		slot.OtherClicks = row.DimensionClicks
		slot.OtherUniqueVisitors = row.DimensionUniqueVisitors
		for _, seen := range slot.Values {
			slot.OtherClicks -= seen.Clicks
			slot.OtherUniqueVisitors -= seen.UniqueVisitors
		}
	}

	return out
}

// LinkStatsInput declares its authorization in its type: LinkViewerScope, the
// same floor as the QR download. Reading statistics changes nothing.
//
// Both dates are time.Time with a timeFormat tag, which makes Huma parse them
// as plain dates and answer a bad one itself — a malformed shape and an
// impossible date alike (time.Parse rejects 2026-02-31 as "day out of range").
// The handler therefore does not re-check the format; it checks only the one
// thing Huma cannot, which is how the two parameters relate to each other.
type LinkStatsInput struct {
	authz.LinkViewerScope
	From time.Time `query:"from" timeFormat:"2006-01-02" doc:"First day to include, as YYYY-MM-DD in UTC. Defaults to 29 days before 'to'. Clamped so the window never starts more than 89 days before today."`
	To   time.Time `query:"to" timeFormat:"2006-01-02" doc:"Last day to include, as YYYY-MM-DD in UTC. Defaults to today; a later date is treated as today."`
}

// LinkStatsOutput is the body of GET /v1/links/{link_id}/stats.
type LinkStatsOutput struct {
	Body LinkStats
}

// getLinkStats is GET /v1/links/{link_id}/stats.
func (d Deps) getLinkStats(ctx context.Context, in *LinkStatsInput) (*LinkStatsOutput, error) {
	link := in.Link()

	start, end, err := statsWindow(in.From, in.To, d.now())
	if err != nil {
		if !errors.Is(err, errFromAfterTo) {
			d.Log.Error("resolve stats window", "error", err, "link_id", link.ID)
			return nil, huma.Error500InternalServerError("could not read the statistics")
		}
		// No ErrorDetail: that typed-value convention carries a value the
		// caller must act on, and there is nothing here beyond the message.
		return nil, huma.Error422UnprocessableEntity("from must not be later than to")
	}

	// Read through the existing query rather than adding one for a single
	// column. It is already filtered by team_id, which is the filter that
	// matters — the scope resolved the team, and this re-states it.
	row, err := d.Queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
		ID: link.ID, TeamID: link.TeamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("link not found")
	}
	if err != nil {
		d.Log.Error("load link for stats", "error", err, "link_id", link.ID)
		return nil, huma.Error500InternalServerError("could not read the statistics")
	}

	seriesRows, err := d.Queries.GetLinkClickSeries(ctx, db.GetLinkClickSeriesParams{
		LinkID: link.ID, FromDay: start, ToDay: end,
	})
	if err != nil {
		d.Log.Error("read click series", "error", err, "link_id", link.ID)
		return nil, huma.Error500InternalServerError("could not read the statistics")
	}

	breakdownRows, err := d.Queries.GetLinkClickBreakdowns(ctx, db.GetLinkClickBreakdownsParams{
		LinkID: link.ID, FromDay: start, ToDay: end, TopValues: TopValuesPerDimension,
	})
	if err != nil {
		d.Log.Error("read click breakdowns", "error", err, "link_id", link.ID)
		return nil, huma.Error500InternalServerError("could not read the statistics")
	}

	series, totals := buildSeries(seriesRows, start, end)

	return &LinkStatsOutput{Body: LinkStats{
		LinkID:           link.ID,
		From:             start.Format(dayLayout),
		To:               end.Format(dayLayout),
		AnalyticsEnabled: row.AnalyticsEnabled,
		Totals:           totals,
		Series:           series,
		Breakdowns:       buildBreakdowns(breakdownRows),
	}}, nil
}
