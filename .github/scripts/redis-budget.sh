#!/usr/bin/env bash
# Decides whether this month's Upstash command usage is on course to run out.
# .github/workflows/redis-budget.yml runs it on the stats Upstash's Developer
# API returns; the reasoning behind the alarm lives in that file's header.
#
# Usage: redis-budget.sh LIMIT NOW < stats.json
#
#   stdin  The body of GET https://api.upstash.com/v2/redis/stats/{id}.
#          total_monthly_requests is required; total_monthly_script_requests
#          is reported when present, for information only.
#   LIMIT  The database's monthly command limit, a positive integer.
#   NOW    The current time in Unix seconds. An argument rather than a clock
#          read so the self-test (redis-budget.test.sh) can fix it.
#
# Prints exactly one line on stdout, whatever the outcome: the status line the
# workflow sends to the heartbeat monitor and writes to the job summary. It
# never prints the response it was given, only the figures it extracted from
# it. Exits
#
#   0  OK
#   2  alarm: usage is at or above 70% of LIMIT, or projected to exceed it
#   1  bad input: an argument or the stats are unusable, so nothing was decided
#
# The month is assumed to be the calendar month in UTC. Upstash documents
# neither whether its "current month" is a calendar or a rolling one nor which
# timezone it counts in, and no single run settles both. A manual run in the
# middle of the month settles the first: if total_monthly_requests matches
# the console's figure for usage since the 1st, the month is a calendar one.
# The timezone shows only at the turn of the month, in the first runs on the
# 1st. If the counter has not reset by then, Upstash's month starts after UTC
# midnight, and those runs read last month's total against a day-1 clock:
# no projection applies yet, but a last month that ended at or above 70%
# raises a false alarm that clears once the counter resets.
#
# Needs only bash and jq, and runs on the bash 3.2 macOS ships as well as on
# the runner's, so the self-test can be run locally.

set -euo pipefail

# The share of LIMIT that raises the alarm by itself, whatever the projection
# says. It mirrors the first of the thresholds Upstash's own budget emails use
# on paid plans, and leaves the remaining 30% of the month's commands as time
# to act on it.
readonly alarm_percent=70
readonly seconds_per_day=86400

# Counts are capped at 12 digits, a trillion commands, so that the widest
# product below — used × 31 days × 86,400 seconds — stays inside bash's
# 64-bit arithmetic. Leading zeros are refused because bash reads them as
# octal.
readonly count_pattern='^(0|[1-9][0-9]{0,11})$'

bad_input() {
	echo "BAD INPUT: $1"
	exit 1
}

if [[ $# -ne 2 ]]; then
	bad_input "usage: redis-budget.sh LIMIT NOW < stats.json"
fi

limit=$1
now=$2

if ! [[ $limit =~ $count_pattern ]] || ((limit == 0)); then
	bad_input "LIMIT must be a positive integer of at most 12 digits"
fi
if ! [[ $now =~ $count_pattern ]]; then
	bad_input "NOW must be Unix seconds"
fi

# jq's stderr is discarded on purpose: some of its error messages quote the
# value they failed on, and that value is the response. Every outcome it can
# reach is instead one of this script's own fixed lines.
#
# A missing or null total_monthly_requests is bad input, never zero. Reading it
# as zero would report a healthy month on the very day Upstash renamed the
# field.
if ! fields=$(jq --slurp --raw-output '
	def count: type == "number" and . >= 0 and . == floor;

	if length != 1 then
		["bad", "the stats are not exactly one JSON document"]
	elif (.[0] | type) != "object" then
		["bad", "the stats are not a JSON object"]
	elif (.[0].total_monthly_requests | count | not) then
		["bad", "total_monthly_requests is missing or not a non-negative integer"]
	else
		.[0] as $stats
		| [
			"ok",
			$stats.total_monthly_requests,
			($stats.total_monthly_script_requests | if count then . else "" end)
		]
	end
	| @tsv
' 2>/dev/null); then
	bad_input "the stats are not valid JSON"
fi

IFS=$'\t' read -r verdict used scripts <<<"$fields"

if [[ $verdict != ok ]]; then
	bad_input "$used"
fi
if ! [[ $used =~ $count_pattern ]]; then
	bad_input "total_monthly_requests is out of range"
fi
# Informational only, so an unusable value is dropped rather than refused.
if ! [[ $scripts =~ $count_pattern ]]; then
	scripts=""
fi

# Seconds since the start of the current calendar month in UTC, and that
# month's length in days. jq rather than date(1), whose flags differ between
# GNU and BSD.
if ! calendar=$(jq --null-input --raw-output --argjson now "$now" '
	($now | gmtime) as [$year, $month, $day, $hour, $minute, $second]
	| (($year % 4 == 0 and $year % 100 != 0) or $year % 400 == 0) as $leap
	| [31, (if $leap then 29 else 28 end), 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][$month] as $days
	| [($day - 1) * 86400 + $hour * 3600 + $minute * 60 + ($second | floor), $days]
	| @tsv
' 2>/dev/null); then
	bad_input "NOW could not be read as a date"
fi

IFS=$'\t' read -r elapsed days_in_month <<<"$calendar"

tenths=$((used * 1000 / limit))
line="used ${used} of ${limit} commands this month ($((tenths / 10)).$((tenths % 10))%)"
reasons=""

if ((used * 100 >= limit * alarm_percent)); then
	reasons+="${reasons:+, }at or above ${alarm_percent}%"
fi

# used / elapsed days × days in the month, kept in whole numbers by scaling
# both sides to seconds. The comparison is made on the exact products, not on
# the rounded figure printed, so a projection that only rounds down to the
# limit still counts as over it.
#
# Not before a full day has passed: a burst in the first hours of the month,
# a deploy's cold caches or one busy newsletter, would otherwise project to
# many times the limit and raise an alarm on noise. Until then the 70% rule
# above is the only one that can fire.
if ((elapsed >= seconds_per_day)); then
	line+="; projected $((used * days_in_month * seconds_per_day / elapsed)) by month end"
	if ((used * days_in_month * seconds_per_day > limit * elapsed)); then
		reasons+="${reasons:+, }projected over the limit"
	fi
else
	line+="; no projection before day 2"
fi

if [[ -n $scripts ]]; then
	line+="; script requests ${scripts}"
fi

if [[ -n $reasons ]]; then
	echo "ALARM (${reasons}): ${line}"
	exit 2
fi

echo "OK: ${line}"
