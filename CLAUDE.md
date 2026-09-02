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
| Migrations | **Supabase CLI only** (`supabase migration new`, `db push`) | No golang-migrate, no Atlas — one owner of schema state |
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
  api-client/     # TS client generated from the API's OpenAPI spec (openapi-typescript)
supabase/         # Supabase CLI-owned migrations (top-level, not under apps/api)
.github/workflows/
```

Inside `apps/api`: `cmd/api/main.go` plus `internal/{api,db,redis,auth,scanning,qr}`.

`apps/api` and `apps/cli` are **separate Go modules**, no `go.work` — the CLI talks HTTP, it does not import backend packages.

---

## Conventions

- **API versioning**: `/v1` path prefix. The public redirect surface is deliberately _unversioned_ and _not_ in the OpenAPI spec.
- **Errors**: Huma's default RFC 9457 `application/problem+json`. Do not build a custom error model.
- **Pagination**: offset/limit (`page`, `per_page`, capped at 100) with a typed `Page[T]` response envelope — never pagination headers.
- **Filtering**: flat, explicitly typed query params per endpoint. Not a generic `filter=field:op:value` scheme.
- **Auth in handlers**: declare `Security: {"bearerAuth": {}}` on operations that need it; a global middleware enforces it only where declared.
- **JWT verification**: fetch + cache Supabase's JWKS (`https://<project>.supabase.co/auth/v1/.well-known/jwks.json`), verify ES256 locally. **Not** the legacy HS256 shared secret.
- **Commits**: Conventional Commits (checked in CI, not hard-blocking yet).
- **Testing**: Vitest + RTL (+ MSW) for the frontend, Playwright + axe-core for E2E, `go test` for Go. E2E runs against Vercel previews, not on every push.

---

## API surface (summary)

All under `/v1`, all Bearer-authenticated, except the public redirect surface.

`GET /me` · teams (`POST|GET /teams`, `GET|PATCH /teams/{id}`) · members (`GET|POST /teams/{id}/members`, `PATCH|DELETE .../{user_id}`) · domains (under team, plus `POST /domains/{id}/verify`) · folders · tags · links (`POST|GET /teams/{id}/links`, `GET|PATCH|DELETE /links/{id}`) · `PUT|DELETE /links/{id}/password` (deliberately separate from PATCH — own audit action, own rate limit) · `GET /links/{id}/qr` (returns raw image bytes) · `GET /links/{id}/stats` · `GET /teams/{id}/audit-log`.

**Public, hostname-routed, plain chi handlers outside Huma:** `GET /{slug}` (redirect) · `GET /{slug}/verify` (password interstitial, server-rendered HTML, deliberately framework-free) · `POST /{slug}/verify` (tight rate limit).

---

## Data model (summary)

Tables: `team`, `team_member`, `domain`, `folder`, `tag`, `link`, `link_tag`, `link_scan_result`, `link_click_stats`, `audit_log`.

- UUID PKs for entities; `bigint identity` for `link_click_stats` and `audit_log`.
- Slug uniqueness is **`(domain_id, slug)`**, never `slug` alone.
- `link.team_id` is denormalized from `domain.team_id` so every authorization check avoids a join.
- Analytics is a generic rollup: `(link_id, bucket_start /*date*/, dimension_type, dimension_value) → clicks, unique_visitors`. **Daily** granularity. No raw click table exists, and none should be added.
- `link.password_hash` nullable, Argon2id. Never log the plaintext or the hash into `audit_log.metadata`.

---

## Non-obvious constraints (things that will bite you)

- **Vercel Hobby retains runtime logs for 1 hour.** Sentry is the only durable error record — wire it up early, not last.
- **Supabase free tier has no backups at all.** Not "short retention" — none. (See open items.)
- **Supabase Branching costs money per hour** and isn't covered by the Spend Cap → migrations run against the single project on merge to `main`, never per-PR.
- **`apps/api` doesn't get Vercel's automatic build skipping** (it's Go, not in the pnpm workspace graph) — it needs an explicit Ignored Build Step: `git diff --quiet HEAD^ HEAD -- apps/api supabase`.
- **Frontend previews must point at the matching API preview** via Vercel Related Projects + `@vercel/related-projects`, not a hardcoded URL.
- **Upstash has no official Go rate-limit SDK** (unlike JS/Python) — the sliding window is hand-rolled against Redis.
- **301 vs 302 is per-link and defaults to 302.** Warn users inline when they pick 301: browsers cache it, so clicks go uncounted and destination changes stop taking effect.
- **QR codes always encode the short URL**, never the destination — that's what makes changing a destination safe.
- `piglig/go-qr` is the QR library; validate the centered-logo size against the chosen error-correction budget.

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

- **Backups**: Supabase free tier provides none. A scheduled `supabase db dump` to off-site storage is the obvious mitigation, not yet designed.
- **Signup gate**: open self-service vs. maintainer approval for new teams on the shared instance. Affects abuse exposure and shared free-tier budget.
- **Concrete rate-limit numbers** (link creation, redirect, password check) — mechanism decided, values not.
- **`audit_log.action` value taxonomy** — falls out of the endpoint list, needs writing down.
- **Alert notification channel** (email vs. webhook) for free-tier thresholds, Sentry and Better Stack.
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
