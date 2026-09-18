# Link Statistics: the Recorded Range — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /v1/links/{link_id}/stats` reports the range of days it has data for, and the statistics page uses that to replace two guesses with an answer and a one-click jump.

**Architecture:** One new sqlc query, filtered by the retention floor and not by the requested window, feeds one new nullable field on the existing response. The frontend renders a button in the two empty views when that field is set, and honest copy when it is not. Nothing about `statsView`'s three-way decision changes.

**Tech Stack:** Go (chi + Huma + sqlc + pgx), Postgres, `@hey-api/openapi-ts`, TanStack Start/Router/Query, react-i18next, Vitest + Testing Library, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-18-link-stats-recorded-range-design.md`

## Global Constraints

- The new field is `Recorded *StatRange` with the tag `json:"recorded,omitempty"`, which reaches TypeScript as `recorded?: StatRange`. Huma cannot express a nullable object — it panics on `nullable:"true"` over an object ref — so absence carries "no statistics" instead of null. What the contract must never become is `recorded?: StatRange | null`: optional _and_ nullable is the two-check form this design rejects.
- The new query filters by the **retention floor**, never by the requested window. Clamping a computed result instead is the rejected approach — it produces an inverted range when every row is below the floor.
- `recorded` is absent exactly when the link has no rows at or above the floor.
- The retention floor has one definition in Go after this change: `retentionFloor(now time.Time) time.Time`. `statsWindow` and the handler both call it. Never re-derive `today.AddDate(0, 0, -(RetentionDays - 1))` anywhere else.
- The button's render condition is `recorded !== undefined` and nothing else. An overlap check against the requested window is dead code; the reason belongs in a comment, not in a test.
- No new translation string names the retention window as a number. `stats.rangeRetentionNote` already says "90 days" and the range picker renders it on the same page.
- **Commits** follow Conventional Commits, max 50 characters including type and scope. No co-author line and no generator footer, ever.
- **All git writes go through GitButler (`but`)** — never `git add`, `git commit`, `git checkout`. The lane for this work is `feat/stats-recorded-range`, which already carries the spec commit.
- Run `pnpm format` before each commit. Never bypass Lefthook.
- `apps/web/src/routeTree.gen.ts` is not touched by this plan — no route is added.

---

### Task 1: The API reports `recorded`

**Files:**

- Modify: `apps/api/internal/db/queries/click_stats.sql` (append one statement)
- Modify: `apps/api/internal/api/link_stats.go`
- Test: `apps/api/internal/api/link_stats_test.go`
- Generated (do not hand-edit): `apps/api/internal/db/click_stats.sql.go`, `apps/api/internal/db/models.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `api.StatRange{From, To string}`, the field `LinkStats.Recorded *StatRange`, and `retentionFloor(now time.Time) time.Time`. Task 2 regenerates the client from the resulting schema.

Working directory for every Go command below is `apps/api`.

- [ ] **Step 1: Extend the test's response mirror**

`statsBody` in `apps/api/internal/api/link_stats_test.go` deliberately re-declares the response instead of importing it, so a renamed JSON tag fails a test. Add the new field to it, after `AnalyticsEnabled`:

```go
	Recorded *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"recorded"`
```

- [ ] **Step 2: Write the three failing tests**

Append to `apps/api/internal/api/link_stats_test.go`. `pinToday`, `seedStatRow`, `statsPath`, `newTenancyFixture`, `f.createLink`, `f.do` and `decode` already exist in this file — read them once before writing.

```go
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

// TestLinkStatsReportsNoRecordedRangeForALinkNobodyClicked pins the null half
// of the contract. The page renders a different sentence for it, so "no rows"
// must not arrive as a zero-valued range.
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
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/api/ -run TestLinkStatsReportsTheRecordedRange -count=1`

Expected: FAIL. `body.Recorded` is nil because nothing populates it yet.

These tests need a database. `supabase start` must be running, the same way `go test ./...` needs it in CI (`.github/workflows/ci-api.yml`).

- [ ] **Step 4: Add the query**

Append to `apps/api/internal/db/queries/click_stats.sql`:

```sql
-- The window a link has data for, which is deliberately not the window the
-- caller asked about: the stats endpoint reports it so a page looking at an
-- empty week can tell "nothing was ever clicked" from "wrong week".
--
-- Filtered by the retention floor, and that filter is load-bearing rather than
-- tidy. The retention job runs nightly while the endpoint's floor moves at
-- midnight, so rows the endpoint would refuse to serve can survive for the
-- better part of a day. Reported, they name a window that comes back empty.
-- Applying the floor to the result instead of inside the query has a worse
-- failure: when every row is below the floor, raising the range's start to the
-- floor leaves it later than the range's own end.

-- name: GetLinkRecordedRange :one
select min(bucket_start) as first_day, max(bucket_start) as last_day
from link_click_stats
where link_id = sqlc.arg(link_id)
  and bucket_start >= sqlc.arg(floor_day);
```

- [ ] **Step 5: Generate and read what came out**

Run: `sqlc generate` (from `apps/api`)

Then read the generated `GetLinkRecordedRangeRow` in `apps/api/internal/db/click_stats.sql.go`.

**Expected:** both fields typed `*time.Time`. `sqlc.yaml` maps a nullable `date` to `*time.Time` through `emit_pointers_for_null_types: true` plus an explicit `date`/`nullable` override.

**If either field is not a pointer**, sqlc did not infer the aggregate as nullable. Do not work around it in Go with a zero-value check — a zero `time.Time` is a real date as far as `Format` is concerned. Change the query to make the nullability explicit and regenerate:

```sql
select min(bucket_start)::date as first_day, max(bucket_start)::date as last_day
```

If that still yields non-pointers, stop and report it rather than guessing: the contract's whole point is that "no data" and "a date" are different values.

- [ ] **Step 6: Extract the floor helper**

In `apps/api/internal/api/link_stats.go`, add above `statsWindow`:

```go
// retentionFloor is the oldest day this endpoint will serve, and now the only
// place that day is computed. statsWindow clamps the requested window up to it
// and GetLinkRecordedRange filters by it; two derivations of one boundary is
// the drift CLAUDE.md's retention note exists to prevent, and here it would
// show as a reported range the endpoint refuses to serve.
func retentionFloor(now time.Time) time.Time {
	return dayOf(now).AddDate(0, 0, -(RetentionDays - 1))
}
```

and replace the inline derivation inside `statsWindow`:

```go
	today := dayOf(now)
	floor := retentionFloor(now)
```

- [ ] **Step 7: Add the response type and the field**

In `apps/api/internal/api/link_stats.go`, add above `LinkStats`:

```go
// StatRange is a pair of days, inclusive, as YYYY-MM-DD in UTC.
type StatRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}
```

and add to `LinkStats`, after `AnalyticsEnabled`:

```go
	// Absent rather than null when the link has no statistics, and that is a
	// concession to the schema generator rather than a preference. Huma refuses
	// a nullable object outright — `nullable:"true"` on a field whose ref is an
	// object panics, and automatic nullability covers only scalars — so a
	// required `$ref` answered with `null` would document a shape the endpoint
	// does not send. `omitempty` on a pointer makes Huma mark the property
	// optional and non-nullable, which is what the handler actually does: Go
	// omits the key. A reader still makes one check and still cannot meet a
	// half-populated range.
	Recorded *StatRange `json:"recorded,omitempty" doc:"The first and last day this link has statistics for, whatever window was requested — null when it has none. Bounded by the same 90-day retention floor the window is, so a range reported here can always be requested. This is what is still stored, not what ever happened: rows older than the floor are deleted nightly, and a link whose clicks have all aged out is indistinguishable from one that was never clicked."`
```

- [ ] **Step 8: Call the query from the handler**

In `getLinkStats`, after the `GetLinkClickBreakdowns` call and before `buildSeries`:

```go
	recordedRow, err := d.Queries.GetLinkRecordedRange(ctx, db.GetLinkRecordedRangeParams{
		LinkID: link.ID, FloorDay: retentionFloor(d.now()),
	})
	if err != nil {
		d.Log.Error("read recorded range", "error", err, "link_id", link.ID)
		return nil, huma.Error500InternalServerError("could not read the statistics")
	}

	// Both columns are null together — they come from one aggregate over the
	// same rows — but both are checked, because a half-populated range would
	// format a zero time.Time into a real-looking date.
	var recorded *StatRange
	if recordedRow.FirstDay != nil && recordedRow.LastDay != nil {
		recorded = &StatRange{
			From: recordedRow.FirstDay.UTC().Format(dayLayout),
			To:   recordedRow.LastDay.UTC().Format(dayLayout),
		}
	}
```

and add `Recorded: recorded,` to the returned `LinkStats` literal, after `AnalyticsEnabled`.

- [ ] **Step 8b: Pin the floor's two callers to each other**

The spec asks for this explicitly, and it is the cheap half of what `TestRetentionCutoffIsTheStatsEndpointsFloor` already does for the deletion job. Append to `apps/api/internal/api/link_stats_test.go`:

```go
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
```

- [ ] **Step 9: Run the new tests**

Run: `go test ./internal/api/ -run 'TestLinkStats.*Recorded|TestLinkStatsIgnoresRows|TestRecordedRangeUses' -count=1 -v`

Expected: PASS, all five.

- [ ] **Step 10: Run the whole Go gate**

Run, from `apps/api`:

```bash
go vet ./... && gofmt -l . && go test ./... -count=1
```

Expected: `go vet` silent, `gofmt -l` prints nothing, every test passes. The existing stats tests must still pass — `statsWindow`'s behaviour is unchanged by the refactor, and `TestRetentionCutoffIsTheStatsEndpointsFloor` is the one that would notice if it were not.

- [ ] **Step 11: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "feat(api): report the recorded stats range"
```

---

### Task 2: Regenerate the client and repair the fixtures

**Files:**

- Generated: `apps/api/openapi.json`, `packages/api-client/src/generated/types.gen.ts`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.test.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.a11y.test.tsx`
- Modify: `apps/web/src/server/links.test.ts`

**Interfaces:**

- Consumes: `LinkStats.Recorded` from Task 1.
- Produces: the TypeScript types `StatRange` and `LinkStats['recorded']`, which Tasks 4 and 5 import from `@kurze-url/api-client`.

- [ ] **Step 1: Regenerate**

Run from the repository root: `pnpm generate:api`

This runs `go run ./cmd/openapi > openapi.json`, regenerates `packages/api-client`, and formats.

- [ ] **Step 2: Verify the generated contract, do not assume it**

Run: `grep -n "recorded" packages/api-client/src/generated/types.gen.ts`

Expected: a line reading `recorded?: StatRange;` — optional, and **not** followed by `| null`.

**A `recorded?: StatRange | null` means the contract is the one the spec rejects**, because optional and nullable together is the two-check form. Stop and report it rather than building on it.

Also confirm `StatRange` itself is exported:

Run: `grep -n "export type StatRange" packages/api-client/src/generated/types.gen.ts`

- [ ] **Step 3: Run typecheck**

Run from the repository root: `pnpm run typecheck`

Expected: PASS, with no fixture changes anywhere. This follows from the ruling that made the field optional, and it is worth confirming rather than assuming: an optional property costs existing `LinkStats` literals nothing, where a required one would have broken every fixture in three files. If typecheck fails here, the generated field is not optional and Step 2's check was misread.

- [ ] **Step 4: Run the gate**

Run from the repository root:

```bash
pnpm run typecheck && pnpm run lint && pnpm run test
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "build(api-client): regenerate for recorded"
```

---

### Task 3: The copy

**Files:**

- Modify: `apps/web/src/i18n/locales/en.json`
- Modify: `apps/web/src/i18n/locales/de.json`
- Test: `apps/web/src/i18n/catalogues.test.ts` (existing, not modified)

**Interfaces:**

- Consumes: nothing.
- Produces: the keys `stats.showRecorded`, `stats.noClicksElsewhere`, `stats.disabledElsewhere`, used by Tasks 4 and 5.

Keys inside each `stats` object are sorted alphabetically; keep them that way or `pnpm run lint` will complain about the file's key order.

- [ ] **Step 1: Reword the two guesses and add the three new keys — English**

In `apps/web/src/i18n/locales/en.json`, inside `stats`:

Replace the value of `disabledBody` with:

```
"Nothing is recorded while it is off, so this is not the same as a link nobody clicked."
```

Replace the value of `noClicksBody` with:

```
"Nothing has been recorded for this link."
```

Add:

```json
"disabledElsewhere": "Counting is off now, but statistics recorded before it was switched off are still here.",
"noClicksElsewhere": "This link has statistics outside the window you are looking at.",
"showRecorded": "Show {{from}} – {{to}}",
```

- [ ] **Step 2: The same in German**

In `apps/web/src/i18n/locales/de.json`, inside `stats`:

Replace the value of `disabledBody` with:

```
"Solange die Zählung aus ist, wird nichts erfasst — das ist etwas anderes als ein Link, den niemand geklickt hat."
```

Replace the value of `noClicksBody` with:

```
"Für diesen Link wurde nichts erfasst."
```

Add:

```json
"disabledElsewhere": "Die Zählung ist jetzt aus, aber davor erfasste Zahlen sind noch da.",
"noClicksElsewhere": "Für diesen Link gibt es Zahlen außerhalb des gewählten Zeitraums.",
"showRecorded": "{{from}} – {{to}} anzeigen",
```

The en dash in `showRecorded` is the same character `stats.rangeSummary` uses, so both renderings of a date range look alike.

- [ ] **Step 3: Run the catalogue tests**

Run: `pnpm --filter=web exec vitest run src/i18n/catalogues.test.ts`

Expected: PASS. Both catalogues have the same keys, and none of the three new values is identical across languages, so none needs an `identicalByDesign` entry.

- [ ] **Step 4: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "feat(web): copy for the recorded range"
```

---

### Task 4: The jump control

**Files:**

- Create: `apps/web/src/components/stat-recorded-jump.tsx`
- Create: `apps/web/src/components/stat-recorded-jump.test.tsx`
- Create: `apps/web/src/components/stat-recorded-jump.stories.tsx`

**Interfaces:**

- Consumes: `StatRange` from `@kurze-url/api-client` (Task 2), `stats.showRecorded` (Task 3), `formatDay` from `../lib/format`, `StatsWindow` from `../lib/stats-window`.
- Produces: `StatRecordedJump` with props `{ language: Language; onSelect: (window: StatsWindow) => void; recorded: StatRange }`. Task 5 renders it.

Its own component rather than JSX inside the page: it needs no router, so its test needs nothing but an `I18nextProvider`, while the page body's own tests have to build a memory router for the `RouterLink` it contains.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/components/stat-recorded-jump.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { StatRecordedJump } from './stat-recorded-jump.tsx';

/**
 * Same pattern as `stat-breakdown-card.test.tsx`: any component that calls
 * `useTranslation` needs an `I18nextProvider` in its tree, or `t(...)` throws
 * looking up `react-i18next`'s default context.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type; not a declaration this file can edit.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

const RECORDED = { from: '2026-06-12', to: '2026-07-03' };

describe(StatRecordedJump, () => {
	it('names the range it would show, formatted for the language', () => {
		renderWithI18n(<StatRecordedJump language="en" onSelect={vi.fn()} recorded={RECORDED} />);

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
	});

	it('formats the same range in German', () => {
		renderWithI18n(<StatRecordedJump language="de" onSelect={vi.fn()} recorded={RECORDED} />, 'de');

		expect(
			screen.getByRole('button', { name: '12. Juni 2026 – 3. Juli 2026 anzeigen' }),
		).toBeInTheDocument();
	});

	it('hands the recorded range back unchanged when pressed', async () => {
		const onSelect = vi.fn<(window: { from: string; to: string }) => void>();
		renderWithI18n(<StatRecordedJump language="en" onSelect={onSelect} recorded={RECORDED} />);

		await userEvent.click(screen.getByRole('button'));

		// The exact days the API reported, not a window recomputed from them:
		// the API bounded them by the retention floor, and recomputing would
		// throw that away.
		expect(onSelect).toHaveBeenCalledWith({ from: '2026-06-12', to: '2026-07-03' });
	});
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `pnpm --filter=web exec vitest run src/components/stat-recorded-jump.test.tsx`

Expected: FAIL — the module does not exist.

The two expected label strings were computed from `format.ts`'s own formatter rather than guessed — `Intl.DateTimeFormat` with `day: 'numeric'`, `month: 'short'`, `year: 'numeric'` and `timeZone: 'UTC'` renders `2026-06-12` as `Jun 12, 2026` under `en-US` and `12. Juni 2026` under `de-DE`. If a run ever disagrees, take the string from the failure message: `format.ts` is the authority on how a day is written, and this test's job is only to prove the component asks it.

- [ ] **Step 3: Write the component**

Create `apps/web/src/components/stat-recorded-jump.tsx`:

```tsx
import type { StatRange } from '@kurze-url/api-client';
import { useTranslation } from 'react-i18next';

import { formatDay } from '../lib/format';
import type { Language } from '../lib/preferences';
import type { StatsWindow } from '../lib/stats-window';
import { Button } from './ui/button';

export interface StatRecordedJumpProps {
	/** The active language, for formatting the two dates. */
	readonly language: Language;
	/** Called with the recorded range, so the page can navigate to it. */
	readonly onSelect: (window: StatsWindow) => void;
	/** The range the API reported, already bounded by the retention floor. */
	readonly recorded: StatRange;
}

/**
 * Offers the window a link actually has data for.
 *
 * It exists because the alternative is advice: an empty window cannot say
 * whether widening it would help, and the page used to guess ("Try a longer
 * window") at both readers it could not tell apart. The endpoint now reports
 * the answer, and the useful form of an answer here is a window the reader can
 * take rather than a hint they have to translate into two dates.
 *
 * The range is handed back exactly as it arrived. The API bounded it by the
 * retention floor so that a range it reports is always one it can serve;
 * rebuilding a window from these dates locally would discard that guarantee.
 *
 * @param props - The component's props.
 * @param props.language - The active language, for formatting the two dates.
 * @param props.onSelect - Called with the recorded range when the reader presses the button.
 * @param props.recorded - The range the API reported.
 * @returns The rendered button.
 */
export function StatRecordedJump({
	language,
	onSelect,
	recorded,
}: StatRecordedJumpProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<Button
			onClick={() => {
				onSelect({ from: recorded.from, to: recorded.to });
			}}
			type="button"
		>
			{t('stats.showRecorded', {
				from: formatDay(recorded.from, language),
				to: formatDay(recorded.to, language),
			})}
		</Button>
	);
}
```

- [ ] **Step 4: Run the test**

Run: `pnpm --filter=web exec vitest run src/components/stat-recorded-jump.test.tsx`

Expected: PASS, all three.

- [ ] **Step 5: Add the story**

Every other `stat-*` component ships one. Create `apps/web/src/components/stat-recorded-jump.stories.tsx`:

```tsx
import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { StatRecordedJump } from './stat-recorded-jump';

const meta = {
	args: {
		language: 'en',
		onSelect: () => undefined,
		recorded: { from: '2026-06-12', to: '2026-07-03' },
	},
	component: StatRecordedJump,
	title: 'Links/StatRecordedJump',
} satisfies Meta<typeof StatRecordedJump>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const German: Story = {
	args: { language: 'de' },
};
```

- [ ] **Step 6: Run lint, typecheck and the Storybook suite**

```bash
pnpm run lint && pnpm run typecheck && pnpm --filter=web run test:storybook
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "feat(web): add the recorded-range jump"
```

---

### Task 5: The two empty states use it

**Files:**

- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.tsx` (the `disabled` and `empty` blocks inside `StatsPageBody`)
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.a11y.test.tsx`

**Interfaces:**

- Consumes: `StatRecordedJump` (Task 4), `stats.noClicksElsewhere` / `stats.disabledElsewhere` (Task 3), `LinkStats['recorded']` (Task 2).
- Produces: nothing later tasks import.

The a11y test file already renders the real `StatsPageBody` inside the real `AuthedShell` with a memory router, which is what these assertions need; extending it is cheaper and more faithful than building a second harness.

- [ ] **Step 1: Write the failing tests**

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.a11y.test.tsx`, add two fixtures beside the existing ones:

```tsx
const RECORDED_ELSEWHERE = { from: '2026-06-12', to: '2026-07-03' };

const EMPTY_WITH_HISTORY_STATS: LinkStats = {
	...EMPTY_STATS,
	recorded: RECORDED_ELSEWHERE,
};

const DISABLED_WITH_ELSEWHERE_STATS: LinkStats = {
	...DISABLED_STATS,
	recorded: RECORDED_ELSEWHERE,
};
```

and a new `describe` block:

```tsx
describe('the empty views and the recorded range', () => {
	it('offers the recorded window when counting is on and the window is empty', () => {
		renderComposedPage(EMPTY_WITH_HISTORY_STATS);

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
		expect(
			screen.getByText('This link has statistics outside the window you are looking at.'),
		).toBeInTheDocument();
	});

	it('offers it when counting is off and data was recorded before that', () => {
		renderComposedPage(DISABLED_WITH_ELSEWHERE_STATS);

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				'Counting is off now, but statistics recorded before it was switched off are still here.',
			),
		).toBeInTheDocument();
	});

	it('says so plainly when there is nothing anywhere', () => {
		renderComposedPage(EMPTY_STATS);

		expect(screen.queryByRole('button', { name: /^Show / })).not.toBeInTheDocument();
		expect(screen.getByText('Nothing has been recorded for this link.')).toBeInTheDocument();
	});

	it('leaves the disabled view without advice when there is nothing anywhere', () => {
		renderComposedPage(DISABLED_STATS);

		expect(screen.queryByRole('button', { name: /^Show / })).not.toBeInTheDocument();
	});
});
```

`EMPTY_STATS` and `DISABLED_STATS` already exist in this file and carry no `recorded` at all, which is what a link with no statistics reports.

- [ ] **Step 2: Run them and watch them fail**

Run: `pnpm --filter=web exec vitest run "src/routes/_authed/teams.\$teamSlug.links.\$linkId_.stats.a11y.test.tsx"`

Expected: FAIL on the two "offers" cases — no button is rendered. The two "nothing anywhere" cases may already pass, which is correct: they assert the absence of something that does not exist yet, and they are here to stay honest once it does.

- [ ] **Step 3: Render the control in both empty views**

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId_.stats.tsx`, import the component:

```tsx
import { StatRecordedJump } from '../../components/stat-recorded-jump';
```

Inside `StatsPageBody`, above the `return`, add:

```tsx
// The condition is the field alone, with nothing about the window in it,
// and that is an invariant rather than an oversight. Reaching either empty
// view requires `totals.clicks === 0`; every recorded click writes a
// `total` row (`analytics.Dimensions.Rows` always emits one) and the
// upsert only ever adds a positive count — so a servable row inside the
// requested window would have made the totals positive and neither empty
// view would be on screen. In an empty view a present `recorded` is
// therefore always outside the window, and an overlap check here would
// guard a state the data model cannot produce.
const recorded = stats.recorded;
```

Replace the `disabled` block's `EmptyDescription` and `EmptyContent` with:

```tsx
					<EmptyDescription>
						{t(recorded === undefined ? 'stats.disabledBody' : 'stats.disabledElsewhere')}
					</EmptyDescription>
				</EmptyHeader>
				<EmptyContent>
					{recorded === undefined ? null : (
						<StatRecordedJump language={language} onSelect={onWindowChange} recorded={recorded} />
					)}
					<RouterLink params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
						{t('stats.disabledAction')}
					</RouterLink>
				</EmptyContent>
```

and give the `empty` block a description that switches and an `EmptyContent` it does not have today:

```tsx
					<EmptyDescription>
						{t(recorded === undefined ? 'stats.noClicksBody' : 'stats.noClicksElsewhere')}
					</EmptyDescription>
				</EmptyHeader>
				{recorded === undefined ? null : (
					<EmptyContent>
						<StatRecordedJump language={language} onSelect={onWindowChange} recorded={recorded} />
					</EmptyContent>
				)}
```

`onWindowChange` is already a prop of `StatsPageBody` and already takes a `StatsWindow`; the route's `RouteComponent` wires it to `navigate({ search: next })`, so pressing the button navigates to the recorded window with no further plumbing.

- [ ] **Step 4: Run the file's whole suite**

Run: `pnpm --filter=web exec vitest run "src/routes/_authed/teams.\$teamSlug.links.\$linkId_.stats.a11y.test.tsx"`

Expected: PASS, including the file's existing axe runs — the new button is a `Button` from the design system, and the two empty views keep their heading structure.

- [ ] **Step 5: Run the full web gate**

```bash
pnpm run lint && pnpm run typecheck && pnpm run test
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "feat(web): answer instead of guessing"
```

---

### Task 6: End to end

**Files:**

- Modify: `apps/web/e2e/fixtures/seed.ts`
- Modify: `apps/web/e2e/stats.spec.ts`

**Interfaces:**

- Consumes: everything above.
- Produces: nothing.

`seedLinkClicks` currently places its three days at one, two and three days ago and takes only a link id. It needs to be able to place them further back so a default 30-day window misses them.

- [ ] **Step 1: Give the seeding fixture an offset**

In `apps/web/e2e/fixtures/seed.ts`, change the signature and thread the offset into the `daysAgo` each row carries:

```ts
export async function seedLinkClicks(
	linkId: string,
	options: Readonly<{ daysAgoOffset?: number }> = {},
): Promise<SeededClicks> {
	const offset = options.daysAgoOffset ?? 0;
	const rows = DAYS.flatMap((day) => rowsForDay(day, offset));
```

and in `rowsForDay`, take the offset and add it to every `daysAgo` it writes:

```ts
/**
 * @param day - The day to expand.
 * @param offset - Extra days to push the bucket further back, so a spec can place data outside a default window.
 * @returns That day's rows.
 */
function rowsForDay(day: DaySeed, offset: number): SeedRow[] {
```

Every `daysAgo: day.daysAgo` inside it becomes `daysAgo: day.daysAgo + offset`.

Add to the `DAYS` docstring, after the existing paragraph about the midnight race:

```
 * `seedLinkClicks` can push all three further back through its `daysAgoOffset`
 * option, which is how a spec places data outside the page's default 30-day
 * window without inventing a second seed.
```

- [ ] **Step 2: Write the failing e2e case**

Append to `apps/web/e2e/stats.spec.ts`:

```ts
/**
 * The page's default window is 30 days, so data seeded 41 days back is invisible
 * in it — the case the `recorded` field exists for. Nothing here computes a date:
 * the button is supposed to name the window and take the reader there, so the test
 * presses it and asserts the figures rather than asserting a URL it derived itself.
 */
test('offers the window that actually has data', async ({ page, teamId, teamSlug }) => {
	await createLink(page, teamSlug, {
		destinationUrl: `https://example.org/stats-old-${Date.now()}`,
	});
	const linkId = await linkIdForTeam(teamId);
	const seeded = await seedLinkClicks(linkId, { daysAgoOffset: 40 });

	await page.goto(`/teams/${teamSlug}/links/${linkId}/stats`);

	await expect(
		page.getByText('This link has statistics outside the window you are looking at.'),
	).toBeVisible();

	const jump = page.getByRole('button', { name: /^Show / });
	await expect(jump).toBeVisible();
	await jump.click();

	await expect(summaryFigure(page, 'Clicks')).toHaveText(String(seeded.totals.clicks));
});
```

`summaryFigure`, `createLink`, `linkIdForTeam` and `seedLinkClicks` are already imported at the top of this file.

- [ ] **Step 3: Run it**

There is no failing-first state to observe here, and pretending otherwise would be theatre: by this task the feature is built, so this test is a regression guard rather than a driver. It should pass on its first run.

Run it against a local **production build** — `pnpm --filter=web run build`, then `node .output/server/index.mjs` on port 3000 — with the local Go API and Supabase running, and with `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` and `E2E_DATABASE_URL` exported.

**Do not run this against the Vite dev server.** It injects a `<nav aria-label="Workbench destinations">`, which makes `getByLabel(/destination/iu)` resolve to two elements and fails `createLink` for a reason that has nothing to do with the test. This cost a full debugging round on 2026-09-18.

Run: `pnpm --filter=web exec playwright test stats.spec.ts --reporter=line`

Expected: three tests pass.

- [ ] **Step 4: Prove the new test is not vacuous**

A green regression guard that would stay green with the feature removed is worth nothing, and this one asserts against a page whose empty state renders plenty of text. Temporarily change the last assertion to `String(seeded.totals.clicks + 1)` and run the file again.

Expected: FAIL, reporting `Expected: "26"` against `Received: "25"` — which proves the assertion reads the figure the seeded data produced, through the jump the button performed. Put the assertion back and re-run before moving on.

- [ ] **Step 5: Run the whole e2e suite locally**

Run: `pnpm --filter=web exec playwright test --reporter=line`

Expected: everything passes except `warns that the short domain does not resolve`, which needs a shared hostname ending in `.invalid`. Locally it is `127.0.0.1:8080`, so `ShortUrlNotice` correctly renders nothing. That failure is pre-existing and environment-bound; it passes against a preview.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/stats-recorded-range -m "test(web): cover the recorded-range jump"
```

---

## After the last task

Open the pull request with the `create-pr` skill against `main`. The branch is `feat/stats-recorded-range` and already carries the spec commit ahead of Task 1.

Two things belong in the pull request body because a reviewer cannot see them in the diff:

- The contract's shape was decided by measurement, not by the spec's first guess: Huma cannot express a nullable object, so `recorded` is optional and absent rather than required and null. The generated type was read before anything was built on it.
- `apps/api/openapi.json` and `packages/api-client/src/generated/**` are `pnpm generate:api` output, not hand edits.
