# URL Shortener — Database Schema Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code (concrete Supabase CLI migrations + sqlc queries).

Project context: Postgres via Supabase, schema owned by Supabase CLI migrations, accessed from Go via sqlc (see `04-backend-architecture.md`). Consolidates data-model notes scattered across `01-architecture.md`, `02-external-services-and-hosting.md`, and `03-frontend.md` into an actual schema. Tenant unit is a **team** (= one participating Verein) — all table/column names use `team`, not the German word, for a consistently English schema; "Verein" stays the term used in user-facing prose and UI copy.

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Naming | **`team`, not `verein`, in all identifiers** (decided 2026-09-01) | Renamed throughout — see below |
| Password protection | **In MVP scope, not deferred** (decided 2026-09-01) | Moved out of "deferred" — see "Password protection" below |
| Audit log | Built in from day one | Generic `audit_log` table |
| Row Level Security (RLS) | Not enabled for MVP — app-layer authorization only | Service-role connection bypasses RLS anyway unless connection carries per-request user context |
| Primary keys | UUID for entity tables, `bigint identity` for high-volume append-only tables (`link_click_stats`, `audit_log`) | UUIDs avoid enumeration on externally-referenced resources |
| Analytics storage | Daily-granularity rollups only for MVP, generic dimension/value model | Not hourly |
| Unique-visitor counting | Deduplicated in **Redis**, not Postgres | Postgres never receives raw per-visitor data |

## Entity overview

```sql
-- Tenancy

team (
  id                uuid primary key default gen_random_uuid(),
  name              text not null,
  created_at        timestamptz not null default now()
)

team_member (
  team_id           uuid references team(id),
  user_id           uuid references auth.users(id),   -- Supabase-managed
  role              text not null check (role in ('owner','admin','editor','viewer')),
  created_at        timestamptz not null default now(),
  primary key (team_id, user_id)
)

-- Domains

domain (
  id                    uuid primary key default gen_random_uuid(),
  team_id               uuid not null references team(id),
  hostname              text not null unique,          -- globally unique (DNS is global)
  verification_status   text not null default 'pending' check (verification_status in ('pending','verified','failed')),
  vercel_domain_ref     text,                            -- opaque reference into Vercel's domain API state
  created_at            timestamptz not null default now(),
  verified_at           timestamptz
)

-- Organization

folder (
  id                uuid primary key default gen_random_uuid(),
  team_id           uuid not null references team(id),
  parent_folder_id  uuid references folder(id),          -- nullable; unused, see below
  name              text not null,
  created_at        timestamptz not null default now()
)

tag (
  id                uuid primary key default gen_random_uuid(),
  team_id           uuid not null references team(id),
  name              text not null,
  unique (team_id, name)
)

-- Links

link (
  id                    uuid primary key default gen_random_uuid(),
  domain_id             uuid not null references domain(id),
  team_id               uuid not null references team(id),  -- denormalized from domain_id, see note below
  slug                  text not null,
  destination_url       text not null,
  redirect_type         smallint not null default 302 check (redirect_type in (301,302)),
  state                 text not null default 'active' check (state in ('active','disabled','expired','flagged')),
  folder_id             uuid references folder(id),
  expires_at            timestamptz,
  password_hash         text,                              -- nullable; set = link requires a password. See "Password protection" below.
  analytics_enabled     boolean not null default true,     -- added 2026-09-02; see "Analytics opt-out" below
  created_by            uuid not null references auth.users(id),
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  -- QR rendering config: all nullable, backend applies defaults when generating
  qr_size               int,
  qr_error_correction   text check (qr_error_correction in ('low','medium','quartile','high')),
  qr_margin             int,
  qr_logo_url           text,
  qr_fg_color           text,
  qr_bg_color           text,
  unique (domain_id, slug)
)

link_tag (
  link_id           uuid references link(id) on delete cascade,
  tag_id            uuid references tag(id) on delete cascade,
  primary key (link_id, tag_id)
)

link_scan_result (
  id                uuid primary key default gen_random_uuid(),
  link_id           uuid not null references link(id) on delete cascade,
  provider          text not null default 'google_safe_browsing',
  verdict           text not null check (verdict in ('clean','flagged','error')),
  scanned_at        timestamptz not null default now(),
  raw_response      jsonb
)

-- Analytics (aggregated only — no raw click/event table)

link_click_stats (
  id                bigint generated always as identity primary key,
  link_id           uuid not null references link(id) on delete cascade,
  bucket_start      date not null,                      -- daily granularity for MVP
  dimension_type    text not null check (dimension_type in (
                      'total','browser','os','device','country',
                      'referrer','utm_source','bot_status','qr_vs_regular')),
  dimension_value   text,                                 -- null when dimension_type = 'total'
  clicks            bigint not null default 0,
  unique_visitors   bigint not null default 0,
  -- NULLS NOT DISTINCT is load-bearing, not a style choice: Postgres treats
  -- nulls as distinct in a unique constraint by default, so the
  -- dimension_type = 'total' row (whose dimension_value is null) would
  -- duplicate on every upsert instead of incrementing. Corrected 2026-09-02.
  unique nulls not distinct (link_id, bucket_start, dimension_type, dimension_value)
)

-- Audit

audit_log (
  id                bigint generated always as identity primary key,
  team_id           uuid references team(id),
  actor_user_id     uuid references auth.users(id),
  action            text not null,                       -- e.g. 'link.created', 'link.destination_changed', 'domain.added', 'member.role_changed'
  entity_type       text not null,
  entity_id         uuid,
  metadata          jsonb,
  created_at        timestamptz not null default now()
)
```

Notes:

- `auth.users` is Supabase-managed (created by the OAuth 2.1 Server flow, see `01-architecture.md`). A `public.profile` table synced from it via a Postgres trigger is standard Supabase practice if app-specific display fields (name, avatar) are needed beyond what `auth.users` carries — not included above since nothing currently requires it, easy to add later.
- `link.team_id` is denormalized from `link.domain_id → domain.team_id` rather than requiring a join for every authorization check in the Go backend (which does its own `WHERE team_id = ?` filtering, see "RLS" below). This assumes a link's domain never gets reassigned to a different team — reasonable given nothing in the feature list suggests that should be possible, but worth keeping in mind if that assumption ever changes.

  Amended 2026-09-03 (see `docs/superpowers/specs/2026-09-03-links-and-shared-domain-design.md`): `domain.team_id` is nullable, and a row with `team_id IS NULL` is the instance's shared hostname that every team may use. So `link.team_id` equals `domain.team_id` only for custom domains; on a shared domain it is simply the creating team. The invariant that matters is unchanged: `link.team_id` never moves, and no authorization check needs a join.

- **Folders are flat.** `parent_folder_id` is present but never written by the API. The original annotation above ("nullable, allows nesting") described the schema's capacity, not a requirement anyone had actually written down, and shipping a real tree would add four obligations none of which earn their cost yet: cycle prevention on every parent change (no constraint can express "this chain does not return to me"), a depth cap and the recursion it implies on every read, a recursion decision for `?folder_id=` filtering (whether it includes descendants), and delete semantics for a folder with children. The reasoning and the rejected alternative — dropping the column outright — are in `docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`. Keeping the column costs nothing and means nesting arrives later as an API change rather than a migration.

## Password protection

Decided 2026-09-01: this stays in MVP scope rather than being deferred, based on the concern that omitting it isn't neutral — it's a Core-tier item in the original feature list ("Optional password protection, expiration dates, and geotargeting" under Security & Privacy by Default), and a half-considered retrofit later is a worse security position than designing it properly now.

Schema: `link.password_hash` (nullable text) — `null` means the link is unprotected (the common case); set means a redirect must collect and verify a password before proceeding.

Implementation notes, since this is itself security-sensitive:

- **Hashing**: Argon2id, not a reversible scheme and not bcrypt-as-first-choice — Argon2id remains the current OWASP-recommended default for password hashing in 2026. Go's extended standard library (`golang.org/x/crypto/argon2`) covers this directly.
- **Brute-force protection**: the password-check endpoint needs its own, tighter rate limit than general API rate limiting (see `04-backend-architecture.md`) — scoped per-link (or per-link-per-IP), since a short or guessable password would otherwise be crackable quickly even with Argon2id's inherent slowness.
- **Audit log hygiene**: `audit_log.metadata` must never capture the plaintext password or the hash itself on a `link.password_set`/`link.password_changed` action — log that a password was set/changed/removed, not the value.
- **Redirect flow implication**: for a password-protected link, the redirect can no longer be an instant cache-hit-and-go — the visitor needs an interstitial page to enter the password first, verified against `password_hash` before the actual redirect happens. This is a real branch in the redirect flow described in `01-architecture.md`, worth reflecting there too rather than only here.

## Analytics opt-out (`link.analytics_enabled`)

Added 2026-09-02, during implementation. `01-architecture.md` requires that "analytics collection must be possible to disable per link or per account", but the schema above originally had no field for it. `link.analytics_enabled` (boolean, not null, default true) fills that gap: when false the redirect path records no click at all — no rollup row, and no entry in the Redis unique-visitor set.

Per-link satisfies the stated requirement ("per link _or_ per account"). A team-level switch can layer on top later without changing this column. It was added during the redirect-path work rather than deferred because the redirect handler is the only place that can honour it, and retrofitting would mean reopening the hot path plus a second migration.

## Analytics rollup design

Rather than one wide table with a column per possible browser/OS/country (impossible — those are open-ended sets) or one row per raw click (ruled out for privacy and storage reasons, see `01-architecture.md`), `link_click_stats` uses a generic `(link_id, day, dimension_type, dimension_value) → counts` shape. One row per link per day per distinct value actually seen for each dimension — so a quiet link on a quiet day produces very few rows (only `dimension_type = 'total'`), and row volume scales with real traffic, not with a link × day × dimension cartesian product.

Daily granularity, not hourly, for MVP: 90 days of daily buckets already gives a perfectly usable time-series chart (matches the "Time Series" requirement from the original feature list) at a fraction of the row volume hourly buckets would produce. Hourly is a straightforward later addition (same table shape, `bucket_start` just becomes a timestamp instead of a date) if finer resolution is ever wanted.

## Unique-visitor dedup (Redis, not Postgres)

To count a click as a "unique visitor" without ever storing a raw visitor identifier in Postgres: on each redirect, the backend computes the daily-rotating salted hash of IP + User-Agent (per `01-architecture.md`), then checks a Redis set like `uniq:{link_id}:{date}` for that hash. Not present → add it (TTL ~25h) and increment `unique_visitors` in the day's `link_click_stats` row; present → only increment `clicks`. Postgres only ever receives the resulting aggregate counts, never the hash itself — stronger for privacy than even a hashed-value column would be, and keeps Redis (already in the stack for caching) doing double duty rather than adding new infrastructure.

## Audit log

Generic rather than per-entity: one `audit_log` table with `action`/`entity_type`/`entity_id`/`metadata` covers link changes, domain additions, membership/role changes, and anything added later, instead of a separate history table per entity type. `metadata` (jsonb) carries the specifics of each action (e.g. old/new destination URL on a `link.destination_changed` event) — schemaless on purpose, since the shape of "what changed" differs per action type. See "Password protection" above for the one explicit exception to logging full before/after values.

Amended 2026-09-03: the example action `link.destination_changed` is superseded by `link.updated`. One PATCH can change several fields atomically, and one row per request keeps the log a faithful record of what was asked; which fields moved lives in `metadata.changed`.

Indexes: `(team_id, created_at desc)` for a team's activity feed, `(entity_type, entity_id)` for a specific entity's history.

## Row Level Security (RLS)

Not enabled for MVP. The reasoning is specific to this project's architecture, not a general RLS dismissal: since all data access is mediated by the Go API (no direct client-to-Supabase queries planned for links/domains/analytics — see the API-first principle in `01-architecture.md`), the backend will most likely connect using a service-role connection for simpler, better-pooled database access. A service-role connection bypasses RLS entirely by design, so enabling RLS without also re-architecting the connection to carry per-request user context (e.g. `SET request.jwt.claims` per request, which complicates connection pooling) would add setup cost for zero actual protection. Authorization is therefore the Go backend's responsibility, centrally, via `team_id` checks against the caller's role from `team_member`.

Worth revisiting if the project ever adds a code path with direct client-to-Supabase access (e.g. a future feature that queries Supabase straight from the frontend rather than through the Go API) — that path would need RLS to be meaningful, since it wouldn't pass through the Go backend's checks at all.

## Indexes (beyond what the constraints above already create)

- `link`: index on `team_id`; index on `created_by`; index on `state`; a `pg_trgm` GIN index on `slug` and `destination_url` for the "search and filter by URL, alias" requirement (substring/fuzzy matching that a plain B-tree index doesn't support well). `pg_trgm` is a standard Postgres extension, available on Supabase.
- `domain`: index on `team_id` (the `hostname` unique constraint already gives a lookup index).
- `team_member`: index on `user_id` (the composite primary key already indexes `team_id` first).

## Deferred — not built now, but the schema doesn't block adding them later

These remain out of the MVP schema — password protection was moved out of this list (see above), the rest stays deferred:

- Geotargeting: would add a `geotargeting_rules jsonb` column to `link`.
- Click-count-based expiration: would add a `max_clicks bigint` column to `link`, alongside the existing `expires_at`.
- Configurable query-parameter rules (accept/reject/override/add-fixed): would add a `query_param_rules jsonb` column to `link`.
- Link health monitoring (Healthy/Redirected/Broken/Unknown): would need its own `link_health_check` table (status, last_checked_at) plus a scheduled checker job.
- Link reporting and domain blocklist: would need `link_report` and `domain_blocklist` tables.

## Corrections applied during implementation (2026-09-02)

Recorded here so this doc stays the schema's source of truth rather than drifting from the migration:

- **`link.analytics_enabled`** added — see "Analytics opt-out" above.
- **`link_click_stats` uses `unique nulls not distinct`** — see the inline note above. Plain `unique` was a latent bug.
- **`link_click_stats.dimension_type` carries a `check` constraint** listing the nine permitted values, so a typo in application code fails the insert rather than silently creating a new dimension. The set must stay in sync with what the backend emits per click.
- **Explicit `on delete` clauses** were added to the foreign keys (`cascade` for the tenancy and link relationships, `set null` for `folder.parent_folder_id`, `link.folder_id` and the `audit_log` references). The sketch above left them implicit.
- **GeoIP** resolves from Vercel's `x-vercel-ip-country` request header rather than a bundled database — see `01-architecture.md`.

## Not yet decided / to revisit

- Whether `public.profile` (synced from `auth.users`) is needed now or can wait until the frontend actually needs app-specific display fields.
- Exact set of `action` values for `audit_log` — will fall out naturally once the API endpoints (next planning topic) are defined, since each mutating endpoint should log one.
- Reflect the password-protected-link redirect-flow branch in `01-architecture.md`'s core redirect flow description (currently only describes the cache-hit/cache-miss happy path).
