# URL Shortener — External Services & Hosting Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code.

**Project context correction (2026-09-01)**: this is an open-source project for non-profit associations ("Vereine"), not affiliated with AWESOME!. Earlier "AWESOME!" references below mean "the maintainer(s) operating the shared instance." Hosting model reconfirmed the same day: one shared multi-tenant instance run by the maintainer(s) for participating Vereine, not per-Verein self-hosting — so the Vercel-native decision stands unchanged. License: **MIT** (final).

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| License | **MIT** (decided 2026-09-01) | Permissive; AGPL explicitly ruled out — no plan to protect against a competing hosted fork |
| Database | Supabase (Postgres), free tier | Managed/SaaS, not self-hosted |
| Cache | Upstash Redis, free tier | In MVP scope; fronts the redirect path |
| Compute/hosting | **Vercel** | Native APIs (Go serverless functions), no Docker; one shared multi-tenant instance, not per-Verein self-hosting |
| CLI auth | OAuth (Authorization Code + PKCE via Supabase's OAuth 2.1 Server), primary; plain API key as fallback, built only once CI/headless use actually comes up | See `01-architecture.md` |
| Malware/phishing scanning | **Google Safe Browsing API**, free | Licensing accepted 2026-09-01: non-profit/community use is a reasonable read of "non-commercial," no separate formal legal review — see note below |
| Custom domain provisioning | **Maintainer-in-the-loop, not self-service** | A Verein claims a hostname in the app; the maintainer adds it to the `kurze-url-api` Vercel project by hand. The backend never calls Vercel. Decided 2026-09-06 — see `docs/superpowers/specs/2026-09-06-custom-domains-design.md` for why the self-service SDK flow sketched below was rejected (a self-mutating deployment token, and an unverified assumption about Hobby-plan API access) |
| Free-tier scaling | **Reactive**: monitoring + alerts on known ceilings, upgrade when a limit is actually approached | Concrete thresholds decided 2026-09-01 — see "Alert thresholds" below |
| Transactional email (custom SMTP) | **Resend**, free tier (decided 2026-09-01) | Needed for real team-invite emails — see "Transactional email" below and `06-api-design.md` |
| Error tracking | **Sentry**, free Developer tier (decided 2026-09-01) | Not just "nice to have" — see "Observability: error tracking" below, ties to a real Vercel log-retention limitation (1 day on Pro, this project's plan) |
| Uptime monitoring | **Better Stack**, free tier (decided 2026-09-01) — not UptimeRobot | UptimeRobot's free tier is now personal/non-commercial-use only — see "Observability: uptime monitoring" below — built 2026-09-07, three monitors, email alerts |

## Free-tier limits (verified August 2026)

**Supabase (free tier)**

- Projects pause after 1 week of inactivity; max 2 active projects per account.
- 500 MB database storage.
- 50,000 monthly active users.
- 5 GB egress + 5 GB cached egress.
- Unlimited API requests.
- Mitigation for the pause behavior: a scheduled keep-alive ping. Accepted as sufficient for now rather than upgrading to a paid plan immediately.
- **No backups at all.** Not short retention: none. Answered on 2026-09-07 (`docs/superpowers/specs/2026-09-07-backups-design.md`): a nightly encrypted dump to a private GitHub repository, fourteen daily and twelve weekly, with the restore procedure in `docs/restore.md`. Supabase Pro's managed backups were the alternative and remain the fallback if the session pooler ever stops serving a full dump — they cost 25 $/month and, being in the same account as the thing they protect, cover data loss but not account loss.
- Supabase also ships an **OAuth 2.1 Server** feature ("Sign in with Your App"), supporting Authorization Code + PKCE and refresh-token grants — this is what the CLI's OAuth login is built on. It does not support the Device Authorization Grant (RFC 8628).

**Upstash Redis (free tier)**

- 256 MB storage.
- 500K commands/month (~16.7K/day).
- 10 GB bandwidth/month.
- Up to 10 free databases (unclaimed databases are deleted after 3 days — claim into the account promptly).

**Vercel — chosen hosting platform**

- Go supported as a first-class serverless function runtime.
- Cold-start latency is a factor on the redirect path — this is why Redis caching is in MVP scope; the cache absorbs most of the cold-start risk on the hottest path.
- **Custom domains for multi-tenant use**: Vercel has a documented pattern for exactly this case (SaaS platforms letting customers bring their own domain). **Not what got built** — the 2026-09-06 custom-domains design chose maintainer-in-the-loop provisioning instead (see the decision table above and the design doc it cites) — but the programmatic flow below is what a later switch to self-service would use, so it stays here rather than getting deleted. Via the Vercel SDK:
  1. `projectsAddProjectDomain` — add the tenant's domain to the project.
  2. Vercel attempts automatic SSL issuance; if the domain is already known to Vercel, the tenant must add a TXT record to prove ownership.
  3. `projectsVerifyProjectDomain` / `projectsGetProjectDomain` — check/trigger verification status; poll or webhook until verified.
  4. `projectsRemoveProjectDomain` + `domainsDeleteDomain` — clean removal when a team drops a domain.
  - No wildcard domain or nameserver takeover is needed for this use case (that pattern is only for `*.acme.com`-style subdomain-per-tenant setups, which isn't what's being built here — each team brings a fully distinct domain).
  - DNS propagation after a tenant adds records can take 24–48 hours — worth setting expectations in the UI during onboarding.
- No server-side response caching by default — the 301-vs-302 caching trade-off discussed in `01-architecture.md` is a client/browser-side concern, not something Vercel adds on top.

**Cloudflare — considered, not chosen**

- Go is supported via Cloudflare Containers (GA since April 2026), running standard Go HTTP servers as persistent services from Docker Hub images, with active-CPU pricing.
- Containers require the Workers **Paid** plan — no free tier for this path. This, plus committing to a single platform's native APIs rather than maintaining two integration paths, is why Vercel was chosen instead.

## Malware/phishing scanning: Google Safe Browsing API

Chosen for MVP. What it checks: URLs against Google's continuously updated lists of phishing/social-engineering and malware-hosting sites.

**Licensing, decided 2026-09-01**: Google's terms restrict Safe Browsing to "non-commercial use only" — for commercial use ("for sale or revenue-generating purposes") Google points to the paid **Web Risk API** instead. Given the project is a non-profit, open-source URL shortener for Vereine with no revenue-generating purpose, this reads as a reasonable fit for "non-commercial" use. Decision: proceed on that basis without a separate formal legal review — the risk is accepted as low, not eliminated with certainty (a provider could in principle interpret its own terms more narrowly). Web Risk API remains a drop-in-shaped fallback if that assessment is ever challenged, since scanning already runs async and out-of-band from link creation, so swapping providers later wouldn't require an architecture change.

Free-tier alternative considered and not chosen: VirusTotal (4 requests/minute, 500/day hard caps) — broader multi-engine coverage but limits that could bind at real usage volume.

## Transactional email: Resend (custom SMTP for Supabase)

Decided 2026-09-01, arising from API design work on team invitations (see `06-api-design.md`): Supabase's **built-in** auth email sending is capped at **2 emails/hour** — far too low for real team-invite volume, even for one small Verein onboarding a handful of members at once. The fix is configuring **custom SMTP** on the Supabase project (a dashboard setting, not application code); the built-in cap cannot otherwise be raised.

**Resend** chosen over the alternatives compared (Postmark: 100/month; SendGrid: 100/day but trial-only; AWS SES: 3,000/month but needs IAM setup; Brevo: ~9,000/month permanent, largest free tier but a heavier dashboard/setup): free tier of 3,000 emails/month, and the simplest setup of the group — an API key dropped in as the SMTP password, no domain-IAM ceremony. 3,000/month is comfortably beyond what this project needs even optimistically (invite emails plus whatever volume of signup/magic-link/password-reset emails Supabase itself sends, once everything routes through the same SMTP config).

**Enabling custom SMTP does not lift the cap to Resend's quota.** Supabase replaces its 2/hour default with its own 30/hour default, adjustable at Auth → Rate Limits. Set it to **50/hour**: above the application's own `RATE_LIMIT_INVITE_PER_HOUR` (20 per team) so that limiter is the one users hit and the error is one this project can translate; enough for a Verein onboarding ~20 members while some of them immediately request a magic link; and low enough that a single runaway hour cannot drain the 100/day Resend quota and lock everyone out for the rest of the day.

Worth revisiting if sign-in ever stops being magic-link-only — with passwords, email volume falls back to invitations and resets, and 50/hour becomes generous.

No Go backend code is involved — this is purely a Supabase project setting. The Go backend triggers invites via Supabase's Admin API (already using the service-role key it needs regardless), and Supabase sends the actual email through the configured SMTP.

## Alert thresholds for free-tier monitoring (decided 2026-09-01)

Two-stage thresholds (warning at 70%, critical at 90%) on every known free-tier ceiling, so there's a real lead window to upgrade before anything actually breaks:

| Resource               | Cap                  | Warning (70%)        | Critical (90%)     |
| ---------------------- | -------------------- | -------------------- | ------------------ |
| Supabase DB storage    | 500 MB               | 350 MB               | 450 MB             |
| Supabase MAU           | 50,000               | 35,000               | 45,000             |
| Supabase egress        | 5 GB/mo              | 3.5 GB               | 4.5 GB             |
| Upstash Redis storage  | 256 MB               | 180 MB               | 230 MB             |
| Upstash Redis commands | 500K/mo (~16.7K/day) | 350K/mo (~11.7K/day) | 450K/mo (~15K/day) |
| Upstash bandwidth      | 10 GB/mo             | 7 GB                 | 9 GB               |
| Resend emails          | 100/day (3,000/mo)   | 70/day               | 90/day             |

**Resend's daily cap, not its monthly one, is the number to watch.** The free tier is 3,000/month _and_ 100/day, which are the same ceiling stated twice — 100 × 30 ≈ 3,000 — so the daily limit is what an onboarding burst actually hits. It matters more than the monthly figure suggests because magic-link login means **every sign-in costs an email**, not just invitations. Exhausting a day's quota does not degrade gracefully: Supabase's send fails and nobody can log in until the next day.

**Redis commands is the one most likely to bind first in practice**, not the others — and by more than this document first assumed. The figure here used to say a redirect costs "at least one Redis GET", which put the ~16.7K/day cap at roughly 500-700 redirects/hour sustained. The implemented redirect path runs two Lua scripts rather than one command: `ratelimit.lua` (GET, GET, INCR, EXPIRE) and `redirect_lookup.lua` (GET, SADD, plus an EXPIRE on a visitor's first hit of the day). Upstash does not document whether a script counts as a single `EVAL` or as the commands inside it, so a cached redirect costs either 2 commands or 6-7, and the sustained ceiling is somewhere between roughly 350 and 110 redirects an hour.

Two things follow, both settled with the rate-limit values on 2026-09-08. A refused redirect still costs the two GETs the rate limiter reads, so the limiter does not stop consumption even when it fires. And one IP staying inside `RATE_LIMIT_REDIRECT_PER_MIN=60` spends a whole month's budget in 21 hours to 2.9 days — no per-IP value both tolerates NAT and prevents that, because the two are three orders of magnitude apart. The command budget is therefore a monitoring problem rather than a limiter problem, and it belongs to the alert-channel item still open in `CLAUDE.md`. Worth noting for any later attempt to make the hot path cheaper: the rate limiter is the more expensive of the two scripts, and it guards the budget it spends the most of.

**Implementation**: both platforms expose the numbers needed to check this — Supabase via its Management API (project usage/stats endpoints) and Upstash via its Developer API (`Get Database Stats`). A Vercel Cron Job that polls both once a day, compares against the table above, and sends a notification (email/webhook) on a threshold crossing covers this with no extra infrastructure. The same cron job can double as the Supabase keep-alive ping (a trivial query against the DB) — one job, two purposes. Consider also alerting if the keep-alive itself fails for >3 days, as an early warning inside the 7-day pause window rather than finding out only after a project has already paused.

## Observability: error tracking (Sentry)

Decided 2026-09-01, added on top of the resource-threshold monitoring above rather than replacing it — the "Alert thresholds" section above answers "are we about to hit a free-tier ceiling," not "is the application actually throwing errors right now," which is a real gap on its own.

Finding that makes this more than a nice-to-have: **Vercel retains runtime logs for 1 day on the Pro plan** (Hobby: 1 hour; 30-day retention needs the paid Observability Plus add-on). Without a separate error-tracking layer, any bug that isn't caught within a day of happening leaves no trace at all — for a small, infrequently-checked project, that's a real risk of genuinely never finding out about recurring failures (e.g. a Safe Browsing scan silently failing, a Redis connection error on the redirect hot path).

**Sentry**'s free "Developer" tier: 5,000 events/month, 1 user. The 1-user ceiling matches a solo/small-maintainer project fine for now; the 5,000/month event cap is generous relative to this project's expected traffic (a handful of low-volume Vereine, not a high-throughput consumer app) — worth re-checking once there are meaningfully more participating Vereine. Both `apps/api` (Go SDK) and `apps/web` (React/TanStack SDK) report into the same Sentry organization; separate DSNs per app but sharing the one free event quota. This closes the gap Vercel's own 1-day log window leaves open, independent of whether anyone happened to be looking at the time.

### Dashboard settings a maintainer still has to confirm

Three things the code cannot do for itself, all of them one-time and none of them verified by CI. They live here rather than in a task checklist because they outlive the task that produced them.

1. **Enable Spike Protection on both Sentry projects.** It is free, it is off by default, and it is the only server-side backstop against one incident burning the whole month's 5,000 events. The API's own per-message throttle (`internal/observability/sloghandler.go`) covers the shape this project actually hit — the same failure logged on every request during a dependency outage — but it cannot help with a burst spread across many distinct messages, and there is no equivalent throttle in the browser SDK at all.
2. **Confirm "Automatically expose System Environment Variables" is on for the API's Vercel project.** `VERCEL_ENV` and `VERCEL_GIT_COMMIT_SHA` are read at runtime to set Sentry's `environment` and `release`. With that setting off both are simply absent, nothing fails, and every production event arrives tagged `environment: development` with no release attached — which quietly removes the cross-project correlation (same release, same environment, API and web side by side) the whole design rests on.
3. **After the first production deploy, request a `.map` URL next to a built asset on the live site and confirm it 404s.** Source maps are uploaded to Sentry and then deleted from the output (`apps/web/vite.config.ts`'s `filesToDeleteAfterUpload`), but nothing in CI verifies the deletion — and the failure mode is publishing this app's source next to its bundle, which is worse than the minified stack traces the upload was meant to fix.

## Observability: uptime monitoring (Better Stack, not UptimeRobot)

Decided 2026-09-01: a simple external check that the redirect path (`GET /<slug>` on the shared default domain) actually responds — catches the case where the whole service is down, which error tracking alone wouldn't necessarily surface loudly.

**UptimeRobot was the default first choice and is explicitly ruled out**: as of December 2024, UptimeRobot's free plan terms restrict it to **personal, non-commercial use only** — a shared multi-tenant tool operated for multiple Vereine doesn't comfortably fit that, even though the project itself is non-profit and non-revenue-generating (the same kind of licensing-language mismatch already navigated for Google Safe Browsing in this document, but here the cleaner path is just picking a different provider rather than accepting the risk). **Better Stack**'s free tier (10 monitors, ~3-minute check interval, Slack + email alerts, one status page) doesn't carry that same explicit restriction in what's publicly documented — chosen on that basis, though its terms of service are worth a direct read before relying on it, the same "reasonable-fit assumption, not a legal confirmation" caveat already used elsewhere in this document.

**Built 2026-09-07** as three monitors rather than one: `/health/deep` on the API hostname (token-guarded, checks Postgres and Redis), the flat `/health` on `go.kurze-url.app` (proves DNS, TLS and the process on the redirect surface), and the web project's landing page. Monitoring a real short link end to end was considered and rejected: a cache hit never reaches Postgres, so it would not keep Supabase awake, and at a three-minute interval it would add 480 clicks a day to a real link's statistics.

## Not yet decided / to revisit

- Notification channel for alerts (email vs. a webhook into chat/Discord/etc. — whatever the maintainer team actually watches). Now applies to both the resource-threshold alerts above and Sentry/Better Stack notifications — one channel decision probably wants to cover all three.
- Whether Vercel's 1-day Pro log retention ever becomes enough of a problem (beyond what Sentry's error capture already covers) to justify Log Drains or an Observability Plus upgrade — not needed for MVP.
