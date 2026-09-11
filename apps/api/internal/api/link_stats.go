package api

import (
	"errors"
	"time"
)

// dayLayout is how every date in this endpoint is written and read: the bucket
// column is a date, so an RFC 3339 timestamp would imply a precision the data
// does not have.
const dayLayout = "2006-01-02"

// RetentionDays bounds what this endpoint will serve. docs/planning/01-architecture.md
// promises 90-day automatic deletion of analytics; nothing implements that
// deletion yet, so this bound is currently the only thing honouring it — the
// rows are still in the table, they are simply not readable through here.
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
