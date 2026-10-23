package cli

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// statsPath is the stats route of a link, as the router sees it.
func statsPath(link apiclient.Link) string {
	return "/v1/links/" + link.ID + "/stats"
}

// statsHarness is a signed-in person in team A, with the one link `sommerfest`
// on the shared host and its stats answered by stats.
func statsHarness(t *testing.T, config string, stats apiclient.LinkStats) *harness {
	t.Helper()
	h := linkHarness(t, config, teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	h.api.respond(http.MethodGet, statsPath(lookupShared), http.StatusOK, stats)
	return h
}

// statsDay is day i of the 30 days that end on testNow, i running from 1 (the
// oldest, 2026-09-24) to 30 (today, 2026-10-23). A day has i clicks of which
// i-1 are human, and half as many visitors, rounded down.
func statsDay(i int) apiclient.StatDay {
	date := testNow.AddDate(0, 0, i-30).Format(time.DateOnly)
	return apiclient.StatDay{
		Date:                date,
		Clicks:              int64(i),
		UniqueVisitors:      int64(i / 2),
		HumanClicks:         int64(i - 1),
		HumanUniqueVisitors: int64((i - 1) / 2),
	}
}

// thirtyDays is the whole series, oldest day first.
func thirtyDays() []apiclient.StatDay {
	days := make([]apiclient.StatDay, 0, 30)
	for i := 1; i <= 30; i++ {
		days = append(days, statsDay(i))
	}
	return days
}

// windowsStats is the series above with totals that are not its sum, so a
// 30-day figure that was added up instead of taken from the totals shows.
func windowsStats(series []apiclient.StatDay) apiclient.LinkStats {
	return apiclient.LinkStats{
		AnalyticsEnabled: true,
		Totals: apiclient.StatCounts{
			Clicks: 999, UniqueVisitors: 777, HumanClicks: 888, HumanUniqueVisitors: 666,
		},
		Series: series,
	}
}

// windowsOutput is what windowsStats must print: the last seven days summed
// for the first column, the totals for the second, each right-aligned under
// its heading.
const windowsOutput = "https://go.kurze-url.app/sommerfest\n" +
	"\n" +
	"                     7 days   30 days\n" +
	"Humans\n" +
	"  Clicks                182       888\n" +
	"  Visitors*              89       666\n" +
	"All (incl. bots)\n" +
	"  Clicks                189       999\n" +
	"  Visitors*              93       777\n" +
	"\n" +
	"* Visitors are counted per day and added up: someone who comes back on three days counts three times.\n"

func TestStatsRequestsTheLast30Days(t *testing.T) {
	h := statsHarness(t, "team: sv-gruenwald\n", windowsStats(thirtyDays()))

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	calls := h.api.callsTo(http.MethodGet, statsPath(lookupShared))
	require.Len(t, calls, 1, "one request, however many windows are shown")
	require.Equal(t, url.Values{"from": {"2026-09-24"}, "to": {"2026-10-23"}}, calls[0].Query)
}

// The window is taken in UTC, as the API reads it: at 01:00 in Berlin on the
// 24th it is still the 23rd in UTC, and a person's local date must not move it.
func TestStatsWindowIsInUTC(t *testing.T) {
	h := statsHarness(t, "team: sv-gruenwald\n", windowsStats(thirtyDays()))
	berlin := time.FixedZone("CEST", 2*60*60)
	h.deps.Now = func() time.Time { return time.Date(2026, 10, 24, 1, 0, 0, 0, berlin) }

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	calls := h.api.callsTo(http.MethodGet, statsPath(lookupShared))
	require.Len(t, calls, 1)
	require.Equal(t, url.Values{"from": {"2026-09-24"}, "to": {"2026-10-23"}}, calls[0].Query)
}

func TestStatsWindows(t *testing.T) {
	h := statsHarness(t, "team: sv-gruenwald\n", windowsStats(thirtyDays()))

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 0, code, h.stderr.String())
	// The seven days are days 24 to 30: 24+25+...+30 clicks is 189, and the 30
	// days read 999, which no sum of the series gives, so they are the totals.
	require.Equal(t, windowsOutput, h.stdout.String())
	require.Empty(t, h.stderr.String())
	humans, all := strings.Index(h.stdout.String(), "Humans"), strings.Index(h.stdout.String(), "All (incl. bots)")
	require.Positive(t, humans)
	require.Less(t, humans, all, "the humans block comes first")
}

// The spec's own example, number for number: the layout is part of the
// command's contract.
func TestStatsLayoutIsTheSpecsExample(t *testing.T) {
	series := make([]apiclient.StatDay, 0, 30)
	for i := 1; i <= 29; i++ {
		day := statsDay(i)
		day.Clicks, day.UniqueVisitors, day.HumanClicks, day.HumanUniqueVisitors = 0, 0, 0, 0
		series = append(series, day)
	}
	today := statsDay(30)
	today.Clicks, today.UniqueVisitors, today.HumanClicks, today.HumanUniqueVisitors = 30, 25, 12, 10
	series = append(series, today)
	h := statsHarness(t, "team: sv-gruenwald\n", apiclient.LinkStats{
		AnalyticsEnabled: true,
		Totals:           apiclient.StatCounts{Clicks: 100, UniqueVisitors: 80, HumanClicks: 40, HumanUniqueVisitors: 33},
		Series:           series,
	})

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	require.Equal(t,
		"https://go.kurze-url.app/sommerfest\n"+
			"\n"+
			"                     7 days   30 days\n"+
			"Humans\n"+
			"  Clicks                 12        40\n"+
			"  Visitors*              10        33\n"+
			"All (incl. bots)\n"+
			"  Clicks                 30       100\n"+
			"  Visitors*              25        80\n"+
			"\n"+
			"* Visitors are counted per day and added up: someone who comes back on three days counts three times.\n",
		h.stdout.String())
}

// The API serves the series in date order, but the command does not rely on
// it: "the last seven" is the seven latest dates, wherever they arrive.
func TestStatsSortsTheSeriesBeforeSumming(t *testing.T) {
	series := thirtyDays()
	rand.New(rand.NewPCG(7, 11)).Shuffle(len(series), func(i, j int) { series[i], series[j] = series[j], series[i] })
	require.NotEqual(t, thirtyDays(), series, "the shuffle must actually move days")
	h := statsHarness(t, "team: sv-gruenwald\n", windowsStats(series))

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	require.Equal(t, windowsOutput, h.stdout.String())
}

// A link made three days ago has three days of series, and the seven-day
// column is those three, not an error and not zeros.
func TestStatsWithFewerThanSevenDaysSumsWhatThereIs(t *testing.T) {
	series := []apiclient.StatDay{statsDay(28), statsDay(29), statsDay(30)}
	h := statsHarness(t, "team: sv-gruenwald\n", windowsStats(series))

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	// 28+29+30 clicks, 27+28+29 of them human.
	require.Contains(t, h.stdout.String(), "  Clicks                 87       999\n")
	require.Contains(t, h.stdout.String(), "  Clicks                 84       888\n")
}

func TestStatsWithNoSeriesPrintsZeros(t *testing.T) {
	h := statsHarness(t, "team: sv-gruenwald\n", apiclient.LinkStats{AnalyticsEnabled: true})

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	require.Contains(t, h.stdout.String(), "  Clicks                  0         0\n")
}

// Zeros would read "nobody clicked", which is not what a link that is not
// being counted means.
func TestStatsWhenCountingIsOff(t *testing.T) {
	h := statsHarness(t, "team: sv-gruenwald\n", apiclient.LinkStats{AnalyticsEnabled: false})

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "Counting is switched off for this link, so there are no figures.\n", h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestStatsWhenCountingIsOffSpeaksGerman(t *testing.T) {
	h := statsHarness(t, "language: de\nteam: sv-gruenwald\n", apiclient.LinkStats{AnalyticsEnabled: false})

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	require.Equal(t, "Für diesen Link ist die Zählung ausgeschaltet, deshalb gibt es keine Zahlen.\n", h.stdout.String())
}

func TestStatsInGerman(t *testing.T) {
	h := statsHarness(t, "language: de\nteam: sv-gruenwald\n", windowsStats(thirtyDays()))

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"https://go.kurze-url.app/sommerfest\n"+
			"\n"+
			"                      7 Tage   30 Tage\n"+
			"Menschen\n"+
			"  Klicks                 182       888\n"+
			"  Besucher*               89       666\n"+
			"Alle (inkl. Bots)\n"+
			"  Klicks                 189       999\n"+
			"  Besucher*               93       777\n"+
			"\n"+
			"* Besucher werden pro Tag gezählt und addiert: Wer an drei Tagen wiederkommt, zählt dreimal.\n",
		h.stdout.String())
}

// A number wider than its heading widens the whole column, heading included,
// so the right edges still line up.
func TestStatsRightAlignsWideNumbers(t *testing.T) {
	stats := windowsStats(thirtyDays())
	stats.Totals.Clicks = 12345678
	h := statsHarness(t, "team: sv-gruenwald\n", stats)

	require.Equal(t, 0, h.run("link", "stats", "sommerfest"), h.stderr.String())

	table := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")[2:] // from the header on
	var widths []int
	for _, line := range table {
		// The heading and the four rows are the indented lines; the two
		// block titles, the blank line and the footnote are not.
		if strings.HasPrefix(line, " ") {
			widths = append(widths, utf8.RuneCountInString(line))
		}
	}
	require.NotEmpty(t, widths)
	for _, w := range widths {
		require.Equal(t, widths[0], w, "every row ends in the same column:\n%s", h.stdout.String())
	}
	require.Contains(t, h.stdout.String(), "12345678\n")
}

func TestStatsHonoursTheDomainFlag(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared, lookupCustom)
	h.api.respond(http.MethodGet, statsPath(lookupCustom), http.StatusOK, windowsStats(thirtyDays()))

	code := h.run("link", "stats", "sommerfest", "--domain", "links.sv-gruenwald.de")

	require.Equal(t, 0, code, h.stderr.String())
	require.True(t, strings.HasPrefix(h.stdout.String(), "https://links.sv-gruenwald.de/sommerfest\n"), h.stdout.String())
}

// The short URL names the link, so a link of another team needs no --team.
func TestStatsFindsALinkOfAnotherTeamByItsURL(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")
	h.api.respond(http.MethodGet, statsPath(lookupFest), http.StatusOK, windowsStats(thirtyDays()))

	code := h.run("link", "stats", "https://go.kurze-url.app/fest")

	require.Equal(t, 0, code, h.stderr.String())
	require.True(t, strings.HasPrefix(h.stdout.String(), "https://go.kurze-url.app/fest\n"), h.stdout.String())
}

func TestStatsExplainsAnAnswerOfNotFound(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	h.api.problem(http.MethodGet, statsPath(lookupShared), http.StatusNotFound, "not found")

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: Not found, or you have no access to it.\n", h.stderr.String())
}

func TestStatsNeedsExactlyOneLink(t *testing.T) {
	for _, args := range [][]string{{"link", "stats"}, {"link", "stats", "a", "b"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			h := newHarness(t)

			require.Equal(t, 2, h.run(args...))

			require.Contains(t, h.stderr.String(), "Run 'kurze-url link stats --help' for usage.")
			require.Empty(t, h.api.requests())
		})
	}
}
