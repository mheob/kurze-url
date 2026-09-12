# Analytics Retention — Design

**Status:** approved 2026-09-12 **Amends:** `CLAUDE.md` (the unimplemented-deletion open item closes and becomes a non-obvious constraint; a third API environment variable joins `HEALTH_CHECK_TOKEN` and `SENTRY_DSN`), `docs/planning/01-architecture.md` (line 73's "90-day automatic deletion, confirmed" gains the mechanism that confirms it), `docs/planning/02-external-services-and-hosting.md` (a second Better Stack heartbeat).

The thirteenth implementation spec, and the smallest. It closes a promise that has been open since the planning documents were written.

`docs/planning/01-architecture.md:73` says "Retention: 90-day automatic deletion, confirmed." Nothing has ever implemented it. There is no scheduled prune, no `delete from link_click_stats` anywhere in the repository, and the oldest rollup row is as old as the redirect path. `docs/planning/08-legal-and-compliance.md:29` lists the 90-day retention among the things the Datenschutzerklärung will document as _already_ the design — so this is a statement to Vereine and their visitors, not internal hygiene.

Yesterday's stats endpoint closed the half that could be closed from the read side: `GET /v1/links/{link_id}/stats` clamps what it will serve to today minus 89 days, measured against today. That stops the API from handing out rows the promise says should be gone. It does not make them gone.

## Goal

Rows in `link_click_stats` older than the retention window are deleted, every day, by a job whose absence is noticed.

## Scope

### In scope

- `POST /internal/retention`, token-guarded, outside Huma and outside the OpenAPI document.
- One `delete` statement, bounded by the same constant the read side clamps to.
- `RETENTION_TOKEN`, its own environment variable, documented in `apps/api/.env.example`.
- `.github/workflows/retention.yml`: a daily schedule, the call, and a Better Stack heartbeat ping on success.

### Out of scope, and why

- **`audit_log`.** No document promises it a retention period, and it is the record of who changed what — the thing one consults precisely when something went wrong months ago. Deleting it needs its own decision, not this job's coattails.
- **`link_scan_result`.** Same: no promise, and the latest verdict per link is what the system reads.
- **Backup retention.** The nightly encrypted dumps in `kurze-url-backups` keep fourteen daily and twelve weekly copies, so a deleted analytics row survives in a backup for up to three months. `docs/planning/08-legal-and-compliance.md:63` already records that the three-month backup window has to appear in the legal texts. That is a disclosure question, not a job to build here, and shortening the backup window to match would trade a real recovery capability for a cosmetic alignment.
- **Configurable retention.** 90 days is a statement in a privacy policy. A deployment that can quietly change it is a deployment that can quietly break the statement.
- **Batching, dry-run mode, and a manual "delete everything older than X" escape hatch.** See "What this deliberately does not build".

## Global constraints

Inherited and not re-litigated here:

- No RLS. This job runs with no caller identity at all — it is not a tenant-scoped operation, and it must never become one.
- Never store a full IP address. This spec only deletes.
- The redirect path is the hot path. This spec adds nothing to `GET /{slug}`.
- Conventional Commits, subject capped at 50 characters including type and scope; `pnpm format` before every commit; the Lefthook hooks are not bypassed.
- Never a `Co-Authored-By` line or a generator footer, in a commit or a PR body.

## The deletion

```sql
-- name: DeleteExpiredClickStats :execrows
delete from link_click_stats
where bucket_start < sqlc.arg(oldest_kept)::date;
```

`:execrows` rather than `:exec`, because the row count is the whole observability story — see "Zero is the right answer for eighty days".

**The boundary is the read side's boundary, passed in rather than written twice.** `api.RetentionDays = 90` already defines the window the stats endpoint serves: its floor is `today - (RetentionDays - 1)`, so it serves `bucket_start >= today-89`. This job deletes `bucket_start < today-89`. The two are complementary by construction — no row is both served and deleted, and no row is neither — but only as long as they are the same number. They will not be if one is a literal in SQL and the other a constant in Go.

So the handler computes `oldest_kept` from `api.RetentionDays` using the same `dayOf(d.now())` reduction `statsWindow` uses, and passes it as a parameter. A test asserts the two agree by computing both from the same clock rather than by comparing two literals.

Getting this wrong is not symmetric. Deleting too much destroys data the API would still show — irreversible, and visible to a Verein as statistics that vanished mid-window. Deleting too little leaves rows the promise says are gone — a compliance gap, and recoverable by fixing the boundary. Neither is acceptable, but the first is the one that cannot be undone, which is why the boundary is derived rather than restated.

## The endpoint

**`POST /internal/retention`**, registered on the root router beside `/health/deep`, outside Huma and absent from the OpenAPI document. `POST` because it mutates; `/internal/` because no Verein has business calling it.

```go
root.With(middleware.Recoverer).Post("/internal/retention", deps.HandleRetention)
```

Like `/health/deep`, it answers on every hostname. That is a consequence of sitting on the root router above the hostname split, and it is acceptable for the same reason: **the token is the security boundary, not the hostname.** A Verein's custom domain will expose the path and answer 404 there exactly as it does everywhere else without the token.

Authorization copies `HandleDeepHealth` line for line, because that shape was reasoned through once and does not need reinventing:

- `RETENTION_TOKEN` empty disables the endpoint outright — 404 for every caller. **Fail closed.** A forgotten variable must not leave a delete endpoint open.
- A wrong token gets the same 404, never a 401. A caller who may not use a route does not learn it exists — the convention `assertMembership` and every entity scope already follow.
- `subtle.ConstantTimeCompare`, not `!=`. Not because a timing attack across the network is practical, but because one line removes the question permanently.
- The token travels in `X-Retention-Token`, its own header. A header shared with the health check would invite a shared value.

**It is its own token, deliberately not `HEALTH_CHECK_TOKEN`.** That value sits in Better Stack's monitor configuration and in this repository's GitHub secrets, and everything it currently authorizes is a read: dependency status and a `select 1`. Letting the same string also delete analytics widens the blast radius of a leak from "someone learns Postgres is up" to "someone destroys every Verein's statistics". Two variables cost one line of configuration each.

The response is JSON, and carries the count:

```json
{ "deleted": 0, "oldest_kept": "2026-06-15" }
```

`oldest_kept` is in the body because it is the one value that proves the job and the API agree about where the window starts. A human debugging a disagreement months from now can read it off a single curl rather than reasoning about two constants in two languages.

A failed delete answers 500 and logs at error level, which is what reaches Sentry.

## Zero is the right answer for eighty days

The oldest rollup row in production was written on 2026-09-02. The window is 90 days. **This job will delete nothing until roughly 2026-12-01**, and until then a correct run and a completely broken run produce the same visible outcome: no rows disappear.

Three consequences shape the design.

**The row count is logged and returned, not discarded.** `0 deleted` in a workflow log is evidence the job ran. Nothing in the log is evidence it did not.

**Correctness rests entirely on tests until December.** There will be no production deletion to observe, so the boundary test that seeds rows on both sides and asserts exactly which vanish is not one test among several — it is the only thing standing behind the claim that this works at all. It is called out as such in the plan.

**Absence is what has to be alarmed, not failure.** A job that errors makes noise: the workflow fails and GitHub emails. A job that stops being scheduled makes none — GitHub disables scheduled workflows after 60 days of repository inactivity, and a disabled workflow does not fail, it simply never runs. That is precisely the hazard `docs/superpowers/specs/2026-09-07-backups-design.md` met with a Better Stack heartbeat watching for the absence of a backup, proven in a live drill on 2026-09-09. The same mechanism, for the same reason: on success the workflow pings a heartbeat URL; Better Stack alerts when the ping stops arriving.

The heartbeat ping is the workflow's **last** step and runs only on success, so a failed delete withholds it. That makes the heartbeat cover both hazards with one signal — the job erroring and the job vanishing — and it is why the ping does not live inside the endpoint.

## The workflow

`.github/workflows/retention.yml`, modelled on `keep-alive.yml`:

- `schedule: - cron: "43 3 * * *"`. Daily, and deliberately not on the hour: `keep-alive.yml` already records that GitHub queues every scheduled job at `:00` and runs the backlog late.
- `workflow_dispatch`, so the job can be run by hand — the only way to exercise it before December.
- A `pull_request` trigger filtered to the workflow's own path, matching `keep-alive.yml`, so a change to the file is checked before it merges. The delete step is skipped on pull requests: it would run against production from an unmerged branch.
- `concurrency: group: retention, cancel-in-progress: false`. Two concurrent deletes would be harmless — the statement is idempotent — but a cancelled one mid-statement is not a thing worth finding out about.
- Each secret is checked for emptiness before use and the step fails loudly if it is missing, the way `keep-alive.yml` checks both of its own. A silent skip is how a retention job stops running without anyone noticing.

**It is a separate workflow, not a step inside `keep-alive.yml`.** The two jobs share a cadence and nothing else: one exists to stop a database pausing, the other to keep a promise in a privacy policy. Sharing a job would mean sharing a status, and "the daily job failed" would no longer say which duty is currently unmet.

## What this deliberately does not build

**No batching.** Running daily, the job deletes one day of rows. The first run in December deletes one day, because every day before it also ran. Batching defends against a first run that spans months, which only happens if this ships late — and it is shipping with eighty days of margin. A `limit` clause here would be machinery for a scenario the schedule already prevents.

**No dry-run mode.** The first eighty days _are_ the dry run: the job runs daily against production and deletes nothing, while the tests carry the proof. Adding a flag would create a second code path to maintain and a way to believe the job is armed when it is not.

**No manual "delete everything older than X" parameter.** An endpoint that takes a cutoff is an endpoint that can be told to delete everything. The only cutoff this job accepts is the one it computes.

## Testing

- **The boundary**, in `apps/api/internal/db`: seed rows at `today-91`, `today-90`, `today-89` and `today-1`, run the delete, and assert the first two are gone and the last two remain. `today-89` is the case that matters — it is the oldest row the API still serves, and it must survive.
- **The two boundaries agree**, in `apps/api/internal/api`: compute the stats endpoint's floor and the job's `oldest_kept` from one pinned clock and assert they are the same date. This is the test that fails if someone later "simplifies" either side to a literal.
- **Row count**, in `apps/api/internal/db`: the delete reports the number of rows it removed, and reports zero without erroring when nothing matches.
- **The token guard**, in `apps/api/internal/api`, mirroring `health_test.go`: no header, wrong token, and an empty configured token each answer 404; the correct token answers 200 with a body carrying `deleted` and `oldest_kept`.
- **Only the one table**, in `apps/api/internal/db`: seed an ancient `audit_log` row and an ancient `link_click_stats` row, run the job, and assert the audit row survives. The scope decision is a promise to the maintainer as much as the deletion is a promise to visitors.
- **A tenancy-free operation stays tenancy-free**: the delete carries no `team_id`, which in this codebase is normally a data-leak defect. A test comment states why it is correct here — the job acts for the instance, not for a caller — so the next reader does not "fix" it.

## Documentation this changes

- `CLAUDE.md`: the "90-day analytics deletion is promised and unimplemented" open item is deleted and replaced by a non-obvious constraint describing the job, its token, its heartbeat, and the fact that the delete boundary and the stats endpoint's floor are one constant that must stay one constant. The environment-variable line naming `HEALTH_CHECK_TOKEN` and `SENTRY_DSN` gains `RETENTION_TOKEN`.
- `docs/planning/01-architecture.md`: line 73's bare "confirmed" gains the mechanism, so the promise and its implementation stop living in different documents.
- `docs/planning/02-external-services-and-hosting.md`: the Better Stack section gains the second heartbeat beside the backup one.
- `apps/api/.env.example`: `RETENTION_TOKEN`, documented in the file's established voice — what it guards, that empty disables the endpoint, and why it is not the health token.
