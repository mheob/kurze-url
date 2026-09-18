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
	Recorded         *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"recorded"`
	Totals struct {
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

// TestLinkStatsAnswers404ForADeletedLink proves that a request for a deleted
// link answers 404 rather than leaking a 500. The deletion is caught by the
// LinkViewerScope's resolve phase, not by the handler's pgx.ErrNoRows branch:
// that branch is defence in depth for a race between the scope's check and
// the handler's read, which this harness cannot stage. The branch is untested
// but necessary — a row can vanish between two queries in production, and the
// handler must handle it without crashing.
func TestLinkStatsAnswers404ForADeletedLink(t *testing.T) {
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

// TestLinkStatsReportsTheRecordedRangeOutsideTheWindow is the case the field
// exists for: the requested window is empty, and the page has to be able to
// tell "nothing was ever clicked" from "you are looking at the wrong week".
func TestLinkStatsReportsTheRecordedRangeOutsideTheWindow(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "older", "https://example.org/older")

	seedStatRow(t, f, created.ID, "2026-08-01", "total", nil, 3, 2)
	seedStatRow(t, f, created.ID, "2026-08-04", "total", nil, 5, 4)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), "from=2026-09-05&to=2026-09-11"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	require.EqualValues(t, 0, body.Totals.Clicks, "the window itself is empty")
	require.NotNil(t, body.Recorded, "rows exist outside the window")
	require.Equal(t, "2026-08-01", body.Recorded.From)
	require.Equal(t, "2026-08-04", body.Recorded.To)
}

// TestLinkStatsReportsNoRecordedRangeForALinkNobodyClicked pins the absent
// half of the contract. The page renders a different sentence for it, so "no
// rows" must not arrive as a zero-valued range.
func TestLinkStatsReportsNoRecordedRangeForALinkNobodyClicked(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "fresh", "https://example.org/fresh")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Nil(t, decode[statsBody](t, rec).Recorded)
}

// TestLinkStatsIgnoresRowsBelowTheRetentionFloor is why the floor is in the
// query rather than applied to its result. The retention job runs nightly
// while the endpoint's floor moves at midnight, so rows it can no longer serve
// survive for up to a day. Reported, they would send the reader to a window
// that comes back empty — and if they are the only rows, clamping a range's
// start up to the floor would leave that start later than its own end.
func TestLinkStatsIgnoresRowsBelowTheRetentionFloor(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "stale", "https://example.org/stale")

	// The floor on 2026-09-11 is 2026-06-14 (today minus 89). Both of these
	// are older than that and are awaiting the next retention run.
	seedStatRow(t, f, created.ID, "2026-06-10", "total", nil, 7, 5)
	seedStatRow(t, f, created.ID, "2026-06-12", "total", nil, 9, 6)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Nil(t, decode[statsBody](t, rec).Recorded,
		"rows the endpoint would not serve must not be advertised as a window to request")
}

// TestLinkStatsRecordedRangeStartsAtTheFloor is the mixed case: some rows are
// too old to serve and some are not. The range must begin at the oldest
// servable day, not at the oldest row.
func TestLinkStatsRecordedRangeStartsAtTheFloor(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "mixed", "https://example.org/mixed")

	seedStatRow(t, f, created.ID, "2026-06-10", "total", nil, 7, 5)
	seedStatRow(t, f, created.ID, "2026-06-14", "total", nil, 4, 3)
	seedStatRow(t, f, created.ID, "2026-07-02", "total", nil, 2, 1)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)
	require.NotNil(t, body.Recorded)
	require.Equal(t, "2026-06-14", body.Recorded.From, "the floor itself is servable")
	require.Equal(t, "2026-07-02", body.Recorded.To)
}

// TestRecordedRangeUsesTheWindowsOwnFloor holds the endpoint's two floors
// together. `statsWindow` clamps a requested window up to the retention floor
// and `GetLinkRecordedRange` filters by it; the two deriving that day
// separately is the drift CLAUDE.md's retention note warns about, and here it
// would surface as a range the endpoint advertises and then refuses to serve.
// A request reaching further back than retention allows must come back with a
// window that starts exactly where the oldest reportable row can sit.
func TestRecordedRangeUsesTheWindowsOwnFloor(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "floor", "https://example.org/floor")

	seedStatRow(t, f, created.ID, "2026-06-14", "total", nil, 1, 1)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), "from=2020-01-01&to=2026-09-11"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[statsBody](t, rec)

	require.Equal(t, "2026-06-14", body.From, "the window clamps to the floor")
	require.NotNil(t, body.Recorded)
	require.Equal(t, body.From, body.Recorded.From,
		"the oldest reportable row sits exactly on the window's own floor")
}

// TestLinkStatsOmitsTheRecordedKeyEntirely is the one assertion that can see
// the difference between "absent" and "null", which every other test in this
// file is blind to: decoding into a pointer leaves it nil either way. The
// distinction is the contract. Huma cannot express a nullable object, so a
// `recorded` that arrived as null would be a value the published schema says
// cannot occur — and the only thing standing between here and there is the
// `omitempty` on one struct tag.
func TestLinkStatsOmitsTheRecordedKeyEntirely(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-11")
	created := f.createLink(t, "keyless", "https://example.org/keyless")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.NotContains(t, raw, "recorded",
		"a link with no statistics must omit the key, not send null")

	// And the other direction, so this test fails if the key stops being sent
	// at all rather than only when it should be.
	seedStatRow(t, f, created.ID, "2026-09-10", "total", nil, 1, 1)
	rec = f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		statsPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	raw = nil
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Contains(t, raw, "recorded")
}
