#!/usr/bin/env bash
# Self-test for redis-budget.sh: runs it against fixed stats and a fixed
# clock, and checks the exit code and the parts of the status line that carry
# the decision. Plain bash, no framework, so it runs anywhere the script
# does — on a pull request in redis-budget.yml, and locally with
#
#   bash .github/scripts/redis-budget.test.sh
#
# Exits non-zero if any case fails, after running all of them.

set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/redis-budget.sh"
limit=500000
failures=0

# Fixed instants, all UTC. October 2026 has 31 days, November 30, February
# 2027 28 and February 2028 29.
oct_01_2000=1790884800 # 2026-10-01T20:00:00Z, still day 1
oct_11=1791676800      # 2026-10-11T00:00:00Z, 10 days elapsed
oct_16=1792108800      # 2026-10-16T00:00:00Z, 15 days elapsed
oct_25=1792886400      # 2026-10-25T00:00:00Z, 24 days elapsed
oct_30=1793318400      # 2026-10-30T00:00:00Z, 29 days elapsed
nov_01_0005=1793491205 # 2026-11-01T00:00:05Z, five seconds into the month
nov_11=1794355200      # 2026-11-11T00:00:00Z, 10 days elapsed
feb_11_2027=1802304000 # 2027-02-11T00:00:00Z, 10 days elapsed
feb_11_2028=1833840000 # 2028-02-11T00:00:00Z, 10 days elapsed

# check NAME EXPECTED_EXIT NOW STATS [EXPECTED_SUBSTRING...]
#
# A substring starting with "!" must not appear in the output.
check() {
	local name=$1 expected_exit=$2 now=$3 stats=$4
	shift 4

	local output exit_code=0
	output=$(bash "$script" "$limit" "$now" <<<"$stats") || exit_code=$?

	local problems=""
	if [[ $exit_code -ne $expected_exit ]]; then
		problems+=" exit ${exit_code}, wanted ${expected_exit};"
	fi
	if [[ $(printf '%s\n' "$output" | wc -l | tr -d ' ') -ne 1 ]]; then
		problems+=" not exactly one line;"
	fi

	local expected
	for expected in "$@"; do
		if [[ $expected == !* ]]; then
			if [[ $output == *"${expected#!}"* ]]; then
				problems+=" contains '${expected#!}';"
			fi
		elif [[ $output != *"$expected"* ]]; then
			problems+=" lacks '${expected}';"
		fi
	done

	if [[ -n $problems ]]; then
		echo "FAIL  ${name}:${problems}"
		echo "      output: ${output}"
		failures=$((failures + 1))
	else
		echo "ok    ${name}"
	fi
}

stats() {
	printf '{"total_monthly_requests": %s, "total_monthly_read_requests": 1, "database_name": "never-print-me"}' "$1"
}

# The 70% rule.
check "well below" 0 "$oct_16" "$(stats 100000)" \
	"OK: " "used 100000 of 500000" "(20.0%)" "projected 206666 by month end" "!never-print-me"
check "just below 70%" 0 "$oct_25" "$(stats 349999)" \
	"OK: " "(69.9%)"
check "exactly 70%" 2 "$oct_25" "$(stats 350000)" \
	"ALARM (at or above 70%): " "(70.0%)" "projected 452083" "!projected over"
check "above 70%" 2 "$oct_30" "$(stats 400000)" \
	"ALARM (at or above 70%): " "(80.0%)" "projected 427586" "!projected over"

# The projection.
check "below 70% but projected over" 2 "$oct_11" "$(stats 200000)" \
	"ALARM (projected over the limit): " "(40.0%)" "projected 620000"
check "both rules at once" 2 "$oct_11" "$(stats 400000)" \
	"ALARM (at or above 70%, projected over the limit): "
check "day 1, high but below 70%: no projection" 0 "$oct_01_2000" "$(stats 300000)" \
	"OK: " "(60.0%)" "no projection before day 2"
check "day 1, at or above 70%: the 70% rule alone" 2 "$oct_01_2000" "$(stats 375000)" \
	"ALARM (at or above 70%): " "no projection before day 2"

# Month lengths: the same 10 days and the same usage project differently.
check "31-day month" 0 "$oct_11" "$(stats 100000)" "projected 310000"
check "30-day month" 0 "$nov_11" "$(stats 100000)" "projected 300000"
check "February" 0 "$feb_11_2027" "$(stats 100000)" "projected 280000"
check "leap February" 0 "$feb_11_2028" "$(stats 100000)" "projected 290000"

# The month boundary: five seconds into November is a new month, not the
# 31st day of October, so there is nothing to project yet.
check "just after midnight on the 1st" 0 "$nov_01_0005" "$(stats 12)" \
	"OK: " "used 12 of 500000" "no projection before day 2"

# Script requests are reported when present, and only then.
check "script requests present" 0 "$oct_16" \
	'{"total_monthly_requests": 100000, "total_monthly_script_requests": 61000}' \
	"; script requests 61000"
check "script requests absent" 0 "$oct_16" "$(stats 100000)" "!script requests"

# Bad input: nothing is decided, and nothing from the response is echoed.
check "missing field" 1 "$oct_16" '{"total_monthly_read_requests": 5, "database_name": "never-print-me"}' \
	"BAD INPUT: " "total_monthly_requests" "!never-print-me"
check "null field" 1 "$oct_16" "$(stats null)" "BAD INPUT: "
check "non-numeric" 1 "$oct_16" "$(stats '"lots"')" "BAD INPUT: " "!lots"
check "negative" 1 "$oct_16" "$(stats -5)" "BAD INPUT: "
check "fractional" 1 "$oct_16" "$(stats 1.5)" "BAD INPUT: "
check "out of range" 1 "$oct_16" "$(stats 1e20)" "BAD INPUT: "
check "not JSON" 1 "$oct_16" 'Unauthorized: never-print-me' "BAD INPUT: " "!never-print-me"
check "not an object" 1 "$oct_16" '[1, 2]' "BAD INPUT: "
check "two documents" 1 "$oct_16" "$(stats 1) $(stats 2)" "BAD INPUT: "
check "empty" 1 "$oct_16" '' "BAD INPUT: "

# Bad arguments. An assignment in front of a function call holds for that call
# only, so the limit is back to 500000 afterwards.
limit=0 check "zero limit" 1 "$oct_16" "$(stats 1)" "BAD INPUT: " "LIMIT"
limit=0500000 check "octal-looking limit" 1 "$oct_16" "$(stats 1)" "BAD INPUT: " "LIMIT"
check "non-numeric NOW" 1 "yesterday" "$(stats 1)" "BAD INPUT: " "NOW"

if ((failures > 0)); then
	echo "${failures} case(s) failed"
	exit 1
fi
echo "all cases passed"
