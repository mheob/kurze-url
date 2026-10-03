# Safe Browsing Scanning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every active link's destination is checked against Google Safe Browsing (v5 `hashes.search`) right after it is created or re-pointed and daily after that, and a link Google reports is blocked at the redirect only while a Google confirmation younger than thirty minutes backs the block.

**Architecture:** A new `apps/api/internal/scanning` package canonicalizes destinations, sends only 4-byte SHA-256 prefixes to Google and matches full hashes locally behind a provider-neutral `Checker`; `internal/api` applies each verdict in one locked transaction per link (state, a `link_scan_result` row on change, a system-actor audit entry, then the Redis confirmation key and the redirect-cache invalidation), starts a best-effort check after every create and destination change, and exposes a token-guarded `POST /internal/scan` that `.github/workflows/scan.yml` calls twice an hour. The redirect and verify paths re-check a flagged link that has no fresh confirmation (2 seconds, shared per instance through `singleflight`), and the web app shows a badge, a detail-page notice and a "Google Safe Browsing" audit actor. The task boundaries are the controller's; reading the code changed three details inside them, recorded under "Rulings on the spec": the sweep's due query gets no index (Task 1), every verdict read and write still filters by `team_id` (Task 1, Task 5), and `updateLink` gains the `huma.StatusError` case its 409 needs, which also turns a latent 500 into the 422 it was meant to be (Task 5).

**Tech Stack:** Go 1.27 (chi, Huma v2, sqlc v1.31.1 on pgx/v5, go-redis v9, `golang.org/x/net/idna`, `golang.org/x/sync/singleflight`), Supabase CLI migrations, Upstash Redis, GitHub Actions, TanStack Start + react-i18next + shadcn/ui on Base UI, Vitest + RTL, Storybook with the a11y addon.

**Spec:** `docs/superpowers/specs/2026-10-03-safe-browsing-design.md`. Read it before any task; it is the authority wherever this plan and it disagree.

## Global Constraints

Copied from the spec and `CLAUDE.md`. Every task's requirements include all of them.

- **Golden rule 2:** an `active` link's redirect calls nothing new. The only new waiting is on a `flagged` link, at most 2 seconds, at most about once per 30 minutes per link per instance.
- **No URL leaves the server:** only 4-byte SHA-256 prefixes are sent to Google.
- **The API key is never part of a URL.** It travels in the `X-Goog-Api-Key` header, because Go's `*url.Error` puts the request URL into error text, which reaches logs and Sentry.
- **Blocking needs a fresh confirmation:** a flagged link is shown the block page only if Google confirmed the threat within the last 30 minutes, or within `cacheDuration` if that is shorter. The confirmation key `sb:confirmed:<link_id>` (through `Client.Key`, so it carries the environment prefix) gets a TTL of `min(ValidFor, 30 minutes) − 1 minute`; if that TTL is not positive, no key is set.
- **Endpoint and limits:** `GET https://safebrowsing.googleapis.com/v5/hashes:search` with repeated `hashPrefixes` (base64 of 4 bytes each), at most 1000 per request; 5-second client timeout, no redirects followed, response body capped at 1 MiB; a 429 or a quota error is `scanning.ErrQuotaExceeded`; any other non-200 is an ordinary error carrying the status, never the body.
- **Matching:** a URL is reported for a threat type only when one of its own full hashes equals a returned `fullHash`, and only for details whose attributes include neither `CANARY` nor `FRAME_ONLY`. Unknown threat types are kept as reported strings. `ValidFor` is the response's `cacheDuration`.
- **Immediate checks:** a goroutine after `createLink` commits and after `updateLink` commits a changed `destination_url`, on `context.WithoutCancel` of the request context with a 10-second timeout, recovering panics. Best-effort by design.
- **Redirect, flagged branch:** confirmation key exists → 403 block page; no key → one check with a 2-second timeout shared through `singleflight`; threats → set the key, 403; clean → forward exactly as an active link (click recorded) and lift the flag in the background; error, timeout or no checker → `KindUnavailable`, 503, `Retry-After: 300`, logged at Warn.
- **PATCH on a flagged link:** a changed `destination_url` sets `state` back to `active` in the same update; any `state` in the body is refused with 409 and `huma.ErrorDetail{Location: "body.state", Value: "flagged"}`; `updateLink` reads the link `FOR UPDATE`.
- **Sweep:** `POST /internal/scan` on the root router above the hostname split, outside Huma and the OpenAPI document; `X-Scan-Token` against `SCAN_TOKEN` in constant time; unset token → 404 for everyone; no checker → 503; a fixed batch (`scanBatchSize = 200`) and a 25-second budget; response keys `checked`, `flagged`, `unflagged`, `failed`, `remaining`.
- **Workflow:** `.github/workflows/scan.yml`, `cron: "7,37 * * * *"` plus `workflow_dispatch`, a pull-request trigger scoped to its own path with the steps skipped on pull requests, one `curl --fail` with the token, then a ping to `SCAN_HEARTBEAT_URL` only on success.
- **Environment variables:** `SAFE_BROWSING_API_KEY` (unset: scanning off with a startup Warn; `/internal/scan` 503; a flagged link answers the neutral 503 page), `SCAN_TOKEN` (unset: 404 for everyone), `SCAN_HEARTBEAT_URL` (GitHub secret only). All three documented valueless in `apps/api/.env.example` and in `CLAUDE.md`.
- **Audit:** `link.flagged` and `link.unflagged` join the closed taxonomy (23 values), each in both the constant block and `knownActions`; `audit.Entry.ActorUserID` becomes `*uuid.UUID`, nil is the system; metadata `threat_types` and `destination_url`.
- **Sentry:** each new flag logs at Error; `scanning.ErrQuotaExceeded` gets an `observability.CoalesceRule` with a one-hour window; every other check failure logs at Warn.
- **Tenancy:** every query filters by `team_id` except the instance-wide sweep reads (`ListDueLinksForScan`, `CountDueLinksForScan`), which carry the retention job's justification comment.
- **Strings:** every user-facing string exists in English and German, including the framework-free redirect pages. No hardcoded copy. `catalogues.test.ts` rejects a German value identical to its English one unless the key is in its `identicalByDesign` list.
- **Accessibility:** WCAG 2.1 AA. Storybook runs with `a11y: { test: 'error' }`.
- **JSDoc:** any function with a `/** … */` block carries `@param` (with dotted tags for destructured props, `root0` for anonymous ones) and `@returns`.
- **Generated files are never hand-edited:** `apps/web/src/components/ui/*`, `apps/web/src/routeTree.gen.ts` (must not change in this plan at all), `packages/api-client/src/generated/*` (regenerated by `pnpm run generate:api`), `apps/api/internal/db/*.sql.go`, `models.go`, `db.go`, `batch.go` (regenerated by `sqlc generate`).
- **Git:** GitButler only, on the lane `feat/safe-browsing`: `but commit -b feat/safe-browsing -m "<msg>" <ids>`, with the IDs copied from the first column of `but diff`. Never a git write command. The user's `create-commit` skill wraps exactly this; use it if your harness offers it. Conventional Commits, subject at most 50 characters including type and scope, no co-author or generator footer. `but commit` skips Lefthook, so run `pnpm format` (and, for Go, `gofmt -l` and `go vet`) yourself before every commit.
- **Commands** run from `/Users/ab/dev/customer/itsb/kurze-url` unless a step says otherwise. A dispatched shell has pnpm but no node: prefix JS commands with `eval "$(fnm env)" &&`. Go tests that touch Postgres need the local Supabase stack (`supabase start`, Postgres on `127.0.0.1:54322`); tests that touch Redis start their own container through testcontainers and need Docker. Web unit tests: `cd apps/web && npx vitest run --project unit <path>`.
- **Never read** `.env`, secrets or `E2E_DATABASE_URL`, and never call Google or any external API from a test or a step. Where a step says to verify something against a Google documentation page, that means reading the page in a browser, not calling the API.

## Review Focus

1. **Real-world destination spellings** — an internationalized host (`https://Bücher.de/`), an uppercase host, a percent-encoded host, userinfo, a port, an IPv6 literal, a trailing dot — must canonicalize to the form Google lists, or a listed phishing site passes unflagged with no error anywhere. _Tests: Task 2 (`TestCanonicalizeRealWorldDestinations`), Task 3 (`TestCheckFindsAnInternationalizedHostUnderItsPunycode`)._
2. **The first sweeps after deploy find every existing link due**, a backlog larger than one run's budget: the run must stop at its budget, leave the rest due, count it under `remaining` and not under `failed`, and still answer 200 so the heartbeat fires. _Tests: Task 6 (`TestScanReportsTheBatchLimitAndWhatRemains`, `TestSweepStopsAtItsBudgetAndLeavesTheRestDue`)._
3. **A link deleted, or its destination changed, between due-selection and `applyVerdict`** must neither error nor apply the old URL's verdict to the new URL; the link stays due for its new destination. _Tests: Task 5 (`TestApplyVerdictDiscardsAVerdictForAReplacedDestination`, `TestApplyVerdictForADeletedLinkIsANoOp`), Task 6 (`TestScanDiscardsAVerdictForADestinationChangedMidCheck`)._
4. **Redis erroring during a flagged redirect** (Upstash's quota is this project's scarcest resource): a failed confirmation `GET` must read as "no key" and re-check, and a failed `SET` must not turn a confirmed block into an error page. _Test: Task 7 (`TestARedisFailureDuringAFlaggedRedirectFallsBackToARecheck`)._
5. **A full hash returned for one URL of a batch** — Google answers a prefix with every listed hash under it — must not flag a neighbouring URL that merely shares the prefix or the host. _Tests: Task 3 (`TestCheckFlagsOnlyTheURLWhoseExpressionIsListed`, `TestCheckIgnoresAPrefixMatchWhoseFullHashDiffers`)._

## Rulings on the spec

Places where the spec leaves room, and what this plan does there. Each is implemented and tested in the task named.

1. **No index for the due query (Task 1).** The spec allows "whatever shape the plan's `EXPLAIN` shows the query uses". The due predicate's `scan_destination is distinct from destination_url` branch compares two columns of one row, which no index can answer, so every plan for it is a sequential scan of `link`; an index on `scan_checked_at` would only turn every daily check into a non-HOT update. Task 1 records the `EXPLAIN` and puts the reasoning beside the query.
2. **Due ordering (Task 1).** "Never checked" includes a link whose `scan_destination` differs from `destination_url`: its current destination has never been checked. Ties break on `created_at`, then `id`.
3. **Verdict reads and writes filter by `team_id` (Tasks 1 and 5).** The spec permits the scanner's writes to skip it, but the scanner always knows the team (the due list returns it, every handler has it), so only the two due-list queries are instance-wide.
4. **A URL missing from `Check`'s result map has no verdict (Tasks 3, 5, 6, 7).** It is never read as clean: the sweep counts it as `failed`, the redirect answers the neutral 503.
5. **A link no longer `active` or `flagged` when its verdict arrives is left alone (Task 5)**, not even its timestamps, so it "becomes due again when it is re-enabled" as the spec says.
6. **`scan` on `GET /v1/links/{link_id}` (Task 8)** is omitted until the _current_ destination has been checked; `since` is present only when the latest `link_scan_result` row has the current verdict for the current destination.
7. **`audit.Log` refuses an actor that does not fit its action (Task 4):** a nil actor outside `link.flagged`/`link.unflagged`, and a member on those two. The web tells "Google Safe Browsing" from "A deleted account" by the action alone, so a nil actor anywhere else would be mislabelled.
8. **The confirmation key is dropped (Task 5)** when a flagged link's destination changes and whenever a new confirmation would have a non-positive TTL, so a key can never vouch for a URL it was not set for.
9. **A sweep whose `Check` fails as a whole answers 502 (Task 6)**, so the workflow withholds its heartbeat and Better Stack reports that scanning stopped.
10. **`singleflight` comes from `golang.org/x/sync` (Task 7).** `go.mod` already carries `golang.org/x/sync v0.23.0` as an indirect dependency, so `go mod tidy` only promotes it; nothing new is downloaded, and an in-package copy would be a second implementation of a solved problem.
11. **IDN hosts are converted to Punycode with `golang.org/x/net/idna` (Task 2)**, already a direct dependency. Google's canonicalization rules tell clients to do it, and browsers list and visit the ASCII form.
12. **`export_test.go` (Tasks 5 and 6)** hands `package api_test`, where the database fixtures live, `applyVerdict` and `sweep` with a batch limit of the test's choosing. It is the standard Go idiom and is compiled only into tests.
13. **Requests carry at most 250 prefixes (Task 3; controller's ruling).** The spec's 1000 is Google's ceiling for `hashes.search`, and staying below it satisfies the spec. But the method is a GET: 1000 padded base64 prefixes are about 26 KB of query string, past the 8 KB many front ends refuse with 414, and nothing here can check Google's limit without calling it. At 250, a request stays near 6.5 KB. A sweep of 200 links then takes about 24 requests instead of 6, which is nothing against the quota. It is one named constant, `maxPrefixesPerRequest`; the tests read it through `MaxPrefixesPerRequestForTest` rather than restating it.

## File map

| File | Task | Responsibility |
| --- | --- | --- |
| `supabase/migrations/<timestamp>_safe_browsing.sql` | 1 | `link.scan_checked_at`, `link.scan_destination`, `link_scan_result.destination_url`, `link_scan_result.threat_types` |
| `apps/api/internal/db/queries/link_scan.sql` | 1 | due list, due count, locked read, verdict write, result insert, latest result |
| `apps/api/internal/db/queries/link_crud.sql` | 1 | `GetLinkForAPI` gains the scan columns; `GetLinkForAPIForUpdate` |
| `apps/api/internal/db/link_scan_test.go` | 1 | query tests, isolated in a rolled-back REPEATABLE READ transaction |
| `apps/api/internal/scanning/scanning.go` | 2, 3 | package doc; `Result`, `Checker`, `ErrQuotaExceeded`, `QuotaExceeded` |
| `apps/api/internal/scanning/canonical.go` | 2 | canonicalization and expressions |
| `apps/api/internal/scanning/client.go` | 3 | the `hashes.search` client |
| `apps/api/internal/scanning/export_test.go` | 3 | test seams for the client |
| `apps/api/internal/scanning/canonical_test.go`, `client_test.go` | 2, 3 | unit tests |
| `apps/api/internal/config/config.go` | 3, 6 | `SafeBrowsingAPIKey`, `ScanToken` |
| `apps/api/cmd/api/main.go`, `main_test.go` | 3 | wiring and the Sentry coalesce rule |
| `apps/api/.env.example` | 3, 6 | the three variables |
| `apps/api/internal/audit/audit.go` and tests | 4 | system actor, two actions |
| every `audit.Entry` caller in `apps/api/internal/api/*.go` | 4 | `&member.UserID` |
| `apps/api/internal/cache/safebrowsing.go`, `safebrowsing_test.go` | 5 | the confirmation key |
| `apps/api/internal/api/scan.go` | 5 | `applyVerdict`, background checks, confirmation helpers |
| `apps/api/internal/api/links.go` | 5, 8 | triggers, PATCH rules; the `scan` field and the `state` enum |
| `apps/api/internal/api/export_test.go` | 5, 6 | test seams |
| `apps/api/internal/api/scan_fake_test.go`, `scan_test.go`, `scan_internal_test.go` | 5 | fake checker and pipeline tests |
| `apps/api/internal/api/scan_sweep.go`, `scan_sweep_test.go` | 6 | `POST /internal/scan` |
| `apps/api/internal/api/router.go` | 6 | the route |
| `.github/workflows/scan.yml` | 6 | the schedule |
| `apps/api/internal/api/flagged.go`, `flagged_test.go` | 7 | the flagged branch of redirect and verify |
| `apps/api/internal/api/redirect.go`, `verify.go` and their tests | 7 | call `admit` |
| `apps/api/internal/pages/pages.go`, `templates/flagged.html`, `pages_test.go` | 7 | block page, neutral page |
| `apps/api/internal/api/link_scan_view_test.go` | 8 | the nested verdict and the schema |
| `apps/api/openapi.json`, `packages/api-client/src/generated/*` | 8 | regenerated |
| `apps/web/src/lib/safe-browsing.ts` and test | 9 | threat categories and Google's links |
| `apps/web/src/components/link-scan-notice.tsx`, test, stories | 9 | the detail-page notice |
| `apps/web/src/components/link-list.tsx`, test, stories | 9 | the badge |
| `apps/web/src/lib/audit-actor.ts`, `components/audit-entry-table.tsx`, tests, stories | 9 | the system actor |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, `.role.test.tsx` | 9 | render the notice |
| `apps/web/src/i18n/locales/{en,de}.json`, `catalogues.test.ts` | 9 | copy |
| `CLAUDE.md`, `docs/planning/0{1,2,5,6,7,8}-*.md` | 10 | documentation |

---

### Task 1: Migration and sqlc queries

**Files:**

- Create: `supabase/migrations/<timestamp>_safe_browsing.sql` (the CLI picks the timestamp)
- Create: `apps/api/internal/db/queries/link_scan.sql`
- Modify: `apps/api/internal/db/queries/link_crud.sql:30-37` (`GetLinkForAPI`), and append `GetLinkForAPIForUpdate` after it
- Create: `apps/api/internal/db/link_scan_test.go`
- Regenerated: `apps/api/internal/db/link_crud.sql.go`, `apps/api/internal/db/link_scan.sql.go` (new), `apps/api/internal/db/models.go`

**Interfaces:**

- Consumes: nothing new.
- Produces (sqlc-generated, in package `db`; field names are sqlc's and are checked in Step 6):
  - `ListDueLinksForScan(ctx, ListDueLinksForScanParams{Now time.Time, BatchLimit int32}) ([]ListDueLinksForScanRow, error)`, row `{ID, TeamID uuid.UUID; DestinationURL string}`
  - `CountDueLinksForScan(ctx, now time.Time) (int64, error)`
  - `GetLinkForScan(ctx, GetLinkForScanParams{ID, TeamID uuid.UUID}) (GetLinkForScanRow, error)`, row `{ID, TeamID uuid.UUID; DestinationURL, State, Slug, Hostname string}`
  - `RecordLinkScan(ctx, RecordLinkScanParams{State string; CheckedAt time.Time; CheckedDestination string; ID, TeamID uuid.UUID}) error`
  - `InsertLinkScanResult(ctx, InsertLinkScanResultParams{LinkID uuid.UUID; Verdict, DestinationURL string; ThreatTypes []string; ScannedAt time.Time}) error`
  - `GetLatestLinkScanResult(ctx, GetLatestLinkScanResultParams{LinkID, TeamID uuid.UUID}) (GetLatestLinkScanResultRow, error)`, row `{Verdict string; ThreatTypes []string; DestinationURL string; ScannedAt time.Time}`
  - `GetLinkForAPIRow` gains `ScanCheckedAt *time.Time` and `ScanDestination *string`
  - `GetLinkForAPIForUpdate(ctx, GetLinkForAPIForUpdateParams{ID, TeamID uuid.UUID}) (GetLinkForAPIForUpdateRow, error)` with `GetLinkForAPIRow`'s fields

- [ ] **Step 1: Create the migration**

Run: `supabase migration new safe_browsing`

Expected: `Created new migration at supabase/migrations/<timestamp>_safe_browsing.sql`. Write into that file:

```sql
-- Safe Browsing scanning (docs/superpowers/specs/2026-10-03-safe-browsing-design.md).
--
-- link.state stays the only switch the redirect path reads. These two columns
-- record when a link was last checked and which destination that check
-- judged. A verdict never applies to a URL it did not see, so a link whose
-- scan_destination differs from destination_url is due again, whatever
-- scan_checked_at says.
alter table link
  add column scan_checked_at timestamptz,
  add column scan_destination text;

-- A row is written only when a link's verdict changes, from active to flagged
-- or back, so the table stays small and still holds the whole history. The
-- first check that finds a link clean writes nothing. Nothing has ever written
-- this table, so it is empty everywhere and the not-null column needs no
-- backfill. 'error' stays in verdict's check constraint and stays unused: a
-- failed check is logged, not stored. The table also stays outside the
-- retention job, as docs/superpowers/specs/2026-09-12-analytics-retention-design.md
-- decided.
alter table link_scan_result
  add column destination_url text not null,
  add column threat_types text[] not null default '{}';
```

- [ ] **Step 2: Apply it to the local stack**

Run: `supabase migration up --local`

Expected: `Applying migration <timestamp>_safe_browsing.sql...` and `Local database is up to date.` If the CLI refuses because the local history is out of step, run `supabase db reset`, which replays every migration plus `supabase/seed.sql` exactly as CI does.

The Preview database does **not** get this migration from the GitHub integration (it applies on merge to `main` only). The controller applies it to the Preview project by hand before the branch's e2e can pass; do not attempt it from this task.

- [ ] **Step 3: Write the failing query tests**

Create `apps/api/internal/db/link_scan_test.go`:

```go
package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// scanNow is the clock every test in this file runs the due queries at.
//
// The due list and its count are instance-wide on purpose, and `go test ./...`
// runs other packages' tests in parallel processes against this database,
// committing links of their own. Two things keep these tests exact, the same
// two isolateDelete (click_stats_test.go) relies on:
//
//   - Everything a test seeds goes through its own REPEATABLE READ transaction,
//     rolled back when the test ends. No other process can see, scan or lock
//     it, and both reads of a count delta see one snapshot.
//   - Every seeded link is created in the year 2000, which no other test
//     commits. Every committed fixture link anywhere is a never-checked link,
//     so among those the seeded ones sort first, and a list filtered to the
//     seeded ids is never cut short by somebody else's row.
//     internal/api's sweep tests commit links created in 2001 for the same
//     reason, which still sorts after these.
//
// A new test that commits links created before 2001 would break the second
// half.
var scanNow = time.Date(2000, 6, 1, 12, 0, 0, 0, time.UTC)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}

// scanFixture is one team with one verified domain, seeded inside a REPEATABLE
// READ transaction that the test's cleanup rolls back.
type scanFixture struct {
	tx       pgx.Tx
	queries  *db.Queries
	teamID   uuid.UUID
	userID   uuid.UUID
	domainID uuid.UUID
	hostname string
}

func newScanFixture(t *testing.T) *scanFixture {
	t.Helper()
	ctx := context.Background()

	tx, err := testPool(t).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	f := &scanFixture{tx: tx, queries: db.New(tx), hostname: "s" + uuid.NewString()[:8] + ".test"}
	f.teamID, f.userID = seedTeamWithOwner(ctx, t, tx)
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.teamID, f.hostname).Scan(&f.domainID))
	return f
}

// scanSeed describes one link by the columns the due predicate reads.
type scanSeed struct {
	State       string
	CreatedAt   string // RFC 3339, in the year 2000; see scanNow
	Destination string
	CheckedAt   *time.Time
	CheckedURL  *string
	ExpiresAt   *time.Time
}

func (f *scanFixture) link(t *testing.T, seed scanSeed) uuid.UUID {
	t.Helper()
	if seed.State == "" {
		seed.State = "active"
	}
	if seed.Destination == "" {
		seed.Destination = "https://example.org/" + uuid.NewString()
	}

	var id uuid.UUID
	require.NoError(t, f.tx.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, state, expires_at,
		                   created_by, created_at, scan_checked_at, scan_destination)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
		f.domainID, f.teamID, "s-"+uuid.NewString()[:8], seed.Destination, seed.State,
		seed.ExpiresAt, f.userID, at(t, seed.CreatedAt), seed.CheckedAt, seed.CheckedURL,
	).Scan(&id))
	return id
}

// ownIDs keeps the rows whose id is one of ids, in the order the query
// returned them. Every other row was committed by somebody else.
func ownIDs(rows []db.ListDueLinksForScanRow, ids ...uuid.UUID) []uuid.UUID {
	mine := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		mine[id] = true
	}
	var out []uuid.UUID
	for _, row := range rows {
		if mine[row.ID] {
			out = append(out, row.ID)
		}
	}
	return out
}

// The order is the spec's: links whose current destination was never
// checked, then flagged links, then the longest-unchecked. Three links that
// are not due are seeded beside them, so a predicate that is too loose shows
// up as an extra id rather than passing silently.
func TestListDueLinksForScanPutsTheOldestDebtFirst(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()

	never := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	expiresLater := f.link(t, scanSeed{
		CreatedAt: "2000-01-02T00:00:00Z", ExpiresAt: ptr(at(t, "2001-01-01T00:00:00Z")),
	})
	moved := f.link(t, scanSeed{
		CreatedAt: "2000-01-03T00:00:00Z", Destination: "https://example.org/new",
		CheckedAt: ptr(at(t, "2000-06-01T11:45:00Z")), CheckedURL: ptr("https://example.org/old"),
	})
	flagged := f.link(t, scanSeed{
		State: "flagged", CreatedAt: "2000-01-04T00:00:00Z", Destination: "https://example.org/flagged",
		CheckedAt: ptr(at(t, "2000-06-01T11:30:00Z")), CheckedURL: ptr("https://example.org/flagged"),
	})
	stale := f.link(t, scanSeed{
		CreatedAt: "2000-01-05T00:00:00Z", Destination: "https://example.org/stale",
		CheckedAt: ptr(at(t, "2000-05-30T00:00:00Z")), CheckedURL: ptr("https://example.org/stale"),
	})
	fresh := f.link(t, scanSeed{
		CreatedAt: "2000-01-06T00:00:00Z", Destination: "https://example.org/fresh",
		CheckedAt: ptr(at(t, "2000-06-01T11:00:00Z")), CheckedURL: ptr("https://example.org/fresh"),
	})
	disabled := f.link(t, scanSeed{State: "disabled", CreatedAt: "2000-01-07T00:00:00Z"})
	expired := f.link(t, scanSeed{
		CreatedAt: "2000-01-08T00:00:00Z", ExpiresAt: ptr(at(t, "2000-05-01T00:00:00Z")),
	})

	// A limit as large as everything due, so rows other tests committed cannot
	// push a seeded one out of the page.
	total, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)
	rows, err := f.queries.ListDueLinksForScan(ctx, db.ListDueLinksForScanParams{
		Now: scanNow, BatchLimit: int32(total),
	})
	require.NoError(t, err)

	require.Equal(t,
		[]uuid.UUID{never, expiresLater, moved, flagged, stale},
		ownIDs(rows, never, expiresLater, moved, flagged, stale, fresh, disabled, expired))
}

func TestListDueLinksForScanStopsAtTheLimit(t *testing.T) {
	f := newScanFixture(t)

	first := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	second := f.link(t, scanSeed{CreatedAt: "2000-01-02T00:00:00Z"})
	f.link(t, scanSeed{CreatedAt: "2000-01-03T00:00:00Z"})

	rows, err := f.queries.ListDueLinksForScan(context.Background(), db.ListDueLinksForScanParams{
		Now: scanNow, BatchLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, first, rows[0].ID)
	require.Equal(t, second, rows[1].ID)
	require.Equal(t, f.teamID, rows[0].TeamID, "the sweep needs the team to filter its writes")
}

// Both counts run in one REPEATABLE READ snapshot, so what other processes
// commit in between cannot move the difference.
func TestCountDueLinksForScanCountsExactlyWhatIsDue(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()

	before, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)

	f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	f.link(t, scanSeed{
		State: "flagged", CreatedAt: "2000-01-02T00:00:00Z", Destination: "https://example.org/f",
		CheckedAt: ptr(at(t, "2000-06-01T11:59:00Z")), CheckedURL: ptr("https://example.org/f"),
	})
	f.link(t, scanSeed{
		CreatedAt: "2000-01-03T00:00:00Z", Destination: "https://example.org/s",
		CheckedAt: ptr(at(t, "2000-05-01T00:00:00Z")), CheckedURL: ptr("https://example.org/s"),
	})
	f.link(t, scanSeed{
		CreatedAt: "2000-01-04T00:00:00Z", Destination: "https://example.org/fresh",
		CheckedAt: ptr(at(t, "2000-06-01T11:00:00Z")), CheckedURL: ptr("https://example.org/fresh"),
	})
	f.link(t, scanSeed{State: "disabled", CreatedAt: "2000-01-05T00:00:00Z"})
	f.link(t, scanSeed{CreatedAt: "2000-01-06T00:00:00Z", ExpiresAt: ptr(at(t, "2000-05-01T00:00:00Z"))})

	after, err := f.queries.CountDueLinksForScan(ctx, scanNow)
	require.NoError(t, err)
	require.EqualValues(t, 3, after-before)
}

func TestGetLinkForScanReadsTheLinkAndItsHostnameWithinItsTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z", Destination: "https://example.org/read"})

	row, err := f.queries.GetLinkForScan(ctx, db.GetLinkForScanParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, "https://example.org/read", row.DestinationURL)
	require.Equal(t, "active", row.State)
	require.Equal(t, f.hostname, row.Hostname)
	require.NotEmpty(t, row.Slug)

	_, err = f.queries.GetLinkForScan(ctx, db.GetLinkForScanParams{ID: id, TeamID: uuid.New()})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

// updated_at is the dashboard's "last changed by somebody". A daily check of
// every link must not turn it into "last scanned".
func TestRecordLinkScanWritesTheCheckButNotUpdatedAt(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})
	_, err := f.tx.Exec(ctx, `update link set updated_at = '2000-01-01T00:00:00Z' where id = $1`, id)
	require.NoError(t, err)

	require.NoError(t, f.queries.RecordLinkScan(ctx, db.RecordLinkScanParams{
		ID: id, TeamID: f.teamID, State: "flagged",
		CheckedAt: scanNow, CheckedDestination: "https://example.org/checked",
	}))
	// Another team's id writes nothing.
	require.NoError(t, f.queries.RecordLinkScan(ctx, db.RecordLinkScanParams{
		ID: id, TeamID: uuid.New(), State: "active",
		CheckedAt: scanNow, CheckedDestination: "https://example.org/other",
	}))

	var (
		state, checkedURL    string
		checkedAt, updatedAt time.Time
	)
	require.NoError(t, f.tx.QueryRow(ctx,
		`select state, scan_checked_at, scan_destination, updated_at from link where id = $1`, id,
	).Scan(&state, &checkedAt, &checkedURL, &updatedAt))
	require.Equal(t, "flagged", state)
	require.True(t, scanNow.Equal(checkedAt))
	require.Equal(t, "https://example.org/checked", checkedURL)
	require.True(t, at(t, "2000-01-01T00:00:00Z").Equal(updatedAt))
}

func TestGetLatestLinkScanResultReturnsTheNewestRowWithinTheTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{CreatedAt: "2000-01-01T00:00:00Z"})

	require.NoError(t, f.queries.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
		LinkID: id, Verdict: "flagged", DestinationURL: "https://example.org/x",
		ThreatTypes: []string{"MALWARE"}, ScannedAt: at(t, "2000-02-01T00:00:00Z"),
	}))
	require.NoError(t, f.queries.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
		LinkID: id, Verdict: "clean", DestinationURL: "https://example.org/x",
		ThreatTypes: []string{}, ScannedAt: at(t, "2000-03-01T00:00:00Z"),
	}))

	latest, err := f.queries.GetLatestLinkScanResult(ctx, db.GetLatestLinkScanResultParams{
		LinkID: id, TeamID: f.teamID,
	})
	require.NoError(t, err)
	require.Equal(t, "clean", latest.Verdict)
	require.Empty(t, latest.ThreatTypes)
	require.Equal(t, "https://example.org/x", latest.DestinationURL)
	require.True(t, at(t, "2000-03-01T00:00:00Z").Equal(latest.ScannedAt))

	_, err = f.queries.GetLatestLinkScanResult(ctx, db.GetLatestLinkScanResultParams{
		LinkID: id, TeamID: uuid.New(),
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "the result table has no team_id, so the link's must filter it")
}

func TestGetLinkForAPIReportsTheLastCheckAndTheLockedReadFiltersByTeam(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id := f.link(t, scanSeed{
		CreatedAt: "2000-01-01T00:00:00Z", Destination: "https://example.org/api",
		CheckedAt: ptr(scanNow), CheckedURL: ptr("https://example.org/api"),
	})

	row, err := f.queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.NotNil(t, row.ScanCheckedAt)
	require.True(t, scanNow.Equal(*row.ScanCheckedAt))
	require.NotNil(t, row.ScanDestination)
	require.Equal(t, "https://example.org/api", *row.ScanDestination)

	locked, err := f.queries.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{ID: id, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, id, locked.ID)

	_, err = f.queries.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{ID: id, TeamID: uuid.New()})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
```

`ptr` and `seedTeamWithOwner` already exist in `apps/api/internal/db/tenancy_test.go`; `testPool` in `schema_test.go`.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/db/ -run 'LinkScan|DueLinks|LinkForScan|LinkForAPIReports' -count=1`

Expected: FAIL to compile with `undefined: db.ListDueLinksForScanParams` (and the other new names).

- [ ] **Step 5: Write the queries**

Create `apps/api/internal/db/queries/link_scan.sql`:

```sql
-- Safe Browsing scanning. Two of these queries are instance-wide on purpose
-- and say so where they are: the sweep's due list and its count act for the
-- instance, like the retention job's deletes, and scoping them to a team would
-- make "every link is checked daily" depend on who happened to call. Every
-- other query here filters by team_id although the scanner belongs to no team,
-- because it always knows the link's team: the due list hands it over, and so
-- does every handler that starts a check.

-- ListDueLinksForScan is one sweep's batch, the oldest debt first: links whose
-- current destination has never been checked, then flagged links (due on
-- every sweep, so a false positive Google corrects is lifted even if nobody
-- visits the link), then the longest-unchecked. created_at and id make the
-- order total.
--
-- No team_id, and that is correct here; see the note at the top.
--
-- No index either, deliberately. The `scan_destination is distinct from
-- destination_url` branch compares two columns of one row, which no index can
-- answer, so every plan for this predicate is a sequential scan of link, and
-- an index on scan_checked_at would only turn every daily check into a non-HOT
-- update for nothing. At this instance's size (thousands of rows, 48 sweeps a
-- day) the scan is cheap. Re-run the EXPLAIN in
-- docs/superpowers/plans/2026-10-03-safe-browsing.md (Task 1) if link ever
-- reaches six figures.

-- name: ListDueLinksForScan :many
select l.id, l.team_id, l.destination_url
from link l
where l.state in ('active', 'flagged')
  and (l.expires_at is null or l.expires_at > sqlc.arg('now')::timestamptz)
  and (l.scan_checked_at is null
       or l.scan_destination is distinct from l.destination_url
       or l.scan_checked_at < sqlc.arg('now')::timestamptz - interval '24 hours'
       or l.state = 'flagged')
order by
  (l.scan_checked_at is null or l.scan_destination is distinct from l.destination_url) desc,
  (l.state = 'flagged') desc,
  l.scan_checked_at asc nulls first,
  l.created_at,
  l.id
limit sqlc.arg('batch_limit');

-- CountDueLinksForScan repeats ListDueLinksForScan's predicate for the sweep's
-- `remaining`. Flagged links are due on every sweep, so this never reaches
-- zero while any link is blocked. Instance-wide, like the list.

-- name: CountDueLinksForScan :one
select count(*)
from link l
where l.state in ('active', 'flagged')
  and (l.expires_at is null or l.expires_at > sqlc.arg('now')::timestamptz)
  and (l.scan_checked_at is null
       or l.scan_destination is distinct from l.destination_url
       or l.scan_checked_at < sqlc.arg('now')::timestamptz - interval '24 hours'
       or l.state = 'flagged');

-- GetLinkForScan reads the one link a verdict is about to be applied to,
-- locked, inside applyVerdict's transaction. The lock is what makes "is this
-- still the URL that was checked?" and the write that follows one decision: a
-- PATCH changing the destination either waits for it, or this waits for the
-- PATCH and then sees the new URL and discards the verdict. hostname and slug
-- come along for the redirect-cache key. FOR UPDATE OF l rather than FOR
-- UPDATE: the domain row needs no lock, and locking it would serialize every
-- verdict on the shared domain behind every other.

-- name: GetLinkForScan :one
select l.id, l.team_id, l.destination_url, l.state, l.slug, d.hostname
from link l
join domain d on d.id = l.domain_id
where l.id = sqlc.arg('id') and l.team_id = sqlc.arg('team_id')
for update of l;

-- RecordLinkScan writes a completed check and the state its verdict leaves the
-- link in. updated_at is deliberately not touched: it is the dashboard's "last
-- changed by somebody", and a daily check of every link would make it mean
-- "last scanned" instead.

-- name: RecordLinkScan :exec
update link set
  state = sqlc.arg('state'),
  scan_checked_at = sqlc.arg('checked_at')::timestamptz,
  scan_destination = sqlc.arg('checked_destination')::text
where id = sqlc.arg('id') and team_id = sqlc.arg('team_id');

-- InsertLinkScanResult records a verdict change. link_scan_result has no
-- team_id column; its one caller inserts only for the link GetLinkForScan just
-- read and locked under that link's team_id, in the same transaction.

-- name: InsertLinkScanResult :exec
insert into link_scan_result (link_id, verdict, destination_url, threat_types, scanned_at)
values (sqlc.arg('link_id'), sqlc.arg('verdict'), sqlc.arg('destination_url'),
        sqlc.arg('threat_types'), sqlc.arg('scanned_at'));

-- GetLatestLinkScanResult backs the nested scan verdict on GET
-- /v1/links/{link_id}. Filtered by team through the link: the result table
-- carries no team_id of its own, and the rule that every read filters by
-- tenant has no exception for a table that happens not to.

-- name: GetLatestLinkScanResult :one
select r.verdict, r.threat_types, r.destination_url, r.scanned_at
from link_scan_result r
join link l on l.id = r.link_id
where r.link_id = sqlc.arg('link_id') and l.team_id = sqlc.arg('team_id')
order by r.scanned_at desc, r.id desc
limit 1;
```

In `apps/api/internal/db/queries/link_crud.sql`, replace `GetLinkForAPI` (lines 30-37) with the version below, and add `GetLinkForAPIForUpdate` directly after it:

```sql
-- name: GetLinkForAPI :one
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.folder_id, l.created_by, l.created_at, l.updated_at,
       l.scan_checked_at, l.scan_destination
from link l
join domain d on d.id = l.domain_id
where l.id = $1 and l.team_id = $2;

-- GetLinkForAPIForUpdate is GetLinkForAPI, copied verbatim and locked, for
-- updateLink. That handler writes state back from this read, and the Safe
-- Browsing scanner may flag the link while the PATCH's transaction is open:
-- unlocked, the PATCH would write the flag straight back to active. FOR UPDATE
-- OF l, so the domain row stays unlocked.

-- name: GetLinkForAPIForUpdate :one
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.folder_id, l.created_by, l.created_at, l.updated_at,
       l.scan_checked_at, l.scan_destination
from link l
join domain d on d.id = l.domain_id
where l.id = $1 and l.team_id = $2
for update of l;
```

- [ ] **Step 6: Generate and check the names**

Run: `cd apps/api && sqlc generate && grep -n "type ListDueLinksForScanParams\|type RecordLinkScanParams\|type InsertLinkScanResultParams\|type GetLinkForScanRow\|type GetLatestLinkScanResultRow" -A 8 internal/db/link_scan.sql.go && grep -n "ScanCheckedAt\|ScanDestination" internal/db/link_crud.sql.go`

Expected: `internal/db/link_scan.sql.go` exists; the structs carry exactly the field names listed under **Interfaces** (`Now`, `BatchLimit int32`, `CheckedAt time.Time`, `CheckedDestination string`, `DestinationURL`, `ThreatTypes []string`, `ScannedAt time.Time`, `Hostname`); `GetLinkForAPIRow` and `GetLinkForAPIForUpdateRow` both carry `ScanCheckedAt *time.Time` and `ScanDestination *string`; `models.go` gains the four columns. If sqlc spelled any field differently, use its spelling in the tests and tell later tasks' implementers by editing their **Interfaces** blocks in this plan.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/db/ -run 'LinkScan|DueLinks|LinkForScan|LinkForAPIReports' -count=1 -v`

Expected: PASS, all seven tests.

- [ ] **Step 8: Run the whole module's build and the db package**

Run: `cd apps/api && go build ./... && go test ./internal/db/ -count=1`

Expected: builds; `ok  github.com/mheob/kurze-url/apps/api/internal/db`. `internal/api` still compiles because nothing reads the new fields yet.

- [ ] **Step 9: Record the EXPLAIN that justifies "no index"**

Run (the container name follows `project_id = "kurze-url"` in `supabase/config.toml`):

```bash
docker exec -i supabase_db_kurze-url psql -U postgres -d postgres <<'SQL'
explain select l.id, l.team_id, l.destination_url
from link l
where l.state in ('active', 'flagged')
  and (l.expires_at is null or l.expires_at > now())
  and (l.scan_checked_at is null
       or l.scan_destination is distinct from l.destination_url
       or l.scan_checked_at < now() - interval '24 hours'
       or l.state = 'flagged')
order by
  (l.scan_checked_at is null or l.scan_destination is distinct from l.destination_url) desc,
  (l.state = 'flagged') desc,
  l.scan_checked_at asc nulls first, l.created_at, l.id
limit 200;
SQL
```

Expected: a `Limit` over a `Sort` over `Seq Scan on link l` whose `Filter:` contains `(scan_destination IS DISTINCT FROM destination_url)`. Nothing to change; the comment above `ListDueLinksForScan` already says why.

- [ ] **Step 10: Format, vet and commit**

`apps/web/src/routeTree.gen.ts` must not appear in `but diff` in this or any later task.

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: no gofmt output, no vet or lint findings, and `but diff` lists the migration, the two query files, the test file and the three regenerated `internal/db` files. Copy their IDs from the first column, then:

```bash
but commit -b feat/safe-browsing -m "feat(db): record safe browsing checks" <ids>
```

---

### Task 2: Canonicalization and expressions

**Files:**

- Create: `apps/api/internal/scanning/scanning.go`
- Create: `apps/api/internal/scanning/canonical.go`
- Test: `apps/api/internal/scanning/canonical_test.go`

**Interfaces:**

- Consumes: `golang.org/x/net/idna` (already a direct dependency through `golang.org/x/net v0.59.0`).
- Produces, in package `scanning`:
  - `func Canonicalize(raw string) (string, error)` — Google's canonical form, scheme included, so the published test vectors read back verbatim.
  - `func Expressions(raw string) ([]string, error)` — the host-suffix/path-prefix expressions, at most 30.
  - `var ErrNoHost error`
  - unexported, used by Task 3: `type canonicalURL`, `func canonicalize(raw string) (canonicalURL, error)`, `func (c canonicalURL) expressions() []string`

- [ ] **Step 1: Write the failing tests**

Google's canonicalization examples are published on its "URLs and Hashing" page. The vectors below are the ones Google has published since the v3 protocol and repeats for v4; open `https://developers.google.com/safe-browsing/reference/URLs.Hashing` (the v5 page) in a browser and compare. If the v5 page lists a vector that is not here, add it to `TestCanonicalizeMatchesGooglesExamples` verbatim; if one here is missing from the page, keep it (it is still Google's) and mention it in the task report. Do the same for the three expression examples, and check what the v5 page says about IPv6 hosts: this plan normalizes them to RFC 5952 inside brackets, which is what browsers put in the address bar, but no vector of Google's pins it.

Create `apps/api/internal/scanning/canonical_test.go`:

```go
package scanning_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// Google's own canonicalization examples, copied verbatim. They are the only
// specification of the edge cases that counts: a destination canonicalized
// differently from the way Google canonicalized its list entry hashes to a
// different value, and a listed phishing site then passes as clean with no
// error anywhere.
func TestCanonicalizeMatchesGooglesExamples(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://host/%25%32%35", "http://host/%25"},
		{"http://host/%25%32%35%25%32%35", "http://host/%25%25"},
		{"http://host/%2525252525252525", "http://host/%25"},
		{"http://host/asdf%25%32%35asd", "http://host/asdf%25asd"},
		{"http://host/%%%25%32%35asd%%", "http://host/%25%25%25asd%25%25"},
		{"http://www.google.com/", "http://www.google.com/"},
		{
			"http://%31%36%38%2e%31%38%38%2e%39%39%2e%32%36/%2E%73%65%63%75%72%65/%77%77%77%2E%65%62%61%79%2E%63%6F%6D/",
			"http://168.188.99.26/.secure/www.ebay.com/",
		},
		{
			"http://195.127.0.11/uploads/%20%20%20%20/.verify/.eBaysecure=updateuserdataxplimnbqmn-xplmvalidateinfoswqpcmlx=hgplmcx/",
			"http://195.127.0.11/uploads/%20%20%20%20/.verify/.eBaysecure=updateuserdataxplimnbqmn-xplmvalidateinfoswqpcmlx=hgplmcx/",
		},
		{
			"http://host%23.com/%257Ea%2521b%2540c%2523d%2524e%25f%255E00%252611%252A22%252833%252944_55%252B",
			"http://host%23.com/~a!b@c%23d$e%25f^00&11*22(33)44_55+",
		},
		{"http://3279880203/blah", "http://195.127.0.11/blah"},
		{"http://www.google.com/blah/..", "http://www.google.com/"},
		{"www.google.com/", "http://www.google.com/"},
		{"www.google.com", "http://www.google.com/"},
		{"http://www.evil.com/blah#frag", "http://www.evil.com/blah"},
		{"http://www.GOOgle.com/", "http://www.google.com/"},
		{"http://www.google.com.../", "http://www.google.com/"},
		{"http://www.google.com/foo\tbar\rbaz\n2", "http://www.google.com/foobarbaz2"},
		{"http://www.google.com/q?", "http://www.google.com/q?"},
		{"http://www.google.com/q?r?", "http://www.google.com/q?r?"},
		{"http://www.google.com/q?r?s", "http://www.google.com/q?r?s"},
		{"http://evil.com/foo#bar#baz", "http://evil.com/foo"},
		{"http://evil.com/foo;", "http://evil.com/foo;"},
		{"http://evil.com/foo?bar;", "http://evil.com/foo?bar;"},
		{"http://\x01\x80.com/", "http://%01%80.com/"},
		{"http://notrailingslash.com", "http://notrailingslash.com/"},
		{"http://www.gotaport.com:1234/", "http://www.gotaport.com/"},
		{"  http://www.google.com/  ", "http://www.google.com/"},
		{"http:// leadingspace.com/", "http://%20leadingspace.com/"},
		{"http://%20leadingspace.com/", "http://%20leadingspace.com/"},
		{"%20leadingspace.com/", "http://%20leadingspace.com/"},
		{"https://www.securesite.com/", "https://www.securesite.com/"},
		{"http://host.com/ab%23cd", "http://host.com/ab%23cd"},
		{"http://host.com//twoslashes?more//slashes", "http://host.com/twoslashes?more//slashes"},
	} {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			got, err := scanning.Canonicalize(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// The spellings a Verein actually types. Google's table has no IDN, no
// userinfo, no IPv6 and no query with a percent-encoded umlaut, and each of
// those is a way for a listed host to slip past unhashed.
func TestCanonicalizeRealWorldDestinations(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// Google's rules: an internationalized domain name becomes Punycode.
		{"https://Bücher.de/", "https://xn--bcher-kva.de/"},
		{"https://BÜCHER.DE/Über", "https://xn--bcher-kva.de/%C3%9Cber"},
		{"https://WWW.Verein.DE/Mitglieder", "https://www.verein.de/Mitglieder"},
		{"https://%76erein.de/", "https://verein.de/"},
		{"https://kasse:geheim@verein.de/", "https://verein.de/"},
		{"https://verein.de:8443/x", "https://verein.de/x"},
		{"https://verein.de./x", "https://verein.de/x"},
		{"https://[2001:DB8:0:0::1]:443/x", "https://[2001:db8::1]/x"},
		{"https://verein.de/a/./b/../c", "https://verein.de/a/c"},
		{"https://verein.de/anmeldung?name=M%C3%BCller#oben", "https://verein.de/anmeldung?name=M%C3%BCller"},
		{"https://0x7f.1/", "https://127.0.0.1/"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scanning.Canonicalize(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCanonicalizeRejectsAURLWithoutAHost(t *testing.T) {
	_, err := scanning.Canonicalize("https:///nohost")
	require.ErrorIs(t, err, scanning.ErrNoHost)
}

// Google's own expression examples. The order is not part of the protocol —
// every expression is hashed and looked up — so only the set is compared.
func TestExpressionsMatchGooglesExamples(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"http://a.b.c/1/2.html?param=1", []string{
			"a.b.c/1/2.html?param=1", "a.b.c/1/2.html", "a.b.c/", "a.b.c/1/",
			"b.c/1/2.html?param=1", "b.c/1/2.html", "b.c/", "b.c/1/",
		}},
		{"http://a.b.c.d.e.f.g/1.html", []string{
			"a.b.c.d.e.f.g/1.html", "a.b.c.d.e.f.g/",
			"c.d.e.f.g/1.html", "c.d.e.f.g/",
			"d.e.f.g/1.html", "d.e.f.g/",
			"e.f.g/1.html", "e.f.g/",
			"f.g/1.html", "f.g/",
		}},
		{"http://1.2.3.4/1/", []string{"1.2.3.4/1/", "1.2.3.4/"}},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := scanning.Expressions(tc.in)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

// Five hosts times six paths is the ceiling: the exact host plus up to four
// suffixes from the last five labels, never the bare top-level domain; the
// path with and without its query, the root, and up to three directories.
func TestExpressionsNeverExceedThirty(t *testing.T) {
	got, err := scanning.Expressions("https://a.b.c.d.e.f.g/1/2/3/4/5.html?q=1")
	require.NoError(t, err)
	require.Len(t, got, 30)
	require.Contains(t, got, "a.b.c.d.e.f.g/1/2/3/4/5.html?q=1")
	require.Contains(t, got, "f.g/1/2/3/")
	require.NotContains(t, got, "b.c.d.e.f.g/", "only the last five labels are walked")
	require.NotContains(t, got, "g/", "the bare top-level domain is never an expression")
	require.NotContains(t, got, "a.b.c.d.e.f.g/1/2/3/4/", "at most three directories below the root")
}

// An address has no parent domain to walk up to.
func TestExpressionsCheckAnIPHostOnlyExactly(t *testing.T) {
	got, err := scanning.Expressions("https://203.0.113.7/a/b.html?x=1")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"203.0.113.7/a/b.html?x=1", "203.0.113.7/a/b.html", "203.0.113.7/", "203.0.113.7/a/",
	}, got)

	got, err = scanning.Expressions("https://[2001:db8::1]/a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"[2001:db8::1]/a", "[2001:db8::1]/"}, got)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/scanning/ -count=1`

Expected: FAIL with `no non-test Go files` or `undefined: scanning.Canonicalize`.

- [ ] **Step 3: Write the package doc**

Create `apps/api/internal/scanning/scanning.go`:

```go
// Package scanning checks link destinations against Google Safe Browsing, the
// one package in this module that talks to it.
//
// It uses the v5 hashes.search method, never urls.search: only the first four
// bytes of the SHA-256 of each canonicalized URL expression leave this
// process, never a URL. A destination can carry personal data — a prefilled
// form, a member's own page — and Google's terms let it reuse and share URLs
// sent to urls.search, but not hash prefixes. The price is that this package
// canonicalizes, expands, hashes and compares full hashes itself, exactly as
// Google's "URLs and Hashing" rules describe.
package scanning
```

- [ ] **Step 4: Write the canonicalizer**

Create `apps/api/internal/scanning/canonical.go`:

```go
package scanning

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// ErrNoHost means a URL has no host to check. destination.Validate refuses
// every such destination, so meeting it means a caller passed something that
// was never a link destination.
var ErrNoHost = errors.New("scanning: url has no host")

// canonicalURL is a URL in the form Safe Browsing hashes, split into the parts
// expressions are built from. host, path and query are already
// percent-escaped. scheme plays no part in any expression; it is kept only so
// Canonicalize can print Google's test vectors back.
type canonicalURL struct {
	scheme   string
	host     string
	path     string
	query    string
	hasQuery bool
}

func (c canonicalURL) String() string {
	out := c.scheme + "://" + c.host + c.path
	if c.hasQuery {
		out += "?" + c.query
	}
	return out
}

// Canonicalize returns raw in the canonical form Google's Safe Browsing rules
// define. It exists for the test vectors; Check uses the parts directly.
func Canonicalize(raw string) (string, error) {
	c, err := canonicalize(raw)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}

// Expressions returns the host-suffix/path-prefix expressions Google's rules
// derive from raw: at most five hosts times six paths. Each one is hashed and
// looked up on its own, so a list entry for a whole host or a directory
// matches every URL below it.
func Expressions(raw string) ([]string, error) {
	c, err := canonicalize(raw)
	if err != nil {
		return nil, err
	}
	return c.expressions(), nil
}

// controlStripper removes the three characters Google's rules drop wherever
// they appear. A strings.Replacer rather than strings.Map, because Map decodes
// runes and turns an invalid UTF-8 byte into U+FFFD, and the raw bytes are
// exactly what gets hashed: Google's vector "http://\x01\x80.com/" depends on
// the 0x80 surviving as itself.
var controlStripper = strings.NewReplacer("\t", "", "\r", "", "\n", "")

// canonicalize follows Google's rules in their order: strip whitespace and
// the three control characters, drop the fragment, percent-unescape until
// nothing changes, then normalize the host and the path separately and
// re-escape all three parts. The unescape comes before the URL is split, so a
// host spelled with escapes ("%31%36%38%2e…") is read as the host it spells,
// as Google's vectors require; net/url refuses such a host outright, which is
// why the split here is by hand.
func canonicalize(raw string) (canonicalURL, error) {
	s := controlStripper.Replace(strings.TrimSpace(raw))
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = unescapeFully(s)

	c := canonicalURL{scheme: "http"}
	if i := strings.Index(s, "://"); i > 0 && isScheme(s[:i]) {
		c.scheme = asciiLower(s[:i])
		s = s[i+len("://"):]
	}

	authority, rest := s, ""
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		authority, rest = s[:i], s[i:]
	}
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	host := canonicalHost(withoutPort(authority))
	if host == "" {
		return canonicalURL{}, ErrNoHost
	}
	c.host = escape(host)

	path := rest
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		path, c.query, c.hasQuery = rest[:i], escape(rest[i+1:]), true
	}
	c.path = escape(canonicalPath(path))
	return c, nil
}

// isScheme reports whether s is an RFC 3986 scheme. A URL without one is read
// as http, as Google's rules say; "www.google.com/" is one of its vectors.
func isScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// withoutPort drops a port, keeping an IPv6 literal's brackets intact.
func withoutPort(authority string) string {
	if strings.HasPrefix(authority, "[") {
		if i := strings.IndexByte(authority, ']'); i >= 0 {
			return authority[:i+1]
		}
		return authority
	}
	if i := strings.LastIndexByte(authority, ':'); i >= 0 {
		return authority[:i]
	}
	return authority
}

// canonicalHost applies Google's host rules — leading and trailing dots
// stripped, runs of dots collapsed, any legal IPv4 spelling normalized,
// everything lowercased — plus the one their rules state in prose: an
// internationalized name becomes Punycode, which is the form a browser opens
// and Google lists. A host that is not valid UTF-8, or that IDNA refuses, is
// left as it is and percent-escaped later, which is what Google's
// "\x01\x80.com" vector expects. An IPv6 literal is normalized to RFC 5952,
// the form a browser's address bar shows.
func canonicalHost(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if addr, err := netip.ParseAddr(host[1 : len(host)-1]); err == nil && addr.Is6() {
			return "[" + addr.String() + "]"
		}
		return asciiLower(host)
	}

	host = strings.Trim(host, ".")
	for strings.Contains(host, "..") {
		host = strings.ReplaceAll(host, "..", ".")
	}
	if !isASCII(host) && utf8.ValidString(host) {
		if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			host = ascii
		}
	}
	if ip, ok := parseIPv4(host); ok {
		return ip
	}
	return asciiLower(host)
}

// parseIPv4 reads host the way inet_aton does — one to four parts, each
// decimal, octal with a leading 0, or hexadecimal with 0x, the last part
// filling every byte the earlier ones left — because Google's rules tell
// clients to accept any legal encoding, and a browser opens all of them.
func parseIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if host == "" || len(parts) > 4 {
		return "", false
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		value, ok := parseIPv4Part(part)
		if !ok {
			return "", false
		}
		values[i] = value
	}

	var addr uint64
	for i, value := range values[:len(values)-1] {
		if value > 0xff {
			return "", false
		}
		addr |= value << (8 * (3 - i))
	}
	last := values[len(values)-1]
	if last >= 1<<(8*(5-len(values))) {
		return "", false
	}
	addr |= last

	return netip.AddrFrom4([4]byte{
		byte(addr >> 24), byte(addr >> 16), byte(addr >> 8), byte(addr),
	}).String(), true
}

func parseIPv4Part(part string) (uint64, bool) {
	base := 10
	switch {
	case len(part) > 2 && (part[:2] == "0x" || part[:2] == "0X"):
		base, part = 16, part[2:]
	case len(part) > 1 && part[0] == '0':
		base, part = 8, part[1:]
	}
	value, err := strconv.ParseUint(part, base, 32)
	return value, err == nil
}

// canonicalPath resolves "/./" and "/../" and collapses runs of slashes. The
// query is never passed here: Google's rules leave its slashes alone, as the
// "?more//slashes" vector shows.
func canonicalPath(path string) string {
	if path == "" {
		return "/"
	}
	trailing := strings.HasSuffix(path, "/") ||
		strings.HasSuffix(path, "/.") || strings.HasSuffix(path, "/..")

	var segments []string
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}

	out := "/" + strings.Join(segments, "/")
	if trailing && len(segments) > 0 {
		out += "/"
	}
	return out
}

// unescapeFully percent-unescapes until nothing changes, Google's
// "repeatedly unescape" rule. Every pass that changes the string makes it
// shorter, so it terminates.
func unescapeFully(s string) string {
	for {
		next := unescapeOnce(s)
		if next == s {
			return s
		}
		s = next
	}
}

// unescapeOnce decodes every well-formed %XX once and leaves a stray % as it
// is, which is what lets "%%%25%32%35asd%%" settle into Google's expected
// "%%%asd%%" before re-escaping.
func unescapeOnce(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escape percent-escapes every byte at or below 0x20, at or above 0x7F, '#'
// and '%', in uppercase hex, and nothing else — Google's rule, which is not
// net/url's.
func escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || c == '#' || c == '%' {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiLower lowercases A to Z only. strings.ToLower would rewrite an invalid
// UTF-8 byte as U+FFFD, and those bytes are hashed as they are.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// expressions pairs every host suffix with every path prefix.
func (c canonicalURL) expressions() []string {
	hosts := hostSuffixes(c.host)
	paths := pathPrefixes(c.path, c.query)
	out := make([]string, 0, len(hosts)*len(paths))
	for _, host := range hosts {
		for _, path := range paths {
			out = append(out, host+path)
		}
	}
	return out
}

// hostSuffixes is the exact host plus up to four more, formed from the last
// five labels by dropping the leading one each time, never the bare
// top-level domain. An address is checked only as itself.
func hostSuffixes(host string) []string {
	if isIPHost(host) {
		return []string{host}
	}
	suffixes := []string{host}
	labels := strings.Split(host, ".")
	for i := max(1, len(labels)-5); i < len(labels)-1; i++ {
		suffixes = append(suffixes, strings.Join(labels[i:], "."))
	}
	return suffixes
}

func isIPHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is4()
}

// pathPrefixes is the path with its query (when there is one), the path
// without it, the root, and up to three directories below the root, each with
// its trailing slash.
func pathPrefixes(path, query string) []string {
	var prefixes []string
	add := func(p string) {
		if !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	if query != "" {
		add(path + "?" + query)
	}
	add(path)
	add("/")

	directories := strings.Split(strings.TrimPrefix(path, "/"), "/")
	prefix := "/"
	for i := 0; i < len(directories)-1 && i < 3; i++ {
		prefix += directories[i] + "/"
		add(prefix)
	}
	return prefixes
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/scanning/ -count=1 -v`

Expected: PASS, every subtest. A vector that fails names its input in the subtest name; fix the function it exercises, never the vector.

- [ ] **Step 6: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: clean; `but diff` lists the three files under **Files**. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): canonicalize urls for safe browsing" <ids>
```

---

### Task 3: The `hashes.search` client, configuration and wiring

**Files:**

- Modify: `apps/api/internal/scanning/scanning.go` (Task 2's package doc gains the types)
- Create: `apps/api/internal/scanning/client.go`
- Create: `apps/api/internal/scanning/export_test.go`
- Test: `apps/api/internal/scanning/client_test.go`
- Modify: `apps/api/internal/config/config.go:143-147` (after `RetentionToken`) and `:265` (load it)
- Test: `apps/api/internal/config/config_test.go` (append)
- Modify: `apps/api/internal/api/api.go:44-45` (add `Scanner` after `DomainVerifier`)
- Modify: `apps/api/cmd/api/main.go:34-61` (`sentryCoalesceRules`) and `:262-264` (wiring, after the invitations block)
- Test: `apps/api/cmd/api/main_test.go` (append)
- Modify: `apps/api/.env.example` (after `RETENTION_TOKEN=`)

**Interfaces:**

- Consumes: `canonicalize`, `canonicalURL.expressions()` (Task 2).
- Produces, in package `scanning`:
  - `type Result struct { ThreatTypes []string; ValidFor time.Duration }`
  - `type Checker interface { Check(ctx context.Context, urls []string) (map[string]Result, error) }` — a URL missing from the map has no verdict and must never be read as clean.
  - `var ErrQuotaExceeded error`; `func QuotaExceeded(err error) bool`
  - `type Client`; `func NewClient(apiKey string) *Client`; `(*Client).Check` implements `Checker`
- Produces in `config`: `Config.SafeBrowsingAPIKey string`.
- Produces in `api`: `Deps.Scanner scanning.Checker` (nil means scanning is off).

- [ ] **Step 1: Write the failing client tests**

Create `apps/api/internal/scanning/export_test.go`:

```go
package scanning

import "time"

// This file is compiled only into this package's tests.

// NewClientForTest points a Client at a test server, with a timeout short
// enough for a test to wait out.
func NewClientForTest(apiKey, endpoint string, timeout time.Duration) *Client {
	c := NewClient(apiKey)
	c.endpoint = endpoint
	c.http.Timeout = timeout
	return c
}

// RequestTimeoutForTest is the timeout NewClient sets.
const RequestTimeoutForTest = requestTimeout

// MaxPrefixesPerRequestForTest is where Check splits a lookup.
const MaxPrefixesPerRequestForTest = maxPrefixesPerRequest
```

Create `apps/api/internal/scanning/client_test.go`:

```go
package scanning_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

const testKey = "not-a-key"

type detail struct {
	ThreatType string   `json:"threatType"`
	Attributes []string `json:"attributes,omitempty"`
}

type fullHash struct {
	FullHash        string   `json:"fullHash"`
	FullHashDetails []detail `json:"fullHashDetails"`
}

// fakeGoogle stands in for hashes.search. Like the real endpoint, it answers a
// request with every listed full hash whose four-byte prefix the request asked
// for — which is exactly how one URL's lookup comes back carrying a hash that
// belongs to a neighbour's expression, or to nobody's.
type fakeGoogle struct {
	mu            sync.Mutex
	listed        map[[sha256.Size]byte][]detail
	cacheDuration string
	status        int
	body          string
	delay         time.Duration
	requests      []*http.Request
}

func newFakeGoogle(t *testing.T) (*fakeGoogle, *scanning.Client) {
	t.Helper()
	g := &fakeGoogle{listed: map[[sha256.Size]byte][]detail{}, cacheDuration: "300s"}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	return g, scanning.NewClientForTest(testKey, server.URL+"/v5/hashes:search", time.Second)
}

// list puts an expression on the fake list, the way Google lists a URL
// pattern: by the SHA-256 of its canonical expression.
func (g *fakeGoogle) list(expression string, details ...detail) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listed[sha256.Sum256([]byte(expression))] = details
}

// listCollision lists a full hash that shares expression's four-byte prefix
// and differs after it: a prefix hit that is not a match.
func (g *fakeGoogle) listCollision(expression string, details ...detail) {
	sum := sha256.Sum256([]byte(expression))
	sum[sha256.Size-1] ^= 0xff
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listed[sum] = details
}

// configure changes how the fake answers, under its lock: the server reads
// these fields on its own goroutine.
func (g *fakeGoogle) configure(change func(*fakeGoogle)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	change(g)
}

func (g *fakeGoogle) recorded() []*http.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.requests)
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r.Clone(context.Background()))
	status, body, delay, cacheDuration := g.status, g.body, g.delay, g.cacheDuration
	listed := maps.Clone(g.listed)
	g.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}

	asked := map[[4]byte]bool{}
	for _, encoded := range r.URL.Query()["hashPrefixes"] {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != 4 {
			http.Error(w, "bad prefix", http.StatusBadRequest)
			return
		}
		asked[[4]byte(raw)] = true
	}

	answer := struct {
		FullHashes    []fullHash `json:"fullHashes,omitempty"`
		CacheDuration string     `json:"cacheDuration,omitempty"`
	}{CacheDuration: cacheDuration}
	for sum, details := range listed {
		if asked[[4]byte(sum[:4])] {
			answer.FullHashes = append(answer.FullHashes, fullHash{
				FullHash:        base64.StdEncoding.EncodeToString(sum[:]),
				FullHashDetails: details,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func TestCheckSendsTheKeyInAHeaderAndNeverInTheURL(t *testing.T) {
	g, client := newFakeGoogle(t)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.NoError(t, err)

	requests := g.recorded()
	require.Len(t, requests, 1)
	require.Equal(t, testKey, requests[0].Header.Get("X-Goog-Api-Key"))
	require.NotContains(t, requests[0].URL.String(), testKey)
	require.Empty(t, requests[0].URL.Query().Get("key"))
}

// *url.Error prints the request URL into its text, and error text reaches the
// logs and Sentry. That is the whole reason the key is a header.
func TestCheckKeepsTheKeyOutOfItsErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL + "/v5/hashes:search"
	server.Close()
	client := scanning.NewClientForTest(testKey, endpoint, time.Second)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), testKey)
}

func TestCheckSendsOnlyFourBytePrefixes(t *testing.T) {
	g, client := newFakeGoogle(t)

	_, err := client.Check(context.Background(),
		[]string{"https://geheim.verein.test/mitglieder?token=abc"})
	require.NoError(t, err)

	query := g.recorded()[0].URL.Query()
	require.Equal(t, []string{"hashPrefixes"}, slices.Collect(maps.Keys(query)))
	for _, encoded := range query["hashPrefixes"] {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		require.Len(t, raw, 4)
	}
	raw := g.recorded()[0].URL.RawQuery
	require.NotContains(t, raw, "geheim")
	require.NotContains(t, raw, "mitglieder")
}

// 200 URLs with seven unique expressions each and five shared make 1,405
// prefixes, which needs two requests at the limit.
func TestCheckSplitsAtTheRequestLimitAndSendsEachPrefixOnce(t *testing.T) {
	g, client := newFakeGoogle(t)
	urls := make([]string, 0, 200)
	for i := range 200 {
		urls = append(urls, fmt.Sprintf("https://h%d.example.org/a/b/c/d.html?q=%d", i, i))
	}

	results, err := client.Check(context.Background(), urls)
	require.NoError(t, err)
	require.Len(t, results, 200)

	limit := scanning.MaxPrefixesPerRequestForTest
	seen := map[string]bool{}
	for _, request := range g.recorded() {
		prefixes := request.URL.Query()["hashPrefixes"]
		require.LessOrEqual(t, len(prefixes), limit)
		for _, prefix := range prefixes {
			require.False(t, seen[prefix], "prefix %s sent twice", prefix)
			seen[prefix] = true
		}
	}
	require.Greater(t, len(seen), limit, "the input must need more than one request")
	require.Len(t, g.recorded(), (len(seen)+limit-1)/limit)
	for _, u := range urls {
		require.Empty(t, results[u].ThreatTypes)
	}
}

func TestCheckFlagsOnlyTheURLWhoseExpressionIsListed(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("shared.test/phishing.html", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{
		"https://shared.test/phishing.html",
		"https://shared.test/sommerfest",
		"https://other.test/",
	})
	require.NoError(t, err)

	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://shared.test/phishing.html"].ThreatTypes)
	require.Empty(t, results["https://shared.test/sommerfest"].ThreatTypes,
		"a neighbour on the same host shares prefixes, not the match")
	require.Empty(t, results["https://other.test/"].ThreatTypes)
}

// A listed host covers everything below it, subdomains included.
func TestCheckFlagsEveryURLBelowAListedHost(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("evil.test/", detail{ThreatType: "MALWARE"})

	results, err := client.Check(context.Background(),
		[]string{"https://evil.test/a", "https://www.evil.test/b?c=d"})
	require.NoError(t, err)

	require.Equal(t, []string{"MALWARE"}, results["https://evil.test/a"].ThreatTypes)
	require.Equal(t, []string{"MALWARE"}, results["https://www.evil.test/b?c=d"].ThreatTypes)
}

func TestCheckIgnoresAPrefixMatchWhoseFullHashDiffers(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.listCollision("verein.test/", detail{ThreatType: "MALWARE"})

	results, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.NoError(t, err)
	require.Empty(t, results["https://verein.test/"].ThreatTypes)
}

// CANARY means "do not enforce", and FRAME_ONLY means "enforce only on
// frames"; a top-level redirect is not a frame.
func TestCheckDoesNotEnforceCanaryOrFrameOnlyEntries(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("canary.test/", detail{ThreatType: "MALWARE", Attributes: []string{"CANARY"}})
	g.list("frame.test/", detail{ThreatType: "SOCIAL_ENGINEERING", Attributes: []string{"FRAME_ONLY"}})
	g.list("both.test/",
		detail{ThreatType: "MALWARE", Attributes: []string{"CANARY"}},
		detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(),
		[]string{"https://canary.test/", "https://frame.test/", "https://both.test/"})
	require.NoError(t, err)

	require.Empty(t, results["https://canary.test/"].ThreatTypes)
	require.Empty(t, results["https://frame.test/"].ThreatTypes)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://both.test/"].ThreatTypes)
}

// Google's reference tells clients to tolerate values they do not know.
// Dropping one would turn a report into "clean".
func TestCheckKeepsAThreatTypeItDoesNotKnow(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("neu.test/", detail{ThreatType: "THREAT_TYPE_FROM_THE_FUTURE"})

	results, err := client.Check(context.Background(), []string{"https://neu.test/"})
	require.NoError(t, err)
	require.Equal(t, []string{"THREAT_TYPE_FROM_THE_FUTURE"}, results["https://neu.test/"].ThreatTypes)
}

func TestCheckFindsAnInternationalizedHostUnderItsPunycode(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.list("xn--bcher-kva.de/", detail{ThreatType: "SOCIAL_ENGINEERING"})

	results, err := client.Check(context.Background(), []string{"https://Bücher.de/anmeldung"})
	require.NoError(t, err)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, results["https://Bücher.de/anmeldung"].ThreatTypes)
}

// A verdict whose validity cannot be read may not be relied on at all.
func TestCheckReadsTheCacheDuration(t *testing.T) {
	for _, tc := range []struct {
		cacheDuration string
		want          time.Duration
	}{
		{"300s", 5 * time.Minute},
		{"1.5s", 1500 * time.Millisecond},
		{"", 0},
		{"0s", 0},
		{"soon", 0},
		{"-5s", 0},
	} {
		t.Run(fmt.Sprintf("%q", tc.cacheDuration), func(t *testing.T) {
			g, client := newFakeGoogle(t)
			g.configure(func(g *fakeGoogle) { g.cacheDuration = tc.cacheDuration })

			results, err := client.Check(context.Background(), []string{"https://verein.test/"})
			require.NoError(t, err)
			require.Equal(t, tc.want, results["https://verein.test/"].ValidFor)
		})
	}
}

func TestCheckClassifiesGooglesRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		quota  bool
	}{
		{"429", http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, true},
		{"403 resource exhausted", http.StatusForbidden, `{"error":{"code":403,"status":"RESOURCE_EXHAUSTED"}}`, true},
		{"403 anything else", http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED","message":"secret detail"}}`, false},
		{"503", http.StatusServiceUnavailable, "secret detail", false},
		{"302 is not followed", http.StatusFound, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, client := newFakeGoogle(t)
			g.configure(func(g *fakeGoogle) { g.status, g.body = tc.status, tc.body })

			_, err := client.Check(context.Background(), []string{"https://verein.test/"})
			require.Error(t, err)
			require.Equal(t, tc.quota, scanning.QuotaExceeded(err))
			require.Contains(t, err.Error(), fmt.Sprint(tc.status))
			require.NotContains(t, err.Error(), "secret detail", "the body never reaches an error")
		})
	}
}

func TestCheckRefusesAnOversizedResponse(t *testing.T) {
	g, client := newFakeGoogle(t)
	g.configure(func(g *fakeGoogle) { g.status, g.body = http.StatusOK, strings.Repeat("a", 1<<20+1) })

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.ErrorContains(t, err, "exceeds")
}

func TestCheckGivesUpAfterItsTimeout(t *testing.T) {
	require.Equal(t, 5*time.Second, scanning.RequestTimeoutForTest)

	g := &fakeGoogle{listed: map[[sha256.Size]byte][]detail{}, delay: 300 * time.Millisecond}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	client := scanning.NewClientForTest(testKey, server.URL+"/v5/hashes:search", 50*time.Millisecond)

	_, err := client.Check(context.Background(), []string{"https://verein.test/"})
	require.Error(t, err)
}

func TestCheckWithNothingToCheckMakesNoRequest(t *testing.T) {
	g, client := newFakeGoogle(t)

	results, err := client.Check(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, results)

	// A URL without a host has no verdict: it is left out of the map, which
	// every caller reads as "not checked", never as clean.
	results, err = client.Check(context.Background(), []string{"https:///nohost"})
	require.NoError(t, err)
	require.NotContains(t, results, "https:///nohost")

	require.Empty(t, g.recorded())
}

func TestQuotaExceededMatchesAWrappedRefusal(t *testing.T) {
	wrapped := fmt.Errorf("sweep: %w", fmt.Errorf("%w: hashes.search answered 429", scanning.ErrQuotaExceeded))
	require.True(t, scanning.QuotaExceeded(wrapped))
	require.False(t, scanning.QuotaExceeded(errors.New("quota exceeded, but not ours")))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/scanning/ -count=1`

Expected: FAIL to compile with `undefined: NewClient` (in `export_test.go`) and `undefined: scanning.QuotaExceeded`.

- [ ] **Step 3: Add the types**

Replace `apps/api/internal/scanning/scanning.go` (Task 2 created it with the package doc only) with:

```go
// Package scanning checks link destinations against Google Safe Browsing, the
// one package in this module that talks to it.
//
// It uses the v5 hashes.search method, never urls.search: only the first four
// bytes of the SHA-256 of each canonicalized URL expression leave this
// process, never a URL. A destination can carry personal data — a prefilled
// form, a member's own page — and Google's terms let it reuse and share URLs
// sent to urls.search, but not hash prefixes. The price is that this package
// canonicalizes, expands, hashes and compares full hashes itself, exactly as
// Google's "URLs and Hashing" rules describe.
package scanning

import (
	"context"
	"errors"
	"time"
)

// Result is one URL's verdict.
type Result struct {
	// ThreatTypes are the threat types Google reports for the URL, sorted and
	// de-duplicated. Empty means clean. A value this package does not know is
	// kept as Google sent it: Google's reference tells clients to tolerate new
	// ones, and dropping one would turn a report into "clean".
	ThreatTypes []string
	// ValidFor is how long the verdict may be relied on, read from the
	// response's cacheDuration. Zero when Google sent none, or one this
	// package could not read: a verdict that may not be relied on at all is
	// the safe reading of either.
	ValidFor time.Duration
}

// Checker is the seam api.Deps holds. It is provider-neutral on purpose:
// Google's Web Risk API, the documented fallback should the non-commercial
// reading of Safe Browsing's terms ever fail, would be a second
// implementation of it, not a change to its callers.
type Checker interface {
	// Check returns one Result per URL it could judge. A URL missing from the
	// map has no verdict; never read that as clean.
	Check(ctx context.Context, urls []string) (map[string]Result, error)
}

// ErrQuotaExceeded means Google refused a lookup because the project's quota
// is spent. It is distinct from every other failure so cmd/api can report it
// to Sentry once an hour rather than once a minute per message: the condition
// does not clear by itself, and every flagged redirect and every sweep would
// otherwise report it again.
var ErrQuotaExceeded = errors.New("scanning: safe browsing quota exceeded")

// QuotaExceeded reports whether err is, or wraps, ErrQuotaExceeded. It is the
// observability.CoalesceRule matcher cmd/api registers, shaped like
// cache.QuotaExceeded beside it.
func QuotaExceeded(err error) bool {
	return errors.Is(err, ErrQuotaExceeded)
}
```

- [ ] **Step 4: Write the client**

Create `apps/api/internal/scanning/client.go`:

```go
package scanning

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	searchEndpoint = "https://safebrowsing.googleapis.com/v5/hashes:search"

	// maxPrefixesPerRequest keeps one request's URL short. hashes.search
	// itself accepts up to 1000 prefixes, but this is a GET: 1000 padded
	// base64 prefixes are about 26 KB of query string, past the 8 KB many
	// front ends refuse with 414. 250 keeps a request near 6.5 KB. One link
	// needs at most 30; the sweep's batch needs a few thousand, which this
	// splits.
	maxPrefixesPerRequest = 250

	// requestTimeout bounds one request. The callers bound the whole check
	// tighter where they must: two seconds on the redirect path, the sweep's
	// 25-second budget.
	requestTimeout = 5 * time.Second

	// maxResponseBytes caps what is read from Google. A response for 250
	// prefixes is a few kilobytes even when every one of them is listed.
	maxResponseBytes = 1 << 20

	// apiKeyHeader carries the key, never the URL: Go's *url.Error prints the
	// request URL into its text, and error text reaches the logs and Sentry.
	apiKeyHeader = "X-Goog-Api-Key"

	prefixSize = 4
)

// Client is the hashes.search implementation of Checker. Safe for concurrent
// use; build one per process.
type Client struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// NewClient builds a Client for one API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   apiKey,
		endpoint: searchEndpoint,
		http: &http.Client{
			Timeout: requestTimeout,
			// A redirect from Google's API is an error to report, not a
			// place to send the key.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type searchResponse struct {
	FullHashes []struct {
		FullHash        string `json:"fullHash"`
		FullHashDetails []struct {
			ThreatType string   `json:"threatType"`
			Attributes []string `json:"attributes"`
		} `json:"fullHashDetails"`
	} `json:"fullHashes"`
	CacheDuration string `json:"cacheDuration"`
}

// Check looks every URL up in as few requests as the prefix limit allows.
// Each URL's own expressions are hashed here, the prefixes of all of them are
// de-duplicated and sent, and a URL is reported for a threat type only when
// one of its own full hashes is among those Google returns: Google answers a
// prefix with every listed hash under it, so a returned hash proves nothing
// about a URL that merely shares the prefix.
func (c *Client) Check(ctx context.Context, urls []string) (map[string]Result, error) {
	results := make(map[string]Result, len(urls))

	own := make(map[string][][sha256.Size]byte, len(urls))
	seen := map[[prefixSize]byte]bool{}
	var prefixes []string
	for _, raw := range urls {
		if _, done := own[raw]; done {
			continue
		}
		canonical, err := canonicalize(raw)
		if err != nil {
			// No verdict for this one; see Checker.
			continue
		}
		hashes := fullHashes(canonical.expressions())
		own[raw] = hashes
		for _, hash := range hashes {
			prefix := [prefixSize]byte(hash[:prefixSize])
			if !seen[prefix] {
				seen[prefix] = true
				prefixes = append(prefixes, base64.StdEncoding.EncodeToString(prefix[:]))
			}
		}
	}
	if len(prefixes) == 0 {
		return results, nil
	}

	listed := map[[sha256.Size]byte][]string{}
	var validFor time.Duration
	for start := 0; start < len(prefixes); start += maxPrefixesPerRequest {
		response, err := c.search(ctx, prefixes[start:min(start+maxPrefixesPerRequest, len(prefixes))])
		if err != nil {
			return nil, err
		}
		// The shortest validity of all the requests: a URL's prefixes may
		// have been spread across several of them.
		if duration := parseCacheDuration(response.CacheDuration); start == 0 || duration < validFor {
			validFor = duration
		}
		for _, full := range response.FullHashes {
			hash, ok := decodeFullHash(full.FullHash)
			if !ok {
				continue
			}
			for _, detail := range full.FullHashDetails {
				if detail.ThreatType != "" && enforceable(detail.Attributes) {
					listed[hash] = append(listed[hash], detail.ThreatType)
				}
			}
		}
	}

	for raw, hashes := range own {
		var threats []string
		for _, hash := range hashes {
			threats = append(threats, listed[hash]...)
		}
		results[raw] = Result{ThreatTypes: uniqueSorted(threats), ValidFor: validFor}
	}
	return results, nil
}

func (c *Client) search(ctx context.Context, prefixes []string) (searchResponse, error) {
	query := url.Values{"hashPrefixes": prefixes}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return searchResponse{}, fmt.Errorf("scanning: build hashes.search request: %w", err)
	}
	req.Header.Set(apiKeyHeader, c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return searchResponse{}, fmt.Errorf("scanning: hashes.search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return searchResponse{}, fmt.Errorf("scanning: read hashes.search response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return searchResponse{}, fmt.Errorf("scanning: hashes.search response exceeds %d bytes", maxResponseBytes)
	}

	// The status, never the body: a body is Google's to word, and error text
	// reaches the logs and Sentry.
	if resp.StatusCode == http.StatusTooManyRequests || isResourceExhausted(resp.StatusCode, body) {
		return searchResponse{}, fmt.Errorf("%w: hashes.search answered %d", ErrQuotaExceeded, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return searchResponse{}, fmt.Errorf("scanning: hashes.search answered %d", resp.StatusCode)
	}

	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return searchResponse{}, fmt.Errorf("scanning: decode hashes.search response: %w", err)
	}
	return parsed, nil
}

// isResourceExhausted recognises the other shape a quota refusal takes in
// Google's APIs: a 403 whose google.rpc.Status is RESOURCE_EXHAUSTED.
func isResourceExhausted(status int, body []byte) bool {
	if status != http.StatusForbidden {
		return false
	}
	var problem struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &problem) == nil && problem.Error.Status == "RESOURCE_EXHAUSTED"
}

// enforceable reports whether a detail may block a top-level redirect.
// CANARY means "do not enforce"; FRAME_ONLY means "enforce only on frames",
// and a redirect is not a frame.
func enforceable(attributes []string) bool {
	for _, attribute := range attributes {
		if attribute == "CANARY" || attribute == "FRAME_ONLY" {
			return false
		}
	}
	return true
}

// parseCacheDuration reads a protobuf Duration ("300s", "1.5s"). Anything
// missing, unreadable or negative is zero: a verdict that may not be relied
// on at all.
func parseCacheDuration(value string) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration < 0 {
		return 0
	}
	return duration
}

// decodeFullHash accepts every base64 alphabet protobuf's JSON mapping allows.
func decodeFullHash(encoded string) ([sha256.Size]byte, bool) {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		raw, err := encoding.DecodeString(encoded)
		if err == nil && len(raw) == sha256.Size {
			return [sha256.Size]byte(raw), true
		}
	}
	return [sha256.Size]byte{}, false
}

func fullHashes(expressions []string) [][sha256.Size]byte {
	out := make([][sha256.Size]byte, 0, len(expressions))
	for _, expression := range expressions {
		out = append(out, sha256.Sum256([]byte(expression)))
	}
	return out
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}
```

- [ ] **Step 5: Run the client tests to verify they pass**

Run: `cd apps/api && go test ./internal/scanning/ -count=1 -v`

Expected: PASS, including Task 2's tests.

- [ ] **Step 6: Write the failing configuration and Sentry tests**

Append to `apps/api/internal/config/config_test.go`:

```go
// Optional like every external service here: unset turns scanning off, it does
// not stop the API starting.
func TestSafeBrowsingAPIKeyIsOptional(t *testing.T) {
	setRequired(t)
	t.Setenv("SAFE_BROWSING_API_KEY", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, cfg.SafeBrowsingAPIKey)

	t.Setenv("SAFE_BROWSING_API_KEY", "a-key")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.Equal(t, "a-key", cfg.SafeBrowsingAPIKey)
}
```

Append to `apps/api/cmd/api/main_test.go` (add `"github.com/mheob/kurze-url/apps/api/internal/scanning"` to its imports):

```go
// Google's refusal arrives wrapped by whichever caller logged it — the sweep,
// a redirect's re-check, an immediate check — and must land in its own hourly
// slot, not the Redis rule's.
func TestSafeBrowsingsQuotaRefusalIsCoalescedHourly(t *testing.T) {
	refusal := fmt.Errorf("safe browsing check failed: %w",
		fmt.Errorf("%w: hashes.search answered 429", scanning.ErrQuotaExceeded))

	var matched []observability.CoalesceRule
	for _, rule := range sentryCoalesceRules() {
		if rule.Match(refusal) {
			matched = append(matched, rule)
		}
	}

	require.Len(t, matched, 1)
	require.Equal(t, "safe browsing quota exhausted", matched[0].Key)
	require.Equal(t, safeBrowsingQuotaReportInterval, matched[0].Window)
}
```

- [ ] **Step 7: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/config/ ./cmd/api/ -count=1`

Expected: FAIL to compile with `cfg.SafeBrowsingAPIKey undefined` and `undefined: safeBrowsingQuotaReportInterval`.

- [ ] **Step 8: Add the configuration**

In `apps/api/internal/config/config.go`, after the `RetentionToken string` field (line 143), add:

```go

	// SafeBrowsingAPIKey authenticates hashes.search, the Google Safe Browsing
	// lookup internal/scanning makes. Empty turns scanning off: cmd/api logs a
	// warning at startup and leaves api.Deps.Scanner nil, POST /internal/scan
	// answers 503, and a flagged link answers the neutral 503 page instead of
	// the block page, because Google's terms forbid blocking on a verdict
	// older than thirty minutes. Optional like every external service here;
	// the redirect surface must start without it.
	SafeBrowsingAPIKey string
```

In `Load`, after `cfg.RetentionToken = os.Getenv("RETENTION_TOKEN")` (line 265), add:

```go
	cfg.SafeBrowsingAPIKey = os.Getenv("SAFE_BROWSING_API_KEY")
```

- [ ] **Step 9: Add the Deps field**

In `apps/api/internal/api/api.go`, add `"github.com/mheob/kurze-url/apps/api/internal/scanning"` to the imports and, after `DomainVerifier domainVerifier` (line 44), add:

```go
	// Scanner checks destinations against Google Safe Browsing. Nil means
	// scanning is off (SAFE_BROWSING_API_KEY is unset), and every caller reads
	// that as "no verdict", never as clean. Declared as scanning.Checker
	// rather than a local interface because that is the provider-neutral seam
	// the design names: Web Risk would be a second implementation of it.
	// scanning.NewClient's *Client is the production implementation, wired in
	// cmd/api/main.go.
	Scanner scanning.Checker
```

- [ ] **Step 10: Wire it in `cmd/api`**

In `apps/api/cmd/api/main.go`, add `"github.com/mheob/kurze-url/apps/api/internal/scanning"` to the imports. After `redisQuotaReportInterval` (line 45), add:

```go

// safeBrowsingQuotaReportInterval is how often each instance tells Sentry that
// Google is refusing Safe Browsing lookups because the project's quota is
// spent. An hour, for redisQuotaReportInterval's reason: the condition does
// not clear by itself, and every flagged redirect, every immediate check and
// every sweep would otherwise report it again.
const safeBrowsingQuotaReportInterval = time.Hour
```

Replace the body of `sentryCoalesceRules` with:

```go
	return []observability.CoalesceRule{
		{
			Key:    "redis quota exhausted",
			Match:  cache.QuotaExceeded,
			Window: redisQuotaReportInterval,
		},
		{
			Key:    "safe browsing quota exhausted",
			Match:  scanning.QuotaExceeded,
			Window: safeBrowsingQuotaReportInterval,
		},
	}
```

After the invitations block (the `if cfg.SupabaseServiceRoleKey != "" { … } else { … }` that ends at line 264), add:

```go

	// Scanning is optional at startup, like invitations: without a key the
	// API runs, nothing is scanned, and the consequences are the ones
	// config.Config.SafeBrowsingAPIKey lists. Built once here, not per
	// request, so its HTTP client's connections are reused.
	if cfg.SafeBrowsingAPIKey != "" {
		deps.Scanner = scanning.NewClient(cfg.SafeBrowsingAPIKey)
		log.Info("safe browsing scanning enabled")
	} else {
		log.Warn("SAFE_BROWSING_API_KEY is unset — destinations are not scanned, " +
			"POST /internal/scan answers 503, and a flagged link answers 503")
	}
```

- [ ] **Step 11: Document the variable**

In `apps/api/.env.example`, after the `RETENTION_TOKEN=` line, add:

```
# Google Safe Browsing, v5 hashes.search: only 4-byte SHA-256 prefixes of a
# destination's canonical expressions are sent, never the URL. Create it in a
# Google Cloud project with the Safe Browsing API enabled, and restrict the
# key to that one API. It travels in the X-Goog-Api-Key header, never in a URL,
# because Go prints a request's URL into its errors and errors reach Sentry.
#
# Unset turns scanning off: startup logs a warning, POST /internal/scan answers
# 503, and a flagged link answers a neutral 503 page instead of the block page,
# because Google's terms forbid blocking on a verdict older than thirty
# minutes. Set it on Production and Preview.
SAFE_BROWSING_API_KEY=
```

- [ ] **Step 12: Run everything this task touched**

Run: `cd apps/api && go test ./internal/scanning/ ./internal/config/ ./cmd/api/ -count=1 && go build ./...`

Expected: `ok` for all three packages; the build succeeds.

- [ ] **Step 13: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: clean; `but diff` lists exactly the files under **Files**. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): add the safe browsing client" <ids>
```

---

### Task 4: A system actor and two audit actions

**Files:**

- Modify: `apps/api/internal/audit/audit.go:52-53` (add the actions after the password group), `:81-89` (add `ErrActorMismatch`), `:91-113` (`knownActions`), `:136-144` (`Entry`), `:158-187` (`Log`)
- Modify: `apps/api/internal/audit/audit_test.go`, `apps/api/internal/audit/audit_denylist_test.go` (`ActorUserID: userID` becomes `&userID`; new tests)
- Modify: every `audit.Entry` literal in `apps/api/internal/api/`: `members.go:221,402,459`, `tags.go:153,242,282`, `link_password.go:108,174`, `links.go:558,988,1078`, `teams.go:152,269`, `domains.go:257,417,566`, `folders.go:155,247,302`

**Interfaces:**

- Consumes: nothing new.
- Produces, in package `audit`:
  - `const ActionLinkFlagged Action = "link.flagged"`, `const ActionLinkUnflagged Action = "link.unflagged"`
  - `Entry.ActorUserID *uuid.UUID` — nil is the system, allowed only for the two actions above
  - `var ErrActorMismatch error`

- [ ] **Step 1: Point every existing caller at its user**

Run:

```bash
cd apps/api
sed -i '' -E 's/ActorUserID: (member|actor|claims)\.UserID,/ActorUserID: \&\1.UserID,/' internal/api/*.go
sed -i '' -E 's/ActorUserID: userID,/ActorUserID: \&userID,/' internal/audit/audit_test.go internal/audit/audit_denylist_test.go
grep -rn "ActorUserID: " internal/api/*.go internal/audit/*_test.go | grep -v "ActorUserID: &" | grep -v "auditlog.go"
```

Expected: the last command prints nothing; `grep -c "ActorUserID: &" internal/api/*.go` totals 19. (`auditlog.go` reads `row.ActorUserID`, which is already a pointer, and is untouched.) The build is broken until Step 4, because `Entry.ActorUserID` is still a value.

- [ ] **Step 2: Write the failing tests**

In `apps/api/internal/audit/audit_test.go`, extend the two link tests:

```go
func TestLinkActionsAreInTheTaxonomy(t *testing.T) {
	for _, action := range []audit.Action{
		audit.ActionLinkCreated,
		audit.ActionLinkUpdated,
		audit.ActionLinkDeleted,
		audit.ActionPasswordSet,
		audit.ActionPasswordChanged,
		audit.ActionPasswordRemoved,
		audit.ActionLinkFlagged,
		audit.ActionLinkUnflagged,
	} {
		t.Run(string(action), func(t *testing.T) {
			require.NotErrorIs(t, audit.CheckAction(action), audit.ErrUnknownAction)
		})
	}
}

func TestLinkActionNamesFollowTheEntityDotVerbShape(t *testing.T) {
	require.Equal(t, audit.Action("link.created"), audit.ActionLinkCreated)
	require.Equal(t, audit.Action("link.updated"), audit.ActionLinkUpdated)
	require.Equal(t, audit.Action("link.deleted"), audit.ActionLinkDeleted)
	require.Equal(t, audit.Action("link.password_set"), audit.ActionPasswordSet)
	require.Equal(t, audit.Action("link.password_changed"), audit.ActionPasswordChanged)
	require.Equal(t, audit.Action("link.password_removed"), audit.ActionPasswordRemoved)
	require.Equal(t, audit.Action("link.flagged"), audit.ActionLinkFlagged)
	require.Equal(t, audit.Action("link.unflagged"), audit.ActionLinkUnflagged)
	require.Equal(t, "link", audit.EntityLink)
}
```

Append:

```go
// The Safe Browsing scanner is nobody's account. Its entries carry a null
// actor, and the metadata it writes passes the denylist: neither threat_types
// nor destination_url has a forbidden word segment.
func TestLogWritesASystemEntryWithoutAnActor(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, _ := seedTeam(ctx, t, pool)
	linkID := uuid.New()

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:     teamID,
			Action:     audit.ActionLinkFlagged,
			EntityType: audit.EntityLink,
			EntityID:   linkID,
			Metadata: map[string]any{
				"threat_types":    []string{"SOCIAL_ENGINEERING"},
				"destination_url": "https://example.org/x",
			},
		})
	}))

	var (
		actor *uuid.UUID
		raw   []byte
	)
	require.NoError(t, pool.QueryRow(ctx,
		`select actor_user_id, metadata from audit_log where team_id = $1 and entity_id = $2`,
		teamID, linkID).Scan(&actor, &raw))
	require.Nil(t, actor)
	require.JSONEq(t,
		`{"threat_types":["SOCIAL_ENGINEERING"],"destination_url":"https://example.org/x"}`, string(raw))
}

// The web reads a null actor as "Google Safe Browsing" on the two system
// actions and as "A deleted account" on every other, so the actor and the
// action have to agree when the entry is written.
func TestLogRefusesAnActorThatDoesNotFitTheAction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	for _, tc := range []struct {
		name   string
		actor  *uuid.UUID
		action audit.Action
	}{
		{"a member's action without its member", nil, audit.ActionLinkUpdated},
		{"the system's action with a member", &userID, audit.ActionLinkFlagged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := db.InTx(ctx, pool, func(q *db.Queries) error {
				return audit.Log(ctx, q, audit.Entry{
					TeamID:      teamID,
					ActorUserID: tc.actor,
					Action:      tc.action,
					EntityType:  audit.EntityLink,
					EntityID:    uuid.New(),
				})
			})
			require.ErrorIs(t, err, audit.ErrActorMismatch)
		})
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/audit/ -count=1`

Expected: FAIL to compile with `undefined: audit.ActionLinkFlagged`, `undefined: audit.ErrActorMismatch`, and `cannot use &userID (value of type *uuid.UUID) as uuid.UUID value`.

- [ ] **Step 4: Change the audit package**

In `apps/api/internal/audit/audit.go`, after `ActionPasswordRemoved` (line 52), add:

```go

	// The two actions no person takes. The Safe Browsing scanner writes them
	// with a nil ActorUserID, and nothing else may: the web reads a null actor
	// on exactly these two as "Google Safe Browsing" and on every other action
	// as "A deleted account", because audit_log.actor_user_id is also null for
	// an author whose account was deleted later. Metadata carries the
	// threat_types Google reported and the destination_url it judged.
	ActionLinkFlagged   Action = "link.flagged"
	ActionLinkUnflagged Action = "link.unflagged"
```

After `ErrForbiddenMetadata` (line 88), add:

```go

	// ErrActorMismatch keeps the actor and the action in agreement: a
	// member's action needs its member, and the system's actions must have
	// none. See ActionLinkFlagged for why the web depends on it.
	ErrActorMismatch = errors.New("audit: the actor does not fit the action")
```

Add both actions to `knownActions` after `ActionPasswordRemoved: {},`:

```go
	ActionLinkFlagged:       {},
	ActionLinkUnflagged:     {},
```

and below `knownActions` add:

```go

// systemActions are the actions written without an actor.
var systemActions = map[Action]struct{}{
	ActionLinkFlagged:   {},
	ActionLinkUnflagged: {},
}
```

Replace `Entry` (lines 136-144) with:

```go
// Entry is one audit record. Every field is required except Metadata and,
// for the system's own actions only, ActorUserID: nil there is the system.
type Entry struct {
	TeamID      uuid.UUID
	ActorUserID *uuid.UUID
	Action      Action
	EntityType  string
	EntityID    uuid.UUID
	Metadata    map[string]any
}
```

In `Log`, after the `CheckAction` call add:

```go
	if _, system := systemActions[e.Action]; system != (e.ActorUserID == nil) {
		return fmt.Errorf("%w: %q", ErrActorMismatch, e.Action)
	}
```

and replace the parameter block at lines 175-182 with:

```go
	teamID, entityID := e.TeamID, e.EntityID
	if err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: e.ActorUserID,
		Action:      string(e.Action),
		EntityType:  e.EntityType,
		EntityID:    &entityID,
		Metadata:    raw,
	}); err != nil {
```

Update the package's comment on `Action` (line 22-25) from "The taxonomy this plan defines" to say the taxonomy has twenty-three values, kept here and in `knownActions`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go build ./... && go test ./internal/audit/ -count=1 && go test ./internal/api/ -run 'Audit|CreateLinkWritesOneAuditRow|UpdateLinkWritesOneAuditRow' -count=1`

Expected: the build succeeds and every listed test passes; the API's audit entries still name their member (`TestAuditLog…` reads `*page.Items[0].ActorUserID`).

- [ ] **Step 6: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: clean; `but diff` lists `audit.go`, the two audit test files and the seven API handler files. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): let the system write audit entries" <ids>
```

---

### Task 5: Applying verdicts, immediate checks and the PATCH rules

**Files:**

- Create: `apps/api/internal/cache/safebrowsing.go`
- Test: `apps/api/internal/cache/safebrowsing_test.go`
- Create: `apps/api/internal/api/scan.go`
- Modify: `apps/api/internal/api/links.go:135-163` (add `rowFromGetForUpdate`), `:574-577` (`createLink`'s success branch), `:851-1012` (`updateLink`)
- Create: `apps/api/internal/api/export_test.go`
- Create: `apps/api/internal/api/scan_fake_test.go`
- Test: `apps/api/internal/api/scan_test.go`, `apps/api/internal/api/scan_internal_test.go`

**Interfaces:**

- Consumes: Task 1's `GetLinkForScan`, `RecordLinkScan`, `InsertLinkScanResult`, `GetLinkForAPIForUpdate`; Task 3's `scanning.Checker`, `scanning.Result`, `scanning.QuotaExceeded`, `Deps.Scanner`; Task 4's nil actor and `audit.ActionLinkFlagged`/`ActionLinkUnflagged`.
- Produces, in package `cache` (each costs the Redis commands its comment states):
  - `func (c *Client) ConfirmThreats(ctx context.Context, linkID string, threatTypes []string, ttl time.Duration) error` (SET)
  - `func (c *Client) ThreatConfirmation(ctx context.Context, linkID string) ([]string, bool, error)` (GET)
  - `func (c *Client) ClearThreatConfirmation(ctx context.Context, linkID string) error` (DEL)
- Produces, in package `api` (unexported, used by Tasks 6 and 7):
  - `type scanTarget struct { LinkID, TeamID uuid.UUID; URL string }`
  - `type verdictOutcome string` with `verdictFlagged`, `verdictUnflagged`, `verdictConfirmed`, `verdictUnchanged`, `verdictStale`, `verdictSkipped`, `verdictGone`
  - `func (d Deps) applyVerdict(ctx context.Context, target scanTarget, result scanning.Result) (verdictOutcome, error)`
  - `func (d Deps) applyAndLog(ctx context.Context, target scanTarget, result scanning.Result)`
  - `func (d Deps) scanSoon(ctx context.Context, target scanTarget)`
  - `func (d Deps) inBackground(ctx context.Context, linkID uuid.UUID, work func(context.Context))`
  - `func (d Deps) logCheckFailure(err error, attrs ...any)`
  - `func (d Deps) confirmThreats(ctx context.Context, linkID uuid.UUID, threats []string, validFor time.Duration)`
  - `func (d Deps) clearThreatConfirmation(ctx context.Context, linkID uuid.UUID)`
  - `func confirmationTTL(validFor time.Duration) time.Duration`
  - `var errNoVerdict error`
- Produces for tests (`export_test.go`): `api.VerdictOutcome`, `(api.Deps).ApplyVerdictForTest(ctx, linkID, teamID uuid.UUID, url string, result scanning.Result) (api.VerdictOutcome, error)`.
- Produces for tests (`scan_fake_test.go`, package `api_test`): `newFakeChecker() *fakeChecker` with `flag(url string, threats ...string)`, `pass(url string)`, `answer(url string, result scanning.Result)`, `callCount() int`; Tasks 6 and 7 add `fail`, `onCheck` and `slow`.

- [ ] **Step 1: Write the failing cache tests**

Create `apps/api/internal/cache/safebrowsing_test.go`:

```go
package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAThreatConfirmationRoundTripsUnderTheEnvironmentPrefix(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	require.NoError(t, client.ConfirmThreats(ctx, "link-1",
		[]string{"MALWARE", "SOCIAL_ENGINEERING"}, 10*time.Minute))

	threats, ok, err := client.ThreatConfirmation(ctx, "link-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"MALWARE", "SOCIAL_ENGINEERING"}, threats)

	ttl, err := client.Raw().TTL(ctx, client.Key("sb:confirmed:link-1")).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 9*time.Minute)

	_, err = client.Raw().Get(ctx, "sb:confirmed:link-1").Result()
	require.ErrorIs(t, err, redis.Nil, "a preview must not confirm a block in production's keyspace")
}

func TestAMissingConfirmationIsNotAnError(t *testing.T) {
	_, ok, err := newTestClient(t).ThreatConfirmation(context.Background(), "nobody")
	require.NoError(t, err)
	require.False(t, ok)
}

// go-redis reads a zero expiration as "keep forever", and a confirmation that
// never expires is a block on data Google's terms call stale after thirty
// minutes. A confirmation naming no threat would block with no reason to show.
func TestAConfirmationWithoutAPositiveTTLOrAThreatIsRefused(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	require.Error(t, client.ConfirmThreats(ctx, "link-2", []string{"MALWARE"}, 0))
	require.Error(t, client.ConfirmThreats(ctx, "link-2", []string{"MALWARE"}, -time.Minute))
	require.Error(t, client.ConfirmThreats(ctx, "link-2", nil, time.Minute))

	_, ok, err := client.ThreatConfirmation(ctx, "link-2")
	require.NoError(t, err)
	require.False(t, ok, "a refused confirmation must not have been written")
}

func TestClearingAConfirmationRemovesIt(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	require.NoError(t, client.ConfirmThreats(ctx, "link-3", []string{"MALWARE"}, time.Minute))

	require.NoError(t, client.ClearThreatConfirmation(ctx, "link-3"))

	_, ok, err := client.ThreatConfirmation(ctx, "link-3")
	require.NoError(t, err)
	require.False(t, ok)
}

// Only a hand-written key could look like this, and it must not block.
func TestAConfirmationNamingNoThreatDoesNotCount(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	require.NoError(t, client.Raw().Set(ctx, client.Key("sb:confirmed:link-4"), ",", time.Minute).Err())

	_, ok, err := client.ThreatConfirmation(ctx, "link-4")
	require.NoError(t, err)
	require.False(t, ok)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/cache/ -run Confirm -count=1`

Expected: FAIL to compile with `client.ConfirmThreats undefined`.

- [ ] **Step 3: Write the confirmation key**

Create `apps/api/internal/cache/safebrowsing.go`:

```go
package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// threatConfirmationPrefix keys the short-lived record that Google confirmed a
// flagged link's threats. Google's terms forbid blocking on a verdict older
// than thirty minutes, so link.state = 'flagged' alone never shows the block
// page: the redirect path shows it only while this key exists, and asks Google
// again when it does not. The value is the confirmed threat types,
// comma-separated, which the block page needs to say what was found.
//
// Only flagged links ever read it, so an active link's redirect costs nothing
// new. A flagged link costs one GET per redirect, plus one SET per re-check.
const threatConfirmationPrefix = "sb:confirmed:"

// errUnusableConfirmation refuses a confirmation that would outlive the terms'
// window or name no threat.
var errUnusableConfirmation = errors.New(
	"cache: a threat confirmation needs at least one threat type and a positive ttl")

// ConfirmThreats records that Google confirmed threatTypes for linkID just
// now, for ttl. One Redis command (SET with an expiry).
//
// A ttl that is not positive is refused rather than written: go-redis reads a
// zero expiration as "keep forever", and a confirmation that never expires is
// a block on data the terms call stale. The caller decides what not writing
// means; see api.confirmThreats.
func (c *Client) ConfirmThreats(
	ctx context.Context, linkID string, threatTypes []string, ttl time.Duration,
) error {
	if ttl <= 0 || len(threatTypes) == 0 {
		return errUnusableConfirmation
	}
	value := strings.Join(threatTypes, ",")
	if err := c.rdb.Set(ctx, c.Key(threatConfirmationPrefix+linkID), value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: confirm threats: %w", err)
	}
	return nil
}

// ThreatConfirmation reads a link's confirmation back. One Redis command
// (GET). ok is false when there is none, and also when the stored value names
// no threat type, which only a hand-written key could produce and which must
// not block anything.
func (c *Client) ThreatConfirmation(ctx context.Context, linkID string) ([]string, bool, error) {
	raw, err := c.rdb.Get(ctx, c.Key(threatConfirmationPrefix+linkID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache: read threat confirmation: %w", err)
	}

	var threats []string
	for _, threat := range strings.Split(raw, ",") {
		if threat != "" {
			threats = append(threats, threat)
		}
	}
	return threats, len(threats) > 0, nil
}

// ClearThreatConfirmation drops a link's confirmation. One Redis command
// (DEL).
func (c *Client) ClearThreatConfirmation(ctx context.Context, linkID string) error {
	if err := c.rdb.Del(ctx, c.Key(threatConfirmationPrefix+linkID)).Err(); err != nil {
		return fmt.Errorf("cache: clear threat confirmation: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the cache tests to verify they pass**

Run: `cd apps/api && go test ./internal/cache/ -count=1`

Expected: PASS (Docker running; without it every cache test skips).

- [ ] **Step 5: Write the fake checker and the test seam**

Create `apps/api/internal/api/scan_fake_test.go`:

```go
package api_test

import (
	"context"
	"sync"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// fakeChecker stands in for Google Safe Browsing in handler tests. It answers
// only for the URLs a test named, and leaves every other URL out of its
// answer — which the code under test must read as "no verdict", never as
// clean. That is also what keeps an instance-wide sweep test from writing to
// links other tests, and other packages in parallel processes, have
// committed: their URLs get no verdict, so nothing is written to them.
//
// Safe for concurrent use: the immediate checks call it from goroutines, and
// CI runs this package under -race.
type fakeChecker struct {
	mu      sync.Mutex
	results map[string]scanning.Result
	err     error
	delay   time.Duration
	during  func(context.Context)
	calls   int
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{results: map[string]scanning.Result{}}
}

// flag makes url report threats, valid for five minutes.
func (c *fakeChecker) flag(url string, threats ...string) {
	c.answer(url, scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute})
}

// pass makes url check clean.
func (c *fakeChecker) pass(url string) {
	c.answer(url, scanning.Result{ValidFor: 5 * time.Minute})
}

func (c *fakeChecker) answer(url string, result scanning.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[url] = result
}

func (c *fakeChecker) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *fakeChecker) Check(ctx context.Context, urls []string) (map[string]scanning.Result, error) {
	c.mu.Lock()
	c.calls++
	err, delay, during := c.err, c.delay, c.during
	answer := make(map[string]scanning.Result, len(urls))
	for _, url := range urls {
		if result, ok := c.results[url]; ok {
			answer[url] = result
		}
	}
	c.mu.Unlock()

	if during != nil {
		during(ctx)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return answer, nil
}
```

Create `apps/api/internal/api/export_test.go`:

```go
package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// This file is compiled only into this package's tests. It hands package
// api_test, where the database fixtures live, the parts of the scan pipeline
// no route exposes on a test's terms: applyVerdict with a verdict the test
// chooses, and — added with the sweep — a sweep with a batch smaller than
// scanBatchSize.

// VerdictOutcome is applyVerdict's verdictOutcome, for assertions.
type VerdictOutcome = verdictOutcome

// ApplyVerdictForTest runs applyVerdict for one link.
func (d Deps) ApplyVerdictForTest(
	ctx context.Context, linkID, teamID uuid.UUID, url string, result scanning.Result,
) (VerdictOutcome, error) {
	return d.applyVerdict(ctx, scanTarget{LinkID: linkID, TeamID: teamID, URL: url}, result)
}
```

- [ ] **Step 6: Write the failing pipeline tests**

Create `apps/api/internal/api/scan_internal_test.go`:

```go
package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// min(ValidFor, thirty minutes) minus a minute. Not positive means no key at
// all, so a verdict Google says may not be relied on never blocks a second
// redirect.
func TestConfirmationTTLStaysInsideThirtyMinutes(t *testing.T) {
	for _, tc := range []struct{ validFor, want time.Duration }{
		{5 * time.Minute, 4 * time.Minute},
		{30 * time.Minute, 29 * time.Minute},
		{2 * time.Hour, 29 * time.Minute},
		{time.Minute, 0},
		{0, -time.Minute},
	} {
		require.Equal(t, tc.want, confirmationTTL(tc.validFor), "validFor %s", tc.validFor)
	}
}
```

Create `apps/api/internal/api/scan_test.go`:

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// The fixture link's destination, from newFixture's defaults.
const fixtureDestination = "https://example.org/hello"

func flagged(threats ...string) scanning.Result {
	return scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute}
}

func clean() scanning.Result {
	return scanning.Result{ValidFor: 5 * time.Minute}
}

func linkState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var state string
	require.NoError(t, pool.QueryRow(context.Background(),
		`select state from link where id = $1`, id).Scan(&state))
	return state
}

func scanCheckedAt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) *time.Time {
	t.Helper()
	var checkedAt *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`select scan_checked_at from link where id = $1`, id).Scan(&checkedAt))
	return checkedAt
}

func scanResultCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`select count(*) from link_scan_result where link_id = $1`, id).Scan(&count))
	return count
}

// eventuallyState waits for a background check a handler started to land.
func eventuallyState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var state string
		err := pool.QueryRow(context.Background(), `select state from link where id = $1`, id).Scan(&state)
		return err == nil && state == want
	}, 5*time.Second, 20*time.Millisecond, "link %s never became %q", id, want)
}

// eventuallyChecked waits until a check of url has been recorded for the link.
func eventuallyChecked(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var checked *string
		err := pool.QueryRow(context.Background(),
			`select scan_destination from link where id = $1`, id).Scan(&checked)
		return err == nil && checked != nil && *checked == url
	}, 5*time.Second, 20*time.Millisecond, "link %s was never checked against %s", id, url)
}

// confirmationTTLLeft is negative when there is no confirmation key at all.
func confirmationTTLLeft(t *testing.T, client *cache.Client, id uuid.UUID) time.Duration {
	t.Helper()
	ttl, err := client.Raw().TTL(context.Background(), client.Key("sb:confirmed:"+id.String())).Result()
	require.NoError(t, err)
	return ttl
}

type auditRow struct {
	actor    *uuid.UUID
	metadata string
}

func auditRows(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, action string) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`select actor_user_id, metadata::text from audit_log where entity_id = $1 and action = $2`,
		id, action)
	require.NoError(t, err)
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var row auditRow
		require.NoError(t, rows.Scan(&row.actor, &row.metadata))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestApplyVerdictFlagsAnActiveLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := captureLogs(f)
	teamID := fixtureTeamID(t, f)

	// Cache the active link first, so the flag has something stale to clear.
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, teamID, fixtureDestination,
		flagged("SOCIAL_ENGINEERING"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("flagged"), outcome)

	var (
		state, checkedURL string
		checkedAt         time.Time
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select state, scan_checked_at, scan_destination from link where id = $1`, f.linkID,
	).Scan(&state, &checkedAt, &checkedURL))
	require.Equal(t, "flagged", state)
	require.True(t, f.deps.Now().Equal(checkedAt))
	require.Equal(t, fixtureDestination, checkedURL)

	var (
		verdict, judged string
		threats         []string
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select verdict, threat_types, destination_url from link_scan_result where link_id = $1`, f.linkID,
	).Scan(&verdict, &threats, &judged))
	require.Equal(t, "flagged", verdict)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, threats)
	require.Equal(t, fixtureDestination, judged)

	entries := auditRows(t, f.pool, f.linkID, "link.flagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor, "the scanner is the system, not a member")
	require.JSONEq(t,
		`{"threat_types":["SOCIAL_ENGINEERING"],"destination_url":"https://example.org/hello"}`,
		entries[0].metadata)

	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 3*time.Minute)
	require.LessOrEqual(t, ttl, 4*time.Minute)

	_, err = f.deps.Cache.Raw().Get(ctx, f.deps.Cache.Key(link.CacheKey(f.hostname, "hello"))).Result()
	require.ErrorIs(t, err, redis.Nil,
		"the cached active link must be gone, or the next redirect forwards it for another hour")

	require.Contains(t, logs.String(), `level=ERROR msg="link flagged by Safe Browsing"`,
		"an Error log is how the maintainer hears of a flag")
}

func TestApplyVerdictUnflagsAFlaggedLinkThatChecksClean(t *testing.T) {
	f := newFixture(t, withState("flagged"))
	ctx := context.Background()
	logs := captureLogs(f)
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, f.linkID.String(), []string{"MALWARE"}, 10*time.Minute))
	cacheKey := link.CacheKey(f.hostname, "hello")
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: f.linkID, State: "flagged", DestinationURL: fixtureDestination,
	}, time.Hour))

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination, clean())
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("unflagged"), outcome)

	require.Equal(t, "active", linkState(t, f.pool, f.linkID))

	var (
		verdict string
		threats []string
	)
	require.NoError(t, f.pool.QueryRow(ctx,
		`select verdict, threat_types from link_scan_result where link_id = $1`, f.linkID,
	).Scan(&verdict, &threats))
	require.Equal(t, "clean", verdict)
	require.Empty(t, threats)

	entries := auditRows(t, f.pool, f.linkID, "link.unflagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor)

	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), "the confirmation must be gone")
	_, err = f.deps.Cache.Raw().Get(ctx, f.deps.Cache.Key(cacheKey)).Result()
	require.ErrorIs(t, err, redis.Nil)
	require.Contains(t, logs.String(), `level=INFO msg="link unflagged by Safe Browsing"`)
}

func TestApplyVerdictRefreshesTheConfirmationOfAStillFlaggedLink(t *testing.T) {
	f := newFixture(t, withState("flagged"))

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("confirmed"), outcome)

	require.Zero(t, scanResultCount(t, f.pool, f.linkID), "a row is written only when the verdict changes")
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
	require.Greater(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID), 3*time.Minute)
}

func TestApplyVerdictOnACleanActiveLinkOnlyRecordsTheCheck(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		fixtureDestination, clean())
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("unchanged"), outcome)

	checkedAt := scanCheckedAt(t, f.pool, f.linkID)
	require.NotNil(t, checkedAt)
	require.True(t, f.deps.Now().Equal(*checkedAt))
	require.Zero(t, scanResultCount(t, f.pool, f.linkID), "the first clean check writes no row")
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))
}

// Review focus: the verdict is for a URL the link no longer points at. It must
// not touch the link at all, so the link stays due for its new destination.
func TestApplyVerdictDiscardsAVerdictForAReplacedDestination(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		"https://example.org/the-old-destination", flagged("SOCIAL_ENGINEERING"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("stale"), outcome)

	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
	require.Zero(t, scanResultCount(t, f.pool, f.linkID))
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
}

// Review focus: the link was deleted while its check was in flight.
func TestApplyVerdictForADeletedLinkIsANoOp(t *testing.T) {
	f := newFixture(t)
	teamID := fixtureTeamID(t, f)
	_, err := f.pool.Exec(context.Background(), `delete from link where id = $1`, f.linkID)
	require.NoError(t, err)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, teamID,
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("gone"), outcome)
	require.Empty(t, auditRows(t, f.pool, f.linkID, "link.flagged"))
}

// The read filters by team even for the scanner, so a verdict addressed to
// the wrong team finds nothing to write.
func TestApplyVerdictWithAnotherTeamsIDWritesNothing(t *testing.T) {
	f := newFixture(t)

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, uuid.New(),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("gone"), outcome)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
}

// A link that does not redirect cannot harm anyone, and it must become due
// again when it is re-enabled — so not even the timestamps change.
func TestApplyVerdictLeavesADisabledLinkAlone(t *testing.T) {
	f := newFixture(t, withState("disabled"))

	outcome, err := f.deps.ApplyVerdictForTest(context.Background(), f.linkID, fixtureTeamID(t, f),
		fixtureDestination, flagged("MALWARE"))
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("skipped"), outcome)
	require.Equal(t, "disabled", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// A verdict Google says may not be relied on still flags the link, but no
// confirmation is written, and an older one is dropped: a key must never
// vouch for longer than the newest answer allows.
func TestApplyVerdictWithoutAUsableValidityFlagsButConfirmsNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, f.linkID.String(), []string{"MALWARE"}, 10*time.Minute))

	outcome, err := f.deps.ApplyVerdictForTest(ctx, f.linkID, fixtureTeamID(t, f), fixtureDestination,
		scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 0})
	require.NoError(t, err)
	require.Equal(t, api.VerdictOutcome("flagged"), outcome)
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))
}

// scanningFixture is a tenancy fixture whose /v1 surface has a fake checker.
func scanningFixture(t *testing.T) (*tenancyFixture, *fakeChecker) {
	t.Helper()
	f := newTenancyFixture(t)
	checker := newFakeChecker()
	f.deps.Scanner = checker
	f.rebuildRouter()
	return f, checker
}

func uniqueDestination(kind string) string {
	return "https://" + kind + "-" + uuid.NewString()[:8] + ".test/login"
}

func TestCreatingALinkChecksItsDestinationRightAway(t *testing.T) {
	f, checker := scanningFixture(t)
	destination := uniqueDestination("phish")
	checker.flag(destination, "SOCIAL_ENGINEERING")

	created := f.createLink(t, "sofort", destination)
	require.Equal(t, "active", created.State,
		"the link is live from the moment it is created; the check comes after")

	eventuallyState(t, f.pool, created.ID, "flagged")
}

func TestChangingTheDestinationChecksTheNewOneRightAway(t *testing.T) {
	f, checker := scanningFixture(t)
	first, second := uniqueDestination("ok"), uniqueDestination("phish")
	checker.pass(first)
	checker.flag(second, "MALWARE")

	created := f.createLink(t, "umzug", first)
	eventuallyChecked(t, f.pool, created.ID, first)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": second})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	eventuallyState(t, f.pool, created.ID, "flagged")
}

func TestAnEditThatKeepsTheDestinationStartsNoCheck(t *testing.T) {
	f, checker := scanningFixture(t)
	destination := uniqueDestination("ok")
	checker.pass(destination)

	created := f.createLink(t, "ruhig", destination)
	eventuallyChecked(t, f.pool, created.ID, destination)
	require.Equal(t, 1, checker.callCount())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"redirect_type": 301})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	require.Never(t, func() bool { return checker.callCount() > 1 },
		300*time.Millisecond, 20*time.Millisecond)
}

func flagLink(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `update link set state = 'flagged' where id = $1`, id)
	require.NoError(t, err)
}

// Only Google lifts a flag. Disabling is refused too: disable-then-enable would
// otherwise be a way around the block.
func TestTheStateOfAFlaggedLinkIsNotTheCallersToChange(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "gesperrt", "https://example.org/gesperrt")
	flagLink(t, f.pool, created.ID)

	for _, state := range []string{"active", "disabled"} {
		rec := f.do(t, f.members[authz.RoleOwner], http.MethodPatch, "/v1/links/"+created.ID.String(),
			map[string]any{"state": state})
		require.Equal(t, http.StatusConflict, rec.Code, "state %q, body: %s", state, rec.Body.String())

		problem := decode[problemBody](t, rec)
		require.Len(t, problem.Errors, 1)
		require.Equal(t, "body.state", problem.Errors[0].Location)
		require.Equal(t, "flagged", problem.Errors[0].Value)
	}
	require.Equal(t, "flagged", linkState(t, f.pool, created.ID))
}

// The flag belonged to the old URL, so the new one starts active and is
// checked on its own; the confirmation for the old URL must not survive it.
func TestANewDestinationLiftsTheFlagItsOldOneEarned(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "neuesziel", "https://example.org/alt")
	flagLink(t, f.pool, created.ID)
	require.NoError(t, f.deps.Cache.ConfirmThreats(ctx, created.ID.String(), []string{"MALWARE"}, 10*time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "active", decode[linkBody](t, rec).State)

	entries := auditRows(t, f.pool, created.ID, "link.updated")
	require.Len(t, entries, 1)
	var metadata struct {
		Changed []string          `json:"changed"`
		State   map[string]string `json:"state"`
	}
	require.NoError(t, json.Unmarshal([]byte(entries[0].metadata), &metadata))
	require.ElementsMatch(t, []string{"destination_url", "state"}, metadata.Changed)
	require.Equal(t, map[string]string{"from": "flagged", "to": "active"}, metadata.State)

	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, created.ID))
}

// No more lost flags. A transaction stands in for applyVerdict: it locks the
// row and flags it, and the PATCH starts while that is uncommitted. With the
// locked read, the PATCH waits and then reads the flag; with an unlocked one
// it reads "active" first and writes it back over the flag.
func TestUpdateLinkDoesNotOverwriteAFlagSetWhileItWaited(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "wettlauf", "https://example.org/wettlauf")

	scanner, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = scanner.Rollback(context.Background()) })
	_, err = scanner.Exec(ctx, `select id from link where id = $1 for update`, created.ID)
	require.NoError(t, err)
	_, err = scanner.Exec(ctx, `update link set state = 'flagged' where id = $1`, created.ID)
	require.NoError(t, err)

	// Built here, served in the goroutine: require must not run off the test's
	// own goroutine.
	editor := f.members[authz.RoleEditor]
	req := httptest.NewRequest(http.MethodPatch, "/v1/links/"+created.ID.String(),
		strings.NewReader(`{"redirect_type": 301}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signMeToken(t, f.key, editor.id.String(), editor.email))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		done <- rec
	}()

	// Wait until the PATCH is blocked on the row lock — on its own locked read
	// (correct) or on its write (the bug). Either way it has started.
	require.Eventually(t, func() bool {
		var waiting int
		err := f.pool.QueryRow(ctx,
			`select count(*) from pg_stat_activity
			 where wait_event_type = 'Lock'
			   and (query like '%name: GetLinkForAPIForUpdate %' or query like '%name: UpdateLink %')`,
		).Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, scanner.Commit(ctx))
	rec := <-done
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "flagged", linkState(t, f.pool, created.ID),
		"the PATCH wrote back the state it had read before the flag landed")
}

// A side effect of the 409 above: updateLink now passes a shaped error from
// inside its transaction through. Before, a foreign folder answered 500.
func TestUpdateLinkAnswers422ForAnotherTeamsFolder(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createFolder(t, "Fremd")
	created := f.createLink(t, "ordner", "https://example.org/ordner")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"folder_id": foreign.ID.String()})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}
```

- [ ] **Step 7: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'ApplyVerdict|Confirmation|RightAway|KeepsTheDestination|FlaggedLink|NewDestination|WhileItWaited|AnotherTeamsFolder' -count=1`

Expected: FAIL to compile with `undefined: applyVerdict` (from `export_test.go`) and `undefined: confirmationTTL`.

- [ ] **Step 8: Write the pipeline**

Create `apps/api/internal/api/scan.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// backgroundScanTimeout bounds one best-effort check started after a write.
// Ten seconds rather than the redirect path's two: nobody waits on it, and a
// slow answer that still arrives is worth more than one the sweep has to
// repeat.
const backgroundScanTimeout = 10 * time.Second

const (
	// confirmationMaxAge is the thirty minutes of Google's terms: no warning
	// and no block on a verdict older than that.
	confirmationMaxAge = 30 * time.Minute
	// confirmationMargin keeps a confirmation from expiring in the same minute
	// the terms' window closes, whatever the clocks between here, Redis and
	// Google make of "thirty minutes".
	confirmationMargin = time.Minute
)

// errNoVerdict means a check came back without a verdict for the URL asked
// about. A failure, never "clean"; scanning.Checker says why.
var errNoVerdict = errors.New("safe browsing returned no verdict for the destination")

// scanTarget is one link and the destination a check judged for it. TeamID
// travels with it so every write the verdict causes filters by tenant.
type scanTarget struct {
	LinkID uuid.UUID
	TeamID uuid.UUID
	URL    string
}

// verdictOutcome is what applyVerdict made of a verdict.
type verdictOutcome string

const (
	// verdictFlagged: an active link Google reports is flagged now.
	verdictFlagged verdictOutcome = "flagged"
	// verdictUnflagged: a flagged link Google no longer reports is active again.
	verdictUnflagged verdictOutcome = "unflagged"
	// verdictConfirmed: a flagged link is still reported; its confirmation was refreshed.
	verdictConfirmed verdictOutcome = "confirmed"
	// verdictUnchanged: an active link is still clean; only the check was recorded.
	verdictUnchanged verdictOutcome = "unchanged"
	// verdictStale: the destination changed after the check; the verdict was discarded.
	verdictStale verdictOutcome = "stale"
	// verdictSkipped: the link is no longer active or flagged; nothing was written.
	verdictSkipped verdictOutcome = "skipped"
	// verdictGone: the link was deleted before the verdict arrived.
	verdictGone verdictOutcome = "gone"
)

// applyVerdict writes one link's verdict, in one transaction:
//
//   - The link is read FOR UPDATE, so the check and the write are one
//     decision against any concurrent PATCH.
//   - A verdict for a URL the link no longer points at is discarded: a newer
//     destination is waiting for its own check. So is one for a link that is
//     no longer active or flagged, which then becomes due again when it is
//     re-enabled.
//   - Otherwise the check is recorded, and the state follows the verdict:
//     threats on an active link flag it, a clean check on a flagged one lifts
//     the flag. Only those two transitions write a link_scan_result row and a
//     system-actor audit entry.
//
// After the commit, a flag sets the Redis confirmation and clears the cached
// link, and logs at Error, which is how the maintainer hears of it. Lifting a
// flag clears both. A link that was deleted meanwhile is not an error.
func (d Deps) applyVerdict(ctx context.Context, target scanTarget, result scanning.Result) (verdictOutcome, error) {
	now := d.now()
	// Never nil: link_scan_result.threat_types is not null, pgx encodes a nil
	// slice as SQL NULL rather than an empty array, and the audit metadata
	// would carry a null where a list belongs.
	threats := result.ThreatTypes
	if threats == nil {
		threats = []string{}
	}

	var (
		outcome        verdictOutcome
		hostname, slug string
	)
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Filtered by team although the scanner belongs to no team: it always
		// knows the link's team, so the tenancy rule costs nothing to keep.
		current, err := q.GetLinkForScan(ctx, db.GetLinkForScanParams{
			ID: target.LinkID, TeamID: target.TeamID,
		})
		if err != nil {
			return err
		}
		hostname, slug = current.Hostname, current.Slug

		switch {
		case current.DestinationURL != target.URL:
			outcome = verdictStale
			return nil
		case current.State != "active" && current.State != "flagged":
			outcome = verdictSkipped
			return nil
		}

		state := current.State
		switch {
		case len(threats) > 0 && current.State == "active":
			state, outcome = "flagged", verdictFlagged
		case len(threats) == 0 && current.State == "flagged":
			state, outcome = "active", verdictUnflagged
		case len(threats) > 0:
			outcome = verdictConfirmed
		default:
			outcome = verdictUnchanged
		}

		if err := q.RecordLinkScan(ctx, db.RecordLinkScanParams{
			ID:                 target.LinkID,
			TeamID:             target.TeamID,
			State:              state,
			CheckedAt:          now,
			CheckedDestination: target.URL,
		}); err != nil {
			return err
		}
		if outcome != verdictFlagged && outcome != verdictUnflagged {
			return nil
		}

		verdict, action := "flagged", audit.ActionLinkFlagged
		if outcome == verdictUnflagged {
			verdict, action = "clean", audit.ActionLinkUnflagged
		}
		if err := q.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
			LinkID:         target.LinkID,
			Verdict:        verdict,
			DestinationURL: target.URL,
			ThreatTypes:    threats,
			ScannedAt:      now,
		}); err != nil {
			return err
		}
		// No ActorUserID: the scanner is the system. audit.Log refuses a nil
		// actor on any other action.
		return audit.Log(ctx, q, audit.Entry{
			TeamID:     target.TeamID,
			Action:     action,
			EntityType: audit.EntityLink,
			EntityID:   target.LinkID,
			Metadata: map[string]any{
				"threat_types":    threats,
				"destination_url": target.URL,
			},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return verdictGone, nil
	case err != nil:
		return "", fmt.Errorf("apply safe browsing verdict: %w", err)
	}

	switch outcome {
	case verdictFlagged:
		d.confirmThreats(ctx, target.LinkID, threats, result.ValidFor)
		d.invalidateLink(ctx, hostname, slug)
		d.Log.Error("link flagged by Safe Browsing",
			"link_id", target.LinkID, "team_id", target.TeamID, "threat_types", threats)
	case verdictUnflagged:
		d.clearThreatConfirmation(ctx, target.LinkID)
		d.invalidateLink(ctx, hostname, slug)
		d.Log.Info("link unflagged by Safe Browsing", "link_id", target.LinkID, "team_id", target.TeamID)
	case verdictConfirmed:
		d.confirmThreats(ctx, target.LinkID, threats, result.ValidFor)
	}
	return outcome, nil
}

// confirmationTTL is how long a confirmation may vouch for a block: never
// longer than Google's answer allows, never past the terms' thirty minutes,
// and a minute short of either.
func confirmationTTL(validFor time.Duration) time.Duration {
	return min(validFor, confirmationMaxAge) - confirmationMargin
}

// confirmThreats records a fresh confirmation for a flagged link. When its
// TTL would not be positive nothing is written and any older key is dropped,
// so a key never vouches for longer than the newest answer allows; the next
// redirect then asks Google again. A Redis failure is a Warn: the redirect
// path re-checks when the key is missing, which costs a wait, not a wrong
// answer.
func (d Deps) confirmThreats(ctx context.Context, linkID uuid.UUID, threats []string, validFor time.Duration) {
	if d.Cache == nil {
		return
	}
	ttl := confirmationTTL(validFor)
	if ttl <= 0 || len(threats) == 0 {
		d.clearThreatConfirmation(ctx, linkID)
		return
	}
	if err := d.Cache.ConfirmThreats(ctx, linkID.String(), threats, ttl); err != nil {
		d.Log.Warn("safe browsing confirmation write failed", "error", err, "link_id", linkID)
	}
}

// clearThreatConfirmation drops a link's confirmation, best-effort: a key left
// behind still holds a confirmation younger than thirty minutes, and only a
// flagged link ever reads it.
func (d Deps) clearThreatConfirmation(ctx context.Context, linkID uuid.UUID) {
	if d.Cache == nil {
		return
	}
	if err := d.Cache.ClearThreatConfirmation(ctx, linkID.String()); err != nil {
		d.Log.Warn("safe browsing confirmation delete failed", "error", err, "link_id", linkID)
	}
}

// scanSoon checks one link in the background right after a write committed:
// after createLink, and after updateLink changed the destination. Best-effort
// by design — Vercel does not promise to run work past the response, and the
// sweep exists for the check that never finishes.
func (d Deps) scanSoon(ctx context.Context, target scanTarget) {
	if d.Scanner == nil {
		return
	}
	d.inBackground(ctx, target.LinkID, func(ctx context.Context) {
		d.scanOne(ctx, target)
	})
}

func (d Deps) scanOne(ctx context.Context, target scanTarget) {
	results, err := d.Scanner.Check(ctx, []string{target.URL})
	if err != nil {
		d.logCheckFailure(err, "link_id", target.LinkID)
		return
	}
	result, ok := results[target.URL]
	if !ok {
		d.logCheckFailure(errNoVerdict, "link_id", target.LinkID)
		return
	}
	d.applyAndLog(ctx, target, result)
}

// applyAndLog applies a verdict a background check produced. Nobody waits on
// it, so a failure is a Warn and the link stays due for the sweep.
func (d Deps) applyAndLog(ctx context.Context, target scanTarget, result scanning.Result) {
	if _, err := d.applyVerdict(ctx, target, result); err != nil {
		d.Log.Warn("apply safe browsing verdict", "error", err, "link_id", target.LinkID)
	}
}

// inBackground runs work on its own goroutine, detached from the request so
// the response does not cancel it, bounded by backgroundScanTimeout. It
// recovers a panic the way HandleDeepHealth's pings do: a bare goroutine has
// no recover anywhere on its stack, and a panic there would take the whole
// process down, the redirect surface included.
func (d Deps) inBackground(ctx context.Context, linkID uuid.UUID, work func(context.Context)) {
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				d.Log.Error("safe browsing background check panicked",
					"error", fmt.Errorf("panic: %v", r), "link_id", linkID)
			}
		}()
		ctx, cancel := context.WithTimeout(detached, backgroundScanTimeout)
		defer cancel()
		work(ctx)
	}()
}

// logCheckFailure logs a failed check at Warn, which never reaches Sentry: a
// failed check changes nothing, and the link stays due. A spent quota is the
// exception. It is an Error, coalesced in cmd/api to one Sentry event an hour,
// because it does not clear by itself and stops every check until it does.
func (d Deps) logCheckFailure(err error, attrs ...any) {
	if scanning.QuotaExceeded(err) {
		d.Log.Error("safe browsing quota exhausted", append([]any{"error", err}, attrs...)...)
		return
	}
	d.Log.Warn("safe browsing check failed", append([]any{"error", err}, attrs...)...)
}
```

- [ ] **Step 9: Change `createLink` and `updateLink`**

In `apps/api/internal/api/links.go`, after `rowFromGet` (ends line 133) add:

```go

func rowFromGetForUpdate(r db.GetLinkForAPIForUpdateRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, FolderID: r.FolderID, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
```

In `createLink`'s `case err == nil:` branch, after `d.invalidateLink(ctx, created.Hostname, created.Slug)` (line 577), add:

```go
			// After the commit, and without waiting: the link is live from
			// this moment whatever Google says, and the sweep catches a check
			// that never finishes.
			d.scanSoon(ctx, scanTarget{
				LinkID: created.ID, TeamID: created.TeamID, URL: created.DestinationURL,
			})
```

In `updateLink`, change the declaration `previous db.GetLinkForAPIRow` (line 854) to `previous db.GetLinkForAPIForUpdateRow`, and replace the read at lines 860-866 with:

```go
		// FOR UPDATE: state is written back from this read, and the Safe
		// Browsing scanner may flag the link while this transaction is open.
		// Unlocked, this PATCH would write that flag straight back to active.
		// Locked, it either waits for the scanner's commit and sees the flag,
		// or the scanner waits for this one and then finds the destination it
		// checked replaced.
		before, err := q.GetLinkForAPIForUpdate(ctx, db.GetLinkForAPIForUpdateParams{
			ID: in.Link().ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}
		previous = before

		// Only Google lifts a flag, through a later clean check. Disabling is
		// refused as well, or disable-then-enable would be a way around the
		// block. A new destination is the one way out, handled below.
		if before.State == "flagged" && in.Body.State != nil {
			message := "this link is blocked by Safe Browsing: change its destination, " +
				"or wait until Google clears the site"
			return huma.Error409Conflict(message, &huma.ErrorDetail{
				Location: "body.state", Message: message, Value: "flagged",
			})
		}
```

In the destination branch (lines 886-892), add the un-flag inside it, after the `metadata["destination_url"]` assignment:

```go
			// The flag belonged to the old destination. The new one starts
			// active and is checked as soon as this commits.
			if before.State == "flagged" {
				params.State = "active"
				changed = append(changed, "state")
				metadata["state"] = map[string]any{"from": "flagged", "to": "active"}
			}
```

Replace `updated = rowFromGet(before)` in the no-op branch (line 947) with `updated = rowFromGetForUpdate(before)`.

Replace the error switch after the transaction (lines 996-1004) with:

```go
	var status huma.StatusError
	switch {
	case errors.As(err, &status):
		// Already shaped: the 409 above, or a 422 from resolveFolderRef or
		// resolveTagRefs. Before this case existed those 422s fell through to
		// the 500 below.
		return nil, err
	case isUniqueViolation(err):
		return nil, huma.Error409Conflict("that slug is already taken on this domain")
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("link not found")
	case err != nil:
		d.Log.Error("update link", "error", err, "link_id", in.LinkID)
		return nil, huma.Error500InternalServerError("could not update the link")
	}
```

After the `if cacheChanged { … }` block (ends line 1012), add:

```go

	if updated.DestinationURL != previous.DestinationURL {
		if previous.State == "flagged" {
			// The confirmation vouched for the old destination. Left in place,
			// it could block the new one before Google has said a word about it.
			d.clearThreatConfirmation(ctx, updated.ID)
		}
		d.scanSoon(ctx, scanTarget{LinkID: updated.ID, TeamID: member.TeamID, URL: updated.DestinationURL})
	}
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'ApplyVerdict|Confirmation|RightAway|KeepsTheDestination|FlaggedLink|NewDestination|WhileItWaited|AnotherTeamsFolder' -count=1 -v`

Expected: PASS, every test listed.

- [ ] **Step 11: Run the package under the race detector**

Run: `cd apps/api && go test -race ./internal/api/ ./internal/cache/ -count=1`

Expected: `ok` for both. The existing link, folder and tag tests still pass: the fixtures leave `Scanner` nil, so no background check starts.

- [ ] **Step 12: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: clean; `but diff` lists exactly the files under **Files**. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): apply safe browsing verdicts" <ids>
```

---

### Task 6: The sweep endpoint and its workflow

**Files:**

- Create: `apps/api/internal/api/scan_sweep.go`
- Modify: `apps/api/internal/api/router.go:57-63` (add the route after `/internal/retention`)
- Modify: `apps/api/internal/api/export_test.go` (append)
- Modify: `apps/api/internal/api/scan_fake_test.go` (append `fail`, `onCheck`)
- Test: `apps/api/internal/api/scan_sweep_test.go`
- Modify: `apps/api/internal/config/config.go` (after `SafeBrowsingAPIKey`), `config_test.go` (append)
- Modify: `apps/api/.env.example` (after `SAFE_BROWSING_API_KEY=`)
- Create: `.github/workflows/scan.yml`

**Interfaces:**

- Consumes: Task 1's `ListDueLinksForScan`, `CountDueLinksForScan`; Task 5's `scanTarget`, `applyVerdict`, `verdictFlagged`/`verdictUnflagged`, `logCheckFailure`; `Deps.Scanner`.
- Produces: `func (d Deps) HandleScan(w http.ResponseWriter, r *http.Request)`; `Config.ScanToken string`; for tests, `api.ScanReport` and `(api.Deps).SweepForTest(ctx context.Context, limit int) (api.ScanReport, error)`; on `fakeChecker`, `fail(err error)` and `onCheck(fn func(context.Context))`.

- [ ] **Step 1: Write the failing tests**

Append to `apps/api/internal/api/export_test.go`:

```go

// ScanReport is the sweep's report, for assertions.
type ScanReport = scanReport

// SweepForTest runs one sweep with a batch limit the test chooses.
func (d Deps) SweepForTest(ctx context.Context, limit int) (ScanReport, error) {
	return d.sweep(ctx, limit)
}
```

Append to `apps/api/internal/api/scan_fake_test.go`:

```go

// fail makes every check fail with err.
func (c *fakeChecker) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// onCheck runs fn inside every check, with the check's own context, before it
// answers — for a test that needs something to happen while Google is
// "thinking".
func (c *fakeChecker) onCheck(fn func(context.Context)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.during = fn
}
```

Append to `apps/api/internal/config/config_test.go`:

```go
// Fail closed, like RETENTION_TOKEN: unset turns the sweep endpoint into a 404.
func TestScanTokenIsOptional(t *testing.T) {
	setRequired(t)
	t.Setenv("SCAN_TOKEN", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, cfg.ScanToken)

	t.Setenv("SCAN_TOKEN", "a-token")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.Equal(t, "a-token", cfg.ScanToken)
}
```

Create `apps/api/internal/api/scan_sweep_test.go`:

```go
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

const testScanToken = "test-scan-token"

// scanRequest sends one POST /internal/scan. token == "" sends no header at
// all, which is a different case from sending a wrong one.
func scanRequest(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/scan", nil)
	req.Host = "api.test"
	if token != "" {
		req.Header.Set("X-Scan-Token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// scanResponse is the endpoint's body, spelled out rather than imported: the
// workflow's logs read these keys, so a renamed one has to fail here.
type scanResponse struct {
	Checked   int   `json:"checked"`
	Flagged   int   `json:"flagged"`
	Unflagged int   `json:"unflagged"`
	Failed    int   `json:"failed"`
	Remaining int64 `json:"remaining"`
}

func decodeScan(t *testing.T, rec *httptest.ResponseRecorder) scanResponse {
	t.Helper()
	var body scanResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	return body
}

// sweepFixture is newFixture with a destination no other test uses, a fake
// checker that knows only what a test tells it, and a creation date in 2001.
//
// The sweep is instance-wide, and `go test ./...` runs other packages in
// parallel processes that commit due links of their own. Three things keep
// these tests exact anyway. The fake answers only for this test's own URLs,
// so every other link gets no verdict and nothing is written to it. The date
// puts this link at the head of the batch among never-checked links, before
// every link any test commits (internal/db's scan tests seed the year 2000,
// but inside transactions nobody else can see). And Failed, the one count the
// other links land in, is never asserted exactly here unless the batch limit
// keeps them out.
func sweepFixture(t *testing.T, opts ...func(*linkOptions)) (*fixture, *fakeChecker, string) {
	t.Helper()
	destination := uniqueDestination("sweep")
	f := newFixture(t, append([]func(*linkOptions){withDestination(destination)}, opts...)...)
	_, err := f.pool.Exec(context.Background(),
		`update link set created_at = '2001-01-01T00:00:00Z' where id = $1`, f.linkID)
	require.NoError(t, err)

	checker := newFakeChecker()
	f.deps.Scanner = checker
	f.deps.Config.ScanToken = testScanToken
	return f, checker, destination
}

// extraLink adds a due link beside the fixture's, on the same domain and team.
func extraLink(t *testing.T, f *fixture, destination, createdAt string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, created_by, created_at)
		 select domain_id, team_id, $2, $3, created_by, $4::timestamptz from link where id = $1
		 returning id`,
		f.linkID, "extra-"+uuid.NewString()[:8], destination, createdAt).Scan(&id))
	return id
}

func TestScanRefusesWithoutTheToken(t *testing.T) {
	f, _, _ := sweepFixture(t)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "").Code)
}

func TestScanRefusesAWrongToken(t *testing.T) {
	f, _, _ := sweepFixture(t)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "wrong").Code)
}

// An unset token disables the endpoint rather than opening it.
func TestScanIsDisabledWhenNoTokenIsConfigured(t *testing.T) {
	f, _, _ := sweepFixture(t)
	f.deps.Config.ScanToken = ""

	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "").Code)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "anything").Code)
}

// A 503, so the workflow's heartbeat goes missing instead of reporting a scan
// that never ran.
func TestScanAnswers503WithoutAScanner(t *testing.T) {
	f, _, _ := sweepFixture(t)
	f.deps.Scanner = nil

	require.Equal(t, http.StatusServiceUnavailable,
		scanRequest(t, api.NewRouter(f.deps), testScanToken).Code)
}

func TestScanFlagsADueLinkGoogleReports(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	checker.flag(destination, "SOCIAL_ENGINEERING")

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Equal(t, 1, report.Checked, "only this test's destination gets a verdict")
	require.Equal(t, 1, report.Flagged)
	require.Zero(t, report.Unflagged)
	require.GreaterOrEqual(t, report.Remaining, int64(1), "a flagged link is due on every sweep")

	require.Equal(t, "flagged", linkState(t, f.pool, f.linkID))
	entries := auditRows(t, f.pool, f.linkID, "link.flagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor)
}

// A false positive Google corrects is lifted even if nobody visits the link.
func TestScanUnflagsALinkGoogleNoLongerReports(t *testing.T) {
	f, checker, destination := sweepFixture(t, withState("flagged"))
	checker.pass(destination)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Equal(t, 1, report.Unflagged)
	require.Zero(t, report.Flagged)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Len(t, auditRows(t, f.pool, f.linkID, "link.unflagged"), 1)
}

// Review focus: a batch takes no more than its limit, oldest first, and what
// it leaves is still due.
func TestScanReportsTheBatchLimitAndWhatRemains(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	secondURL, thirdURL := uniqueDestination("sweep"), uniqueDestination("sweep")
	second := extraLink(t, f, secondURL, "2001-01-02T00:00:00Z")
	third := extraLink(t, f, thirdURL, "2001-01-03T00:00:00Z")
	for _, url := range []string{destination, secondURL, thirdURL} {
		checker.pass(url)
	}

	report, err := f.deps.SweepForTest(context.Background(), 2)
	require.NoError(t, err)

	require.Equal(t, 2, report.Checked)
	require.Zero(t, report.Failed, "a batch of two holds exactly this test's two oldest links")
	require.NotNil(t, scanCheckedAt(t, f.pool, f.linkID))
	require.NotNil(t, scanCheckedAt(t, f.pool, second))
	require.Nil(t, scanCheckedAt(t, f.pool, third), "the third waits for the next sweep")
	require.GreaterOrEqual(t, report.Remaining, int64(1))
}

// Review focus: the first sweeps after deploy find every existing link due.
// A run that runs out of time is not a failed run: what it did not reach stays
// due, counts as remaining rather than failed, and the endpoint still answers
// 200 so the heartbeat fires.
func TestSweepStopsAtItsBudgetAndLeavesTheRestDue(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	secondURL, thirdURL := uniqueDestination("sweep"), uniqueDestination("sweep")
	second := extraLink(t, f, secondURL, "2001-01-02T00:00:00Z")
	third := extraLink(t, f, thirdURL, "2001-01-03T00:00:00Z")
	for _, url := range []string{destination, secondURL, thirdURL} {
		checker.pass(url)
	}

	// The budget runs out while Google is answering: the answer arrives, but
	// no time is left to write any of it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.onCheck(func(context.Context) { cancel() })

	report, err := f.deps.SweepForTest(ctx, 3)
	require.NoError(t, err)

	require.Zero(t, report.Checked)
	require.Zero(t, report.Failed, "links the budget did not reach are remaining, not failed")
	require.GreaterOrEqual(t, report.Remaining, int64(3))
	for _, id := range []uuid.UUID{f.linkID, second, third} {
		require.Nil(t, scanCheckedAt(t, f.pool, id))
	}
}

// Google failing as a whole is a failed run: 502, nothing written, and the
// workflow withholds its heartbeat.
func TestScanAnswers502WhenGoogleFailsAndChangesNothing(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	checker.fail(errors.New("connection reset"))

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

func TestScanLogsASpentQuotaAtErrorLevel(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	logs := captureLogs(f)
	checker.fail(fmt.Errorf("%w: hashes.search answered 429", scanning.ErrQuotaExceeded))

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, logs.String(), `level=ERROR msg="safe browsing quota exhausted"`)
}

// Review focus: a PATCH lands while Google is answering for the old URL. The
// old URL's verdict must not stick to the new one.
func TestScanDiscardsAVerdictForADestinationChangedMidCheck(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	checker.flag(destination, "SOCIAL_ENGINEERING")
	checker.onCheck(func(ctx context.Context) {
		_, _ = f.pool.Exec(ctx,
			`update link set destination_url = 'https://example.org/replaced' where id = $1`, f.linkID)
	})

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Zero(t, decodeScan(t, rec).Flagged)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID), "the link stays due for its new destination")
}

// No verdict is not a clean verdict.
func TestScanLeavesALinkWithoutAVerdictAlone(t *testing.T) {
	f, _, _ := sweepFixture(t)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.GreaterOrEqual(t, decodeScan(t, rec).Failed, 1)
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// The workflow's logs are read by key; decoding into a map is what notices a
// renamed one, which the struct decode above would leave at its zero value.
func TestScanReportsUnderStableKeys(t *testing.T) {
	f, _, _ := sweepFixture(t)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"checked", "flagged", "unflagged", "failed", "remaining"}, keys)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'Scan|Sweep' -count=1 && go test ./internal/config/ -run ScanToken -count=1`

Expected: FAIL to compile with `undefined: scanReport` (in `export_test.go`) and `cfg.ScanToken undefined`.

- [ ] **Step 3: Add the configuration**

In `apps/api/internal/config/config.go`, after the `SafeBrowsingAPIKey string` field, add:

```go

	// ScanToken guards POST /internal/scan, the sweep .github/workflows/scan.yml
	// calls twice an hour. Empty disables the endpoint outright — it then
	// answers 404 for every caller — for RetentionToken's reason: a forgotten
	// value must not leave an endpoint that writes to every link open to
	// whoever guesses the path. Its own value, not RetentionToken's or
	// HealthCheckToken's, so one leaked string authorizes one job.
	ScanToken string
```

and in `Load`, after `cfg.SafeBrowsingAPIKey = …`, add:

```go
	cfg.ScanToken = os.Getenv("SCAN_TOKEN")
```

- [ ] **Step 4: Write the sweep**

Create `apps/api/internal/api/scan_sweep.go`:

```go
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// scanBatchSize is how many due links one sweep takes. 200 links mean at most
// 6,000 prefixes before de-duplication — six hashes.search requests of a few
// hundred milliseconds — and 200 short transactions of a few round trips each
// to a database in the same region, comfortably inside scanBudget. Twice an
// hour that is 9,600 links a day, several times this instance's link count, so
// the backlog the first sweeps after deploy find (every existing link is due)
// drains within hours.
const scanBatchSize = 200

const (
	// scanBudget bounds the run itself. The workflow's own step failing
	// withholds the heartbeat either way, as for /internal/retention.
	scanBudget = 25 * time.Second
	// scanCountBudget is what the closing count of remaining links gets, from
	// a context the run's budget does not cancel, so a run that used all of
	// its time still reports how much is left.
	scanCountBudget = 5 * time.Second
)

// errScanCheck marks a sweep whose call to Google failed as a whole.
var errScanCheck = errors.New("safe browsing check failed")

// scanReport is POST /internal/scan's body. The workflow's logs read these
// keys.
type scanReport struct {
	// Checked counts links whose verdict was applied, or discarded because the
	// link changed or disappeared while Google was answering.
	Checked   int `json:"checked"`
	Flagged   int `json:"flagged"`
	Unflagged int `json:"unflagged"`
	// Failed counts links that got no verdict, or whose verdict could not be
	// written. They stay due.
	Failed int `json:"failed"`
	// Remaining counts links still due after this run, flagged links
	// included: those are due on every sweep, so this does not reach zero
	// while any link is blocked.
	Remaining int64 `json:"remaining"`
}

// HandleScan answers POST /internal/scan: one sweep of due links, the half of
// scanning that does not depend on Vercel letting a goroutine finish. It
// checks links never checked for their current destination, flagged links
// (so a false positive Google corrects is lifted unvisited), and every other
// link once a day.
//
// The authorization is HandleRetention's, deliberately unchanged: the same 404
// for a missing and a wrong token, the same constant-time compare, the same
// empty-means-disabled rule. Like /internal/retention it sits on the root
// router above the hostname split, outside Huma and the OpenAPI document, so
// the token is the security boundary here, not the hostname.
func (d Deps) HandleScan(w http.ResponseWriter, r *http.Request) {
	if d.Config.ScanToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(r.Header.Get("X-Scan-Token")),
			[]byte(d.Config.ScanToken),
		) != 1 {
		http.NotFound(w, r)
		return
	}

	// 503 rather than a report of nothing: a sweep that cannot check anything
	// must withhold the heartbeat, not tell the monitor all is well.
	if d.Scanner == nil {
		d.Log.Warn("safe browsing sweep requested, but SAFE_BROWSING_API_KEY is unset")
		http.Error(w, "safe browsing scanning is not configured", http.StatusServiceUnavailable)
		return
	}

	report, err := d.sweep(r.Context(), scanBatchSize)
	switch {
	case errors.Is(err, errScanCheck):
		d.logCheckFailure(err, "phase", "sweep")
		http.Error(w, "safe browsing did not answer", http.StatusBadGateway)
		return
	case err != nil:
		d.Log.Error("safe browsing sweep failed", "error", err)
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	// Info, not Debug: in the workflow's history this line is what tells
	// "ran and found nothing due" from "did not run".
	d.Log.Info("safe browsing sweep ran",
		"checked", report.Checked, "flagged", report.Flagged, "unflagged", report.Unflagged,
		"failed", report.Failed, "remaining", report.Remaining)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// sweep checks one batch of due links in as few hashes.search calls as the
// prefix limit allows, and applies each verdict until the budget runs out.
// What the budget does not reach stays due for the next run.
func (d Deps) sweep(ctx context.Context, limit int) (scanReport, error) {
	now := d.now()
	budget, cancel := context.WithTimeout(ctx, scanBudget)
	defer cancel()

	// No team_id, and that is correct here. Everywhere else in this codebase
	// a query without a tenancy filter is a data-leak bug; this one acts for
	// the instance rather than for a caller, like the retention job's
	// deletes, and scoping it to a team would make "every link is checked
	// daily" depend on who happened to call. Every write a verdict causes
	// filters by the team_id this returns.
	due, err := d.Queries.ListDueLinksForScan(budget, db.ListDueLinksForScanParams{
		Now: now, BatchLimit: int32(limit),
	})
	if err != nil {
		return scanReport{}, fmt.Errorf("list due links: %w", err)
	}

	var report scanReport
	if len(due) > 0 {
		urls := make([]string, 0, len(due))
		seen := make(map[string]bool, len(due))
		for _, l := range due {
			if !seen[l.DestinationURL] {
				seen[l.DestinationURL] = true
				urls = append(urls, l.DestinationURL)
			}
		}

		results, err := d.Scanner.Check(budget, urls)
		if err != nil {
			return scanReport{}, fmt.Errorf("%w: %w", errScanCheck, err)
		}

	links:
		for _, l := range due {
			if budget.Err() != nil {
				break
			}
			result, ok := results[l.DestinationURL]
			if !ok {
				// No verdict is not a clean verdict; the link stays due.
				report.Failed++
				continue
			}
			outcome, err := d.applyVerdict(budget, scanTarget{
				LinkID: l.ID, TeamID: l.TeamID, URL: l.DestinationURL,
			}, result)
			switch {
			case err != nil && budget.Err() != nil:
				// The budget ran out inside this link's transaction. Nothing
				// was written, and it is remaining, not failed.
				break links
			case err != nil:
				report.Failed++
				d.Log.Warn("apply safe browsing verdict", "error", err, "link_id", l.ID)
			default:
				report.Checked++
				switch outcome {
				case verdictFlagged:
					report.Flagged++
				case verdictUnflagged:
					report.Unflagged++
				}
			}
		}
	}

	countCtx, cancelCount := context.WithTimeout(context.WithoutCancel(ctx), scanCountBudget)
	defer cancelCount()
	// Instance-wide, for the reason given above ListDueLinksForScan's call.
	remaining, err := d.Queries.CountDueLinksForScan(countCtx, now)
	if err != nil {
		return scanReport{}, fmt.Errorf("count due links: %w", err)
	}
	report.Remaining = remaining
	return report, nil
}
```

- [ ] **Step 5: Mount the route**

In `apps/api/internal/api/router.go`, after `root.With(middleware.Recoverer).Post("/internal/retention", deps.HandleRetention)` (line 63), add:

```go
	// POST /internal/scan checks due links against Safe Browsing, twice an
	// hour from .github/workflows/scan.yml. Placed and guarded exactly like
	// /internal/retention above, for the same reasons.
	root.With(middleware.Recoverer).Post("/internal/scan", deps.HandleScan)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'Scan|Sweep' -count=1 -v && go test ./internal/config/ -count=1`

Expected: PASS.

- [ ] **Step 7: Document the variables**

In `apps/api/.env.example`, after `SAFE_BROWSING_API_KEY=`, add:

```

# Guards POST /internal/scan, the Safe Browsing sweep .github/workflows/scan.yml
# calls at :07 and :37 every hour. Unset disables the endpoint entirely (it
# answers 404), which is deliberate: a forgotten value must not leave an
# endpoint that writes to every link open to whoever guesses the path. Its own
# value, not RETENTION_TOKEN's or HEALTH_CHECK_TOKEN's. Set it on the
# kurze-url-api Production environment and as the GitHub secret SCAN_TOKEN.
# 32 random characters.
SCAN_TOKEN=

# Not read by the API: a GitHub secret only, listed here so all three Safe
# Browsing settings are in one place. scan.yml pings this Better Stack
# heartbeat after a successful sweep and only then, so one signal covers a
# failing sweep and a workflow GitHub has silently disabled after 60 days of
# repository inactivity. Heartbeat period 30 minutes, grace at least 30
# minutes: GitHub starts scheduled runs late.
# SCAN_HEARTBEAT_URL=
```

- [ ] **Step 8: Add the workflow**

Create `.github/workflows/scan.yml`:

```yaml
# Golden rule 3 makes Safe Browsing scanning MVP scope. Every link is checked
# right after it is created or re-pointed, by a goroutine the API starts after
# its response, and Vercel does not promise to let that goroutine finish. This
# workflow is the half that does not depend on it: twice an hour it calls the
# API's sweep, which checks links never checked for their current destination,
# every flagged link (so a false positive Google corrects is lifted even if
# nobody visits the link), and every other link once a day.
#
# The heartbeat is the last step and runs only if the sweep succeeded, so one
# signal covers both hazards, exactly as in retention.yml: a failing sweep
# withholds the ping, and so does a workflow that never runs at all — GitHub
# disables scheduled workflows after 60 days of repository inactivity, and a
# disabled workflow does not fail, it stops.
#
# Worth knowing when reading the logs: the first sweeps after this shipped
# find every existing link due and drain the backlog 200 links at a time, so a
# large "remaining" then is expected. "remaining" also never reaches zero while
# any link is flagged, because a flagged link is due on every sweep. A 502
# means Google did not answer and nothing was written; a 503 means
# SAFE_BROWSING_API_KEY is unset on the API.
name: scan

on:
  schedule:
    # Off the hour and off the half hour: GitHub queues every scheduled job at
    # :00 and runs the backlog late.
    - cron: '7,37 * * * *'
  workflow_dispatch:
  pull_request:
    paths:
      - .github/workflows/scan.yml

concurrency:
  group: scan
  cancel-in-progress: false

permissions:
  contents: read

jobs:
  scan:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      # Skipped on pull requests: this calls production, and an unmerged
      # branch must not be able to flag or unflag production links. On a pull
      # request the job therefore only proves the file parses, which is what
      # the path filter above is for.
      - name: Check due links against Safe Browsing
        if: github.event_name != 'pull_request'
        env:
          SCAN_TOKEN: ${{ secrets.SCAN_TOKEN }}
        run: |
          if [ -z "$SCAN_TOKEN" ]; then
            echo "SCAN_TOKEN is not set" >&2
            exit 1
          fi
          curl --fail --silent --show-error --max-time 60 \
            --request POST \
            --header "X-Scan-Token: $SCAN_TOKEN" \
            https://api.kurze-url.app/internal/scan
          echo

      # Last, and only on success: see the note at the top of this file.
      - name: Report to the heartbeat monitor
        if: github.event_name != 'pull_request'
        env:
          SCAN_HEARTBEAT_URL: ${{ secrets.SCAN_HEARTBEAT_URL }}
        run: |
          if [ -z "$SCAN_HEARTBEAT_URL" ]; then
            echo "SCAN_HEARTBEAT_URL is not set" >&2
            exit 1
          fi
          curl --fail --silent --show-error "$SCAN_HEARTBEAT_URL"
```

- [ ] **Step 9: Run the package under the race detector**

Run: `cd apps/api && go test -race ./internal/api/ -count=1`

Expected: `ok`.

- [ ] **Step 10: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
eval "$(fnm env)" && pnpm format:check
but diff
```

Expected: clean (`pnpm format` also formats the YAML); `but diff` lists exactly the files under **Files**. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): sweep due links every 30 minutes" <ids>
```

---

### Task 7: The flagged branch of the redirect and verify paths

**Files:**

- Modify: `apps/api/internal/pages/pages.go:34-50` (kinds), `:52-66` (copy types), `:68-101` (copy), `:148-161` (`RenderError`), and append `RenderFlagged`
- Create: `apps/api/internal/pages/templates/flagged.html`
- Test: `apps/api/internal/pages/pages_test.go` (extend)
- Create: `apps/api/internal/api/flagged.go`
- Modify: `apps/api/internal/api/redirect.go:76-79`
- Modify: `apps/api/internal/api/verify.go:169-174`
- Modify: `apps/api/internal/api/scan_fake_test.go` (append `slow`)
- Test: `apps/api/internal/api/flagged_test.go`
- Modify: `apps/api/internal/api/redirect_test.go:97-111` (`TestRedirectRefusesLinksThatAreNotActive`), `apps/api/internal/api/verify_test.go:193-200` (`TestVerifyOnAnInactiveLinkIsRefusedBeforeCheckingThePassword`)
- Modify: `apps/api/go.mod` (`golang.org/x/sync` becomes a direct dependency)

**Interfaces:**

- Consumes: Task 5's `confirmThreats`, `inBackground`, `applyAndLog`, `logCheckFailure`, `scanTarget`, `errNoVerdict`, `cache.(*Client).ThreatConfirmation`; `Deps.Scanner`.
- Produces, in package `pages`: `const KindUnavailable Kind = "unavailable"`; `func RenderFlagged(w http.ResponseWriter, loc Locale, threatTypes []string)` (always 403).
- Produces, in package `api`: `func (d Deps) admit(w http.ResponseWriter, r *http.Request, locale pages.Locale, l link.Cached, now time.Time) bool` — writes the refusal itself when it returns false.
- Produces on `fakeChecker`: `slow(delay time.Duration)`.

- [ ] **Step 1: Write the failing page tests**

In `apps/api/internal/pages/pages_test.go`, add `pages.KindUnavailable` to the kind list in `TestRenderErrorHasDistinctCopyPerKind`, then append:

```go
// Google's terms, not style: a qualified claim, Google's definition of the
// threat, the attribution, and the admission that Google can be wrong.
func TestRenderFlaggedQualifiesTheThreatAndCreditsGoogle(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleEN, []string{"SOCIAL_ENGINEERING"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	body := rec.Body.String()
	require.Contains(t, body, "<h1>Suspected phishing site</h1>")
	require.Contains(t, body,
		`href="https://developers.google.com/search/docs/monitor-debug/security/social-engineering"`)
	require.Contains(t, body, `href="https://developers.google.com/safe-browsing/v4/advisory"`)
	require.Contains(t, body, "Advisory provided by Google")
	require.Contains(t, body, "cannot guarantee")
	require.NotContains(t, body, "<script", "the redirect surface's pages carry no JavaScript")
}

func TestRenderFlaggedLinksEveryReportedThreatToItsDefinition(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleEN, []string{"UNWANTED_SOFTWARE", "MALWARE"})

	body := rec.Body.String()
	require.Contains(t, body, "<h1>Possibly harmful software</h1>")
	require.Contains(t, body, `href="https://developers.google.com/search/docs/monitor-debug/security/malware"`)
	require.Contains(t, body, `href="https://www.google.com/about/unwanted-software-policy.html"`)
	require.NotContains(t, body, "phishing")
}

// A threat type Google adds later still gets a qualified text and a link, and
// a mix of categories gets the generic heading over each category's text.
func TestRenderFlaggedUsesTheGenericHeadingForAMixOrAnUnknownThreat(t *testing.T) {
	for _, threats := range [][]string{
		{"SOCIAL_ENGINEERING", "MALWARE"},
		{"THREAT_TYPE_FROM_THE_FUTURE"},
		nil,
	} {
		rec := httptest.NewRecorder()
		pages.RenderFlagged(rec, pages.LocaleEN, threats)
		require.Contains(t, rec.Body.String(), "<h1>Suspected unsafe site</h1>", "threats %v", threats)
	}

	rec := httptest.NewRecorder()
	pages.RenderFlagged(rec, pages.LocaleEN, []string{"SOCIAL_ENGINEERING", "MALWARE"})
	require.Contains(t, rec.Body.String(), "phishing site")
	require.Contains(t, rec.Body.String(), "possibly harmful software")

	rec = httptest.NewRecorder()
	pages.RenderFlagged(rec, pages.LocaleEN, []string{"THREAT_TYPE_FROM_THE_FUTURE"})
	require.Contains(t, rec.Body.String(), `href="https://safebrowsing.google.com/"`)
}

func TestRenderFlaggedIsGerman(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleDE, []string{"SOCIAL_ENGINEERING"})

	body := rec.Body.String()
	require.Contains(t, body, `lang="de"`)
	require.Contains(t, body, "Mutmaßliche Phishing-Seite")
	require.Contains(t, body, "Hinweis bereitgestellt von Google")
}

// The old copy said the link "was flagged as unsafe": absolute, and without
// Google's attribution. Any path that still renders KindFlagged through
// RenderError gets the qualified page instead.
func TestTheBlockPageNeverClaimsCertainty(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderError(rec, http.StatusForbidden, pages.LocaleEN, pages.KindFlagged)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), "flagged as unsafe")
	require.Contains(t, rec.Body.String(), "Advisory provided by Google")
}

// Without a fresh confirmation the terms forbid calling the destination
// unsafe, so this page names neither a threat nor Google.
func TestTheUnavailablePageSaysNothingAboutTheDestination(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderError(rec, http.StatusServiceUnavailable, pages.LocaleEN, pages.KindUnavailable)

	body := rec.Body.String()
	require.Contains(t, body, "temporarily unavailable")
	require.NotContains(t, body, "Google")
	require.NotContains(t, body, "unsafe")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/pages/ -count=1`

Expected: FAIL to compile with `undefined: pages.RenderFlagged` and `undefined: pages.KindUnavailable`.

- [ ] **Step 3: Write the pages**

In `apps/api/internal/pages/pages.go`, replace the `KindFlagged` comment and add `KindUnavailable` to the `Kind` block:

```go
	// KindFlagged means Google Safe Browsing reports the link's destination.
	// It always renders the qualified block page (see RenderFlagged), never a
	// line from the error copy: that page has to name the threat, credit
	// Google and say Google can be wrong.
	KindFlagged Kind = "flagged"
```

```go
	// KindUnavailable means a flagged link could not be re-confirmed just
	// now. It says nothing about the destination: without a confirmation from
	// Google younger than thirty minutes, the terms forbid calling it unsafe.
	KindUnavailable Kind = "unavailable"
```

Add `flagged flaggedStrings` as the last field of `localeStrings`, and add these declarations after `localeStrings`:

```go

// threatCategory groups Safe Browsing's threat types by what the block page
// says about them.
type threatCategory string

const (
	categoryPhishing threatCategory = "phishing"
	categoryHarmful  threatCategory = "harmful"
	categoryUnknown  threatCategory = "unknown"
)

// knownThreats are the threat types this page explains, in the order it
// explains them, each with Google's own definition. A type Google adds later
// is shown under the generic text rather than dropped, because dropping it
// would leave a block page that names no reason.
var knownThreats = []struct {
	threatType string
	category   threatCategory
	definition string
}{
	{"SOCIAL_ENGINEERING", categoryPhishing, "https://developers.google.com/search/docs/monitor-debug/security/social-engineering"},
	{"MALWARE", categoryHarmful, "https://developers.google.com/search/docs/monitor-debug/security/malware"},
	{"UNWANTED_SOFTWARE", categoryHarmful, "https://www.google.com/about/unwanted-software-policy.html"},
	{"POTENTIALLY_HARMFUL_APPLICATION", categoryHarmful, "https://developers.google.com/android/play-protect/potentially-harmful-applications"},
}

const (
	// advisoryURL is Google's Safe Browsing advisory, which the attribution
	// line links to.
	advisoryURL = "https://developers.google.com/safe-browsing/v4/advisory"
	// safeBrowsingURL is the definition offered for a threat type this page
	// does not know.
	safeBrowsingURL = "https://safebrowsing.google.com/"
)

// flaggedStrings is the block page's copy. Every claim is qualified
// ("suspected", "possibly", "may"), every threat links Google's definition of
// it, Google is credited, and the page says Google cannot promise to be right.
// All four are conditions of Google's Safe Browsing terms, not style.
type flaggedStrings struct {
	lead       string
	headings   map[threatCategory]string
	bodies     map[threatCategory]string
	advisory   string
	disclaimer string
	// definitions labels each threat type's definition link; the empty key
	// labels the generic one.
	definitions map[string]string
}
```

In `localeCopy`, delete the `KindFlagged` line from both `errors` maps, add `KindUnavailable` to both, and add a `flagged` value to both locales:

```go
				KindUnavailable: {"Link temporarily unavailable", "Link temporarily unavailable", "This short link cannot be opened right now. Please try again in a few minutes."},
```

```go
		flagged: flaggedStrings{
			lead: "This short link is not being forwarded.",
			headings: map[threatCategory]string{
				categoryPhishing: "Suspected phishing site",
				categoryHarmful:  "Possibly harmful software",
				categoryUnknown:  "Suspected unsafe site",
			},
			bodies: map[threatCategory]string{
				categoryPhishing: "Google Safe Browsing reports that its destination may be a phishing site: a page that tries to trick visitors into revealing passwords, payment details or other personal information.",
				categoryHarmful:  "Google Safe Browsing reports that its destination may distribute possibly harmful software, which could damage your device or act against your interests.",
				categoryUnknown:  "Google Safe Browsing reports that its destination may be unsafe.",
			},
			definitions: map[string]string{
				"SOCIAL_ENGINEERING":              "What Google means by phishing",
				"MALWARE":                         "What Google means by malware",
				"UNWANTED_SOFTWARE":               "What Google means by unwanted software",
				"POTENTIALLY_HARMFUL_APPLICATION": "What Google means by potentially harmful apps",
				"":                                "About Google Safe Browsing",
			},
			advisory:   "Advisory provided by Google",
			disclaimer: "Google works to provide accurate and up-to-date information about unsafe websites, but cannot guarantee that it is complete and error-free: some unsafe sites may not be detected, and some safe sites may be reported by mistake.",
		},
```

and for `LocaleDE`:

```go
				KindUnavailable: {"Link vorübergehend nicht verfügbar", "Link vorübergehend nicht verfügbar", "Dieser Kurzlink kann gerade nicht geöffnet werden. Bitte versuchen Sie es in einigen Minuten erneut."},
```

```go
		flagged: flaggedStrings{
			lead: "Dieser Kurzlink wird nicht weitergeleitet.",
			headings: map[threatCategory]string{
				categoryPhishing: "Mutmaßliche Phishing-Seite",
				categoryHarmful:  "Möglicherweise schädliche Software",
				categoryUnknown:  "Mutmaßlich unsichere Seite",
			},
			bodies: map[threatCategory]string{
				categoryPhishing: "Laut Google Safe Browsing ist das Ziel möglicherweise eine Phishing-Seite: eine Seite, die versucht, Besucherinnen und Besucher zur Preisgabe von Passwörtern, Zahlungsdaten oder anderen persönlichen Daten zu verleiten.",
				categoryHarmful:  "Laut Google Safe Browsing wird über das Ziel möglicherweise schädliche Software verbreitet, die Ihrem Gerät schaden oder gegen Ihre Interessen handeln könnte.",
				categoryUnknown:  "Laut Google Safe Browsing ist das Ziel möglicherweise unsicher.",
			},
			definitions: map[string]string{
				"SOCIAL_ENGINEERING":              "Was Google unter Phishing versteht",
				"MALWARE":                         "Was Google unter Malware versteht",
				"UNWANTED_SOFTWARE":               "Was Google unter unerwünschter Software versteht",
				"POTENTIALLY_HARMFUL_APPLICATION": "Was Google unter potenziell schädlichen Apps versteht",
				"":                                "Über Google Safe Browsing",
			},
			advisory:   "Hinweis bereitgestellt von Google",
			disclaimer: "Google bemüht sich um genaue und aktuelle Informationen über unsichere Websites, kann aber nicht garantieren, dass sie vollständig und fehlerfrei sind: Manche unsicheren Seiten werden möglicherweise nicht erkannt, und manche sicheren Seiten werden möglicherweise irrtümlich gemeldet.",
		},
```

"Advisory provided by Google" is the line Google's usage rules ask for. Open `https://developers.google.com/safe-browsing/reference/Appropriate.Usage` in a browser: if Google gives a German wording for it, use that instead of the translation above (here and in Task 9's `links.scanAdvisory`). While there, open each URL in `knownThreats`, `advisoryURL` and `safeBrowsingURL`, and replace any that moved with the address that page now points to — again in both places.

Replace `RenderError` with:

```go
// RenderError writes a localised error page with the given HTTP status.
// KindFlagged is the one kind it does not render from errors: it gets the
// qualified block page, with no threat type to name.
func RenderError(w http.ResponseWriter, status int, loc Locale, kind Kind) {
	if kind == KindFlagged {
		render(w, status, "flagged.html", flaggedPage(loc, nil))
		return
	}

	text, ok := localeCopy[loc].errors[kind]
	if !ok {
		text = localeCopy[LocaleEN].errors[KindServerError]
	}

	render(w, status, "error.html", errorView{
		Lang:    loc,
		Title:   text.title,
		Heading: text.heading,
		Body:    text.body,
	})
}
```

Append to `pages.go`:

```go

type definitionLink struct {
	Label string
	URL   string
}

type threatSection struct {
	category    threatCategory
	Body        string
	Definitions []definitionLink
}

type flaggedView struct {
	Lang        Locale
	Title       string
	Heading     string
	Lead        string
	Threats     []threatSection
	Advisory    string
	AdvisoryURL string
	Disclaimer  string
}

// RenderFlagged writes the block page for a link whose destination Google
// Safe Browsing reports, naming threatTypes. Always a 403. The redirect path
// calls it only with a confirmation from Google younger than thirty minutes.
func RenderFlagged(w http.ResponseWriter, loc Locale, threatTypes []string) {
	render(w, http.StatusForbidden, "flagged.html", flaggedPage(loc, threatTypes))
}

// flaggedPage builds one section per category reported — phishing, then
// harmful software, then anything this page does not know — with a
// definition link for each reported type. One category gets its own heading;
// a mix, or nothing known, gets the generic one.
func flaggedPage(loc Locale, threatTypes []string) flaggedView {
	locale, ok := localeCopy[loc]
	if !ok {
		loc, locale = LocaleEN, localeCopy[LocaleEN]
	}
	text := locale.flagged

	reported := make(map[string]bool, len(threatTypes))
	for _, threatType := range threatTypes {
		reported[threatType] = true
	}
	known := make(map[string]bool, len(knownThreats))
	for _, threat := range knownThreats {
		known[threat.threatType] = true
	}
	unknown := len(threatTypes) == 0
	for _, threatType := range threatTypes {
		if !known[threatType] {
			unknown = true
		}
	}

	var sections []threatSection
	for _, category := range []threatCategory{categoryPhishing, categoryHarmful} {
		var links []definitionLink
		for _, threat := range knownThreats {
			if threat.category == category && reported[threat.threatType] {
				links = append(links, definitionLink{Label: text.definitions[threat.threatType], URL: threat.definition})
			}
		}
		if len(links) > 0 {
			sections = append(sections, threatSection{category: category, Body: text.bodies[category], Definitions: links})
		}
	}
	if unknown {
		sections = append(sections, threatSection{
			category:    categoryUnknown,
			Body:        text.bodies[categoryUnknown],
			Definitions: []definitionLink{{Label: text.definitions[""], URL: safeBrowsingURL}},
		})
	}

	heading := text.headings[categoryUnknown]
	if len(sections) == 1 {
		heading = text.headings[sections[0].category]
	}

	return flaggedView{
		Lang:        loc,
		Title:       heading,
		Heading:     heading,
		Lead:        text.lead,
		Threats:     sections,
		Advisory:    text.advisory,
		AdvisoryURL: advisoryURL,
		Disclaimer:  text.disclaimer,
	}
}
```

Create `apps/api/internal/pages/templates/flagged.html`:

```html
<!doctype html>
<html lang="{{ .Lang }}">
	<head>
		<meta charset="utf-8" />
		<meta name="viewport" content="width=device-width, initial-scale=1" />
		<meta name="robots" content="noindex" />
		<title>{{ .Title }}</title>
		<style>
			:root {
				color-scheme: light dark;
			}
			body {
				margin: 0;
				min-height: 100vh;
				display: grid;
				place-items: center;
				font:
					16px/1.5 system-ui,
					sans-serif;
				padding: 1.5rem;
			}
			main {
				max-width: 36rem;
			}
			h1 {
				font-size: 1.5rem;
				margin: 0 0 0.5rem;
			}
			p {
				margin: 0 0 0.75rem;
			}
			ul {
				margin: 0 0 0.75rem;
				padding-left: 1.25rem;
			}
			a {
				color: inherit;
				text-decoration: underline;
				text-underline-offset: 0.2em;
			}
			.disclaimer {
				font-size: 0.875rem;
			}
		</style>
	</head>
	<body>
		<main>
			<h1>{{ .Heading }}</h1>
			<p>{{ .Lead }}</p>
			{{ range .Threats }}
			<p>{{ .Body }}</p>
			<ul>
				{{ range .Definitions }}
				<li><a href="{{ .URL }}" rel="noreferrer">{{ .Label }}</a></li>
				{{ end }}
			</ul>
			{{ end }}
			<p><a href="{{ .AdvisoryURL }}" rel="noreferrer">{{ .Advisory }}</a></p>
			<p class="disclaimer">{{ .Disclaimer }}</p>
		</main>
	</body>
</html>
```

`pnpm format` (oxfmt) may reflow this file; that is fine as long as `<h1>{{ .Heading }}</h1>` stays on one line, which the page tests read.

- [ ] **Step 4: Run the page tests to verify they pass**

Run: `cd apps/api && go test ./internal/pages/ -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Write the failing redirect tests**

Append to `apps/api/internal/api/scan_fake_test.go`:

```go

// slow makes every check take delay, or until its context ends.
func (c *fakeChecker) slow(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delay = delay
}
```

In `apps/api/internal/api/redirect_test.go`, delete the `{"flagged", http.StatusForbidden},` row from `TestRedirectRefusesLinksThatAreNotActive` and add above the function:

```go
// flagged is not in this table: whether a flagged link answers 403 depends on
// a confirmation from Google younger than thirty minutes, so it has its own
// tests in flagged_test.go.
```

In `apps/api/internal/api/verify_test.go`, change `TestVerifyOnAnInactiveLinkIsRefusedBeforeCheckingThePassword` to use a disabled link, which keeps its point without depending on Google:

```go
func TestVerifyOnAnInactiveLinkIsRefusedBeforeCheckingThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f := newFixture(t, withPasswordHash(hash), withState("disabled"))

	require.Equal(t, http.StatusGone,
		postPassword(t, f, "hello", "hunter2", "203.0.113.1").Code)
}
```

Create `apps/api/internal/api/flagged_test.go`:

```go
package api_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// flaggedFixture is newFixture with a flagged link and a fake checker.
func flaggedFixture(t *testing.T, opts ...func(*linkOptions)) (*fixture, *fakeChecker) {
	t.Helper()
	f := newFixture(t, append([]func(*linkOptions){withState("flagged")}, opts...)...)
	checker := newFakeChecker()
	f.deps.Scanner = checker
	return f, checker
}

func confirm(t *testing.T, f *fixture, ttl time.Duration, threats ...string) {
	t.Helper()
	require.NoError(t, f.deps.Cache.ConfirmThreats(context.Background(), f.linkID.String(), threats, ttl))
}

func TestAFlaggedLinkWithAFreshConfirmationShowsTheBlockPage(t *testing.T) {
	f, checker := flaggedFixture(t)
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "Suspected phishing site")
	require.Contains(t, body, "Advisory provided by Google")
	require.Contains(t, body, "https://developers.google.com/search/docs/monitor-debug/security/social-engineering")
	require.Zero(t, checker.callCount(), "a fresh confirmation needs no second opinion")
}

func TestAFlaggedLinkWithoutAConfirmationAsksGoogleAgain(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Possibly harmful software")
	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 3*time.Minute)
	require.LessOrEqual(t, ttl, 4*time.Minute)
	require.Equal(t, 1, checker.callCount())

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Equal(t, 1, checker.callCount(), "the second redirect is answered from the confirmation")
}

func TestAConfirmationNeverOutlivesThirtyMinutes(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.answer(fixtureDestination, scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 2 * time.Hour})

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)

	ttl := confirmationTTLLeft(t, f.deps.Cache, f.linkID)
	require.Greater(t, ttl, 28*time.Minute)
	require.LessOrEqual(t, ttl, 29*time.Minute)
}

// cacheDuration missing, zero or unreadable all arrive as ValidFor 0: the
// page may be shown on that fresh answer, but nothing may vouch for it later.
func TestAVerdictWithoutAUsableValidityBlocksButConfirmsNothing(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.answer(fixtureDestination, scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: 0})

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Negative(t, confirmationTTLLeft(t, f.deps.Cache, f.linkID))

	require.Equal(t, http.StatusForbidden, get(t, f, "/hello", nil).Code)
	require.Equal(t, 2, checker.callCount())
}

func TestAFlaggedLinkGoogleNoLongerReportsIsForwardedAndUnflagged(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.pass(fixtureDestination)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, fixtureDestination, rec.Header().Get("Location"))
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.NotEmpty(t, *f.rows, "a forwarded click is a click, exactly as for an active link")
	eventuallyState(t, f.pool, f.linkID, "active")
}

func TestAFlaggedLinkAnswers503WhenGoogleFails(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.fail(errors.New("connection reset"))

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "300", rec.Header().Get("Retry-After"))
	body := rec.Body.String()
	require.Contains(t, body, "temporarily unavailable")
	require.NotContains(t, body, "Google", "without a fresh confirmation the page may not call it unsafe")
}

func TestAFlaggedLinkAnswers503WithoutAScanner(t *testing.T) {
	f, _ := flaggedFixture(t)
	f.deps.Scanner = nil

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "300", rec.Header().Get("Retry-After"))
}

// Golden rule 2's one exception is bounded: two seconds, not the client's
// five, and not however long Google takes.
func TestTheRecheckWaitsTwoSecondsAtMost(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")
	checker.slow(10 * time.Second)

	start := time.Now()
	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestConcurrentRedirectsShareOneRecheck(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "MALWARE")
	// Long enough that all five requests reach the re-check while the first
	// one's call is still in flight.
	checker.slow(time.Second)

	codes := make([]int, 5)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = get(t, f, "/hello", nil).Code
		}()
	}
	wg.Wait()

	for _, code := range codes {
		require.Equal(t, http.StatusForbidden, code)
	}
	require.Equal(t, 1, checker.callCount(),
		"one instance asks Google once per flagged link, however many visitors arrive at once")
}

// Review focus: Upstash refusing every command. A failed GET reads as "no
// confirmation" and re-checks; a failed SET does not turn a confirmed block
// into an error page.
func TestARedisFailureDuringAFlaggedRedirectFallsBackToARecheck(t *testing.T) {
	f, checker := flaggedFixture(t)
	checker.flag(fixtureDestination, "SOCIAL_ENGINEERING")
	require.NoError(t, f.deps.Cache.Close())

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "Suspected phishing site")
	require.Equal(t, 1, checker.callCount())
}

// Golden rule 2: an active link calls nothing new, on either path.
func TestAnActiveLinkNeverAsksGoogle(t *testing.T) {
	checker := newFakeChecker()
	checker.flag(fixtureDestination, "MALWARE")

	f := newFixture(t)
	f.deps.Scanner = checker
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	hash, err := auth.HashPassword("Kartoffelsalat!7")
	require.NoError(t, err)
	protected := newFixture(t, withPasswordHash(hash))
	protected.deps.Scanner = checker
	require.Equal(t, http.StatusFound,
		postPassword(t, protected, "hello", "Kartoffelsalat!7", "203.0.113.5").Code)

	require.Zero(t, checker.callCount())
}

// The state check still comes before the password, so a blocked link is never
// an oracle for its password.
func TestVerifyShowsTheBlockPageBeforeAskingForThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f, checker := flaggedFixture(t, withPasswordHash(hash))
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := postPassword(t, f, "hello", "hunter2", "203.0.113.1")

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Advisory provided by Google")
	require.Zero(t, checker.callCount())
}

func TestVerifyOnAFlaggedLinkGoogleClearsChecksThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f, checker := flaggedFixture(t, withPasswordHash(hash))
	checker.pass(fixtureDestination)

	require.Equal(t, http.StatusFound, postPassword(t, f, "hello", "hunter2", "203.0.113.1").Code)
}

func TestTheBlockPageIsGerman(t *testing.T) {
	f, _ := flaggedFixture(t)
	confirm(t, f, 10*time.Minute, "SOCIAL_ENGINEERING")

	rec := get(t, f, "/hello", map[string]string{"Accept-Language": "de-DE,de;q=0.9"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "Mutmaßliche Phishing-Seite")
	require.Contains(t, rec.Body.String(), "Hinweis bereitgestellt von Google")
}
```

- [ ] **Step 6: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'Flagged|Recheck|ActiveLinkNeverAsks|BlockPage|Verify' -count=1`

Expected: FAIL — the confirmed-flag test gets a 403 with the old copy (no "Suspected phishing site"), every re-check test gets 403 without calling the checker, and the clean re-check gets 403 instead of 302.

- [ ] **Step 7: Write the flagged branch**

Create `apps/api/internal/api/flagged.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// flaggedRecheckTimeout bounds the one wait golden rule 2 allows on the
// redirect path, and only for a flagged link without a fresh confirmation:
// Google's terms forbid blocking it on stale data, and forwarding it unchecked
// would be worse. An active link never reaches this file.
const flaggedRecheckTimeout = 2 * time.Second

// unavailableRetryAfter is what the neutral page tells a browser and a
// crawler: long enough that a retry finds Google answering again, short enough
// that a visitor might still try.
const unavailableRetryAfter = "300"

// flaggedRechecks shares one Safe Browsing call between concurrent redirects of
// the same flagged link on this instance, so a link that is being shared
// widely costs one lookup per instance per confirmation window, not one per
// visitor. The key includes the destination, so a re-check started before a
// PATCH never answers for the URL that replaced it.
var flaggedRechecks singleflight.Group

// errScanningOff means SAFE_BROWSING_API_KEY is unset, so a flagged link
// cannot be re-confirmed.
var errScanningOff = errors.New("safe browsing scanning is off")

// admit reports whether a resolved link may be followed, and writes the
// refusal itself when it may not. unavailable decides in its usual order —
// expiry, then state — and only its flagged answer needs more than a page.
func (d Deps) admit(w http.ResponseWriter, r *http.Request, locale pages.Locale, l link.Cached, now time.Time) bool {
	status, kind, blocked := unavailable(l, now)
	switch {
	case !blocked:
		return true
	case kind == pages.KindFlagged:
		return d.admitFlagged(w, r, locale, l)
	default:
		pages.RenderError(w, status, locale, kind)
		return false
	}
}

// admitFlagged decides a flagged link:
//
//   - a confirmation younger than thirty minutes shows the block page, 403;
//   - otherwise Google is asked once, at most two seconds. Threats confirm the
//     block and are remembered; a clean answer forwards the visitor exactly
//     as an active link would and lifts the flag in the background (the sweep
//     lifts it if that goroutine is lost);
//   - an error, a timeout or no scanner answers the neutral 503, which says
//     nothing about the destination, because without a fresh confirmation
//     the terms forbid calling it unsafe.
func (d Deps) admitFlagged(w http.ResponseWriter, r *http.Request, locale pages.Locale, l link.Cached) bool {
	ctx := r.Context()

	if threats, ok := d.threatConfirmation(ctx, l.ID); ok {
		pages.RenderFlagged(w, locale, threats)
		return false
	}

	result, err := d.recheckFlagged(ctx, l)
	if err != nil {
		d.logCheckFailure(err, "link_id", l.ID)
		w.Header().Set("Retry-After", unavailableRetryAfter)
		pages.RenderError(w, http.StatusServiceUnavailable, locale, pages.KindUnavailable)
		return false
	}

	if len(result.ThreatTypes) > 0 {
		d.confirmThreats(ctx, l.ID, result.ThreatTypes, result.ValidFor)
		pages.RenderFlagged(w, locale, result.ThreatTypes)
		return false
	}

	target := scanTarget{LinkID: l.ID, TeamID: l.TeamID, URL: l.DestinationURL}
	d.inBackground(ctx, l.ID, func(ctx context.Context) {
		d.applyAndLog(ctx, target, result)
	})
	return true
}

// threatConfirmation reads a flagged link's confirmation. A Redis failure
// reads as "none" and is a Warn: the caller then asks Google, which costs a
// wait rather than a wrong answer, and the redirect lookup has already
// reported Redis at Error.
func (d Deps) threatConfirmation(ctx context.Context, linkID uuid.UUID) ([]string, bool) {
	if d.Cache == nil {
		return nil, false
	}
	threats, ok, err := d.Cache.ThreatConfirmation(ctx, linkID.String())
	if err != nil {
		d.Log.Warn("safe browsing confirmation read failed, re-checking", "error", err, "link_id", linkID)
		return nil, false
	}
	return threats, ok
}

// recheckFlagged asks Google about one flagged link's destination.
//
// The shared call runs on a context detached from the request that started
// it, bounded by flaggedRecheckTimeout: the first visitor closing the tab must
// not fail the check for everyone waiting on it. It recovers a panic itself,
// because singleflight re-raises one from DoChan on a goroutine of its own,
// where nothing can catch it.
func (d Deps) recheckFlagged(ctx context.Context, l link.Cached) (scanning.Result, error) {
	if d.Scanner == nil {
		return scanning.Result{}, errScanningOff
	}

	outcome := flaggedRechecks.DoChan(l.ID.String()+"|"+l.DestinationURL, func() (value any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flaggedRecheckTimeout)
		defer cancel()

		results, err := d.Scanner.Check(checkCtx, []string{l.DestinationURL})
		if err != nil {
			return nil, err
		}
		result, ok := results[l.DestinationURL]
		if !ok {
			return nil, errNoVerdict
		}
		return result, nil
	})

	select {
	case <-ctx.Done():
		return scanning.Result{}, ctx.Err()
	case shared := <-outcome:
		if shared.Err != nil {
			return scanning.Result{}, shared.Err
		}
		result, ok := shared.Val.(scanning.Result)
		if !ok {
			return scanning.Result{}, errNoVerdict
		}
		return result, nil
	}
}
```

In `apps/api/internal/api/redirect.go`, replace lines 76-79:

```go
	if !d.admit(w, r, locale, resolved, now) {
		return
	}
```

In `apps/api/internal/api/verify.go`, replace lines 169-174:

```go
	// State is checked before the password so a disabled or flagged link never
	// becomes an oracle for guessing its password. A flagged link with a fresh
	// confirmation shows the block page here too; one Google has cleared goes
	// on to the password like an active link.
	if !d.admit(w, r, locale, resolved, d.now()) {
		return link.Cached{}, "", false
	}
```

Then promote the dependency (it is already in the module graph as an indirect one, so nothing is downloaded):

Run: `cd apps/api && go mod tidy && grep -n "golang.org/x/sync" go.mod`

Expected: `golang.org/x/sync v0.23.0` now sits in the first `require` block without `// indirect`; `go.sum` is unchanged.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'Flagged|Recheck|ActiveLinkNeverAsks|BlockPage|Verify|RedirectRefuses' -count=1 -v`

Expected: PASS. `TestTheRecheckWaitsTwoSecondsAtMost` takes about two seconds.

- [ ] **Step 9: Run the whole API under the race detector**

Run: `cd apps/api && go test ./... -count=1 && go test -race ./internal/analytics/... ./internal/api/... -count=1`

Expected: every package `ok` — the CI job's two commands.

- [ ] **Step 10: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && cd ../..
but diff
```

Expected: clean; `but diff` lists exactly the files under **Files**. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): re-check flagged links on redirect" <ids>
```

---

### Task 8: The nested verdict and the `state` enum

**Files:**

- Modify: `apps/api/internal/api/links.go:30-57` (`Link`: the `state` enum and the `scan` field; add `LinkScan` after it), `:755` (`UpdateLinkInput.Body.State` doc), `:801-824` (`getLink`; add `linkScan` after it)
- Test: `apps/api/internal/api/link_scan_view_test.go`
- Regenerated: `apps/api/openapi.json`, `packages/api-client/src/generated/*`

**Interfaces:**

- Consumes: Task 1's `GetLinkForAPIRow.ScanCheckedAt`/`ScanDestination` and `GetLatestLinkScanResult`; Task 5's `ApplyVerdictForTest` (tests only).
- Produces in the API: `type LinkScan struct { Verdict string; ThreatTypes []string; Since *time.Time; CheckedAt time.Time }`, `Link.Scan *LinkScan` (`json:"scan,omitempty"`), `Link.State` with `enum:"active,disabled,expired,flagged"`.
- Produces in `@kurze-url/api-client`: `Link.state: 'active' | 'disabled' | 'expired' | 'flagged'`, `Link.scan?: LinkScan`, and `type LinkScan = { verdict: 'clean' | 'flagged'; threat_types: Array<string> | null; since?: string; checked_at: string }`.

- [ ] **Step 1: Write the failing tests**

Create `apps/api/internal/api/link_scan_view_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// scanView is the nested verdict as a client reads it.
type scanView struct {
	Verdict     string     `json:"verdict"`
	ThreatTypes []string   `json:"threat_types"`
	Since       *time.Time `json:"since"`
	CheckedAt   time.Time  `json:"checked_at"`
}

type linkWithScan struct {
	State string    `json:"state"`
	Scan  *scanView `json:"scan"`
}

// getLinkView reads one link as a viewer and returns it decoded and raw: the
// raw body is what tells an omitted key from a null one.
func getLinkView(t *testing.T, f *tenancyFixture, id uuid.UUID) (linkWithScan, string) {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+id.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	return decode[linkWithScan](t, rec), rec.Body.String()
}

func applyForTest(t *testing.T, f *tenancyFixture, id uuid.UUID, url string, threats ...string) {
	t.Helper()
	_, err := f.deps.ApplyVerdictForTest(context.Background(), id, f.teamID, url,
		scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute})
	require.NoError(t, err)
}

func TestGetLinkOmitsTheScanUntilTheDestinationIsChecked(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "ungeprueft", "https://example.org/ungeprueft")

	_, raw := getLinkView(t, f, created.ID)
	require.NotContains(t, raw, `"scan"`, "absent, not null: the schema promises an optional object")
}

func TestGetLinkReportsAFlag(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "gemeldet", "https://example.org/gemeldet")
	applyForTest(t, f, created.ID, created.DestinationURL, "SOCIAL_ENGINEERING")

	body, _ := getLinkView(t, f, created.ID)

	require.Equal(t, "flagged", body.State)
	require.NotNil(t, body.Scan)
	require.Equal(t, "flagged", body.Scan.Verdict)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, body.Scan.ThreatTypes)
	require.NotNil(t, body.Scan.Since)
	require.False(t, body.Scan.CheckedAt.IsZero())
}

func TestGetLinkReportsACleanCheckWithoutASince(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sauber", "https://example.org/sauber")
	applyForTest(t, f, created.ID, created.DestinationURL)

	body, raw := getLinkView(t, f, created.ID)

	require.NotNil(t, body.Scan)
	require.Equal(t, "clean", body.Scan.Verdict)
	require.Contains(t, raw, `"threat_types":[]`, "a list, never null")
	require.Nil(t, body.Scan.Since, "clean since its first check: no row says when it became clean")
}

// A verdict never describes a URL it did not see.
func TestGetLinkDropsTheScanWhenTheDestinationChanges(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "umgezogen", "https://example.org/alt")
	applyForTest(t, f, created.ID, created.DestinationURL, "MALWARE")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body, raw := getLinkView(t, f, created.ID)
	require.Equal(t, "active", body.State)
	require.NotContains(t, raw, `"scan"`)
}

func TestListLinksNeverCarriesTheScan(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "liste", "https://example.org/liste")
	applyForTest(t, f, created.ID, created.DestinationURL, "MALWARE")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), `"state":"flagged"`)
	require.NotContains(t, rec.Body.String(), `"scan"`)
}

// The generated document, not the Go: packages/api-client is generated from
// it, and CLAUDE.md's Huma nullability entry is about exactly the case where
// the two disagree. A bare tag on an object pointer would publish a required
// property that arrives as null.
func TestLinkSchemaPublishesTheScanAsAnOptionalObjectAndStateAsAnEnum(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)
	schemas := humaAPI.OpenAPI().Components.Schemas.Map()

	linkSchema := schemas["Link"]
	require.NotNil(t, linkSchema)
	require.NotContains(t, linkSchema.Required, "scan")
	scan := linkSchema.Properties["scan"]
	require.NotNil(t, scan)
	require.Equal(t, "#/components/schemas/LinkScan", scan.Ref)
	require.False(t, scan.Nullable)
	require.ElementsMatch(t, []any{"active", "disabled", "expired", "flagged"},
		linkSchema.Properties["state"].Enum)

	linkScan := schemas["LinkScan"]
	require.NotNil(t, linkScan)
	require.ElementsMatch(t, []string{"verdict", "threat_types", "checked_at"}, linkScan.Required)
	require.ElementsMatch(t, []any{"clean", "flagged"}, linkScan.Properties["verdict"].Enum)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'GetLink.*Scan|GetLinkReports|GetLinkDrops|NeverCarriesTheScan|LinkSchemaPublishes' -count=1`

Expected: FAIL — `TestGetLinkReportsAFlag` finds `Scan` nil, and the schema test finds no `scan` property and no enum on `state`.

- [ ] **Step 3: Change the `Link` type**

In `apps/api/internal/api/links.go`, change the `State` field of `Link` (line 41) to:

```go
	State            string     `json:"state" enum:"active,disabled,expired,flagged" doc:"expired follows from expires_at. flagged is set and lifted only by Safe Browsing scanning."`
```

After the `UpdatedAt` field (line 56) add:

```go
	// Scan is an object, so it follows the Huma rule FolderID's comment above
	// describes for a different reason: an omitempty pointer, which Huma
	// publishes as optional and non-nullable, never a bare tag, which would
	// publish a required property the handler answers with null (CLAUDE.md,
	// Huma nullability). Only getLink fills it; every other operation that
	// answers with a Link leaves it out.
	Scan *LinkScan `json:"scan,omitempty" doc:"The Safe Browsing verdict on the current destination. Only GET /v1/links/{link_id} carries it, and only once that destination has been checked."`
```

After the `Link` type add:

```go

// LinkScan is the latest Safe Browsing verdict on a link's current
// destination, nested in GET /v1/links/{link_id} rather than served by an
// endpoint of its own (docs/planning/06-api-design.md).
type LinkScan struct {
	Verdict     string     `json:"verdict" enum:"clean,flagged" doc:"flagged while the link is blocked. Only a later clean check by Google, or a new destination, lifts it."`
	ThreatTypes []string   `json:"threat_types" doc:"Google's threat types for a flagged link, such as SOCIAL_ENGINEERING or MALWARE; empty when clean. Google adds types over time, so treat one you do not know as a generic threat."`
	Since       *time.Time `json:"since,omitempty" doc:"When the link entered this verdict for its current destination. Absent while it has been clean since that destination was first checked."`
	CheckedAt   time.Time  `json:"checked_at" doc:"When Google last checked the current destination, whatever it found."`
}
```

Change the `State` field of `UpdateLinkInput.Body` (line 755) to:

```go
		State            *string     `json:"state,omitempty" enum:"active,disabled" doc:"expired follows from expires_at and flagged is set by scanning; neither is a caller's to write. A flagged link refuses any state with 409: change its destination, or wait until Google clears it."`
```

- [ ] **Step 4: Fill it in `getLink`**

In `getLink`, replace `items := []Link{d.linkResponse(rowFromGet(row))}` (line 817) with:

```go
	body := d.linkResponse(rowFromGet(row))
	scan, err := d.linkScan(ctx, member.TeamID, row)
	if err != nil {
		d.Log.Error("load link scan verdict", "error", err, "link_id", in.LinkID)
		return nil, huma.Error500InternalServerError("could not load the link")
	}
	body.Scan = scan

	items := []Link{body}
```

After `getLink` add:

```go

// linkScan projects a link's Safe Browsing state for getLink. It is absent
// until the link's current destination has been checked: a verdict never
// describes a URL it did not see, so after a destination change there is
// nothing to report until Google has looked at the new one. since comes from
// the latest link_scan_result row, and only when that row records the current
// verdict for the current destination: a link whose flag a new destination
// lifted has no row saying when it became clean.
func (d Deps) linkScan(ctx context.Context, teamID uuid.UUID, row db.GetLinkForAPIRow) (*LinkScan, error) {
	if row.ScanCheckedAt == nil || row.ScanDestination == nil || *row.ScanDestination != row.DestinationURL {
		return nil, nil
	}

	scan := &LinkScan{Verdict: "clean", ThreatTypes: []string{}, CheckedAt: *row.ScanCheckedAt}
	if row.State == "flagged" {
		scan.Verdict = "flagged"
	}

	latest, err := d.Queries.GetLatestLinkScanResult(ctx, db.GetLatestLinkScanResultParams{
		LinkID: row.ID, TeamID: teamID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return scan, nil
	case err != nil:
		return nil, fmt.Errorf("read the latest scan result: %w", err)
	}

	if latest.Verdict == scan.Verdict && latest.DestinationURL == row.DestinationURL {
		since := latest.ScannedAt
		scan.Since = &since
		if scan.Verdict == "flagged" && latest.ThreatTypes != nil {
			scan.ThreatTypes = latest.ThreatTypes
		}
	}
	return scan, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && go test ./internal/api/ -run 'GetLink.*Scan|GetLinkReports|GetLinkDrops|NeverCarriesTheScan|LinkSchemaPublishes' -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Regenerate the document and the client**

Run: `eval "$(fnm env)" && pnpm run generate:api && grep -n "scan?: LinkScan\|export type LinkScan\|'flagged'" packages/api-client/src/generated/types.gen.ts`

Expected: `apps/api/openapi.json` and `packages/api-client/src/generated/types.gen.ts` change; the grep shows `scan?: LinkScan;`, `export type LinkScan = {`, `verdict: 'clean' | 'flagged';` and `state: 'active' | 'disabled' | 'expired' | 'flagged';`. `TestLinkStatsOpenAPISchemaInlinesTheDayCounts` reads the regenerated file, so re-run `cd apps/api && go test ./internal/api/ -run OpenAPI -count=1` and expect PASS.

- [ ] **Step 7: Typecheck the web app against the narrower `state`**

Run: `eval "$(fnm env)" && pnpm typecheck`

Expected: no errors. Every link fixture in `apps/web` is typed as `Link` or built by a function returning `Link`, so `state: 'active'` stays assignable. If one is not and now fails with `Type 'string' is not assignable to type '"active" | "disabled" | "expired" | "flagged"'`, annotate that fixture as `Link` (`import type { Link } from '@kurze-url/api-client'`) the way `teams.$teamSlug.links.$linkId.role.test.tsx` declares `LINK`; do not widen the generated type.

- [ ] **Step 8: Format, vet and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format && pnpm format:check
cd apps/api && test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run ./... && go test ./internal/api/ -count=1 && cd ../..
but diff
```

Expected: clean; `but diff` lists `links.go`, the new test, `openapi.json` and the regenerated client files. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(api): report a link's scan verdict" <ids>
```

---

### Task 9: The web app — badge, notice and system actor

**Files:**

- Create: `apps/web/src/lib/safe-browsing.ts`, `apps/web/src/lib/safe-browsing.test.ts`
- Create: `apps/web/src/components/link-scan-notice.tsx`, `.test.tsx`, `.stories.tsx`
- Modify: `apps/web/src/components/link-list.tsx:8` (import) and `:294-299` (badge); `link-list.test.tsx` (after "marks a password-protected link"); `link-list.stories.tsx` (after `Populated`)
- Modify: `apps/web/src/lib/audit-actor.ts`, `apps/web/src/lib/audit-actor.test.ts`
- Modify: `apps/web/src/components/audit-entry-table.tsx:21-43` (`actionLabelKeys`), `:198-202` (actor label); `audit-entry-table.test.tsx`; `audit-entry-table.stories.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx` (import; render after the `<h1>` at line 718)
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.role.test.tsx` (`renderPage` takes a link; two tests)
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`, `apps/web/src/i18n/catalogues.test.ts`

**Interfaces:**

- Consumes: Task 8's generated `Link['state']`, `Link['scan']`, `LinkScan`; Task 4's `link.flagged`/`link.unflagged` with a null actor.
- Produces: `LinkScanNotice({ scan?: LinkScan; state: Link['state'] })`; `resolveActor(actorUserId: string | undefined, membersById: ReadonlyMap<string, string>, action: string): ActorDisplay` with the new kind `safeBrowsing`; `threatCategories`, `headingCategory`, `threatDefinitions`, `ADVISORY_URL`, `REPORT_ERROR_URL`, `SAFE_BROWSING_URL` in `lib/safe-browsing.ts`.

- [ ] **Step 1: Add the copy**

In `apps/web/src/i18n/locales/en.json`, after `"passwordBadge": "Password protected",` (line 133) insert:

```json
		"flaggedBadge": "Blocked",
		"scanHeadingPhishing": "Suspected phishing site",
		"scanHeadingHarmful": "Possibly harmful software",
		"scanHeadingUnknown": "Suspected unsafe site",
		"scanBlocked": "This link is blocked: visitors see a warning page instead of being forwarded.",
		"scanPhishing": "Google Safe Browsing reports that its destination may be a phishing site — a page that tries to trick people into giving away passwords, payment details or other personal information.",
		"scanHarmful": "Google Safe Browsing reports that its destination may distribute possibly harmful software.",
		"scanUnknown": "Google Safe Browsing reports that its destination may be unsafe.",
		"scanDefinitionSocialEngineering": "What Google means by phishing",
		"scanDefinitionMalware": "What Google means by malware",
		"scanDefinitionUnwantedSoftware": "What Google means by unwanted software",
		"scanDefinitionHarmfulApplication": "What Google means by potentially harmful apps",
		"scanDefinitionUnknown": "About Google Safe Browsing",
		"scanAdvisory": "Advisory provided by Google",
		"scanDisclaimer": "Google works to provide accurate and up-to-date information about unsafe websites, but cannot guarantee that it is complete and error-free: some unsafe sites may not be detected, and some safe sites may be reported by mistake.",
		"scanReport": "Report an incorrect warning to Google",
		"scanNextSteps": "We re-check every 30 minutes. Change the destination, or wait until Google clears the site.",
```

after `"actorDeletedAccount": "A deleted account",` (line 250) insert:

```json
		"actorSafeBrowsing": "Google Safe Browsing",
```

and after `"actionDomainDeleted": "Domain deleted",` (line 289) insert:

```json
		"actionLinkFlagged": "Link blocked by Safe Browsing",
		"actionLinkUnflagged": "Link unblocked by Safe Browsing",
```

In `apps/web/src/i18n/locales/de.json`, at the same three places (lines 133, 250, 289):

```json
		"flaggedBadge": "Gesperrt",
		"scanHeadingPhishing": "Mutmaßliche Phishing-Seite",
		"scanHeadingHarmful": "Möglicherweise schädliche Software",
		"scanHeadingUnknown": "Mutmaßlich unsichere Seite",
		"scanBlocked": "Dieser Link ist gesperrt: Wer ihn öffnet, sieht eine Warnseite und wird nicht weitergeleitet.",
		"scanPhishing": "Laut Google Safe Browsing ist das Ziel möglicherweise eine Phishing-Seite – eine Seite, die Menschen dazu verleiten will, Passwörter, Zahlungsdaten oder andere persönliche Daten preiszugeben.",
		"scanHarmful": "Laut Google Safe Browsing wird über das Ziel möglicherweise schädliche Software verbreitet.",
		"scanUnknown": "Laut Google Safe Browsing ist das Ziel möglicherweise unsicher.",
		"scanDefinitionSocialEngineering": "Was Google unter Phishing versteht",
		"scanDefinitionMalware": "Was Google unter Malware versteht",
		"scanDefinitionUnwantedSoftware": "Was Google unter unerwünschter Software versteht",
		"scanDefinitionHarmfulApplication": "Was Google unter potenziell schädlichen Apps versteht",
		"scanDefinitionUnknown": "Über Google Safe Browsing",
		"scanAdvisory": "Hinweis bereitgestellt von Google",
		"scanDisclaimer": "Google bemüht sich um genaue und aktuelle Informationen über unsichere Websites, kann aber nicht garantieren, dass sie vollständig und fehlerfrei sind: Manche unsicheren Seiten werden möglicherweise nicht erkannt, und manche sicheren Seiten werden möglicherweise irrtümlich gemeldet.",
		"scanReport": "Eine falsche Warnung an Google melden",
		"scanNextSteps": "Wir prüfen alle 30 Minuten erneut. Ändere das Ziel oder warte, bis Google die Seite freigibt.",
```

```json
		"actorSafeBrowsing": "Google Safe Browsing",
```

```json
		"actionLinkFlagged": "Link durch Safe Browsing gesperrt",
		"actionLinkUnflagged": "Link durch Safe Browsing freigegeben",
```

`links.scanAdvisory` must match whatever Task 7 settled for the block page's attribution after checking Google's Appropriate Usage page.

In `apps/web/src/i18n/catalogues.test.ts`, add to `identicalByDesign` after `'links.inTag',` (line 110):

```ts
			// "Google Safe Browsing" is the name of Google's service, a proper
			// noun in both languages, the same reasoning as `brand`. It is the
			// audit log's author for the two entries only the scanner writes.
			'audit.actorSafeBrowsing',
```

Run: `cd apps/web && npx vitest run --project unit src/i18n/catalogues.test.ts`

Expected: PASS (key sets identical; the one identical value is listed).

- [ ] **Step 2: Write the failing library and component tests**

Create `apps/web/src/lib/safe-browsing.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
	headingCategory,
	SAFE_BROWSING_URL,
	threatCategories,
	threatDefinitions,
} from './safe-browsing.ts';

describe(threatCategories, () => {
	it('groups the threat types Google reports into phishing and harmful software', () => {
		expect(threatCategories(['MALWARE', 'SOCIAL_ENGINEERING', 'UNWANTED_SOFTWARE'])).toStrictEqual([
			'phishing',
			'harmful',
		]);
	});

	it('reads a threat type it does not know as a generic threat', () => {
		// Google adds types over time; dropping one would leave a blocked link
		// with no reason on the page.
		expect(threatCategories(['THREAT_TYPE_FROM_THE_FUTURE'])).toStrictEqual(['unknown']);
	});

	it('falls back to the generic threat when Google named none', () => {
		expect(threatCategories([])).toStrictEqual(['unknown']);
	});
});

describe(headingCategory, () => {
	it('gives one category its own heading and a mix the generic one', () => {
		expect(headingCategory(['phishing'])).toBe('phishing');
		expect(headingCategory(['phishing', 'harmful'])).toBe('unknown');
	});
});

describe(threatDefinitions, () => {
	it("links each reported type to Google's own definition", () => {
		expect(
			threatDefinitions('harmful', ['UNWANTED_SOFTWARE', 'MALWARE']).map((d) => d.url),
		).toStrictEqual([
			'https://developers.google.com/search/docs/monitor-debug/security/malware',
			'https://www.google.com/about/unwanted-software-policy.html',
		]);
	});

	it('points a generic threat at Safe Browsing itself', () => {
		expect(threatDefinitions('unknown', ['THREAT_TYPE_FROM_THE_FUTURE'])).toStrictEqual([
			{ labelKey: 'links.scanDefinitionUnknown', url: SAFE_BROWSING_URL },
		]);
	});
});
```

Create `apps/web/src/components/link-scan-notice.test.tsx`:

```tsx
import type { LinkScan } from '@kurze-url/api-client';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { ADVISORY_URL, REPORT_ERROR_URL } from '../lib/safe-browsing';
import { LinkScanNotice } from './link-scan-notice';

/**
 * @param threatTypes - What Google reported.
 * @returns A flagged verdict naming them.
 */
function flaggedScan(threatTypes: readonly string[]): LinkScan {
	return {
		checked_at: '2026-10-03T08:00:00Z',
		since: '2026-10-03T08:00:00Z',
		threat_types: [...threatTypes],
		verdict: 'flagged',
	};
}

/**
 * Same pattern as `audit-entry-table.test.tsx`: a component calling
 * `useTranslation` needs an `I18nextProvider` or `t(...)` throws.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

describe(LinkScanNotice, () => {
	it('renders nothing for a link that is not blocked', () => {
		const { container } = renderWithI18n(<LinkScanNotice state="active" />);
		expect(container).toBeEmptyDOMElement();
	});

	it("names a suspected phishing site and links Google's definition of it", () => {
		renderWithI18n(<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'])} state="flagged" />);

		// A labelled region, not an alert: it is on the page on every visit
		// while the block lasts, and is not news on any of them.
		expect(screen.getByRole('region', { name: 'Suspected phishing site' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by phishing' })).toHaveAttribute(
			'href',
			'https://developers.google.com/search/docs/monitor-debug/security/social-engineering',
		);
	});

	it('credits Google, admits it can be wrong, and says what to do next', () => {
		renderWithI18n(<LinkScanNotice scan={flaggedScan(['MALWARE'])} state="flagged" />);

		expect(screen.getByRole('link', { name: 'Advisory provided by Google' })).toHaveAttribute(
			'href',
			ADVISORY_URL,
		);
		expect(screen.getByText(/cannot guarantee/u)).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'Report an incorrect warning to Google' }),
		).toHaveAttribute('href', REPORT_ERROR_URL);
		expect(screen.getByText(/re-check every 30 minutes/u)).toBeInTheDocument();
	});

	it('gives a mix of threats the generic heading and every category its sentence', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING', 'MALWARE'])} state="flagged" />,
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by phishing' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by malware' })).toBeInTheDocument();
	});

	it('still explains a threat type Google added later', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['THREAT_TYPE_FROM_THE_FUTURE'])} state="flagged" />,
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'About Google Safe Browsing' })).toBeInTheDocument();
	});

	it('is German', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'])} state="flagged" />,
			'de',
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Mutmaßliche Phishing-Seite' }),
		).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'Hinweis bereitgestellt von Google' }),
		).toBeInTheDocument();
	});
});
```

Replace `apps/web/src/lib/audit-actor.test.ts` with:

```ts
import { describe, expect, it } from 'vitest';

import { resolveActor } from './audit-actor.ts';

const MEMBERS = new Map([['user-a', 'anna@example.org']]);

describe(resolveActor, () => {
	it('names a current member by their address', () => {
		expect(resolveActor('user-a', MEMBERS, 'link.updated')).toStrictEqual({
			email: 'anna@example.org',
			kind: 'member',
		});
	});

	it('reports an id that is not in the team as a former member', () => {
		// Removing someone from a team deletes the membership and keeps every
		// entry they wrote, so this is the ordinary case rather than an error.
		expect(resolveActor('user-gone', MEMBERS, 'link.updated')).toStrictEqual({
			kind: 'formerMember',
		});
	});

	it('reports a missing id as a deleted account', () => {
		// `audit_log.actor_user_id` is `on delete set null`, so null means the
		// person's account is gone — a different fact from having left this
		// team, and conflating them would tell a board someone left when they
		// did not.
		expect(resolveActor(undefined, MEMBERS, 'link.updated')).toStrictEqual({
			kind: 'deletedAccount',
		});
	});

	it('names Google Safe Browsing for the two actions only the scanner writes', () => {
		// Also a null id, for a different reason: nobody's account wrote these.
		// The API refuses a null actor on every other action, which is what
		// makes the action enough to tell the two apart.
		expect(resolveActor(undefined, MEMBERS, 'link.flagged')).toStrictEqual({
			kind: 'safeBrowsing',
		});
		expect(resolveActor(undefined, MEMBERS, 'link.unflagged')).toStrictEqual({
			kind: 'safeBrowsing',
		});
	});
});
```

In `apps/web/src/components/audit-entry-table.test.tsx`, add inside `describe(AuditEntryTable, …)`, after the last `it`:

```tsx
it('names Google Safe Browsing as the author of a block and of its lifting', () => {
	renderWithI18n(
		<AuditEntryTable
			entries={[
				{
					...ENTRY,
					action: 'link.flagged',
					actor_user_id: undefined,
					id: 8,
					metadata: { destination_url: 'https://example.org/x', threat_types: ['MALWARE'] },
				},
				{
					...ENTRY,
					action: 'link.unflagged',
					actor_user_id: undefined,
					id: 9,
					metadata: { destination_url: 'https://example.org/x', threat_types: [] },
				},
			]}
			language="en"
			membersById={MEMBERS}
		/>,
	);

	expect(screen.getAllByRole('cell', { name: 'Google Safe Browsing' })).toHaveLength(2);
	expect(screen.getByRole('cell', { name: 'Link blocked by Safe Browsing' })).toBeInTheDocument();
	expect(screen.getByRole('cell', { name: 'Link unblocked by Safe Browsing' })).toBeInTheDocument();
});

it('still reads a missing actor on any other action as a deleted account', () => {
	renderWithI18n(
		<AuditEntryTable
			entries={[{ ...ENTRY, actor_user_id: undefined }]}
			language="en"
			membersById={MEMBERS}
		/>,
	);

	expect(screen.getByRole('cell', { name: 'A deleted account' })).toBeInTheDocument();
});
```

In `apps/web/src/components/link-list.test.tsx`, after the `it('marks a password-protected link', …)` block add:

```tsx
it('marks a link Safe Browsing blocked', async () => {
	renderList({ data: pageOf([linkWith({ state: 'flagged' })]) });

	// Text, not only the destructive colour, for the same WCAG 1.4.1
	// reason as the password badge.
	await expect(screen.findByText('Blocked')).resolves.toBeInTheDocument();
});
```

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.role.test.tsx`, change `renderPage` to take the link (JSDoc gains `@param link - The link the loader returns.`):

```tsx
function renderPage(role: string, link: Link = LINK): ReturnType<typeof render> {
```

and in its body replace `loader: () => LINK,` with `loader: () => link,`. Then add, after the existing `describe('for a viewer', …)` block, inside `describe('the link page', …)`:

```tsx
describe('for a link Safe Browsing blocked', () => {
	it('says so right below the heading', async () => {
		renderPage('viewer', {
			...LINK,
			scan: {
				checked_at: '2026-10-03T08:00:00.000Z',
				since: '2026-10-03T08:00:00.000Z',
				threat_types: ['SOCIAL_ENGINEERING'],
				verdict: 'flagged',
			},
			state: 'flagged',
		});

		await expect(
			screen.findByRole('region', { name: 'Suspected phishing site' }),
		).resolves.toBeInTheDocument();
	});

	it('shows no notice for an active link', async () => {
		renderPage('viewer');

		await expect(
			screen.findByRole('heading', { level: 1, name: 'Details' }),
		).resolves.toBeInTheDocument();
		expect(screen.queryByRole('region', { name: /suspected/iu })).not.toBeInTheDocument();
	});
});
```

- [ ] **Step 3: Run them to verify they fail**

Run:

```bash
cd apps/web && npx vitest run --project unit src/lib/safe-browsing.test.ts src/components/link-scan-notice.test.tsx src/lib/audit-actor.test.ts src/components/audit-entry-table.test.tsx src/components/link-list.test.tsx 'src/routes/_authed/teams.$teamSlug.links.$linkId.role.test.tsx'
```

Expected: FAIL — `safe-browsing.ts` and `link-scan-notice.tsx` do not exist, `resolveActor` returns `deletedAccount` for `link.flagged`, no "Blocked" badge, no region on the link page.

- [ ] **Step 4: Write the library**

Create `apps/web/src/lib/safe-browsing.ts`:

```ts
/**
 * Google's Safe Browsing terms attach three links to every warning: Google's
 * definition of the threat, its advisory, and — for a Verein that thinks a
 * warning is wrong — its form to report that. `apps/api/internal/pages` holds
 * the same addresses for the redirect surface's block page. The lists are
 * short and change only when Google moves a page, so they are kept in step by
 * hand rather than sent through the API; change one, change both.
 */
export const ADVISORY_URL = 'https://developers.google.com/safe-browsing/v4/advisory';

/** Google's form for reporting an incorrect phishing warning. */
export const REPORT_ERROR_URL = 'https://safebrowsing.google.com/safebrowsing/report_error/';

/** Safe Browsing's own overview, the definition for a threat type this app does not know. */
export const SAFE_BROWSING_URL = 'https://safebrowsing.google.com/';

/** What the notice says about a threat: phishing, harmful software, or something Google added later. */
export type ThreatCategory = 'harmful' | 'phishing' | 'unknown';

/** One definition link: its catalogue key and Google's page. */
export interface ThreatDefinition {
	readonly labelKey: string;
	readonly url: string;
}

interface KnownThreat {
	readonly category: Exclude<ThreatCategory, 'unknown'>;
	readonly definition: ThreatDefinition;
}

/** The threat types Safe Browsing reports today, keyed exactly as the API sends them. */
const KNOWN_THREATS: Readonly<Record<string, KnownThreat>> = {
	MALWARE: {
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionMalware',
			url: 'https://developers.google.com/search/docs/monitor-debug/security/malware',
		},
	},
	POTENTIALLY_HARMFUL_APPLICATION: {
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionHarmfulApplication',
			url: 'https://developers.google.com/android/play-protect/potentially-harmful-applications',
		},
	},
	SOCIAL_ENGINEERING: {
		category: 'phishing',
		definition: {
			labelKey: 'links.scanDefinitionSocialEngineering',
			url: 'https://developers.google.com/search/docs/monitor-debug/security/social-engineering',
		},
	},
	UNWANTED_SOFTWARE: {
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionUnwantedSoftware',
			url: 'https://www.google.com/about/unwanted-software-policy.html',
		},
	},
};

const CATEGORY_ORDER: readonly ThreatCategory[] = ['phishing', 'harmful', 'unknown'];

/**
 * @param threatTypes - The threat types Google reported, as the API sends them.
 * @returns Every category they fall into, phishing first; `unknown` for a type Google added after this list was written, and when Google named none.
 */
export function threatCategories(threatTypes: readonly string[]): readonly ThreatCategory[] {
	const present = new Set<ThreatCategory>(
		threatTypes.map((threatType) => KNOWN_THREATS[threatType]?.category ?? 'unknown'),
	);
	if (present.size === 0) present.add('unknown');
	return CATEGORY_ORDER.filter((category) => present.has(category));
}

/**
 * One category gets its own heading; a mix gets the generic one — the same
 * rule the redirect surface's block page follows.
 *
 * @param categories - What `threatCategories` returned.
 * @returns The category whose heading the notice shows.
 */
export function headingCategory(categories: readonly ThreatCategory[]): ThreatCategory {
	const [only] = categories;
	return categories.length === 1 && only !== undefined ? only : 'unknown';
}

/**
 * @param category - One category the link was reported for.
 * @param threatTypes - The threat types Google reported.
 * @returns Google's definition of each reported type in that category, in a fixed order; for `unknown`, Safe Browsing's own overview.
 */
export function threatDefinitions(
	category: ThreatCategory,
	threatTypes: readonly string[],
): readonly ThreatDefinition[] {
	if (category === 'unknown') {
		return [{ labelKey: 'links.scanDefinitionUnknown', url: SAFE_BROWSING_URL }];
	}
	return Object.entries(KNOWN_THREATS)
		.filter(
			([threatType, known]: readonly [string, KnownThreat]) =>
				known.category === category && threatTypes.includes(threatType),
		)
		.map(([, known]: readonly [string, KnownThreat]) => known.definition);
}
```

- [ ] **Step 5: Write the notice**

Create `apps/web/src/components/link-scan-notice.tsx`:

```tsx
/* oxlint-disable typescript/prefer-readonly-parameter-types -- `LinkScan` is generated `@kurze-url/api-client` output whose `threat_types` array is not marked readonly; that is codegen output, never edited by hand. */

import type { Link, LinkScan } from '@kurze-url/api-client';
import { ShieldAlertIcon } from 'lucide-react';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import {
	ADVISORY_URL,
	headingCategory,
	REPORT_ERROR_URL,
	threatCategories,
	threatDefinitions,
	type ThreatCategory,
} from '../lib/safe-browsing';

/** Each category's heading, qualified the way Google's terms require: "suspected", "possibly", never a verdict. */
const headingKeys: Record<ThreatCategory, string> = {
	harmful: 'links.scanHeadingHarmful',
	phishing: 'links.scanHeadingPhishing',
	unknown: 'links.scanHeadingUnknown',
};

/** Each category's sentence, qualified the same way. */
const bodyKeys: Record<ThreatCategory, string> = {
	harmful: 'links.scanHarmful',
	phishing: 'links.scanPhishing',
	unknown: 'links.scanUnknown',
};

export interface LinkScanNoticeProps {
	/** The nested verdict from `GET /v1/links/{link_id}`; absent until the current destination has been checked. */
	readonly scan?: LinkScan;
	/** The link's state. The notice shows only while it is `flagged`. */
	readonly state: Link['state'];
}

/**
 * The link page's notice for a link Google Safe Browsing reports. A status,
 * not an alarm: a labelled region rather than `role="alert"`, because it is
 * on the page on every visit while the block lasts and is news on none of
 * them. It carries what Google's terms attach to every warning — a qualified
 * claim, Google's definition of each threat, "Advisory provided by Google",
 * and the admission that Google can be wrong — and the two things a Verein
 * can do about it: report a wrong warning to Google, or change the
 * destination. There is deliberately no state control. Only Google lifts a
 * block, so the API refuses a `state` on a flagged link with a 409 and the
 * page offers nothing that would send one.
 *
 * @param props - The component's props.
 * @param props.scan - The nested verdict, when the destination has been checked.
 * @param props.state - The link's state; nothing renders unless it is `flagged`.
 * @returns The notice, or nothing for a link that is not blocked.
 */
export function LinkScanNotice({ scan, state }: LinkScanNoticeProps): React.JSX.Element | null {
	const { t } = useTranslation();
	const headingId = useId();
	if (state !== 'flagged') return null;

	const threatTypes = scan?.threat_types ?? [];
	const categories = threatCategories(threatTypes);

	return (
		<section
			aria-labelledby={headingId}
			className="flex flex-col gap-2 border border-destructive/50 bg-destructive/10 p-3 text-sm text-foreground"
		>
			<h2 className="flex items-center gap-2 font-semibold" id={headingId}>
				<ShieldAlertIcon aria-hidden />
				{t(headingKeys[headingCategory(categories)])}
			</h2>
			<p>{t('links.scanBlocked')}</p>
			{categories.map((category) => (
				<div key={category}>
					<p>{t(bodyKeys[category])}</p>
					<ul>
						{threatDefinitions(category, threatTypes).map((definition) => (
							<li key={definition.url}>
								{/* rel="noreferrer": the dashboard's own address is not Google's business. */}
								<a className="underline underline-offset-4" href={definition.url} rel="noreferrer">
									{t(definition.labelKey)}
								</a>
							</li>
						))}
					</ul>
				</div>
			))}
			<p>
				<a className="underline underline-offset-4" href={ADVISORY_URL} rel="noreferrer">
					{t('links.scanAdvisory')}
				</a>
			</p>
			<p>{t('links.scanDisclaimer')}</p>
			<p>
				<a className="underline underline-offset-4" href={REPORT_ERROR_URL} rel="noreferrer">
					{t('links.scanReport')}
				</a>
			</p>
			<p>{t('links.scanNextSteps')}</p>
		</section>
	);
}
```

Create `apps/web/src/components/link-scan-notice.stories.tsx`:

```tsx
import type { LinkScan } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { LinkScanNotice } from './link-scan-notice';

const meta = {
	component: LinkScanNotice,
	title: 'Links/LinkScanNotice',
} satisfies Meta<typeof LinkScanNotice>;

export default meta;

/**
 * @param threatTypes - What Google reported.
 * @returns A flagged verdict naming them.
 */
function flaggedScan(threatTypes: readonly string[]): LinkScan {
	return {
		checked_at: '2026-10-03T08:00:00Z',
		since: '2026-10-03T08:00:00Z',
		threat_types: [...threatTypes],
		verdict: 'flagged',
	};
}

export const Phishing: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['SOCIAL_ENGINEERING']), state: 'flagged' },
};

export const HarmfulSoftware: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['MALWARE', 'UNWANTED_SOFTWARE']), state: 'flagged' },
};

/** Two categories: the generic heading over each category's sentence. */
export const Mixed: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['SOCIAL_ENGINEERING', 'MALWARE']), state: 'flagged' },
};

/** A threat type Google added after this app was written still gets a qualified text and a link. */
export const UnknownThreat: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['THREAT_TYPE_FROM_THE_FUTURE']), state: 'flagged' },
};

export const German: StoryObj<typeof meta> = {
	args: { ...Phishing.args },
	globals: { language: 'de' },
};

// The destructive tokens this notice draws on (`border-destructive/50`,
// `bg-destructive/10`) are checked in dark mode only by a story that sets it,
// the same reasoning as `short-url-notice.stories.tsx`'s `Dark`.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Phishing.args },
	globals: { theme: 'dark' },
};
```

- [ ] **Step 6: Show the badge, the notice and the actor**

In `apps/web/src/components/link-list.tsx`, change line 8 to `import { LockIcon, ShieldAlertIcon } from 'lucide-react';` and, after the password badge's `) : null}` (line 299), add:

```tsx
{
	link.state === 'flagged' ? (
		// Text, not only the destructive colour, for the password
		// badge's WCAG 1.4.1 reason; the icon is aria-hidden.
		<Badge variant="destructive">
			<ShieldAlertIcon aria-hidden />
			{t('links.flaggedBadge')}
		</Badge>
	) : null;
}
```

In `apps/web/src/components/link-list.stories.tsx`, after `Populated` add:

```tsx
/** A link Google Safe Browsing reports, beside an ordinary one. */
export const WithBlockedLink: StoryObj<typeof meta> = {
	args: {
		data: pageOf({
			items: [
				link({ state: 'flagged' }),
				link({
					destination_url: 'https://example.org/other',
					id: 'link-2',
					short_url: 'https://kurze.url/def456',
					slug: 'def456',
				}),
			],
			total_count: 2,
		}),
		page: 1,
		teamSlug: 'verein-a',
	},
};
```

Replace `apps/web/src/lib/audit-actor.ts` with:

```ts
/** What the page can say about who wrote an entry. */
export type ActorDisplay =
	| { readonly email: string; readonly kind: 'member' }
	| { readonly kind: 'deletedAccount' }
	| { readonly kind: 'formerMember' }
	| { readonly kind: 'safeBrowsing' };

/**
 * The two actions the Safe Browsing scanner writes, always without an actor
 * (`apps/api/internal/audit/audit.go`). The API refuses a missing actor on
 * every other action, which is what makes the action enough to tell the two
 * kinds of null apart.
 */
const SYSTEM_ACTIONS: ReadonlySet<string> = new Set(['link.flagged', 'link.unflagged']);

/**
 * Decides which of the four things the page can truthfully say about an
 * entry's actor.
 *
 * The distinction between "a former member" and "a deleted account" is the
 * point. "A former member" is a fact about this team — the membership was
 * deleted and the entries were deliberately not. "A deleted account" is a fact
 * about the person, and reaches us as a null id because
 * `audit_log.actor_user_id` is `on delete set null`. Showing one for the other
 * would tell a board that somebody left when they did not. A null id means a
 * third thing on the two Safe Browsing actions: no account wrote those, Google
 * Safe Browsing did.
 *
 * @param actorUserId - The entry's `actor_user_id`, absent when the account is gone or the system wrote the entry.
 * @param membersById - The team's current members, keyed by user id.
 * @param action - The entry's `action`, which tells the scanner's missing actor from a deleted account's.
 * @returns What to render for this actor.
 */
export function resolveActor(
	actorUserId: string | undefined,
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly.
	membersById: ReadonlyMap<string, string>,
	action: string,
): ActorDisplay {
	if (actorUserId === undefined) {
		return SYSTEM_ACTIONS.has(action) ? { kind: 'safeBrowsing' } : { kind: 'deletedAccount' };
	}

	const email = membersById.get(actorUserId);
	return email === undefined ? { kind: 'formerMember' } : { email, kind: 'member' };
}
```

In `apps/web/src/components/audit-entry-table.tsx`:

- change the import to `import { resolveActor, type ActorDisplay } from '../lib/audit-actor';`
- add to `actionLabelKeys`, keeping its alphabetical order, `'link.flagged': 'audit.actionLinkFlagged',` after `'link.deleted'` and `'link.unflagged': 'audit.actionLinkUnflagged',` after `'link.password_set'`
- after `entityLabelKeys` add:

```tsx
/**
 * Every actor that is not a current member, mapped to its catalogue key — a
 * `Record` over `ActorDisplay`'s own kinds, so a fifth kind added to
 * `audit-actor.ts` without a label here fails `pnpm typecheck`.
 */
const actorLabelKeys: Record<Exclude<ActorDisplay['kind'], 'member'>, string> = {
	deletedAccount: 'audit.actorDeletedAccount',
	formerMember: 'audit.actorFormerMember',
	safeBrowsing: 'audit.actorSafeBrowsing',
};
```

- replace lines 198-202 with:

```tsx
const actor = resolveActor(entry.actor_user_id, membersById, entry.action);
const actorLabel = actor.kind === 'member' ? actor.email : t(actorLabelKeys[actor.kind]);
```

In `apps/web/src/components/audit-entry-table.stories.tsx`, after `DetailsOpen` add:

```tsx
/**
 * The two entries only the Safe Browsing scanner writes, beside an entry whose
 * author's account was deleted: all three carry no actor id, and only the
 * action tells them apart.
 */
export const SafeBrowsingEntries: StoryObj<typeof meta> = {
	args: {
		entries: [
			{
				action: 'link.unflagged',
				created_at: '2026-10-03T10:30:00.000Z',
				entity_id: 'link-3',
				entity_type: 'link',
				id: 7,
				metadata: { destination_url: 'https://verein.example/anmeldung', threat_types: [] },
			},
			{
				action: 'link.flagged',
				created_at: '2026-10-03T08:00:00.000Z',
				entity_id: 'link-3',
				entity_type: 'link',
				id: 6,
				metadata: {
					destination_url: 'https://verein.example/anmeldung',
					threat_types: ['SOCIAL_ENGINEERING'],
				},
			},
			{
				action: 'link.updated',
				created_at: '2026-10-01T09:00:00.000Z',
				entity_id: 'link-3',
				entity_type: 'link',
				id: 5,
				metadata: {},
			},
		],
	},
};
```

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, add `import { LinkScanNotice } from '../../components/link-scan-notice';` after the `LinkQRCard` import, and directly after `<h1>{t(mayEdit ? 'links.edit' : 'links.details')}</h1>` (line 718) add:

```tsx
{
	/* First thing below the heading: a blocked link's page has to say so
			    before anything else. The element is always present — it renders
			    null for an unblocked link — so it never shifts the keyed cards
			    below (see the note on keys further down). */
}
<LinkScanNotice key={`scan-${linkId}`} scan={link.scan} state={link.state} />;
```

- [ ] **Step 7: Run the tests to verify they pass**

Run:

```bash
cd apps/web && npx vitest run --project unit src/lib/safe-browsing.test.ts src/components/link-scan-notice.test.tsx src/lib/audit-actor.test.ts src/components/audit-entry-table.test.tsx src/components/link-list.test.tsx 'src/routes/_authed/teams.$teamSlug.links.$linkId.role.test.tsx' src/i18n/catalogues.test.ts
```

Expected: PASS, every file.

- [ ] **Step 8: Run the whole unit suite, the stories and the static checks**

Run:

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)"
pnpm --filter @kurze-url/web test
pnpm --filter @kurze-url/web run test:storybook
pnpm lint && pnpm typecheck
```

Expected: the unit suite passes; every story passes, including the a11y checks of `Links/LinkScanNotice` (all six), `Links/LinkList/WithBlockedLink` and `Audit/AuditEntryTable/SafeBrowsingEntries`; `pnpm lint` reports zero findings (if it reports only a Tailwind class order, `pnpm lint:fix` applies it); `pnpm typecheck` is clean. No e2e spec renders a flagged link or a system-actor entry, so `e2e/i18n.spec.ts`'s identical-word list needs no "Google Safe Browsing"; verify with `cd apps/web && npx playwright test --list` that the specs still load.

- [ ] **Step 9: Format and commit**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url && eval "$(fnm env)" && pnpm format && pnpm format:check
but diff
```

Expected: `but diff` lists exactly the files under **Files**, and `apps/web/src/routeTree.gen.ts` is not among them. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "feat(web): show safe browsing blocks" <ids>
```

---

### Task 10: Documentation

**Files:**

- Modify: `CLAUDE.md` (golden rule 2; "Repo layout"; the audit taxonomy entry; the environment-variable entry; a new "Non-obvious constraints" entry)
- Modify: `docs/planning/01-architecture.md:66`
- Modify: `docs/planning/02-external-services-and-hosting.md:61-69` and `:129`
- Modify: `docs/planning/05-database-schema.md:72-112`
- Modify: `docs/planning/06-api-design.md:132-136`
- Modify: `docs/planning/07-repo-structure-and-tooling.md:89-98`
- Modify: `docs/planning/08-legal-and-compliance.md:32`

**Interfaces:** none; this task records what Tasks 1-9 built.

- [ ] **Step 1: `CLAUDE.md`**

Append to golden rule 2, after "Every design choice gets checked against this one code path.":

```markdown
One bounded exception: a `flagged` link with no Safe Browsing confirmation younger than thirty minutes waits up to 2 seconds for Google, because Google's terms forbid blocking on stale data and forwarding it unchecked would be worse. An `active` link never waits on anything new (see the Safe Browsing entry under "Non-obvious constraints").
```

In "Repo layout", replace `internal/{analytics,api,audit,auth,authz,cache,config,db,destination,domainverify,link,observability,pages,slug,supabase}` with `internal/{analytics,api,audit,auth,authz,cache,config,db,destination,domainverify,link,observability,pages,qr,scanning,slug,supabase}`, and replace the sentence "`scanning` and `qr` arrive with the features that need them and do not exist yet." with "`scanning` is the only package that talks to Google Safe Browsing."

In the "**The `audit_log.action` taxonomy is closed, and `internal/audit` owns it.**" entry, replace "Twenty-one values, all of them emitted" with "Twenty-three values, all of them emitted", and append to the entry:

```markdown
`link.flagged` and `link.unflagged` are the only actions written without an actor — the Safe Browsing scanner is nobody's account — and `Log` refuses an actor that does not fit its action in either direction (`ErrActorMismatch`), because the web reads a null actor as "Google Safe Browsing" on exactly those two and as "A deleted account" on every other.
```

Append to the "**`HEALTH_CHECK_TOKEN` and `SENTRY_DSN` are the two new API environment variables…**" entry:

```markdown
`SAFE_BROWSING_API_KEY` and `SCAN_TOKEN` joined them on 2026-10-03, documented the same way and empty by default: an unset key turns scanning off with a startup warning (and makes every flagged link answer the neutral 503), an unset token turns `POST /internal/scan` into a 404. `SCAN_HEARTBEAT_URL` is a GitHub secret the API never reads, listed in `.env.example` as a comment so the three stay together.
```

Add a new entry to "Non-obvious constraints (things that will bite you)", after the "**A link's password lives on its own route…**" entry:

```markdown
- **Safe Browsing blocks a link only on a confirmation younger than thirty minutes, and only Google lifts it.** Google's terms forbid treating a URL as unsafe — a warning or a block — unless updated information arrived from Google within the past thirty minutes, so `link.state = 'flagged'` alone never shows the block page. The redirect and verify paths look for `sb:confirmed:<link_id>` (through `Client.Key`, so it carries the environment prefix), whose TTL is `min(cacheDuration, 30 min) − 1 min` and which is never written when that is not positive. Without it a flagged link asks Google again about that one destination — at most 2 seconds, shared per instance through `singleflight` — which is the one wait golden rule 2 allows; an `active` link calls nothing new. A clean answer forwards and lifts the flag in the background; an error, a timeout or no `SAFE_BROWSING_API_KEY` answers the neutral `KindUnavailable` 503 with `Retry-After: 300`, which says nothing about the destination. The API is v5 `hashes.search`: only 4-byte SHA-256 prefixes of canonicalized expressions leave the server, never a URL, and the key travels in `X-Goog-Api-Key` because Go's `*url.Error` prints the request URL into logs and Sentry. `internal/scanning` canonicalizes by hand (Google's test vectors are its table test), converts IDN hosts to Punycode, and reports a URL only when one of its _own_ full hashes comes back — a returned hash proves nothing about a neighbour sharing its prefix. Checks run right after a create or a destination change, in a best-effort goroutine Vercel may kill, and in `POST /internal/scan`, which `.github/workflows/scan.yml` calls at :07 and :37 with `SCAN_TOKEN` (unset: 404; no key: 503; Google failing as a whole: 502) and which pings `SCAN_HEARTBEAT_URL` only on success. A flagged link is due on every sweep, so a false positive Google corrects is lifted unvisited, and the sweep's `remaining` never reaches zero while any link is blocked. A PATCH carrying any `state` for a flagged link answers 409 with `ErrorDetail{Location: "body.state", Value: "flagged"}` — disabling is refused too, or disable-then-enable would lift the block — while a new destination un-flags it in the same update, because the flag belonged to the old URL. `updateLink` reads the row `FOR UPDATE` for that reason: unlocked, it wrote a flag the scanner set in between straight back to `active`. The block page's copy is qualified ("suspected", "possibly"), links Google's definition of each threat and its advisory ("Advisory provided by Google"), and says Google can be wrong; all four are the terms' conditions, not style, and the web notice carries the same. `link_scan_result` gets a row only when a verdict changes. Following a destination's own redirect chain is a known gap, and any revenue tied to the service moves this to Web Risk under Cloud terms.
```

- [ ] **Step 2: The planning documents**

`docs/planning/01-architecture.md`, line 66 — replace the bullet with:

```markdown
- Malware/phishing scanning of destination URLs: async, not blocking link creation — a link goes live immediately and gets flagged if scanning later returns positive. Provider: **Google Safe Browsing API**, licensing accepted 2026-09-01 (non-profit/community use, no separate formal legal review) — see `02-external-services-and-hosting.md` for the full reasoning and the Web Risk API fallback if this is ever challenged. **Mechanism, since 2026-10-03:** v5 `hashes.search`, so only 4-byte hash prefixes leave the server; a check right after every create and destination change, plus a sweep every 30 minutes from GitHub Actions that re-checks every link daily and every flagged link each time; and a flagged link's redirect blocks only on a Google confirmation younger than 30 minutes, re-asking Google (at most 2 seconds) when it has none. See `CLAUDE.md`'s Safe Browsing entry and `docs/superpowers/specs/2026-10-03-safe-browsing-design.md`.
```

`docs/planning/02-external-services-and-hosting.md` — after the licensing paragraph of "Malware/phishing scanning: Google Safe Browsing API" (line 65), add:

```markdown
**API and terms, settled 2026-10-03** (`docs/superpowers/specs/2026-10-03-safe-browsing-design.md`): the v5 `hashes.search` method. The v4 Lookup API is deprecated, with support ending 2027-03-31, and v5 `urls.search` would send the full destination URL, which Google's terms let it reuse and share; `hashes.search` sends only 4-byte SHA-256 prefixes, at the price of canonicalizing and hashing on our side. Four clauses of the terms shaped the design. A URL may be shown as unsafe or blocked only on information received from Google within the past thirty minutes, so a flagged link re-confirms with Google before its block page is shown. A warning must be qualified ("suspected", "possible"), link Google's definition of the threat and carry "Advisory provided by Google". The product must say Google cannot guarantee its information is complete and error-free. And use stays non-commercial. The quota is the default the Google Cloud project is given; a 429 is reported to Sentry once an hour.
```

and after the heartbeat paragraph at line 129, add:

```markdown
Added 2026-10-03: a **third heartbeat**, "Safe Browsing scan", for `.github/workflows/scan.yml`, which calls the API's token-guarded `POST /internal/scan` at :07 and :37 every hour and pings only after a successful sweep. Period 30 minutes, grace at least 30 minutes, because GitHub starts scheduled runs late. Same reason as the other two: a disabled workflow stops rather than fails.
```

`docs/planning/05-database-schema.md` — in the `link (` definition, after `analytics_enabled` (line 83), add:

```sql
  scan_checked_at       timestamptz,                       -- added 2026-10-03: when Safe Browsing last checked this link, whatever it found
  scan_destination      text,                              -- added 2026-10-03: the destination that check judged; differing from destination_url makes the link due
```

and replace the `link_scan_result (` definition (lines 103-110) with:

```sql
link_scan_result (
  id                uuid primary key default gen_random_uuid(),
  link_id           uuid not null references link(id) on delete cascade,
  provider          text not null default 'google_safe_browsing',
  verdict           text not null check (verdict in ('clean','flagged','error')),  -- 'error' allowed, never written: failures are logged
  scanned_at        timestamptz not null default now(),
  raw_response      jsonb,
  destination_url   text not null,                        -- added 2026-10-03: the URL this verdict judged
  threat_types      text[] not null default '{}'          -- added 2026-10-03
)
-- A row is written only when a link's verdict changes (active to flagged or
-- back), so the table stays small and holds the whole history. link.state
-- stays the only switch the redirect path reads.
```

`docs/planning/06-api-design.md` — replace the paragraph "As of 2026-09-03 this ships without the nested `link_scan_result` verdict…" (line 134) with:

```markdown
Since 2026-10-03 the response carries `scan`, omitted until the link's current destination has been checked: `verdict` (`clean` or `flagged`), `threat_types`, `since` (when the latest verdict change for this destination was recorded, absent if none) and `checked_at`. The list endpoint carries only `state`, whose schema is now the enum `active | disabled | expired | flagged`.
```

and append to the `PATCH /v1/links/{link_id}` bullet (line 136):

```markdown
A flagged link refuses any `state` with `409` and `ErrorDetail{Location: "body.state", Value: "flagged"}` — only Google lifts a flag — while a new `destination_url` un-flags it in the same update.
```

`docs/planning/07-repo-structure-and-tooling.md` — after the `secret-scan.yml` bullet (line 89), add:

```markdown
- **`scan.yml`** (added 2026-10-03) — at :07 and :37 every hour, a token-guarded `POST /internal/scan` against production, then a Better Stack heartbeat only on success. On pull requests it only proves the file parses. See `CLAUDE.md`'s Safe Browsing entry.
```

and in "Secrets management", replace "Google Safe Browsing API key" in the `apps/api` bullet with "`SAFE_BROWSING_API_KEY` (Production and Preview; restricted to the Safe Browsing API), `SCAN_TOKEN` (Production)", and append to the GitHub Actions secrets bullet: "`SCAN_TOKEN` and `SCAN_HEARTBEAT_URL` for `scan.yml`, beside `RETENTION_TOKEN` and `RETENTION_HEARTBEAT_URL` for `retention.yml`."

`docs/planning/08-legal-and-compliance.md`, line 32 — replace the bullet with:

```markdown
- A note on scanning: link destinations are checked against Google Safe Browsing through its v5 `hashes.search` method, which receives only 4-byte SHA-256 prefixes of the destination's canonicalized forms — never the URL itself, and never anything about visitors. Google still sees the server's IP address and the time of each check. The Datenschutzerklärung should name Google as the provider of the check and say what is, and is not, sent; whether a hash prefix of a URL is personal data at all is a question for the lawyer.
```

- [ ] **Step 3: Check the documents**

Run: `eval "$(fnm env)" && pnpm format && pnpm format:check && grep -n "Twenty-three\|scanning is the only package\|sb:confirmed" CLAUDE.md`

Expected: formatting clean; the grep finds all three.

- [ ] **Step 4: Commit**

```bash
but diff
```

Expected: `CLAUDE.md` and the six planning documents. Copy their IDs, then:

```bash
but commit -b feat/safe-browsing -m "docs: record safe browsing scanning" <ids>
```

---

## After the last task

For the controller, not an implementer step; none of it can be done from the repository:

1. Apply Task 1's migration to the Preview Supabase project by hand before the branch's e2e runs.
2. The maintainer creates a Google Cloud project with the Safe Browsing API enabled and an API key restricted to it, and sets `SAFE_BROWSING_API_KEY` (Production and Preview) and `SCAN_TOKEN` (Production) on the `kurze-url-api` Vercel project, plus the GitHub secrets `SCAN_TOKEN` and `SCAN_HEARTBEAT_URL`.
3. The maintainer creates the Better Stack heartbeat "Safe Browsing scan": period 30 minutes, grace at least 30 minutes.
4. Live smoke test after deploy: create a link to `https://testsafebrowsing.appspot.com/s/phishing.html`; within seconds it must be flagged, its short URL must show the block page, and the audit log must carry a `link.flagged` entry by "Google Safe Browsing". Then delete the link.
5. Watch the first scheduled sweeps' logs: `remaining` should fall by about 200 per run until the backlog is gone. A run of 414 or 400 answers from Google on large sweeps would mean even 250 prefixes make the request URL too long (Rulings on the spec, item 13): lower `maxPrefixesPerRequest` in `apps/api/internal/scanning/client.go`.
