package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// statsBody mirrors the endpoint's response. It is declared here rather than
// imported from the api package so a rename of a JSON tag fails a test instead
// of silently following the change.
type statsBody struct {
	LinkID           string `json:"link_id"`
	From             string `json:"from"`
	To               string `json:"to"`
	AnalyticsEnabled bool   `json:"analytics_enabled"`
	Totals           struct {
		Clicks              int64 `json:"clicks"`
		UniqueVisitors      int64 `json:"unique_visitors"`
		HumanClicks         int64 `json:"human_clicks"`
		HumanUniqueVisitors int64 `json:"human_unique_visitors"`
	} `json:"totals"`
	Series []struct {
		Date                string `json:"date"`
		Clicks              int64  `json:"clicks"`
		UniqueVisitors      int64  `json:"unique_visitors"`
		HumanClicks         int64  `json:"human_clicks"`
		HumanUniqueVisitors int64  `json:"human_unique_visitors"`
	} `json:"series"`
	Breakdowns map[string]struct {
		Values []struct {
			Value          string `json:"value"`
			Clicks         int64  `json:"clicks"`
			UniqueVisitors int64  `json:"unique_visitors"`
		} `json:"values"`
		OtherValues         int64 `json:"other_values"`
		OtherClicks         int64 `json:"other_clicks"`
		OtherUniqueVisitors int64 `json:"other_unique_visitors"`
	} `json:"breakdowns"`
}

func statsPath(linkID, query string) string {
	if query == "" {
		return "/v1/links/" + linkID + "/stats"
	}
	return "/v1/links/" + linkID + "/stats?" + query
}

// pinToday fixes the clock the handler reads, so every date assertion below is
// about the resolver rather than about the day the suite happens to run.
func pinToday(t *testing.T, f *tenancyFixture, date string) {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", date)
	require.NoError(t, err)
	// An afternoon, not a midnight: the handler must reduce the instant to a
	// calendar day, and a bug that keeps the time of day would survive a
	// midnight input.
	f.deps.Now = func() time.Time { return parsed.Add(14 * time.Hour) }
	f.rebuildRouter()
}

func seedStatRow(
	t *testing.T, f *tenancyFixture, linkID uuid.UUID,
	day, dimensionType string, dimensionValue any, clicks, unique int64,
) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(),
		`insert into link_click_stats
		   (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
		 values ($1, $2::date, $3, $4, $5, $6)`,
		linkID, day, dimensionType, dimensionValue, clicks, unique)
	require.NoError(t, err)
}

// TestLinkStatsAnswersZerosForALinkNobodyClicked is the commonest case there
// is — a link created a minute ago. It must be a 200 with a full window, not a
// 404 and not an empty document: "nobody has clicked this yet" is a fact about
// the link, not a missing resource.
func TestLinkStatsAnswersZerosForALinkNobodyClicked(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "fresh", "https://example.org/fresh")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)
	require.Equal(t, "2026-08-13", body.From)
	require.Equal(t, "2026-09-11", body.To)
	require.Len(t, body.Series, 30, "a 30-day default window, inclusive")
	require.EqualValues(t, 0, body.Totals.Clicks)
	require.True(t, body.AnalyticsEnabled)
	require.Len(t, body.Breakdowns, 8)
	for _, name := range []string{
		"browser", "os", "device", "country",
		"referrer", "utm_source", "bot_status", "qr_vs_regular",
	} {
		breakdown, ok := body.Breakdowns[name]
		require.True(t, ok, "%s must always be present", name)
		require.NotNil(t, breakdown.Values, "%s must be [] and never null", name)
	}
}

// TestLinkStatsReportsTheBotSplitAndTheBreakdowns walks one realistic day
// through the whole path: two queries, the gap fill, the totals sum.
func TestLinkStatsReportsTheBotSplitAndTheBreakdowns(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "newsletter", "https://example.org/newsletter")

	seedStatRow(t, f, created.ID, "2026-09-10", "total", nil, 20, 14)
	seedStatRow(t, f, created.ID, "2026-09-10", "bot_status", "human", 8, 6)
	seedStatRow(t, f, created.ID, "2026-09-10", "bot_status", "bot", 12, 8)
	seedStatRow(t, f, created.ID, "2026-09-10", "country", "DE", 18, 13)
	seedStatRow(t, f, created.ID, "2026-09-10", "qr_vs_regular", "qr", 5, 4)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), "from=2026-09-09&to=2026-09-11"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	require.EqualValues(t, 20, body.Totals.Clicks)
	require.EqualValues(t, 8, body.Totals.HumanClicks)
	require.EqualValues(t, 6, body.Totals.HumanUniqueVisitors)

	require.Len(t, body.Series, 3)
	require.Equal(t, "2026-09-09", body.Series[0].Date)
	require.EqualValues(t, 0, body.Series[0].Clicks)
	require.Equal(t, "2026-09-10", body.Series[1].Date)
	require.EqualValues(t, 20, body.Series[1].Clicks)
	require.EqualValues(t, 8, body.Series[1].HumanClicks)

	// The country breakdown counts all 18 clicks it saw, bots included: the
	// rollup has no (country, bot_status) row, so no breakdown can ever be
	// bot-filtered.
	require.Len(t, body.Breakdowns["country"].Values, 1)
	require.Equal(t, "DE", body.Breakdowns["country"].Values[0].Value)
	require.EqualValues(t, 18, body.Breakdowns["country"].Values[0].Clicks)
	require.Equal(t, "qr", body.Breakdowns["qr_vs_regular"].Values[0].Value)

	// The bot_status breakdown is the other side of the human/total split:
	// it must carry both values, and they must sum to the dimension's clicks
	// — the same 20 the "total" row and body.Totals.Clicks report.
	botStatus := body.Breakdowns["bot_status"]
	require.Len(t, botStatus.Values, 2)
	var humanClicks, botClicks int64
	for _, value := range botStatus.Values {
		switch value.Value {
		case "human":
			humanClicks = value.Clicks
		case "bot":
			botClicks = value.Clicks
		}
	}
	require.EqualValues(t, 8, humanClicks)
	require.EqualValues(t, 12, botClicks)
	require.EqualValues(t, 20, humanClicks+botClicks, "the split must sum to the dimension's clicks")
}

// TestLinkStatsLeavesAnAbsentDimensionEmpty is distinct from the
// nobody-clicked-yet case: here several dimensions do have rows, and
// utm_source specifically does not, because no click carried that parameter.
// Its breakdown must still come back empty, not omitted or defaulted from
// some other dimension's figures.
func TestLinkStatsLeavesAnAbsentDimensionEmpty(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "no-utm", "https://example.org/no-utm")

	seedStatRow(t, f, created.ID, "2026-09-10", "total", nil, 10, 7)
	seedStatRow(t, f, created.ID, "2026-09-10", "browser", "firefox", 10, 7)
	seedStatRow(t, f, created.ID, "2026-09-10", "country", "DE", 10, 7)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	require.Empty(t, body.Breakdowns["utm_source"].Values,
		"no click carried a utm_source, so that breakdown must be empty")
	require.NotEmpty(t, body.Breakdowns["browser"].Values, "browser was seeded and must be populated")
	require.NotEmpty(t, body.Breakdowns["country"].Values, "country was seeded and must be populated")
}

// TestLinkStatsTotalsEqualTheSeries pins the guarantee that made the totals a
// Go sum rather than a third query.
func TestLinkStatsTotalsEqualTheSeries(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "sum", "https://example.org/sum")

	for _, day := range []string{"2026-09-05", "2026-09-07", "2026-09-11"} {
		seedStatRow(t, f, created.ID, day, "total", nil, 3, 2)
		seedStatRow(t, f, created.ID, day, "bot_status", "human", 1, 1)
	}

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	var clicks, unique, human, humanUnique int64
	for _, entry := range body.Series {
		clicks += entry.Clicks
		unique += entry.UniqueVisitors
		human += entry.HumanClicks
		humanUnique += entry.HumanUniqueVisitors
	}
	require.Equal(t, clicks, body.Totals.Clicks)
	require.Equal(t, unique, body.Totals.UniqueVisitors)
	require.Equal(t, human, body.Totals.HumanClicks)
	require.Equal(t, humanUnique, body.Totals.HumanUniqueVisitors)
	require.EqualValues(t, 9, body.Totals.Clicks)
}

// TestLinkStatsCapsTheTopValuesAndReportsTheRest proves the cap end to end,
// not only at the query.
func TestLinkStatsCapsTheTopValuesAndReportsTheRest(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "many", "https://example.org/many")

	for i := 1; i <= 11; i++ {
		seedStatRow(t, f, created.ID, "2026-09-10", "referrer",
			fmt.Sprintf("ref%02d.example", i), int64(i), int64(i))
	}

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	referrer := body.Breakdowns["referrer"]
	require.Len(t, referrer.Values, 10)
	require.Equal(t, "ref11.example", referrer.Values[0].Value)
	require.EqualValues(t, 1, referrer.OtherValues)
	require.EqualValues(t, 1, referrer.OtherClicks)
}

// TestLinkStatsClampsTheWindowToRetention is the privacy-relevant assertion:
// rows older than the floor are in the table — nothing deletes them yet — and
// must not be readable through this endpoint.
func TestLinkStatsClampsTheWindowToRetention(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "old", "https://example.org/old")

	seedStatRow(t, f, created.ID, "2026-01-02", "total", nil, 500, 500)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), "from=2025-09-11&to=2026-09-11"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)
	require.Equal(t, "2026-06-14", body.From, "clamped to today minus 89 days")
	require.Len(t, body.Series, 90)
	require.EqualValues(t, 0, body.Totals.Clicks, "the January row is outside the floor")
}

// TestLinkStatsRefusesAnInvertedWindow is the endpoint's only refusal.
func TestLinkStatsRefusesAnInvertedWindow(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "inverted", "https://example.org/inverted")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), "from=2026-09-05&to=2026-09-01"), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}

// TestLinkStatsRefusesAMalformedDateBeforeTheHandler pins where that
// validation lives. Huma parses a timeFormat parameter itself and answers the
// failure, so the handler must not re-check the format — a duplicated check
// there would be unreachable code.
func TestLinkStatsRefusesAMalformedDateBeforeTheHandler(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "malformed", "https://example.org/malformed")

	for _, query := range []string{"from=13-08-2026", "from=2026-02-31", "to=yesterday"} {
		rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
			statsPath(created.ID.String(), query), nil)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"%s — body: %s", query, rec.Body.String())
	}
}

// TestLinkStatsReportsDisabledAnalytics keeps an empty document from being
// ambiguous: with counting switched off the redirect path records nothing at
// all, which the numbers alone cannot express.
func TestLinkStatsReportsDisabledAnalytics(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "quiet", "https://example.org/quiet")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/links/"+created.ID.String(), map[string]any{"analytics_enabled": false})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// History recorded before the switch was flipped is still reported:
	// disabling counting does not erase what was counted.
	seedStatRow(t, f, created.ID, "2026-09-10", "total", nil, 4, 4)

	rec = f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)
	require.False(t, body.AnalyticsEnabled)
	require.EqualValues(t, 4, body.Totals.Clicks)
}

// TestLinkStatsHidesAnotherTeamsLink is the tenancy assertion. A stranger gets
// 404, never 403: a 403 would confirm the link exists.
func TestLinkStatsHidesAnotherTeamsLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "private", "https://example.org/private")

	rec := f.do(t, f.stranger, http.MethodGet, statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// TestLinkStatsAnswers404ForALinkDeletedAfterAuthorization drives the race
// between the authorization scope's resolve and this handler's own read: a
// link deleted in between must answer 404, like every sibling handler, not
// the generic 500 an unmapped pgx.ErrNoRows would fall into. Deleting through
// the API would also remove the scope's own resolve, proving nothing — a
// direct pool delete is what reproduces the window.
func TestLinkStatsAnswers404ForALinkDeletedAfterAuthorization(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "vanishing", "https://example.org/vanishing")

	_, err := f.pool.Exec(context.Background(), `delete from link where id = $1`, created.ID)
	require.NoError(t, err)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// TestLinkStatsSchemaInlinesTheDayCounts pins the one piece of Huma behaviour
// this response depends on. StatCounts is embedded anonymously so its four
// fields are spliced into the day object; if a future Huma version nested them
// instead, every client would break silently on a field that moved.
func TestLinkStatsSchemaInlinesTheDayCounts(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "schema", "https://example.org/schema")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var raw struct {
		Series []map[string]any `json:"series"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.NotEmpty(t, raw.Series)
	for _, key := range []string{
		"date", "clicks", "unique_visitors", "human_clicks", "human_unique_visitors",
	} {
		require.Contains(t, raw.Series[0], key,
			"a day object carries its counts flat, not nested")
	}
}

// TestLinkStatsOpenAPISchemaInlinesTheDayCounts is
// TestLinkStatsSchemaInlinesTheDayCounts's stronger sibling, asserted against
// the generated apps/api/openapi.json rather than the runtime response.
// packages/api-client is generated from that file, not from encoding/json's
// behaviour, so the two can drift: a future Huma version could nest
// StatCounts in the *schema* while encoding/json kept inlining it at runtime.
// The runtime test above would keep passing and the drift check would merely
// ask for a regeneration — this is what would actually catch a generated
// TypeScript client declaring a field that never arrives.
func TestLinkStatsOpenAPISchemaInlinesTheDayCounts(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.json")
	require.NoError(t, err, "run `go run ./cmd/openapi` to (re)generate it")

	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	statDay, ok := doc.Components.Schemas["StatDay"]
	require.True(t, ok, "StatDay must be a named schema in the OpenAPI document")
	for _, key := range []string{
		"date", "clicks", "unique_visitors", "human_clicks", "human_unique_visitors",
	} {
		require.Contains(t, statDay.Properties, key,
			"the generated schema must inline StatCounts' fields as direct properties, not nest them")
	}
}
