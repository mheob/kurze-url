# Observability: Keep-Alive, Uptime and Error Tracking — Design

**Status:** approved 2026-09-07 **Amends:** `CLAUDE.md` (the repo layout gains `internal/observability`; the runtime-log-retention constraint is corrected from Hobby to Pro; six new entries under "Non-obvious constraints"), `docs/planning/02-external-services-and-hosting.md` (Hobby figures corrected to Pro, Better Stack moves from decided to built, the alert channel is settled).

The ninth implementation spec. Seven plans have merged and the instance is live: a maintainer signs in, a Verein claims a custom domain, and links redirect from `go.kurze-url.app`. Nothing watches any of it.

Two gaps, both operational rather than functional, and both cheap to close together because they share one endpoint.

**The instance can switch itself off.** Supabase's free tier pauses a project after seven days without activity. The production project survives on the maintainer's own occasional use; the preview project sees traffic only while a pull request has an open preview deployment. A paused project is restored by hand and takes minutes, and the preview one would pause exactly between two bursts of work, so the failure lands on a pull request rather than in a quiet moment.

**No error leaves a trace.** Vercel Pro retains runtime logs for one day. Sentry was decided on 2026-09-01 and never built. Today a Redis failure on the redirect hot path, a click-recorder flush that never lands, or a render error in the authenticated app is visible only to whoever happens to look within twenty-four hours. Every real defect this project has had was found by running the thing — but only because someone was running it at the time.

## Goal

An error that happens is recorded durably. An outage is noticed by something other than the maintainer. Neither Supabase project pauses. No successful redirect waits a millisecond longer for any of it, and no visitor's IP address is stored anywhere.

## Scope

### In scope

- `GET /health/deep`, token-guarded, checking Postgres and Redis.
- Three Better Stack monitors, one of which keeps the production Supabase project awake as a side effect.
- One scheduled GitHub Actions workflow that keeps the preview Supabase project awake and provides a second, independent keep-alive for production.
- Sentry in `apps/api`: panics, and every `slog.LevelError` record, with a request-context allowlist.
- Sentry in `apps/web`: browser and server, with source maps uploaded at build time and deleted from the deployed output.
- `internal/observability`, a new package holding everything Sentry-shaped on the Go side.

### Out of scope, and where each lands

- **Backups.** Still the largest unmitigated risk in the project and still undesigned. Unrelated to watching for errors; it gets its own spec.
- **Log Drains and Observability Plus.** Doc 02 already answers this: not needed for MVP. Sentry closes the gap the retention window leaves, and a paid log tier closes a different one nobody has asked about.
- **Vercel resource-threshold alerts** (the "are we about to hit a ceiling" half of doc 02's monitoring section). Configuration in a dashboard, not code, and independent of this work.
- **A public status page.** Better Stack's free tier includes one. It is a decision about what to tell Vereine during an outage, which nobody has needed yet.
- **Sentry cron monitoring** for the keep-alive workflow. A failing scheduled workflow already emails the repository owner. Watching the watcher earns its keep once there is more than one job.
- **Performance tracing and session replay.** `tracesSampleRate` is 0 and replay is off — see "The event budget is the binding constraint" below.

## The two health endpoints are not interchangeable

`/health` stays exactly as it is: flat, unauthenticated, answering on every hostname, touching nothing. Two things depend on that. The platform's own checks call it, and — less obviously — `domainverify`'s reachability probe fetches `https://<claimed-hostname>/health` and requires `"status":"ok"` in the body. Making the existing endpoint touch the database would put a Postgres round trip inside domain verification and inside whatever Vercel does with it.

`GET /health/deep` is the new one, registered on the root router next to `/health` so it answers on every hostname for the same reason the flat one does: a monitor does not know which hostname it is hitting.

It runs two checks under one three-second context budget for the whole endpoint, not three seconds each:

- `select 1` through the pgx pool. Deliberately a real statement rather than `pgxpool.Ping`, which sends an empty statement — whether Supabase's inactivity accounting counts that is an interpretation, and this endpoint's second job is to be counted.
- `PING` through `Cache.Raw()`.

The response names each dependency:

```json
{"status":"ok","checks":{"postgres":"ok","redis":"failed"}}
```

**Postgres failing is 503. Redis failing is 200.** This is the load-bearing decision in this section, and it is about which channel carries which severity. With Postgres down the service cannot serve a cache miss or create a link — an outage, and Better Stack should say so. With Redis down every redirect still works, falling back to Postgres — degradation. Returning 503 there would page the maintainer while the service is serving traffic correctly, which is how an alert channel gets ignored. So Redis failure returns 200 with the field set to `failed` and raises a Sentry event instead: Better Stack carries outages, Sentry carries degradation.

### The token guard, and why a missing token disables the endpoint

`X-Health-Token` must equal `HEALTH_CHECK_TOKEN`. A missing or wrong token answers 404, matching what `assertMembership` and the entity scopes do everywhere else in this codebase — a caller who may not use a route does not learn that it exists.

When `HEALTH_CHECK_TOKEN` is unset the endpoint answers 404 unconditionally. Fail closed, so a forgotten variable cannot expose dependency status and a free database round trip to anyone who guesses the path. The cost is that a forgotten variable also breaks the keep-alive — but not silently: Better Stack alerts on the 404, which is the same signal it would give for an outage, and the GitHub workflow fails and emails.

The endpoint stays out of the OpenAPI spec, like the flat `/health` and the whole redirect surface.

## Keep-alive is a side effect of monitoring, not a job of its own

Three Better Stack monitors, at the free tier's three-minute interval:

| Target | What it proves | Header |
| --- | --- | --- |
| `https://api.kurze-url.app/health/deep` | Postgres and Redis reachable; keeps production Supabase awake | `X-Health-Token` |
| `https://go.kurze-url.app/health` | DNS, TLS and the process on the redirect surface | none |
| `https://kurze-url.app/` | the web project serves | none |

The second monitor is why the redirect path itself is not monitored. Checking a real short link end to end was considered and rejected twice over: a cache hit never reaches Postgres, so it would not keep Supabase awake, and 480 requests a day would add 480 clicks a day to a real link's statistics. `/health` answers on every hostname, so pointing a monitor at it on `go.kurze-url.app` proves resolution, certificate and process on the redirect hostname without touching a link.

**The cost, stated rather than buried:** 480 requests a day, each including a Redis `PING`. Golden rule 7 names roughly 16,700 Redis commands a day as the binding free-tier limit, so the monitor consumes about 3% of it. Acceptable, but it is real budget.

### The scheduled workflow, and the single point of failure it removes

One file, `.github/workflows/keep-alive.yml`, daily at a minute other than `:00` — GitHub queues every scheduled job at the top of the hour and runs them late — plus `workflow_dispatch` so a paused project can be poked without waiting for the schedule.

Two steps. First `psql "$E2E_DATABASE_URL" -c 'select 1'` against the preview project, reusing the secret the e2e workflow already holds — no new credential. Second a `curl` of `https://api.kurze-url.app/health/deep` carrying the token from a new `HEALTH_CHECK_TOKEN` repository secret.

The second step exists because production's keep-alive would otherwise depend entirely on Better Stack continuing to run: a paused monitor, a mistyped header or a change to their free tier and the project pauses seven days later with nothing having said so. A daily `curl` removes that dependency for the price of one secret that is not a database credential. This is not the rejected "Vercel cron as well" approach — there is no second scheduling surface, no cron configuration in `vercel.json`, and no production code involved.

GitHub disables scheduled workflows after sixty days of repository inactivity, with a warning email first. Recorded in `CLAUDE.md` rather than defended against.

## Sentry on the Go side

Everything Sentry-shaped lives in `internal/observability`, following the house rule that Redis lives behind `cache` rather than `internal/redis`. That is three things: the client options and initialisation, the `BeforeSend` scrubber, and the `slog.Handler`. No other package imports `sentry-go`, so the request-context policy has exactly one home.

Initialisation happens in `cmd/api/main.go`. An empty `SENTRY_DSN` disables the SDK entirely, so local development configures nothing. `Environment` comes from `VERCEL_ENV` and `Release` from `VERCEL_GIT_COMMIT_SHA`, which ties every event to a commit and — because `apps/web` uses the same value — makes a bad deployment correlatable across both projects. `SendDefaultPII` is false, `TracesSampleRate` is 0, tracing is off.

### Panics: the middleware order is not a matter of taste

`sentryhttp` sits **inside** `middleware.Recoverer`, with `Repanic: true`, on both surfaces. The two wrong orders both lose something concrete. Recoverer innermost swallows the panic and Sentry never sees it. Sentry outermost without repanic answers 500 itself, and Recoverer never logs the stack trace.

`WaitForDelivery: true` with a two-second timeout. This is the flush, and it is the whole reason the approved hot-path policy holds: the only requests that reach it are panicking ones, which are already 5xx. A successful redirect never enters this code and never waits.

### Errors without a panic, via slog

The failures worth knowing about are not panics — a Redis error on the redirect path (doc 02's own motivating example), a recorder flush that fails, a domain verification that errors. Rather than scattering `sentry.CaptureException` calls, a `slog.Handler` forwards records at `slog.LevelError` to Sentry. Every existing `Log.Error(...)` site becomes an event with no new call site and no second bookkeeping to drift out of step with the first.

`LevelError` only, never `Warn`. No flush, as decided: these events ride the transport queue and drain on a later request, which Fluid Compute's instance reuse makes likely but not certain. The events lost when an instance retires are a deliberate purchase — the alternative charges every successful redirect for the possibility of an error.

### The event budget is the binding constraint

Sentry's free Developer tier allows 5,000 events a month. Two things follow.

Tracing stays at 0 and replay stays off. This project's problem is error visibility, not latency; performance data would consume the same quota that errors need.

And if a recurring error class ever burns the budget, the answer is sampling inside `BeforeSend`, not raising the level threshold. Raising the threshold hides the class that is currently demanding attention, which inverts the purpose.

### The request-context allowlist, and the one line that is mandatory

`BeforeSend` reduces headers to `User-Agent`, clears cookies, empties `event.User.IPAddress`, strips the query string from the URL, and discards the request body. An allowlist rather than a denylist, so `X-Forwarded-For` and `X-Vercel-Forwarded-For` cannot arrive by construction rather than by being remembered.

Dropping the body is not caution, it is required. `POST /{slug}/verify` carries a link's password in cleartext. Without that line the first panic on the password interstitial ships a cleartext password to a third party — the same class of mistake `audit.go`'s `checkMetadata` already exists to prevent on the audit path.

Golden rule 5 says never store a full IP address, ever. Clearing `event.User.IPAddress` is the SDK half; see the web section for why a second, non-code switch is also needed.

## Sentry on the web side

**The package is not guessed.** Sentry has published TanStack Start support, but its current name and stability are verified against the current documentation in the plan's first task, before any code is written. The fallback, if there is no stable first-party SDK, is `@sentry/react` in the browser plus `@sentry/node` in the server entry, wired by hand.

One DSN serves both halves. A Sentry project has one DSN, and Vite inlines `VITE_SENTRY_DSN` into both the client and the server bundle, so there is one variable rather than two. A browser DSN is public by design; that is not a leak.

`Release` is `VERCEL_GIT_COMMIT_SHA`, identical to the API's.

### Two switches, not one

`sendDefaultPii: false` is not sufficient for browser events. Events arrive at Sentry from the visitor's own browser, so Sentry's ingest sees the real client address and stores it as `user.ip_address` unless the project's **"Prevent Storing of IP Addresses"** setting is also enabled. Both switches, on both projects, or golden rule 5 is broken by a default.

`beforeSend` clears cookies and strips query strings, mirroring the Go side. Console breadcrumbs are disabled — they capture arbitrary payloads and the debugging value does not justify it. Fetch breadcrumbs stay: they carry team slugs and link ids, which are not personal data.

### Expected failures must not be reported

This app renders API failures as user interface on purpose. `classifyApiError` turns 403, 422 and field errors into copy in two languages. Reporting those to Sentry would mean one Verein mistyping a hostname repeatedly could consume the monthly quota, and the events would carry no information the UI did not already show its user.

So only unexpected errors are reported. The central `errorComponent` in `__root.tsx` reports what no classification caught; anything `classifyApiError` names reports nothing.

### Source maps are uploaded and then deleted

`@sentry/vite-plugin` uploads at build time, with `SENTRY_AUTH_TOKEN`, `SENTRY_ORG` and `SENTRY_PROJECT` as build-time variables on the web project.

Uploading is only half of it. The maps must then be removed from the deployed output via `sourcemaps.filesToDeleteAfterUpload`, or the application's source sits publicly beside its bundle — a worse outcome than the minified stack traces the upload was meant to fix. When the token is absent the plugin is not registered at all, so local builds and forks still build.

## Configuration

| Variable | Where | Notes |
| --- | --- | --- |
| `HEALTH_CHECK_TOKEN` | API runtime, GitHub secret | Unset disables `/health/deep` |
| `SENTRY_DSN` | API runtime | Empty disables Sentry |
| `VITE_SENTRY_DSN` | Web runtime and build | Public by design |
| `SENTRY_AUTH_TOKEN` | Web build only | Absent skips the source-map upload |
| `SENTRY_ORG`, `SENTRY_PROJECT` | Web build only | For the upload |

`E2E_DATABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY` already exist as repository secrets and are reused, not duplicated.

## What the maintainer has to do by hand

An explicit checklist in the plan, not a footnote. The custom-SMTP episode is the precedent: manual steps that live inside prose get skipped, and the failure then looks like a code defect.

1. Create the Sentry organisation and two projects; collect both DSNs.
2. Create a Sentry auth token with source-map upload scope.
3. Enable "Prevent Storing of IP Addresses" on **both** Sentry projects.
4. Create the Better Stack account and the three monitors, including the custom header on the first.
5. Set the alert channel to email.
6. Set the environment variables on both Vercel projects.
7. Add the `HEALTH_CHECK_TOKEN` repository secret.

Steps 1 through 5 gate verification, so they come before the tasks that need them.

## Testing

**`/health/deep`** gets unit tests for the token guard — absent, wrong, correct — and for the severity rule, which is the part a future reader is most likely to "fix": Redis failing must be 200 with the field set, Postgres failing must be 503. Plus an integration test against the real database, in the shape `internal/db` already uses.

**`BeforeSend` on both sides is a pure function**, which is the reason to put the policy there rather than in middleware. One constructed event carrying `X-Forwarded-For`, a cookie, a password-shaped body and a query string goes in; everything but `User-Agent` is gone afterwards. Falsification: remove the allowlist and the test must fail.

**The slog handler** runs against a fake transport. An `Error` record produces exactly one event; a `Warn` record produces none.

**The web classification rule** gets a test asserting that a classified expected failure reports nothing, falsified by removing the classification check.

No test touches a real DSN.

**Two things no test can prove**, stated rather than papered over: that an event actually arrives in Sentry, and that Supabase does not pause. The first is a closing-checklist item — trigger one error in each project and look. The second shows up as the first green scheduled workflow run and as 200s in the monitor. Every defect this project has actually had was found by running it.

## Open questions

- **The alert channel** is set to email because that needs no further service. Doc 02 lists it as undecided across resource thresholds, Sentry and Better Stack; email settles all three for now, and a webhook into whatever the maintainers actually watch remains the obvious upgrade.
- **Whether 5,000 events a month holds** once more than a handful of Vereine participate. The lever is sampling in `BeforeSend`; the number to watch is Sentry's own quota page.
- **Whether the three-minute monitor interval is worth 3% of the Redis budget.** It is the free tier's finest interval, not a chosen number. If the redirect path ever approaches the command ceiling, this is the first thing to lengthen.
