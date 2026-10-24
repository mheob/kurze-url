package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

const (
	// statsDays is the longer of the two windows `link stats` shows, and the
	// whole span it asks the API for: today and the 29 days before it. The API
	// clamps a request to 90 days, and nothing here needs more than a month.
	statsDays = 30
	// statsRecentDays is the shorter window, the last week.
	statsRecentDays = 7

	// statsIndent sets a figure's name in from the heading of its block.
	statsIndent = "  "
	// statsLabelGap is the space between the widest label and the first
	// column of numbers, and statsColumnGap the one between the two columns.
	// They are the spec's own layout, which is also what keeps the numbers
	// from reading as part of the labels.
	statsLabelGap  = 5
	statsColumnGap = 3
)

// statsOptions are the flags of `link stats`.
type statsOptions struct {
	domain, team string
}

// newLinkStatsCmd shows how often a link was opened.
func newLinkStatsCmd(d *Deps) *cobra.Command {
	var o statsOptions
	cmd := &cobra.Command{
		Use:   "stats <slug|short-url>",
		Short: "Show a link's clicks for the last 7 and 30 days",
		Long: "Shows the clicks and visitors of a link for the last 7 and the last 30 days, first the\n" +
			"human ones and then everything, bots such as the link scanners of mail providers\n" +
			"included. The link is a slug in your default team, or a short URL, which is looked up\n" +
			"in all your teams:\n\n" +
			"  kurze-url link stats sommerfest\n" +
			"  kurze-url link stats https://go.kurze-url.app/sommerfest\n\n" +
			"Visitors are counted per day and added up, so someone who comes back on three days\n" +
			"counts three times.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.linkStats(cmd.Context(), args[0], o)
		},
	}
	addLookupFlags(cmd, &o.domain, &o.team)
	return cmd
}

// linkStats prints a link's figures for the last 7 and 30 days.
//
// It makes one request for the 30 days that end today, in UTC like the API's
// own day boundaries, so a person in Berlin after midnight does not ask for a
// day that has not begun for the API yet. The 30-day column is the answer's
// totals, which the API computes, and the 7-day column is added up here from
// the newest seven days of the series.
//
// A link whose counting is switched off says so instead of printing zeros: all
// zeros reads as "nobody clicked", and that is not what it means.
func (a *App) linkStats(ctx context.Context, arg string, o statsOptions) error {
	link, _, err := a.findLink(ctx, arg, o.domain, o.team)
	if err != nil {
		return err
	}

	to := a.d.Now().UTC()
	from := to.AddDate(0, 0, -(statsDays - 1))
	stats, err := a.API.LinkStats(ctx, link.ID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return err
	}
	if !stats.AnalyticsEnabled {
		return a.result(a.Cat.T("stats.disabled"))
	}
	// The short URL is the server's text, cleaned for the terminal
	// (output.Clean says why).
	return a.result(renderStats(a.Cat, output.Clean(link.ShortURL), lastDays(stats.Series, statsRecentDays), stats.Totals))
}

// lastDays adds up the newest n days of a series.
//
// It sorts first. The API serves the days in order, but "the last seven" is
// meant by date, and a series that arrived shuffled must not turn it into
// seven arbitrary days. The dates are YYYY-MM-DD, which sort as text in
// calendar order. A series shorter than n adds up whatever there is, which is
// what a link made three days ago has.
func lastDays(series []apiclient.StatDay, n int) apiclient.StatCounts {
	days := slices.Clone(series)
	slices.SortFunc(days, func(a, b apiclient.StatDay) int { return strings.Compare(a.Date, b.Date) })
	if len(days) > n {
		days = days[len(days)-n:]
	}

	var sum apiclient.StatCounts
	for _, day := range days {
		sum.Clicks += day.Clicks
		sum.UniqueVisitors += day.UniqueVisitors
		sum.HumanClicks += day.HumanClicks
		sum.HumanUniqueVisitors += day.HumanUniqueVisitors
	}
	return sum
}

// statsRow is one line of the figures: a label and its two numbers, already
// written out.
type statsRow struct {
	label, recent, month string
}

// renderStats lays the figures out the way the spec draws them: the short URL,
// a blank line, the two column headings, and two blocks, the human figures and
// everything, each with its clicks and visitors. The numbers are right-aligned
// under their headings, and a number wider than its heading widens the column,
// heading included, so the right edges always line up. A footnote ends it,
// because the visitors figure is the one that does not mean what its name says.
//
// The text is one string without a final newline, for a.result to print.
func renderStats(c *i18n.Catalog, shortURL string, recent, month apiclient.StatCounts) string {
	humans, all := c.T("stats.humans"), c.T("stats.all")
	clicks, visitors := statsIndent+c.T("stats.clicks"), statsIndent+c.T("stats.visitors")
	recentHeading, monthHeading := c.T("stats.days7"), c.T("stats.days30")

	rows := []statsRow{
		{label: humans},
		{clicks, num(recent.HumanClicks), num(month.HumanClicks)},
		{visitors, num(recent.HumanUniqueVisitors), num(month.HumanUniqueVisitors)},
		{label: all},
		{clicks, num(recent.Clicks), num(month.Clicks)},
		{visitors, num(recent.UniqueVisitors), num(month.UniqueVisitors)},
	}

	labelWidth, recentWidth, monthWidth := 0, width(recentHeading), width(monthHeading)
	for _, r := range rows {
		labelWidth = max(labelWidth, width(r.label))
		recentWidth = max(recentWidth, width(r.recent))
		monthWidth = max(monthWidth, width(r.month))
	}
	labelWidth += statsLabelGap

	gap := strings.Repeat(" ", statsColumnGap)
	line := func(label, recent, month string) string {
		return padRight(label, labelWidth) + padLeft(recent, recentWidth) + gap + padLeft(month, monthWidth)
	}

	lines := []string{shortURL, "", line("", recentHeading, monthHeading)}
	for _, r := range rows {
		if r.recent == "" {
			// A block's heading has no numbers, and no padding after it.
			lines = append(lines, r.label)
			continue
		}
		lines = append(lines, line(r.label, r.recent, r.month))
	}
	lines = append(lines, "", c.T("stats.footnote"))
	return strings.Join(lines, "\n")
}

// num writes a count as plain digits. A thousands separator would be one more
// thing that differs between English and German, and between a terminal and a
// script reading the output.
func num(n int64) string {
	return strconv.FormatInt(n, 10)
}

// width is the number of characters a text takes on the screen. Runes, not
// bytes, so a German label is as wide as it looks.
func width(s string) int {
	return utf8.RuneCountInString(s)
}

// padRight fills s with spaces on the right up to w characters.
func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(w-width(s), 0))
}

// padLeft fills s with spaces on the left up to w characters, which is what
// right-aligns it.
func padLeft(s string, w int) string {
	return strings.Repeat(" ", max(w-width(s), 0)) + s
}
