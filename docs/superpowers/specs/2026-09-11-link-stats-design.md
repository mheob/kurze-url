# Link Statistics — Design

**Status:** approved 2026-09-11 **Amends:** `CLAUDE.md` (the API-surface summary gains the endpoint's settled shape; two new non-obvious constraints about what the rollup can and cannot answer; the open-items list gains the unimplemented 90-day deletion), `docs/planning/06-api-design.md` (`GET /v1/links/{link_id}/stats` gains its settled parameter set, which is narrower than the `from`/`to`/`dimension` sketch there).

The twelfth implementation spec, and the last unbuilt endpoint of the original API surface. Eleven plans have merged: a maintainer creates a team, a Verein claims a custom domain, links redirect from `go.kurze-url.app`, a link can be password-protected, and since 2026-09-10 a link's QR code can be downloaded.

`link_click_stats` has been filling up since the redirect path shipped on 2026-09-02. Nine days of rollups exist and nothing has ever read them: `apps/api/internal/db/queries/click_stats.sql` holds exactly one statement, `UpsertClickStats`, and it only writes. The QR spec closed one half of that gap by making `qr_vs_regular = 'qr'` producible at all. This spec closes the other half by making any of it readable.

## Goal

A team member can ask what a link actually did: how many clicks and how many visitors, day by day, and which browsers, systems, devices, countries, referrers, campaigns and bots those clicks came from — over a window they choose, bounded by the retention promise.

## Scope

### In scope

- `GET /v1/links/{link_id}/stats`, returning one JSON document that answers a whole dashboard.
- Two new sqlc queries against `link_click_stats`.
- The date-window parameters, their defaults, and the clamp to the retention window.
- The precise, written meaning of every number the endpoint returns — in the OpenAPI descriptions, not only in this document.
- Regenerated `openapi.json` and `packages/api-client`.

### Out of scope, and where each lands

- **The frontend.** Decided 2026-09-11: no analytics view ships here. `apps/web/src/components/ui` holds a single component (`button.tsx`), no charting library is installed, and `CLAUDE.md`'s stack table names Tremor for analytics without it ever having been added. The analytics page is the most design-dependent surface in the application, and the frontend design pass is the next item of work after this one. Building it now would mean choosing a chart library in an endpoint spec and then reworking the page a week later. The endpoint is useful on its own: the CLI is a thin HTTP client over the same API, and `short link stats` maps onto this operation with no CLI-specific endpoint.
- **The 90-day deletion.** `docs/planning/01-architecture.md:73` says "Retention: 90-day automatic deletion, confirmed." Nothing implements it. There is no scheduled prune, no `delete from link_click_stats` anywhere in the repository, and the oldest rollup row is as old as the redirect path. This spec **bounds what the endpoint will serve** to the promised 90 days, which is the half that can be settled here, and records the missing deletion as its own follow-up spec. Keeping it separate is a subsystem judgement, not a deferral: a prune job is scheduling, credentials and a token-guarded route, and `keep-alive.yml` already shows the shape it should take.
- **Hourly granularity.** `bucket_start` is a `date`. Doc 05 already decided daily for the MVP and describes hourly as a later, same-shape change.
- **A `dimension` query parameter.** Doc 06 sketched one. Decided 2026-09-11 against it — see "Why one document".
- **Cross-dimension filtering** (clicks from Chrome _in Germany_, human clicks _by country_). The rollup shape cannot answer it, at all, ever — see "What the numbers mean". No amount of API design recovers a row that was never written.
- **CSV or any export format**, and **team-level or account-level aggregates**. Neither has a requester.

## Global constraints

Inherited and not re-litigated here:

- No RLS. Every query filters by `team_id`; the check lives in Go.
- A non-member gets 404, never 403. So does a member whose `link_id` belongs to another team.
- The redirect path is the hot path. This spec adds nothing to `GET /{slug}`.
- Never store a full IP address. This spec reads aggregates only and introduces no new collection.
- Errors use Huma's default RFC 9457 `application/problem+json`.
- Conventional Commits, subject capped at 50 characters including type and scope; `pnpm format` before every commit; the Lefthook hooks are not bypassed.
- `apps/web/src/routeTree.gen.ts` is only included in a commit when the change actually requires it. This spec touches no route.

## Why one document

Doc 06 sketched `from`, `to` and `dimension` — one dimension per request. A dashboard showing a time series and eight breakdowns would then make nine calls, each repeating the same authorization resolve and the same index scan over the same rows. The endpoint answers with one document instead: totals, the daily series, and every breakdown in one response.

Two things make that safe rather than merely convenient. The window is bounded at 90 days, so the series can never exceed 90 entries. And every breakdown is capped at its top ten values with the remainder collapsed, so the six small-cardinality dimensions return everything they have while `referrer`, `country` and `utm_source` — the three an outsider can inflate by sending distinct values — cannot make the response grow. Without that cap, a response's size would be steerable from outside: `analytics.truncate` bounds each value to 128 bytes, but nothing bounds how many distinct values a link accumulates.

## The API surface

**`GET /v1/links/{link_id}/stats`** authorizes through `authz.LinkViewerScope`. Reading statistics changes nothing, so the viewer role is the right floor — the same reasoning that put the QR endpoint at viewer, and the opposite of the password endpoints, which take `LinkEditorScope` because they decide who reaches the link.

### Query parameters

```go
type LinkStatsInput struct {
	authz.LinkViewerScope
	From time.Time `query:"from" timeFormat:"2006-01-02" doc:"First day to include, as YYYY-MM-DD in UTC. Defaults to 29 days before 'to'. Clamped to the 90-day retention window."`
	To   time.Time `query:"to" timeFormat:"2006-01-02" doc:"Last day to include, as YYYY-MM-DD in UTC. Defaults to today, and a later date is treated as today."`
}
```

`timeFormat` is a Huma feature, not a convention invented here: `huma.go:293-300` reads the tag and overrides the RFC 3339 default it would otherwise use for a `time.Time` parameter, and `huma.go:1889-1893` parses the value with exactly that layout, answering a failure itself. `schema.go:585-595` turns the same tag into `format: date` in the generated OpenAPI document.

This matters for where validation lives. `time.Parse` with layout `2006-01-02` rejects both a malformed shape (`13-08-2026`) and a well-shaped impossible date (`2026-02-31`, "day out of range"), and Huma answers that 422 before the handler runs. The handler therefore does **not** re-check the format — the QR spec's `invalid_color` defect was exactly such a duplicated check, unreachable because Huma had already answered. What the handler does check is the one thing Huma cannot: the relationship between two parameters.

The window is resolved in this order, and the order is part of the specification rather than an implementation detail — two of the three steps produce a different answer if they are swapped.

1. **Refuse a self-contradictory pair.** If both parameters were supplied and `from` is later than `to`, answer 422. This is checked against what the client sent, before any clamping: clamping first could turn a coherent request into a contradiction the client never made.
2. **Resolve `to`.** Absent, it is today. Supplied, it is clamped into `[retentionFloor, today]`, where `retentionFloor` is today minus 89 days.
3. **Resolve `from`.** Absent, it is `to` minus 29 days. Then it is clamped into `[retentionFloor, to]`.

All dates are UTC, and `today` means the UTC date at the moment of the request.

| Request | Effective window |
| --- | --- |
| Neither parameter | today minus 29 days through today — a 30-day window |
| `to` after today | today; a skewed clock or a naive "end of month" default should get data, not an error |
| `from` a year ago | `retentionFloor` through the resolved `to` |
| Both entirely in the future | a single day: today |
| Both entirely before the floor | a single day: `retentionFloor` |
| `from` after `to`, both supplied | **422**, the only refusal |

The floor is measured against **today**, never against `to`. Clamping `from` to `to` minus 89 days instead would let a request for two months of 2024 walk the window backwards out of the retention period and serve rows that the retention promise says should no longer exist — which is precisely the promise this endpoint is meant to respect while the deletion job is still missing.

The clamps are silent because the response echoes the effective window. A client that asked for a year and received 90 days can see that from `from` and `to` in the body, which is the only place it could learn it without a second convention.

The `from > to` refusal is a plain `huma.Error422UnprocessableEntity("from must not be later than to")`. It deliberately carries **no** `huma.ErrorDetail{Location, Value}`. That pattern exists in this codebase to hand a client a value it must act on — the blocking link count on a domain 409, the policy token on a password rejection — and there is nothing here for a client to act on beyond the message. Adding a typed token nobody reads would be cargo cult.

### Response body

```go
type LinkStatsOutput struct {
	Body LinkStats
}

// LinkStats answers a whole analytics view in one document.
type LinkStats struct {
	LinkID           uuid.UUID           `json:"link_id"`
	From             string              `json:"from"`
	To               string              `json:"to"`
	AnalyticsEnabled bool                `json:"analytics_enabled"`
	Totals           StatCounts          `json:"totals"`
	Series           []StatDay           `json:"series"`
	Breakdowns       LinkStatsBreakdowns `json:"breakdowns"`
}

// StatCounts is the four numbers every level of this response reports.
type StatCounts struct {
	Clicks              int64 `json:"clicks"`
	UniqueVisitors      int64 `json:"unique_visitors"`
	HumanClicks         int64 `json:"human_clicks"`
	HumanUniqueVisitors int64 `json:"human_unique_visitors"`
}

// StatDay is one calendar day. StatCounts is embedded without a JSON name, so
// its four fields are inlined into this object rather than nested under one.
type StatDay struct {
	Date string `json:"date"`
	StatCounts
}

// LinkStatsBreakdowns names every dimension as its own field rather than
// keying a map. A map would generate as additionalProperties and reach
// TypeScript as an index signature, where every access is a possible
// undefined; named fields reach it as eight typed properties.
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

// StatBreakdown is one dimension's top values plus what they leave out.
type StatBreakdown struct {
	Values              []StatValue `json:"values"`
	OtherValues         int64       `json:"other_values"`
	OtherClicks         int64       `json:"other_clicks"`
	OtherUniqueVisitors int64       `json:"other_unique_visitors"`
}

type StatValue struct {
	Value          string `json:"value"`
	Clicks         int64  `json:"clicks"`
	UniqueVisitors int64  `json:"unique_visitors"`
}
```

Embedding `StatCounts` anonymously is load-bearing and verified rather than assumed: `schema.go:713-740` collects an anonymous field with no explicit JSON name into an `embedded` list and then splices that struct's own fields into the parent's, matching `encoding/json`. A day object therefore carries `date`, `clicks`, `unique_visitors`, `human_clicks` and `human_unique_visitors` side by side, not a nested `stat_counts`. The implementation asserts this against the generated `openapi.json` rather than trusting the reading.

```json
{
	"link_id": "6f1c…",
	"from": "2026-08-13",
	"to": "2026-09-11",
	"analytics_enabled": true,
	"totals": {
		"clicks": 240,
		"unique_visitors": 180,
		"human_clicks": 96,
		"human_unique_visitors": 71
	},
	"series": [
		{
			"date": "2026-08-13",
			"clicks": 4,
			"unique_visitors": 3,
			"human_clicks": 2,
			"human_unique_visitors": 2
		},
		{
			"date": "2026-08-14",
			"clicks": 0,
			"unique_visitors": 0,
			"human_clicks": 0,
			"human_unique_visitors": 0
		}
	],
	"breakdowns": {
		"browser": {
			"values": [{ "value": "Chrome", "clicks": 120, "unique_visitors": 90 }],
			"other_values": 0,
			"other_clicks": 0,
			"other_unique_visitors": 0
		},
		"country": { "values": [], "other_values": 0, "other_clicks": 0, "other_unique_visitors": 0 }
	}
}
```

Three properties of that shape are deliberate.

**`series` carries one entry per day in the window, zero days included.** The table has no row for a day nobody clicked, so the gaps are filled in Go. A time-series chart wants a point per day and a CLI table wants a line per day; making every consumer reimplement that fill — correctly, across a month boundary, in UTC — is how two clients end up disagreeing about February. The cost is bounded: at most 90 small objects.

**All eight dimensions are always present**, empty ones as an empty `values` array with zero remainders. `Values` is initialized to an empty slice, never left nil, so it marshals as `[]` rather than `null`. A client never has to distinguish "no data" from "field absent".

**`analytics_enabled` is in the response** because without it an empty document is ambiguous. `link.analytics_enabled` exists (doc 05, added 2026-09-02) and the frontend already offers it as "Count clicks for this link". When it is false the redirect path records nothing at all, so the statistics are empty for a reason the numbers cannot express. The value comes from the existing `GetLinkForAPI` query — already filtered by `team_id`, already returning the column — rather than a new one. History from before the switch was turned off is still returned; disabling counting does not erase what was counted.

## What the numbers mean

These four statements go into the operation's OpenAPI descriptions verbatim, not only into this document. Each one is a property of the storage design that no amount of API work can change, and each is the kind of thing a reader will otherwise assume the opposite of.

**`unique_visitors` over a multi-day window is the sum of daily uniques.** Deduplication happens in a Redis set keyed per link per day with a TTL of about 25 hours, against a daily-rotating salted hash of IP and User-Agent. There is deliberately no identifier that survives a day — that is the privacy design in `01-architecture.md`, not a limitation to be fixed. A person who opens the link on Monday, Wednesday and Friday contributes three unique visitors to a weekly total. The daily figures are exact; only their sum carries this meaning.

**Breakdowns cannot be filtered by bot status.** The rollup writes one row per dimension per day, never a combination: there is a `country` row and a `bot_status` row, never a `(country, bot_status)` row. So every breakdown counts all clicks, bots included, and only `totals` and `series` can carry the human split — those come from the `bot_status` rows themselves, which is why the split is exact there and impossible anywhere else. A link in a Verein's newsletter is fetched by every mail-provider link scanner it passes; `CLAUDE.md` already names those scanners as a source of redirect traffic in the rate-limit reasoning. Reporting only the total would overstate reach, and reporting only the human figure would leave the difference invisible, so both appear side by side at every level that can support them.

**Days are UTC days.** `recorder.go` buckets with `at.UTC().Truncate(24 * time.Hour)`, so that is what a day is, everywhere, for every Verein. A click at 01:30 Central European Summer Time belongs to the previous day's bucket. Re-bucketing per request is not possible against daily rollups — the information needed to move a click to another day was never stored.

**`utm_source` is sparse by design.** `Dimensions.Rows` omits the row entirely when no `utm_source` parameter was present, because emitting "unknown" for every non-campaign click would roughly double the table's row count for no analytical value. Its breakdown therefore sums to less than the click total, and the difference is non-campaign traffic, not lost data. Every other dimension sums to the total.

## The queries

Two new statements in `apps/api/internal/db/queries/click_stats.sql`, beside the existing `UpsertClickStats`. Both are covered by `link_click_stats_link_id_bucket_start_idx` on `(link_id, bucket_start desc)`.

### Series and totals

```sql
-- The daily series and, once summed in Go, the totals. One scan answers both
-- the all-clicks figures (the 'total' rows) and the human ones (the
-- bot_status = 'human' rows): every click writes both, so the two are exactly
-- comparable and no second query can disagree with the first.
--
-- The casts are not decoration. sum() returns numeric-or-null, and without an
-- explicit ::bigint sqlc generates *int64 for every one of these columns.

-- name: GetLinkClickSeries :many
select
  bucket_start,
  coalesce(sum(clicks) filter (where dimension_type = 'total'), 0)::bigint
    as clicks,
  coalesce(sum(unique_visitors) filter (where dimension_type = 'total'), 0)::bigint
    as unique_visitors,
  coalesce(sum(clicks) filter (
    where dimension_type = 'bot_status' and dimension_value = 'human'), 0)::bigint
    as human_clicks,
  coalesce(sum(unique_visitors) filter (
    where dimension_type = 'bot_status' and dimension_value = 'human'), 0)::bigint
    as human_unique_visitors
from link_click_stats
where link_id = $1
  and bucket_start between $2 and $3
  and (dimension_type = 'total'
       or (dimension_type = 'bot_status' and dimension_value = 'human'))
group by bucket_start
order by bucket_start;
```

`filter` is standard SQL and supported by every Postgres version Supabase runs. A day on which only bots clicked yields no `human` row, the filtered sum is null, and `coalesce` makes it zero — which is the correct answer, not a missing one.

### Breakdowns

```sql
-- Every dimension's top values in one statement, with what they leave out.
-- The window functions run over the already-grouped rows of the first CTE, so
-- dimension_clicks is that dimension's full total while clicks is one value's
-- share of it; Go subtracts the returned rows from the former to get the
-- remainder. Ordering by clicks desc then dimension_value makes the cut
-- deterministic when two values tie.

-- name: GetLinkClickBreakdowns :many
with per_value as (
  select
    dimension_type,
    dimension_value,
    sum(clicks)::bigint as clicks,
    sum(unique_visitors)::bigint as unique_visitors
  from link_click_stats
  where link_id = $1
    and bucket_start between $2 and $3
    and dimension_type <> 'total'
  group by dimension_type, dimension_value
),
ranked as (
  select
    dimension_type,
    dimension_value,
    clicks,
    unique_visitors,
    row_number() over (
      partition by dimension_type
      order by clicks desc, dimension_value
    ) as value_rank,
    sum(clicks) over (partition by dimension_type)::bigint
      as dimension_clicks,
    sum(unique_visitors) over (partition by dimension_type)::bigint
      as dimension_unique_visitors,
    count(*) over (partition by dimension_type)::bigint
      as dimension_values
  from per_value
)
select dimension_type, dimension_value, clicks, unique_visitors,
       dimension_clicks, dimension_unique_visitors, dimension_values
from ranked
where value_rank <= sqlc.arg(top_values)::int
order by dimension_type, value_rank;
```

`top_values` is a parameter carrying the Go constant `TopValuesPerDimension = 10`, so the number has one definition and the call site shows it. Ten is chosen against the data rather than as a round number: `browser`, `os`, `device`, `bot_status` and `qr_vs_regular` have small closed value sets and will return everything they have with a zero remainder, so the cut only ever bites on `country`, `referrer` and `utm_source` — the three that need it.

`dimension_value` is nullable in the schema. The `dimension_type <> 'total'` filter excludes the only rows that are supposed to carry a null, but no constraint enforces that pairing, so a null here is possible in principle. The handler maps it to `"unknown"`, the same spelling `analytics/dimensions.go` already uses for a value it could not determine, rather than dropping the row and silently losing its clicks.

An unrecognized `dimension_type` — a value added to the table's check constraint later without a field here — is skipped rather than dropped into a default bucket. Its clicks are then absent from the response entirely, which is honest: the endpoint does not know what that dimension means.

### Totals

Not a third query. Go sums the series rows it already has. The totals and the series therefore **cannot** disagree, which two separate aggregates over the same range could — one extra `sum` in a loop buys a guarantee that no test could otherwise give.

## Errors

| Condition | Answer |
| --- | --- |
| `from` or `to` not a valid `YYYY-MM-DD` date | 422 from Huma, before the handler |
| `from` later than `to` | 422 from the handler |
| Link does not exist, or belongs to another team, or the caller is not a member | 404 from the scope resolver |
| No rollup rows in the window | **200**, zeros and a fully gap-filled series |
| Either query fails | 500, logged at error level with the link id |

An empty result is not a 404. "Nobody has clicked this yet" is a fact about the link, and a new link is the commonest case there is.

## Rate limiting

None, deliberately. The endpoint is authenticated, viewer-scoped to a single link, and costs two index-covered aggregates over at most 90 days of rows for one link id. That is cheaper than the link list, which has no limit either. The password setter has one because it computes an Argon2id hash; the domain endpoints have theirs because they trigger DNS lookups and outbound fetches. Neither reason applies here. Recorded so that the absence reads as a decision rather than an omission.

## Testing

Go tests in `apps/api/internal/api/link_stats_test.go`, in the style of the existing handler tests, against the real database the other API tests use.

- A link with no rollup rows: 200, every total zero, `series` holding one zero entry per day of the default window, all eight breakdowns present and empty.
- Clicks across three days with a gap between them: the gap day appears with zeros, and the three figures land on the right dates.
- Bots: a day with human and bot clicks reports `clicks` above `human_clicks`, and the `bot_status` breakdown accounts for the difference.
- Totals equal the sum of the series, asserted on a fixture with several days.
- Top-N: eleven distinct referrers produce ten values plus `other_values: 1`, and `other_clicks` equals the eleventh's clicks.
- Ties: two values with equal clicks come back ordered by value, and the assertion pins the order.
- `utm_source` absent from every click leaves that breakdown empty while the others are populated.
- Defaults: no parameters yields a 30-day window ending today.
- Clamp: `from` a year ago comes back as the retention floor — today minus 89 days — in the response body.
- `to` in the future comes back as today.
- A window entirely in the future collapses to the single day "today", not to a 422.
- A window entirely before the floor collapses to the single day `retentionFloor`, and no row older than the floor appears in the response even though such rows exist in the table.
- `from` after `to`, both supplied, is a 422.
- `analytics_enabled: false` is reported as such, with historical rows still returned.
- A viewer may read; a non-member gets 404; a member of another team gets 404.
- A row for the link with `dimension_value` null on a non-total dimension is reported as `unknown` rather than dropped.
- A new row in `matrix_test.go` at `authz.RoleViewer`.
- The generated `openapi.json` shows a day object with `date` and the four count fields flat, proving the embedding behaves as described.

## Documentation this changes

- `CLAUDE.md`: the API-surface line gains the endpoint's settled parameters; two non-obvious constraints are added (the multi-day `unique_visitors` sum, and that breakdowns can never be bot-filtered); the open-items list gains the unimplemented 90-day deletion, naming `01-architecture.md:73` as the promise and `keep-alive.yml` as the shape a prune job should take.
- `docs/planning/06-api-design.md`: line 141's `from`/`to`/`dimension` sketch is replaced by the settled shape, with one sentence on why the `dimension` parameter was dropped.
- `docs/planning/05-database-schema.md`: the analytics rollup section gains a short paragraph naming what the shape cannot answer, so the next person to read it learns the limit from the schema document rather than from a surprising response.
