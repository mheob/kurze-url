# URL Shortener — Project Instructions

Open-source, multi-tenant URL shortener for German non-profit associations ("Vereine"). **One shared instance** operated by the maintainer(s) — not per-Verein self-hosting. License: MIT. This is a private side project, unaffiliated with any employer.

Full reasoning behind every decision below lives in the planning docs (see "Planning docs" at the end). This file is the condensed, load-bearing version: read it first, consult a planning doc when you need the _why_ or the detail.

---

## Golden rules

The things that are easy to get wrong, and expensive to get wrong:

1. **The tenant is called `team`** in every identifier — tables, columns, Go types, API paths, TS types. "Verein" appears only in user-facing German copy. Never `verein_id`.
2. **The redirect path is the hot path.** `GET /<slug>` must never wait on anything optional. Click recording is always async/non-blocking. Every design choice gets checked against this one code path.
3. **Security items are MVP scope, not "later"**: HTTPS-only URL scheme allowlist, SSRF protection with DNS-rebinding re-checks at fetch time, rate limiting, async Safe Browsing scanning, Argon2id password protection. Do not defer these to get a demo working.
4. **There is no RLS.** Postgres enforces _nothing_ about tenancy — the service-role connection bypasses it by design. Every single query path must filter by `team_id` and check the caller's `team_member.role` in Go. If you write a query without a tenancy filter, that is a data-leak bug, not a style issue.
5. **Never store a full IP address, ever.** Unique visitors are counted via a daily-rotating salted hash of IP+UA, deduplicated in Redis. Postgres only ever receives aggregate counts — never the hash, never a raw click row.
6. **i18n from the first component.** English is default, German ships alongside it. No hardcoded user-facing string anywhere, not even temporarily.
7. **Free-tier limits are a design constraint, not a footnote.** Redis command volume binds first (~16.7K/day), because every redirect costs at least one GET.
8. **Accessibility is a requirement** (WCAG 2.1 AA), not a nice-to-have — it is checked in CI at two levels.

---

## Stack

| Layer | Choice | Notes |
| --- | --- | --- |
| Backend | Go, **chi** router + **Huma** (code-first, generates OpenAPI 3.1) | Single persistent `net/http` server, `cmd/api/main.go` |
| DB access | **sqlc** — raw SQL, generated type-safe Go | No ORM. GORM explicitly rejected. |
| Migrations | **Supabase CLI** authors them (`supabase migration new`); Supabase's **GitHub integration** applies them on merge to `main` | No golang-migrate, no Atlas — one owner of schema state. Preview branches stay **off**; only "Deploy to production" is enabled |
| Database | Supabase (Postgres), free tier, **Frankfurt/EU region** |  |
| Cache | Upstash Redis, free tier | Fronts the redirect path; also does unique-visitor dedup and rate limiting |
| Frontend | TanStack Start (React) + Router/Query/Form/Table |  |
| UI | **shadcn/ui on Radix** (`-b radix`), Tremor for analytics, lucide-react | Radix deliberately, _not_ the new Base UI default — Tremor is Radix-based |
| CLI | Go, thin HTTP client over the same API | No shared Go module with the backend |
| Hosting | Vercel — two projects from one monorepo (`apps/api`, `apps/web`) | Go Framework Preset for the API |
| Auth | Supabase OAuth 2.1 Server (Authorization Code + PKCE) | Backend only _verifies_ JWTs |
| Email | Resend as custom SMTP on the Supabase project | Supabase's built-in sender caps at 2 mails/hour |
| Errors/monitoring | Sentry (free tier) + Better Stack uptime |  |

---

## Repo layout

```
apps/
  api/            # Go backend (chi + Huma) — Vercel project, Root Directory = apps/api
  web/            # TanStack Start frontend — Vercel project, Root Directory = apps/web
  cli/            # Go CLI — not deployed; released via goreleaser on git tag
packages/
  api-client/     # TS client generated from the API's OpenAPI spec (@hey-api/openapi-ts)
supabase/         # Supabase CLI-owned migrations (top-level, not under apps/api)
.github/workflows/
```

Inside `apps/api`: `cmd/api/main.go` and `cmd/openapi/main.go` (writes `openapi.json`, which `packages/api-client` is generated from), plus `internal/{analytics,api,audit,auth,authz,cache,config,db,destination,domainverify,link,observability,pages,slug,supabase}`. Redis lives behind `cache`, not an `internal/redis`, and Sentry lives behind `observability`, not an `internal/sentry` — that package is the only importer of `sentry-go`, so the policy about what may leave this process has one home. `scanning` and `qr` arrive with the features that need them and do not exist yet.

`apps/api` and `apps/cli` are **separate Go modules**, no `go.work` — the CLI talks HTTP, it does not import backend packages.

---

## Conventions

- **API versioning**: `/v1` path prefix. The public redirect surface is deliberately _unversioned_ and _not_ in the OpenAPI spec.
- **Errors**: Huma's default RFC 9457 `application/problem+json`. Do not build a custom error model.
- **A 409 that needs to carry a number** travels as `huma.ErrorDetail.Value`, keyed by the operation's own path-parameter `Location` (e.g. `"path.domain_id"`) — never embedded in the free-text `detail` message, which stays free to reword. `deleteDomain` (`apps/api/internal/api/domains.go`) is the first and, so far, only place this exists (the blocking link count on a 409 refusal); `apps/web/src/lib/api-errors.ts` reads it back by that same typed key. This is a deliberate pattern to reuse, not an accident to "clean up" into plain prose or refactor away.
- **Pagination**: offset/limit (`page`, `per_page`, capped at 100) with a typed `Page[T]` response envelope — never pagination headers.
- **Filtering**: flat, explicitly typed query params per endpoint. Not a generic `filter=field:op:value` scheme.
- **Auth in handlers**: declare `Security: {"bearerAuth": {}}` on operations that need it; a global middleware enforces it only where declared.
- **JWT verification**: fetch + cache Supabase's JWKS (`https://<project>.supabase.co/auth/v1/.well-known/jwks.json`), verify ES256 locally. **Not** the legacy HS256 shared secret.
- **Commits**: Conventional Commits (checked in CI, not hard-blocking yet).
- **Testing**: Vitest + RTL (+ MSW) for the frontend, Playwright + axe-core for E2E, `go test` for Go. E2E runs against Vercel previews, not on every push.

---

## API surface (summary)

All under `/v1`, all Bearer-authenticated, except the public redirect surface.

`GET /me` · teams (`POST|GET /teams`, `GET|PATCH /teams/{id}`) · members (`GET|POST /teams/{id}/members`, `PATCH|DELETE .../{user_id}`) · domains (under team, plus `POST /domains/{id}/verify`) · folders · tags · links (`POST|GET /teams/{id}/links`, `GET|PATCH|DELETE /links/{id}`) · `PUT|DELETE /links/{id}/password` (deliberately separate from PATCH — own audit actions, and a rate limit on the setter) · `GET /links/{id}/qr` (returns raw image bytes) · `GET /links/{id}/stats` · `GET /teams/{id}/audit-log`.

**Public, hostname-routed, plain chi handlers outside Huma:** `GET /{slug}` (redirect) · `GET /{slug}/verify` (password interstitial, server-rendered HTML, deliberately framework-free) · `POST /{slug}/verify` (tight rate limit).

---

## Data model (summary)

Tables: `team`, `team_member`, `domain`, `folder`, `tag`, `link`, `link_tag`, `link_scan_result`, `link_click_stats`, `audit_log`.

- UUID PKs for entities; `bigint identity` for `link_click_stats` and `audit_log`.
- Slug uniqueness is **`(domain_id, slug)`**, never `slug` alone.
- `link.team_id` is denormalized from `domain.team_id` so every authorization check avoids a join.
- `domain.team_id` is nullable; `NULL` means the shared instance hostname.
- Analytics is a generic rollup: `(link_id, bucket_start /*date*/, dimension_type, dimension_value) → clicks, unique_visitors`. **Daily** granularity. No raw click table exists, and none should be added.
- `link.password_hash` nullable, Argon2id. Never log the plaintext or the hash into `audit_log.metadata`.
- `team.slug` is globally unique, immutable, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, 3–40 characters. The **frontend** addresses a team by it (`/teams/sv-gruenwald/links`); the **API** addresses teams by UUID everywhere and never accepts a slug in a path.

---

## Non-obvious constraints (things that will bite you)

- **TypeScript codegen tools cannot run on TypeScript 7.** The repo is on 7 (the native port), which does not expose the compiler's `ts.factory` / `ts.SyntaxKind` API. Both `@hey-api/openapi-ts` and `openapi-typescript` crash on it, whatever their declared peer range says — hey-api's admits 7 and still fails. **TypeScript 6** is pinned as a devDependency of `packages/api-client` for the generator alone: it is the last JS-based release, so it still carries the full compiler API, and it is the nearest version to the repo's own that works. The shipped types and `pnpm typecheck` stay on 7, and the generated output is byte-identical either way.
- **Vercel Pro retains runtime logs for 1 day** (Hobby: 1 hour; 30-day retention needs the paid Observability Plus add-on). This project is on Pro, so the earlier "1 hour" figure in these docs was wrong — but the conclusion it supported is not: **Sentry is still the only durable error record.** A day is long enough to miss and short enough to lose a recurring failure nobody was watching for.
- **Supabase free tier has no backups at all.** Not "short retention" — none. (See open items.)
- **Supabase preview branches cost money per hour** and aren't covered by the Spend Cap → migrations run against the single project on merge to `main`, never per-PR. The GitHub integration's **"Deploy to production"** option does exactly that and is enabled; its **preview-branch** half is what costs, and stays off. Do not add a `db push` GitHub Actions workflow — that would make two systems own schema state, which is the thing the "one owner" rule exists to prevent.
- **`apps/api` doesn't get Vercel's automatic build skipping** (it's Go, not in the pnpm workspace graph) — it needs an explicit Ignored Build Step, in `apps/api/vercel.json`'s `ignoreCommand`: `git diff --quiet HEAD^ HEAD -- :/ ':(top,exclude)apps/cli'`. The `:/` (`:(top)`) prefix is load-bearing: Vercel runs this command from the Root Directory (`apps/api`), where a bare `apps/api` pathspec resolves to `apps/api/apps/api`, matches nothing, exits 0 and cancels every build — including the ones that changed the API. **The rule is inverted on purpose — build unless the change is provably irrelevant.** An allowlist of paths that _should_ build was tried first (`:/apps/api :/supabase :/apps/web`) and failed on 2026-09-06: a commit touching only `CLAUDE.md` matched nothing, so the API preview was cancelled while the web preview built anyway. `withRelatedProject` then handed the web preview the API's branch alias, which Vercel answers with a "Deployment was cancelled" page at **HTTP 200** — parsed as JSON it yields no memberships, `assertMembership` throws `notFound()`, and four e2e specs fail on a "Page not found" page that looks like a frontend bug. Only `apps/cli` is excluded, because it is outside the pnpm workspace and so does not trigger a web preview either. `e2e/global-setup.ts` is the backstop if that assumption ever turns out to be wrong.
- **A branch that adds a migration cannot pass e2e until that migration is applied to the Preview database by hand.** Supabase preview branches are off (they cost per hour), and the GitHub integration only applies migrations on merge to `main` — so the Preview project's schema is whatever `main` has. The API preview then runs this branch's code against last week's schema, every query touching a new column fails with SQLSTATE 42703, and the authenticated e2e specs fail on a page reading "Something went wrong." This bit on 2026-09-07: `column "verification_token" does not exist`, four specs red, both Vercel deployments green. Apply the branch's migration to the Preview project before expecting its e2e to pass, and remember that doing so puts Preview _ahead_ of `main` until the branch lands.
- **Frontend previews must point at the matching API preview** via Vercel Related Projects + `@vercel/related-projects`, not a hardcoded URL. **Production is the exception and is pinned**: `API_HOST` on the web project's Production environment wins over the lookup in `apiBaseUrl` (`apps/web/src/server/api.ts`). It has to, because `withRelatedProject` returns whatever Vercel currently calls the API project's production alias, while the Go router matches `API_HOSTNAME` exactly and reads every _other_ `Host` as a short-link domain. When those two drift apart nothing raises: the request still reaches the same process, is parsed as a slug, and 404s, so the only symptom is the health probe calling a healthy API unreachable. This bit on 2026-09-06, when the API moved to `api.kurze-url.app` and the alias stayed on `*.vercel.app`. Both values must name the same host, and `API_HOST` stays unset on Preview so pairing still works there.
- **The three hostnames on the API project are not interchangeable.** `API_HOSTNAME` is `api.kurze-url.app` and serves `/v1`; `SHARED_DOMAIN_HOSTNAME` is `go.kurze-url.app` and serves the redirect surface; `kurze-url-api.vercel.app` and the other platform aliases now fall through to the redirect surface too, so `/v1` on them 404s — that is correct, not a regression. The apex `kurze-url.app` and `www` belong to the **web** project: the redirect surface has no `/` route, so pointing the apex at the API would make the homepage a bare 404.
- **Upstash has no official Go rate-limit SDK** (unlike JS/Python) — the sliding window is hand-rolled against Redis.
- **301 vs 302 is per-link and defaults to 302.** Warn users inline when they pick 301: browsers cache it, so clicks go uncounted and destination changes stop taking effect.
- **QR codes always encode the short URL**, never the destination — that's what makes changing a destination safe.
- `piglig/go-qr` is the QR library; validate the centered-logo size against the chosen error-correction budget.
- **The shared default hostname is a `domain` row with `team_id IS NULL`**, upserted at boot from `SHARED_DOMAIN_HOSTNAME` (`go.kurze-url.app` in Production since 2026-09-06; `short.invalid` still in Preview). Changing the value upserts a _new_ row rather than renaming the old one, so every past value is left behind as a `team_id IS NULL` row: `localhost`, `kurze-url-web.vercel.app` and `kurze-url.vercel.app` were cleared on 2026-09-05, and `short.invalid` (`844f025f-9662-4af3-a033-49681bc51aec`) is still there. Delete only rows with no links — `link.domain_id` is `on delete cascade`, so dropping a domain drops every link on it. Preview keeping `short.invalid` is load-bearing, not neglect: `ShortUrlNotice` shows its warning only for a hostname ending in `.invalid`, and the e2e spec `warns that the short domain does not resolve` asserts that warning against a preview. Any team may create links on it; slugs there are one global, first-come-first-served namespace.
- **A hostname claim is not a reservation.** `domain.hostname` used to carry a global unique constraint; `supabase/migrations/20260906122341_custom_domains.sql` dropped it, because a global constraint made the first team to `INSERT` a row lock that hostname against everyone else — including its actual owner, who might lose the race to claim their own domain. Uniqueness now lives on a partial index, `domain_hostname_verified_key`, scoped to `where verification_status = 'verified'`: several teams may each hold a claim on the same hostname at once, the first to prove ownership (DNS TXT token) and reachability wins `verified`, and every other claim on that hostname is marked `failed`. `GetLinkableDomain` already refuses to attach a link to anything but a `verified` domain, so the exclusivity that actually matters — whose links go live — was never the unique index's job to enforce alone. Belongs on the outcome, not the attempt.
- **Onboarding a custom domain has an order, and getting it wrong looks like a bug.** Verification does two things: it looks up the DNS TXT token, and it fetches `https://<hostname>/health` expecting `"status":"ok"`. The second only passes once Vercel serves that hostname with a certificate, and Vercel only does that for a domain added to the `kurze-url-api` project — which is a manual, maintainer-only step, because the backend deliberately holds no Vercel token. So the order is: the team claims the hostname in the app → **the maintainer adds it to the Vercel project** → the team publishes the TXT record and the CNAME → the team clicks Verify. A team that publishes DNS before the maintainer's step gets a passing TXT check and a failing reachability check, which reads as "your DNS is wrong" when nothing about their DNS is.
- **`DOMAIN_DNS_TARGET`'s real value is not in this repository, and that is deliberate.** Settled 2026-09-07: Vercel now hands out a per-project CNAME target and calls the generic `cname.vercel-dns.com` a legacy record that "will continue to work", so production points Vereine at the per-project one — set as an environment variable on the `kurze-url-api` Vercel project, with the generic record left as the committed default. It is not a secret (every Verein publishes it in their own public DNS zone), but it identifies one specific Vercel project, and the fallback makes a forgotten variable degrade instead of break. Two consequences worth remembering: **the Vercel project is load-bearing** — recreating or moving it changes the target, which by then sits in DNS zones owned by the Vereine, each of whom would have to edit it by hand — and reading the current value means adding a throwaway subdomain to the project and looking at Vercel's DNS instructions. `RATE_LIMIT_DOMAIN_CLAIM_PER_HOUR` (per user), `RATE_LIMIT_DOMAIN_VERIFY_PER_DOMAIN_PER_HOUR` and `RATE_LIMIT_DOMAIN_VERIFY_PER_USER_PER_HOUR` are the other domain-related variables; see `apps/api/.env.example` for their values and reasoning.
- **Slugs are case-insensitive**: stored lowercase, folded on the redirect path. Generated slugs are 8 characters from `23456789abcdefghijkmnpqrstuvwxyz`.
- **Entity-scoped routes** (`/v1/links/{link_id}`, `/v1/domains/{domain_id}`, and folder/tag routes) authorize through per-entity scope structs in `internal/authz` that resolve the entity, then reuse the membership check. A non-member gets 404, never 403.
- **Creating a link must invalidate the redirect cache**, not only updating one — a probe may have cached the not-found sentinel under the new slug's key.
- **`/health` and `/health/deep` are not interchangeable.** The flat one is unauthenticated, answers on every hostname and touches nothing — the platform calls it, and `domainverify`'s reachability probe fetches `https://<claimed-hostname>/health` and requires `"status":"ok"` in the body, so making it touch the database would put a Postgres round trip inside domain verification. `/health/deep` is the token-guarded one that runs `select 1` and a Redis `PING`; `HEALTH_CHECK_TOKEN` unset disables it (404 for everyone), which is fail-closed on purpose. Neither is in the OpenAPI spec.
- **Postgres failing is 503; Redis failing is 200.** In `/health/deep` this is a decision about which channel carries which severity, not an oversight. With Redis down every redirect still works through Postgres — degradation, which travels to Sentry through the error log. Returning 503 there would page the maintainer about a service that is serving, which is how an alert channel gets ignored. `TestDeepHealthAnswers200WhenOnlyRedisIsUnreachable` exists to stop someone "fixing" it.
- **Neither Supabase project may go seven days without activity** — the free tier pauses it, and restoring is manual and slow. Production is kept awake by Better Stack polling `/health/deep` every three minutes, plus a daily `curl` from `.github/workflows/keep-alive.yml` so the whole thing does not depend on Better Stack continuing to run. Preview is kept awake only by that workflow's `psql` step, because a preview deployment has no stable URL to monitor and does not exist at all between pull requests. GitHub disables scheduled workflows after sixty days of repository inactivity, with a warning email first.
- **`observability.Middleware()` goes inside `middleware.Recoverer`, never outside.** Recoverer innermost swallows the panic and Sentry sees nothing; the Sentry middleware outermost without `Repanic` answers 500 itself and Recoverer never logs the stack trace. Its `WaitForDelivery` is the only flush on any request path here, and only a panic reaches it — golden rule 2 permits waiting on a response that is already 5xx, and nothing else.
- **The Sentry scrubber drops the request body, and that line is mandatory.** `POST /{slug}/verify` carries a link's password in cleartext, so without it the first panic on the password interstitial ships that password to Sentry. Headers are an allowlist (`User-Agent` only), never a denylist, so a proxy header the platform adds next year is excluded by construction. Only `slog.LevelError` produces an event: `Warn` is used here for expected conditions, and the free tier allows 5,000 events a month.
- **Web source maps are uploaded and then deleted.** `sentryTanstackStart` must be the **last** Vite plugin, and its `filesToDeleteAfterUpload` is not tidying: uploading without deleting publishes the app's source beside its bundle. The plugin is registered only when `SENTRY_AUTH_TOKEN` is present, so local and fork builds still work. And `apps/web` never reports what `classifyApiError` names — a 403, a 422, a field error are rendered as UI on purpose, and reporting them would spend the monthly budget on events the visitor already saw.
- **Backups live in a separate private repository, and a restore is not just data.** `kurze-url-backups` runs a nightly `supabase db dump` of `public`, the `auth` data and the roles, encrypts it to an `age` public key and keeps it as a release asset — fourteen daily and twelve weekly. The workflow lives there rather than here so that `GITHUB_TOKEN` suffices and the production database URL never sits in a public repository's secrets, and it holds only the public key, so a compromised run can append a backup but not read one. A Better Stack heartbeat watches for the **absence** of a backup, because GitHub disables scheduled workflows after 60 days without repository activity and a backup repository has none by construction. Two details the 2026-09-07 drills settled, both of which had made the procedure as first written unusable: the data dump needs `-s auth,public`, or it pulls in platform-owned `storage` tables the restoring role cannot write and the whole single-transaction restore rolls back; and `roles.sql` always fails on a `GRANT SET ON PARAMETER` the platform alone may make, which is expected and must not stop the restore. The procedure is `docs/restore.md`, in this repository on purpose. Read it before you need it: a restore is the dump **plus six environment variables across both Vercel projects, plus Resend as custom SMTP and the auth URLs on the new Supabase project** — and sign-in here is a magic link with no password fallback, so skipping the mail configuration leaves a correctly populated database nobody can log in to, with a login form that still reports success.
- **`HEALTH_CHECK_TOKEN` and `SENTRY_DSN` are the two new API environment variables this feature added**, both documented and empty-by-default in `apps/api/.env.example`. `apps/web/.env.example` is new too — the app's first — and `VITE_SENTRY_DSN` is the first value that app needs in the browser; both DSNs default to empty, which disables reporting rather than erroring.
- **Sentry initialises inside the router factory, not through Sentry's documented Node `--import` hook.** `getRouter` (`apps/web/src/router.tsx`) calls `initSentry(router.isServer)` unconditionally — the server and client bundles share this module, and both reach that line — because Nitro builds the server bundle and Vercel starts it, and neither hands the process a command line to hook `--import` into. `isServer` only tags the event's `serverName` (`web-ssr` vs. unset); the real gate is whether `VITE_SENTRY_DSN` is set. `sendDefaultPii` is deprecated as of SDK v10 in favor of `dataCollection`, and `@sentry/core` resolves every _unset_ field of a partial `dataCollection` object against its permissive `DEFAULTS`, not the off-state, the instant the object is present at all — so `sentryOptions` in `apps/web/src/lib/observability.ts` sets every field explicitly rather than leaving any to inference.
- **A team slug is not a route-safe name by itself.** TanStack Router matches a static segment before a dynamic one, so any static child route under `/teams/` permanently shadows the team whose slug matches it. The create form therefore lives at `/new-team`, outside the namespace, and `reservedTeamSlugs` in `apps/api/internal/api/teams.go` refuses the values a future static route would claim. Adding a static child route under `/teams/` means adding its segment to that list in the same change.
- **Slug-to-team-id resolution happens in the frontend**, in each team route's `beforeLoad`, out of `GET /v1/me`'s membership list — `requireTeamId` in `apps/web/src/routes/_authed.tsx`. It costs no request, and it keeps a second resolution path out of every tenancy-critical query. Route context carries `teamId`; `params` carries `teamSlug`. The `team` cookie holds the slug.
- **Every rate limit caps one subject in one window, and that shape cannot defend a free-tier ceiling.** Values settled 2026-09-08 and reasoned through one by one in `apps/api/.env.example`; what matters is the shape, not the numbers. A per-IP, per-user, per-team or per-domain window stops a single runaway client, and a shared monthly quota is spent by subjects the limiter never compares. Two consequences are load-bearing. **The redirect limit does not protect Upstash's 500K commands a month**: one IP staying inside `RATE_LIMIT_REDIRECT_PER_MIN=60` spends the month's budget in 21 hours to 2.9 days, a refused redirect still costs the two GETs the limiter reads, and no NAT-tolerant value changes either fact — the lower bound is set by mobile-carrier egress, Verein offices and mail-provider link scanners all arriving from one address. The spread exists because Upstash does not document whether a Lua `EVAL` counts as one command or as the commands inside it; the path runs `ratelimit.lua` (4) and `redirect_lookup.lua` (2–3), so the limiter is the more expensive of the two. That budget is watched, not enforced, and belongs to the alerting open item. **The invitation limit does protect its quota, because it was given a second, global half**: `RATE_LIMIT_INVITE_GLOBAL_PER_MONTH=200` bounds invitations instance-wide over a rolling 30 days, checked after the per-team hourly cap so a team refused for its own burst never spends instance budget. It exists because sign-in is a magic link with no password fallback and Resend's free tier is 3,000 mails a month across all Supabase auth email — an exhausted quota locks every Verein out with no recovery from inside the app. That path fails closed throughout (0 refuses everything, a Redis error answers 500) and logs a global refusal at error level so it reaches Sentry. `signInWithOtp` in `apps/web` spends the same quota outside this counter, bounded only by Supabase's own limits. Also settled: the two domain-verification axes no longer share a threshold (`RATE_LIMIT_DOMAIN_VERIFY_PER_DOMAIN_PER_HOUR=10`, `RATE_LIMIT_DOMAIN_VERIFY_PER_USER_PER_HOUR=40`), because one person legitimately onboards several domains at once and the shared value made the user axis fire while every domain was still well inside its own.
- **A link's password lives on its own route, and both ends of it must invalidate the redirect cache.** `PUT|DELETE /v1/links/{link_id}/password` (`apps/api/internal/api/link_password.go`) is separate from `PATCH` so it gets its own audit actions (`link.password_set`, `link.password_changed`, `link.password_removed`) — the precedent the folders-and-tags decision cites for refusing a tag subresource, which has neither. Only the setter also gets its own rate limit: it alone computes an Argon2id hash, so it alone is a CPU/memory amplifier worth capping, while `DELETE` computes nothing and carries none. `link.Cached` carries `HasPassword`, so a handler that forgets `invalidateLink` leaves a freshly protected link redirecting straight through for up to an hour with a 200, an audit row and a dashboard that all say it worked — or, the other direction, a freshly unprotected link still demanding a password nobody can answer any more; `TestSetLinkPasswordInvalidatesTheRedirectCache` and `TestRemoveLinkPasswordInvalidatesTheRedirectCache` (`apps/api/internal/api/link_password_test.go`) both drive the real redirect handler for exactly that reason, one in each direction. The policy in `apps/api/internal/auth/policy.go` targets predictability rather than strength — 8 characters, 4 distinct, and rejection of anything derived from the link's slug, its destination's hostname, or the Verein's name and slug after German transliteration — because a link password is shared out of band with a group and `Sommerfest26` is the realistic failure, not a short one. Its rules run in a fixed order — length, then repetition, then context, then the common-password list — deliberately not the order the design spec's own numbering implies: context has to come before the common list, because the reverse would make it impossible for any word to ever be both a common-list entry and a context-rule test fixture, a standing trap nobody would remember on the next addition to either list, with a silently-wrong-reason test as the only symptom. `passwort1` is a deliberate exception the policy accepts — 9 characters, 8 distinct, derived from nothing — and `TestValidatePasswordAcceptsAWeakButCompliantPassword` pins it so nobody tightens the policy by accident: it raises the floor, not the whole defense, and the failure-only per-link limit in `verify.go` (`PasswordFailureRateLimitPerHour`) is what bounds the damage from there. The policy is mirrored in `apps/web/src/lib/link-password.ts` for immediate feedback and **will drift**; the API is the enforcement point, and the reason travels as `ErrorDetail{Location: "body.password", Value: "<token>"}` where the token is the Go sentinel's own `Error()` string. Creating a link with a password in one request is deliberately not possible: reopen that when bulk create or import arrives and the two-step window stops being milliseconds.
- **The instance is invitation-only, and two separate gates make it so.** Neither is an oversight, and removing either one opens the instance without the other noticing. `signInWithOtp` is called with `shouldCreateUser: false` (`apps/web/src/server/auth.ts`), so an unknown address never becomes an account — the login form still reports that a link was sent, deliberately, to avoid being an account-enumeration oracle. `MAINTAINER_USER_IDS` gates `POST /v1/teams` (`Config.IsMaintainer`, checked in `createTeam`), so an invited member of one Verein cannot create a second team. Decided 2026-09-08: **the reason is that `go.kurze-url.app` is one shared, first-come-first-served slug namespace, and a domain's reputation is indivisible** — one spammer who self-registers can get the hostname blocklisted, and every Verein's links die together, including those of Vereine who did nothing. Safe Browsing scanning is asynchronous and does not prevent that. Reopen the question when Vereine start arriving that the maintainer does not already know, or when the legal texts exist and the instance goes public; either one needs abuse controls designed first, not retrofitted.

---

## Bootstrap order (suggested first steps)

1. `git init`, MIT `LICENSE`, `.gitignore`, pnpm workspace + directory skeleton.
2. Supabase project (**Frankfurt/EU region**) + Upstash DB (EU if offered). Configure Resend as custom SMTP right away.
3. First migration from the schema in doc 05 → `supabase/migrations/`. Then `sqlc.yaml` + first queries.
4. `apps/api`: chi + Huma skeleton, JWKS auth middleware, health route. Deploy to Vercel early to shake out the Go Framework Preset.
5. **The redirect path first** (`GET /{slug}` + Redis cache + async click recording) — it's the architectural spine; build it before CRUD.
6. Link CRUD via Huma → generate the OpenAPI spec → generate `packages/api-client`.
7. `apps/web`: TanStack Start, `shadcn init -b radix`, i18n scaffolding **before** the first real page, dark/light mode from the first component.
8. CI workflows, then Sentry + Better Stack.

---

## Open items

Not decided yet — do not silently invent an answer, flag it instead:

- **`audit_log.action` value taxonomy** — falls out of the endpoint list, needs writing down.
- **Alert notification channel** is email, to the maintainer's address, for all three of Sentry, Better Stack and the Vercel resource thresholds — chosen because it needs no further service. A webhook into whatever the maintainers actually watch remains the obvious upgrade. Still genuinely open: whether 5,000 Sentry events a month holds once more than a handful of Vereine participate, and whether the three-minute monitor interval is worth its share of the daily Redis command budget. The "roughly 3%" this line used to claim was 480 polls a day against ~16.7K commands — but `/health/deep` spends a `PING`, and `PING` is on Upstash's published list of commands it does not charge for. Whether the free tier's command _limit_ excludes them too is undocumented, so the real figure is between 0% and 3% and nobody has measured it. This item also now owns the Redis command budget itself: the redirect rate limit provably cannot defend it (see the rate-limit entry under "Non-obvious constraints"), so a usage poll with a threshold alert is the only thing that will.
- **Legal texts** (Impressum, Datenschutzerklärung, AVV) need a lawyer before the instance opens to real Vereine; two specific questions are flagged in doc 08.
- **`public.profile` table** — only if the frontend needs display fields beyond `auth.users`.
- Existing-user-invited-to-a-second-team path (no email; they just see the new team on next login).

---

## Deferred features (schema/API leaves room, don't build now)

Geotargeting · click-count-based expiration · configurable query-parameter rules · link health monitoring · link reporting + domain blocklist · bulk create · import/export · preview pages (`/abcd+`) · browser extension · Homebrew/Scoop packaging · passkeys/MFA.

---

## Planning docs

In `docs/planning/`. Detailed reasoning, alternatives considered, and rejected options:

| Doc | Contents |
| --- | --- |
| `01-architecture.md` | System overview, redirect data flow, 301/302, security-by-design, analytics/privacy, CLI auth |
| `02-external-services-and-hosting.md` | Supabase/Upstash/Vercel free-tier limits, Safe Browsing, custom domains, Resend, alert thresholds, Sentry, Better Stack |
| `03-frontend.md` | TanStack, Radix-vs-Base-UI reasoning, Tremor, i18n, accessibility, Storybook, testing strategy |
| `04-backend-architecture.md` | Vercel Go preset, chi, Huma vs. oapi-codegen, sqlc vs. GORM, migrations, rate limiting |
| `05-database-schema.md` | Full schema, analytics rollup design, Redis dedup, audit log, RLS reasoning, indexes |
| `06-api-design.md` | Versioning, auth, pagination, filtering, full endpoint list, team invitations |
| `07-repo-structure-and-tooling.md` | Monorepo layout, two Vercel projects, CI workflows, secrets, goreleaser |
| `08-legal-and-compliance.md` | Impressum, Datenschutzerklärung, AVV, EU region, cookie/consent note |
| `planning-url-shortener.md` | The original feature list (Dev/Core/Advanced tiers) this all traces back to |
| `planning-feedback-2026-08-14.md` | First-pass feedback on that list; superseded by 01–08, kept for history |
| `00-index.md` | Human-facing map of all of the above, plus the decision log |

These were copied in from the planning folder on 2026-09-01. **Treat the copies in this repo as canonical from now on** — they're the ones under version control. Update them here rather than editing the originals, or the two sets will drift.
