# Link Statistics: the Recorded Range — Design

**Status:** approved 2026-09-18 **Amends:** `docs/superpowers/specs/2026-09-18-link-analytics-page-design.md` (its two empty states stop guessing and start answering), `apps/api/internal/api/link_stats.go`'s response contract (one new field).

The sixteenth implementation spec, and the smallest so far: one field on one endpoint, and what the page does with it.

## The problem

`GET /v1/links/{link_id}/stats` answers about a window. When that window is empty it cannot say why, and the page fills the gap with a guess. Two strings do it today:

- `stats.noClicksBody` — "Try a longer window."
- `stats.disabledBody` — "… If counting was only switched off recently, try a longer window — it may still hold data from before then."

Both are advice the page has no basis for. A link clicked forty days ago and a link never clicked at all produce the same seven-day response, and the page tells both readers to widen. For one of them that works; for the other it is a wasted trip through a date picker. At a window already spanning the full ninety days the advice is provably wrong — there is nothing longer to try.

The endpoint knows the answer and does not report it.

## Goal

A reader looking at an empty statistics page learns, without experimenting, whether widening the window would show them anything — and when it would, gets there in one click rather than by guessing dates.

## Scope

### In scope

- One new field on `LinkStats`: `recorded`, the range of days this link has servable rollup rows for.
- One new sqlc query behind it, and one small refactor so the retention floor has a single definition in Go.
- Regenerated `openapi.json` and `packages/api-client`.
- The two empty states on the statistics page: a jump control when there is data elsewhere, and honest copy when there is not.
- Three new translation keys in both catalogues, and two existing ones reworded.

### Not in scope

- **Any change to what `statsView` decides.** The three views stay as they are; only what the two empty ones render changes.
- **Any control when the window already has data.** A reader looking at figures does not need to be told other figures exist outside the window; that is what the range picker is for.
- **Bounding the range picker by `recorded`.** Tempting and unnecessary: a window with no data is a legitimate thing to ask for, and the empty state now explains itself.
- **Reporting anything about data older than retention.** See below — it is not knowable, and what little of it still exists is not servable either.

## What `recorded` means

`recorded` is the range of days for which this link has rollup rows **the endpoint can serve**, independent of the window that was requested.

Three properties are load-bearing and belong in the field's own documentation, because each is the opposite of a reasonable assumption.

**It is "still stored and servable", never "ever".** `POST /internal/retention` deletes rows with `bucket_start < today − 89` every night. A link clicked six months ago and never since has no rows at all, and no query against this schema can distinguish it from a link nobody ever clicked. The API must not imply otherwise, and the page's copy must not either.

**Its underlying query carries the retention floor, rather than clamping its result.** The two are not the same thing, and the difference was found while reviewing this design. The retention job runs once a day while the endpoint serves `bucket_start >= today − 89` continuously, so between a day's boundary and that night's run a link can hold rows the endpoint would no longer serve. Clamping a computed `from` up to the floor looks like the fix and is not: if _every_ row is below the floor, clamping produces `from = floor` against a `to` that is older than it — an inverted range, offered to the reader as a window that is guaranteed to come back empty. Filtering inside the query removes the case instead of patching it, and makes `recorded` mean exactly what it says: the first and last day this endpoint would actually return something for.

**It ignores the requested window entirely.** That is the whole point of the field. A caller asking for seven days learns what exists across ninety.

`recorded` is `null` exactly when the link has no servable rows.

## The API

### The floor gets one definition

`statsWindow` computes `floor := today.AddDate(0, 0, -(RetentionDays - 1))` inline. The new query needs the same day. `CLAUDE.md` already records what happens when one boundary acquires two definitions — this is that situation in miniature, so the expression moves into a `retentionFloor(now time.Time) time.Time` helper in `link_stats.go` that both `statsWindow` and the handler call. `TestRetentionCutoffIsTheStatsEndpointsFloor` continues to hold the endpoint and the deletion job together; this keeps the endpoint agreeing with itself.

### Query

A new statement in `apps/api/internal/db/queries/click_stats.sql`. It is filtered by the retention floor and deliberately **not** by the requested window:

```sql
-- name: GetLinkRecordedRange :one
select min(bucket_start) as first_day, max(bucket_start) as last_day
from link_click_stats
where link_id = sqlc.arg(link_id)
  and bucket_start >= sqlc.arg(floor_day);
```

It runs on every request to this endpoint, not only when the window came back empty. Making it conditional would put a branch in the response's meaning — `recorded` would have to be documented as "populated only when the window is empty", which no consumer could build on — and the saving is two index seeks against `link_click_stats_link_id_bucket_start_idx`. This is a dashboard endpoint; golden rule 2 is about the redirect path.

`sqlc.yaml` already maps a nullable `date` to `*time.Time` (`emit_pointers_for_null_types: true` plus an explicit `date`/`nullable` override), so both columns should arrive as `*time.Time`. The implementation verifies that against the generated code rather than assuming it: an aggregate's inferred nullability is sqlc's judgement, and if either column comes back non-pointer the query needs an explicit cast to make the nullability unambiguous.

### Response

```go
// StatRange is a pair of days, inclusive, as YYYY-MM-DD in UTC.
type StatRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}
```

and on `LinkStats`:

```go
Recorded *StatRange `json:"recorded" doc:"The first and last day this link has statistics for, whatever window was requested — null when it has none. Bounded by the same 90-day retention floor the window is, so a range reported here can always be requested. This is what is still stored, not what ever happened: rows older than the floor are deleted nightly, and a link whose clicks have all aged out is indistinguishable from one that was never clicked."`
```

A pointer with **no** `omitempty`. That combination is already proven in this codebase: `Link.ExpiresAt` is `*time.Time` with a bare `json:"expires_at"`, and `packages/api-client`'s generated `types.gen.ts` renders it as `expires_at: string | null` — required, nullable. The field is therefore always present and the frontend makes one check, which is the reason this shape was chosen over two nullable top-level days. The implementation confirms the generated type reads `recorded: StatRange | null` before building on it; if `omitempty` creeps in, the generated type becomes `recorded?: StatRange | null` and the contract has quietly become the one this design rejected.

The inner names repeat `from`/`to`. Inside `recorded` they are unambiguous, and inventing a second vocabulary for "a range of days" in the same document would be the larger cost.

## The page

`statsView` is unchanged. What changes is what the two empty views render.

| `recorded` | `empty` (counting on, no clicks in window) | `disabled` (counting off, no clicks in window) |
| --- | --- | --- |
| set | "No clicks in this window" + **jump control** | counting-off explanation + **jump control** |
| `null` | "No clicks in this window" + "nothing recorded for this link at all" | counting-off explanation, no advice |

The titles do not change. `stats.noClicksTitle` — "No clicks in this window" — stays correct in both rows; it is the body underneath it that stops guessing.

The jump control is a button labelled with the recorded range — "Show 12 June – 3 July" — that navigates with `{ from, to }` taken straight from `recorded`. The range picker already accepts arbitrary dates, so this needs no new control and no new search-parameter handling. It renders inside `EmptyContent`, where the disabled state's existing "Turn it on in the link settings" link already lives.

**The condition is `recorded !== null`, with nothing about the window in it**, and that is not a simplification for its own sake — it is an invariant worth a comment in the code. Reaching either empty view requires `totals.clicks === 0`. Every recorded click writes a `total` row along with its dimension rows (`analytics.Dimensions.Rows` always emits one), and the upsert only ever adds a positive count, so a servable row inside the requested window would have made `totals.clicks` positive and neither empty view would be on screen. In an empty view a non-null `recorded` is therefore always outside the window, and an extra overlap check would be dead code guarding an unreachable state.

### Copy

Two existing strings lose the guess they were carrying:

- `stats.noClicksBody` drops "Try a longer window."
- `stats.disabledBody` drops its second sentence, keeping the part that explains what switching counting off means.

Three new keys, in `en.json` and `de.json` both:

- a body for `empty` with data elsewhere,
- a body for `disabled` with data elsewhere,
- the jump control's label, interpolating the two formatted dates.

None of them names the retention window as a number. `stats.rangeRetentionNote` — "Statistics are kept for 90 days." — already says it, the range picker renders it on the same page, and a second copy of that figure would be a second definition of `RETENTION_DAYS` living in a translation catalogue where nothing can check it.

Dates are formatted with `formatDay` from `apps/web/src/lib/format.ts`, the same function the chart's axis and the range picker's summary use, so the button and the picker name the same day the same way.

`catalogues.test.ts` requires every key to differ between the two languages; none of the three is a protocol literal or a loanword, so none needs an `identicalByDesign` entry.

## Testing

**Go.** A table test for `retentionFloor` and its two callers agreeing. Handler tests for all three shapes of the contract: a link with rows only outside the requested window reports `recorded`; a link with no rows at all reports `null`; and — the case the design review turned up — a link whose only rows are older than the floor, left behind because the retention job has not run yet, also reports `null` rather than an inverted or unservable range.

**Frontend unit.** Both empty states rendered with and without `recorded`: the control appears only when `recorded` is set, carries the formatted range, and navigates to that window. The unreachable "recorded overlaps the window" case gets a comment, not a test — a test for a state the data model cannot produce documents nothing and outlives the reasoning behind it.

**End-to-end.** `apps/web/e2e/stats.spec.ts` gains a case: seed clicks forty days back, open the page with a seven-day window, press the button, land on figures. This is cheap now — `e2e/fixtures/seed.ts` shipped on 2026-09-18 — but `seedLinkClicks` currently hardcodes its three days at one, two and three days ago. It needs an offset parameter, which is the only change to existing test infrastructure this design requires.

## Consequences

`openapi.json` and `packages/api-client` are regenerated through the existing `pnpm generate:api`. The change is additive: a new required-but-nullable field on a response body. No request shape changes, no field is removed or renamed, and no existing consumer breaks — `apps/web` is currently the only one, since `apps/cli` holds nothing but a `.gitkeep`.

`CLAUDE.md` gains nothing: its API surface summary already describes `GET /links/{id}/stats` as "one document per link", which stays true.
