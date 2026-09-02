# Foundation & Redirect Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the Go API skeleton, the full database schema, and a working, rate-limited, analytics-recording `GET /<slug>` redirect path — the architectural spine every later feature hangs off.

**Architecture:** One Go binary (`apps/api`) serving two hostname-scoped surfaces from a single chi mux: the Huma-registered `/v1` JSON API (Bearer/JWKS authenticated) on the API hostname, and the unversioned, framework-free public redirect surface (`GET /{slug}`, `GET|POST /{slug}/verify`) on every other hostname. The redirect path reads a Redis-cached link record, falls back to Postgres on a miss, and records the click into an in-process aggregation buffer that flushes daily rollups to Postgres out of band — the response never waits on analytics.

**Tech Stack:** Go 1.27 · chi v5 · Huma v2 · pgx/v5 + sqlc · go-redis v9 · golang-jwt/jwt v5 + MicahParks/keyfunc v3 · golang.org/x/crypto/argon2 · Supabase CLI (local stack) · testcontainers-go + testify

**Spec:** `docs/planning/01-architecture.md`, `04-backend-architecture.md`, `05-database-schema.md`, `06-api-design.md`, `07-repo-structure-and-tooling.md` (and `CLAUDE.md`, which is the condensed load-bearing version of all of them)

## Global Constraints

Every task's requirements implicitly include this section.

- **The tenant is called `team`** in every identifier — tables, columns, Go types, API paths. `verein` never appears in an identifier; "Verein" appears only in user-facing German copy.
- **`GET /<slug>` must never wait on anything optional.** Click recording is always async/non-blocking. Any change to this path gets checked against that rule.
- **There is no RLS.** Postgres enforces nothing about tenancy. Every query path filters by `team_id` in Go. A query without a tenancy filter is a data-leak bug. (Plan 1's redirect queries are hostname+slug-scoped and read-only, which is the one legitimate exception — a redirect is public by definition.)
- **Never store a full IP address, ever.** Unique visitors are counted via a daily-rotating salted hash of IP+UA, deduplicated in Redis. Postgres receives aggregate counts only — never the hash, never a raw click row.
- **No hardcoded user-facing string**, not even temporarily. English is default, German ships alongside it. This applies to the server-rendered redirect-surface pages too (`internal/pages`).
- **Security items are MVP scope**: rate limiting and Argon2id password protection ship in this plan. HTTPS-only URL scheme allowlist, SSRF protection with DNS-rebinding re-checks, and async Safe Browsing scanning attach to link _creation_, which lives in plan 2 — they are not deferred, they are in the next plan by dependency, and plan 2 must not start without them.
- **Free-tier limits are a design constraint.** Upstash Redis binds first at 500K commands/month (~16.7K/day). This plan's redirect path costs **2 Redis commands on a cache hit** (rate-limit EVAL + lookup EVAL), giving ~8,300 redirects/day of headroom. See "Redis command budget" below.
- **Go module path:** `github.com/mheob/kurze-url/apps/api`. `apps/api` and `apps/cli` are separate modules, no `go.work`.
- **Migrations are Supabase CLI-owned.** `supabase migration new`, `supabase db push`. No golang-migrate, no Atlas.
- **Errors on `/v1`:** Huma's default RFC 9457 `application/problem+json`. No custom error model.
- **Commits:** Conventional Commits. Use the `create-commit` skill for every commit step in this plan (it routes through GitButler); the raw `git commit` lines shown in steps are the intent, not the command to type.

### Redis command budget

| Path | Commands | Note |
| --- | --- | --- |
| Redirect, cache hit | 2 | `ratelimit.lua` EVAL + `redirect_lookup.lua` EVAL (the latter folds `GET` + unique-visitor `SADD`/`EXPIRE` into one) |
| Redirect, cache miss, link found | 4–5 | \+ `SET` to populate, \+ `SADD` for the visitor the script could not dedup without a link id (\+ `EXPIRE` when that visitor is new) |
| Redirect, cache miss, no such link | 3 | \+ negative-cache `SET` |
| Redirect, rate-limited | 1 | short-circuits |
| Password verify POST | 1–3 | tighter rate-limit EVAL, then `SADD` (\+ `EXPIRE`) only on success — there is no lookup EVAL, because the hash is deliberately never cached and is read from Postgres |

**The headroom figure rests on one unverified assumption**: that Upstash bills a single `EVAL` as one command regardless of how many Redis operations the script performs internally. That is the standard understanding and it is why Upstash's own rate-limit SDK is Lua-based, but it is not confirmed against Upstash's billing documentation, and `CLAUDE.md` treats this number as a design constraint. If Upstash instead counts the commands inside the script, a cache-hit redirect costs 6–7 rather than 2 and the daily headroom falls from ~8,300 to ~2,400. **Confirm this before the instance opens to real Vereine**, and correct this table and `docs/planning/02-external-services-and-hosting.md` together.

The two EVALs are deliberately **not** merged into one. Merging would halve command cost (~16.7K redirects/day instead of ~8.3K) but duplicates the sliding-window algorithm into the lookup script. 8.3K redirects/day is ample for the expected handful of small Vereine; merging is the documented lever if the Upstash 70% warning threshold (350K/month) is ever crossed.

### Two spec corrections this plan makes

1. **`link_click_stats` unique constraint must be `UNIQUE NULLS NOT DISTINCT`.** Doc 05 specifies `dimension_value` is `null` when `dimension_type = 'total'`, and `unique (link_id, bucket_start, dimension_type, dimension_value)`. In Postgres, NULLs are distinct in a unique constraint by default, so the `total` row would duplicate on every upsert instead of incrementing. `NULLS NOT DISTINCT` (PG15+, Supabase runs PG15/17) fixes it. Task 2 applies this.
2. **GeoIP comes from Vercel's `x-vercel-ip-country` request header**, not a bundled MaxMind database. Doc 01 says "GeoIP resolved locally, no third-party geolocation API call per request" — the Vercel header satisfies the intent (no per-request API call, no third party) without shipping and updating a geo database. Task 7 applies this; the value is `unknown` off-Vercel.
3. **`link` needs an `analytics_enabled` column.** Doc 01 requires that "analytics collection must be possible to disable per link or per account", but doc 05's schema has no such field. It is added here rather than later because the redirect hot path is the only place that can honour it, and retrofitting it would mean reopening that path plus a migration. Per-link satisfies the requirement; a team-level switch can layer on top later. Tasks 2, 3, 5, 10 and 11 apply this.

---

## File Structure

```
apps/api/
  go.mod                                  module github.com/mheob/kurze-url/apps/api
  sqlc.yaml                               sqlc config; reads the auth stub + supabase/migrations
  vercel.json                             Vercel Go Framework Preset config
  .env.example                            documented, valueless
  cmd/api/main.go                         wiring + graceful shutdown; the only place deps are constructed
  internal/config/config.go               env -> Config, with required-var validation
  internal/db/schema/0000_auth_stub.sql   sqlc-only stub of Supabase's auth.users (never applied to a real DB)
  internal/db/queries/link.sql            redirect + verify lookups
  internal/db/queries/click_stats.sql     rollup upsert
  internal/db/*.go                        sqlc-generated (do not hand-edit)
  internal/cache/client.go                go-redis wrapper: link cache, unique dedup, rate limit
  internal/cache/lua/ratelimit.lua        sliding-window counter
  internal/cache/lua/redirect_lookup.lua  cache GET + conditional unique-visitor SADD
  internal/link/cached.go                 the cached link record shared by cache + handlers
  internal/analytics/visitor.go           daily-rotating salted visitor hash
  internal/analytics/dimensions.go        request -> rollup dimensions
  internal/analytics/recorder.go          in-process aggregation buffer + flush loop
  internal/auth/password.go               Argon2id hash/verify (PHC-encoded)
  internal/auth/jwt.go                    JWKS fetch/cache + ES256 verification
  internal/pages/pages.go                 EN/DE strings + Accept-Language negotiation
  internal/pages/templates/*.html         server-rendered error + password pages
  internal/api/router.go                  hostname dispatch; builds both surfaces
  internal/api/redirect.go                GET /{slug}
  internal/api/verify.go                  GET|POST /{slug}/verify
  internal/api/v1.go                      Huma setup, bearerAuth scheme, GET /v1/me
  internal/api/middleware.go              real-client-IP, request logging, rate-limit helper
supabase/
  config.toml                             supabase init output
  migrations/<ts>_initial_schema.sql      the whole doc-05 schema, one migration
  seed.sql                                local-dev only: a team, a verified localhost domain, links
```

Each file owns one responsibility. `internal/api` owns HTTP shape only — no SQL, no Redis commands inline; those belong to `internal/db` and `internal/cache`.

---

## Task 1: Go module, config, and a served health endpoint

**Files:**

- Create: `apps/api/go.mod`
- Create: `apps/api/.golangci.yml`
- Create: `apps/api/internal/config/config.go`
- Create: `apps/api/cmd/api/main.go`
- Test: `apps/api/internal/config/config_test.go`

**Interfaces:**

- Consumes: nothing (first task)
- Produces: `config.Config` struct and `config.Load() (Config, error)`. Every later task reads its settings from this struct — field names below are load-bearing.

- [ ] **Step 1: Initialise the module and add the first dependencies**

```bash
cd apps/api
rm -f .gitkeep
go mod init github.com/mheob/kurze-url/apps/api
go get github.com/go-chi/chi/v5
go get github.com/stretchr/testify
go mod tidy
```

- [ ] **Step 2: Configure the linter**

Later tasks run `golangci-lint run`, which needs a ruleset. Create `apps/api/.golangci.yml` (schema v2, matching golangci-lint 2.x):

```yaml
version: '2'

linters:
  default: standard
  enable:
    - bodyclose
    - errorlint
    - misspell
    - nilerr
    - revive
    - sqlclosecheck
    - unconvert
  exclusions:
    rules:
      # Generated by sqlc; not ours to lint.
      - path: internal/db/
        linters:
          - revive
          - unused

formatters:
  enable:
    - gofmt
    - goimports
```

Verify it parses: `cd apps/api && golangci-lint config verify`

- [ ] **Step 3: Write the failing config test**

Create `apps/api/internal/config/config_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/config"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost:54322/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "test-salt")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "8080", cfg.Port)
	require.Equal(t, "localhost", cfg.APIHostname)
	require.Equal(t, 60, cfg.RedirectRateLimitPerMin)
	require.Equal(t, 5, cfg.PasswordRateLimitPerMin)
	require.Equal(t, 20, cfg.LinkCreateRateLimitPerMin)
	require.Equal(t, time.Hour, cfg.LinkCacheTTL)
	require.Equal(t, time.Minute, cfg.NotFoundCacheTTL)
	require.Equal(t, 25*time.Hour, cfg.UniqueVisitorTTL)
}

func TestLoadOverridesFromEnv(t *testing.T) {
	setRequired(t)
	t.Setenv("PORT", "3000")
	t.Setenv("API_HOSTNAME", "api.kurze.url")
	t.Setenv("RATE_LIMIT_REDIRECT_PER_MIN", "120")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "3000", cfg.Port)
	require.Equal(t, "api.kurze.url", cfg.APIHostname)
	require.Equal(t, 120, cfg.RedirectRateLimitPerMin)
}

func TestLoadRejectsMissingRequiredVar(t *testing.T) {
	setRequired(t)
	t.Setenv("VISITOR_SALT", "")

	_, err := config.Load()

	require.ErrorContains(t, err, "VISITOR_SALT")
}

func TestLoadRejectsNonNumericRateLimit(t *testing.T) {
	setRequired(t)
	t.Setenv("RATE_LIMIT_REDIRECT_PER_MIN", "many")

	_, err := config.Load()

	require.ErrorContains(t, err, "RATE_LIMIT_REDIRECT_PER_MIN")
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/config/...` Expected: FAIL — `no required module provides package .../internal/config`

- [ ] **Step 5: Implement the config package**

Create `apps/api/internal/config/config.go`:

```go
// Package config loads the API's runtime settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every runtime setting the API needs. It is loaded once at
// startup and passed by value; nothing mutates it afterwards.
type Config struct {
	Port        string
	DatabaseURL string
	RedisURL    string

	// APIHostname is the single hostname that serves /v1. Every other Host
	// header is treated as a short-link domain and routed to the redirect
	// surface.
	APIHostname string

	JWKSURL     string
	JWTIssuer   string
	JWTAudience string

	// VisitorSalt is the secret keying the daily-rotating visitor hash. It is
	// never logged and never leaves the process.
	VisitorSalt string

	RedirectRateLimitPerMin   int
	PasswordRateLimitPerMin   int
	LinkCreateRateLimitPerMin int

	LinkCacheTTL     time.Duration
	NotFoundCacheTTL time.Duration
	UniqueVisitorTTL time.Duration
}

// Load reads the environment and validates that the required settings are
// present. JWKS settings are optional here: the API starts without them and
// only /v1 operations that declare bearerAuth fail, which keeps the redirect
// surface runnable in local development with no Supabase project.
func Load() (Config, error) {
	cfg := Config{
		Port:        env("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		APIHostname: env("API_HOSTNAME", "localhost"),
		JWKSURL:     os.Getenv("SUPABASE_JWKS_URL"),
		JWTIssuer:   os.Getenv("SUPABASE_JWT_ISSUER"),
		JWTAudience: env("SUPABASE_JWT_AUDIENCE", "authenticated"),
		VisitorSalt: os.Getenv("VISITOR_SALT"),

		LinkCacheTTL:     time.Hour,
		NotFoundCacheTTL: time.Minute,
		UniqueVisitorTTL: 25 * time.Hour,
	}

	for _, required := range []struct {
		name  string
		value string
	}{
		{"DATABASE_URL", cfg.DatabaseURL},
		{"REDIS_URL", cfg.RedisURL},
		{"VISITOR_SALT", cfg.VisitorSalt},
	} {
		if required.value == "" {
			return Config{}, fmt.Errorf("config: %s is required", required.name)
		}
	}

	var err error
	if cfg.RedirectRateLimitPerMin, err = envInt("RATE_LIMIT_REDIRECT_PER_MIN", 60); err != nil {
		return Config{}, err
	}
	if cfg.PasswordRateLimitPerMin, err = envInt("RATE_LIMIT_PASSWORD_PER_MIN", 5); err != nil {
		return Config{}, err
	}
	if cfg.LinkCreateRateLimitPerMin, err = envInt("RATE_LIMIT_LINK_CREATE_PER_MIN", 20); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", name, err)
	}
	return v, nil
}
```

- [ ] **Step 6: Run the config test to verify it passes**

Run: `cd apps/api && go test ./internal/config/...` Expected: PASS (4 tests)

- [ ] **Step 7: Write the failing health-endpoint test**

Create `apps/api/cmd/api/main_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthHandlerReportsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	healthHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ok", body["status"])
}
```

- [ ] **Step 8: Run it to verify it fails**

Run: `cd apps/api && go test ./cmd/api/...` Expected: FAIL — `undefined: healthHandler`

- [ ] **Step 9: Write a minimal main.go with the health handler**

Create `apps/api/cmd/api/main.go`. This is a deliberately thin first version; Task 12 replaces the body of `run` with the full wiring.

```go
// Command api is the single entrypoint Vercel's Go Framework Preset detects.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /health", healthHandler())

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}
```

- [ ] **Step 10: Run all tests and vet**

Run: `cd apps/api && go vet ./... && go test ./...` Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add apps/api
git commit -m "feat(api): add Go module, config loader and health endpoint"
```

---

## Task 2: Database schema migration on the local Supabase stack

**Files:**

- Create: `supabase/config.toml` (generated by `supabase init`)
- Create: `supabase/migrations/<timestamp>_initial_schema.sql`
- Create: `supabase/seed.sql`
- Test: `apps/api/internal/db/schema_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks
- Produces: the physical schema every later task queries. Table and column names are exactly those in `docs/planning/05-database-schema.md`. Local Postgres is reachable at `postgres://postgres:postgres@127.0.0.1:54322/postgres` once `supabase start` is running.

- [ ] **Step 1: Install the Supabase CLI and initialise the project**

```bash
brew install supabase/tap/supabase
supabase init          # run from the repo root; writes supabase/config.toml
supabase start         # boots Postgres + auth in Docker; prints the local URLs
```

If `supabase init` complains that `supabase/` already exists, that is fine — it only adds `config.toml`.

- [ ] **Step 2: Create the migration file**

```bash
supabase migration new initial_schema
```

- [ ] **Step 3: Write the schema into the generated migration file**

Open the file `supabase migration new` just created under `supabase/migrations/` and write:

```sql
-- Initial schema for the multi-tenant URL shortener.
-- Tenant unit is a "team" (= one participating Verein). No RLS: authorization
-- is enforced entirely in the Go API, which connects with a service role.

create extension if not exists pg_trgm;

-- Tenancy -------------------------------------------------------------------

create table team (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  created_at  timestamptz not null default now()
);

create table team_member (
  team_id     uuid not null references team (id) on delete cascade,
  user_id     uuid not null references auth.users (id) on delete cascade,
  role        text not null check (role in ('owner', 'admin', 'editor', 'viewer')),
  created_at  timestamptz not null default now(),
  primary key (team_id, user_id)
);

create index team_member_user_id_idx on team_member (user_id);

-- Domains -------------------------------------------------------------------

create table domain (
  id                   uuid primary key default gen_random_uuid(),
  team_id              uuid not null references team (id) on delete cascade,
  hostname             text not null unique,
  verification_status  text not null default 'pending'
                         check (verification_status in ('pending', 'verified', 'failed')),
  vercel_domain_ref    text,
  created_at           timestamptz not null default now(),
  verified_at          timestamptz
);

create index domain_team_id_idx on domain (team_id);

-- Organization --------------------------------------------------------------

create table folder (
  id                uuid primary key default gen_random_uuid(),
  team_id           uuid not null references team (id) on delete cascade,
  parent_folder_id  uuid references folder (id) on delete set null,
  name              text not null,
  created_at        timestamptz not null default now()
);

create index folder_team_id_idx on folder (team_id);

create table tag (
  id       uuid primary key default gen_random_uuid(),
  team_id  uuid not null references team (id) on delete cascade,
  name     text not null,
  unique (team_id, name)
);

-- Links ---------------------------------------------------------------------

create table link (
  id                   uuid primary key default gen_random_uuid(),
  domain_id            uuid not null references domain (id) on delete cascade,
  -- Denormalized from domain.team_id so every authorization check avoids a
  -- join. Assumes a link's domain is never reassigned to another team.
  team_id              uuid not null references team (id) on delete cascade,
  slug                 text not null,
  destination_url      text not null,
  redirect_type        smallint not null default 302 check (redirect_type in (301, 302)),
  state                text not null default 'active'
                         check (state in ('active', 'disabled', 'expired', 'flagged')),
  folder_id            uuid references folder (id) on delete set null,
  expires_at           timestamptz,
  -- Argon2id, PHC-encoded. Null means the link is unprotected.
  password_hash        text,
  -- When false the redirect path records no click at all for this link.
  analytics_enabled    boolean not null default true,
  created_by           uuid not null references auth.users (id),
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  qr_size              int,
  qr_error_correction  text check (qr_error_correction in ('low', 'medium', 'quartile', 'high')),
  qr_margin            int,
  qr_logo_url          text,
  qr_fg_color          text,
  qr_bg_color          text,
  unique (domain_id, slug)
);

create index link_team_id_idx on link (team_id);
create index link_created_by_idx on link (created_by);
create index link_state_idx on link (state);
create index link_slug_trgm_idx on link using gin (slug gin_trgm_ops);
create index link_destination_url_trgm_idx on link using gin (destination_url gin_trgm_ops);

create table link_tag (
  link_id  uuid not null references link (id) on delete cascade,
  tag_id   uuid not null references tag (id) on delete cascade,
  primary key (link_id, tag_id)
);

create table link_scan_result (
  id            uuid primary key default gen_random_uuid(),
  link_id       uuid not null references link (id) on delete cascade,
  provider      text not null default 'google_safe_browsing',
  verdict       text not null check (verdict in ('clean', 'flagged', 'error')),
  scanned_at    timestamptz not null default now(),
  raw_response  jsonb
);

create index link_scan_result_link_id_scanned_at_idx
  on link_scan_result (link_id, scanned_at desc);

-- Analytics (aggregated only — no raw click table exists, and none should be
-- added). One row per link per day per distinct value seen for a dimension.

create table link_click_stats (
  id               bigint generated always as identity primary key,
  link_id          uuid not null references link (id) on delete cascade,
  bucket_start     date not null,
  dimension_type   text not null check (dimension_type in (
                     'total', 'browser', 'os', 'device', 'country',
                     'referrer', 'utm_source', 'bot_status', 'qr_vs_regular')),
  -- Null exactly when dimension_type = 'total'. NULLS NOT DISTINCT is what
  -- makes the upsert increment that row instead of inserting a duplicate.
  dimension_value  text,
  clicks           bigint not null default 0,
  unique_visitors  bigint not null default 0,
  unique nulls not distinct (link_id, bucket_start, dimension_type, dimension_value)
);

create index link_click_stats_link_id_bucket_start_idx
  on link_click_stats (link_id, bucket_start desc);

-- Audit ---------------------------------------------------------------------

create table audit_log (
  id             bigint generated always as identity primary key,
  team_id        uuid references team (id) on delete set null,
  actor_user_id  uuid references auth.users (id) on delete set null,
  action         text not null,
  entity_type    text not null,
  entity_id      uuid,
  -- Never carries a plaintext password or a password hash.
  metadata       jsonb,
  created_at     timestamptz not null default now()
);

create index audit_log_team_id_created_at_idx on audit_log (team_id, created_at desc);
create index audit_log_entity_idx on audit_log (entity_type, entity_id);
```

- [ ] **Step 4: Apply the migration locally and verify it succeeds**

```bash
supabase db reset      # drops, recreates, replays every migration, then runs seed.sql
```

Expected: no errors; the command finishes with `Finished supabase db reset.`

- [ ] **Step 5: Write the local-dev seed file**

Create `supabase/seed.sql`. It is applied by `supabase db reset` and is local-only — never `db push`ed.

```sql
-- Local development seed. Never applied to the hosted project.
-- Gives the redirect path something to resolve without going through the API.

insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
                        email_confirmed_at, created_at, updated_at)
values ('00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-000000000000',
        'authenticated', 'authenticated', 'dev@example.test',
        '', now(), now(), now())
on conflict (id) do nothing;

insert into team (id, name)
values ('00000000-0000-0000-0000-0000000000b1', 'Dev Verein')
on conflict (id) do nothing;

insert into team_member (team_id, user_id, role)
values ('00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-0000000000a1', 'owner')
on conflict do nothing;

insert into domain (id, team_id, hostname, verification_status, verified_at)
values ('00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'short.test', 'verified', now())
on conflict (hostname) do nothing;

insert into link (id, domain_id, team_id, slug, destination_url, created_by)
values ('00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'hello', 'https://example.org/hello',
        '00000000-0000-0000-0000-0000000000a1')
on conflict (domain_id, slug) do nothing;
```

- [ ] **Step 6: Write a failing schema test**

This test pins the two behaviours the schema must have that are easy to lose in a later migration: domain-scoped slug uniqueness, and the `total` rollup row incrementing rather than duplicating.

Create `apps/api/internal/db/schema_test.go`:

```go
package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// testPool connects to the local Supabase Postgres. Tests skip with a clear
// message when it is not running, so `go test ./...` stays usable offline.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func TestSlugIsUniquePerDomainNotGlobally(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var teamID, userID string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('t') returning id`).Scan(&teamID))
	require.NoError(t, tx.QueryRow(ctx,
		`select id from auth.users limit 1`).Scan(&userID))

	var domainA, domainB string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'a.test') returning id`,
		teamID).Scan(&domainA))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'b.test') returning id`,
		teamID).Scan(&domainB))

	insert := `insert into link (domain_id, team_id, slug, destination_url, created_by)
	           values ($1, $2, 'dup', 'https://example.org', $3)`

	_, err = tx.Exec(ctx, insert, domainA, teamID, userID)
	require.NoError(t, err, "same slug on the first domain")

	_, err = tx.Exec(ctx, insert, domainB, teamID, userID)
	require.NoError(t, err, "same slug on a different domain must be allowed")

	_, err = tx.Exec(ctx, insert, domainA, teamID, userID)
	require.Error(t, err, "same slug twice on the same domain must be rejected")
}

func TestTotalRollupRowIncrementsInsteadOfDuplicating(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var teamID, userID, domainID, linkID string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('t') returning id`).Scan(&teamID))
	require.NoError(t, tx.QueryRow(ctx,
		`select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'c.test') returning id`,
		teamID).Scan(&domainID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 's', 'https://example.org', $3) returning id`,
		domainID, teamID, userID).Scan(&linkID))

	upsert := `insert into link_click_stats
	             (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
	           values ($1, current_date, 'total', null, 1, 1)
	           on conflict (link_id, bucket_start, dimension_type, dimension_value)
	           do update set clicks = link_click_stats.clicks + excluded.clicks,
	                         unique_visitors = link_click_stats.unique_visitors + excluded.unique_visitors`

	_, err = tx.Exec(ctx, upsert, linkID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, upsert, linkID)
	require.NoError(t, err)

	var rows, clicks int
	require.NoError(t, tx.QueryRow(ctx,
		`select count(*), coalesce(sum(clicks), 0) from link_click_stats
		 where link_id = $1 and dimension_type = 'total'`, linkID).Scan(&rows, &clicks))

	require.Equal(t, 1, rows, "the total row must not duplicate — needs UNIQUE NULLS NOT DISTINCT")
	require.Equal(t, 2, clicks)
}
```

- [ ] **Step 7: Add the pgx dependency and run the test**

```bash
cd apps/api
go get github.com/jackc/pgx/v5
go mod tidy
go test ./internal/db/...
```

Expected: PASS (2 tests). If they skip, `supabase start` is not running. If `TestTotalRollupRowIncrementsInsteadOfDuplicating` fails with `2 rows`, the `nulls not distinct` clause is missing from the migration.

- [ ] **Step 8: Commit**

```bash
git add supabase apps/api
git commit -m "feat(db): add initial schema migration, local seed and schema tests"
```

---

## Task 3: sqlc setup and the redirect queries

**Files:**

- Create: `apps/api/sqlc.yaml`
- Create: `apps/api/internal/db/schema/0000_auth_stub.sql`
- Create: `apps/api/internal/db/queries/link.sql`
- Create: `apps/api/internal/db/queries/click_stats.sql`
- Create (generated): `apps/api/internal/db/db.go`, `models.go`, `link.sql.go`, `click_stats.sql.go`, `batch.go`
- Test: `apps/api/internal/db/queries_test.go`

**Interfaces:**

- Consumes: the schema from Task 2.
- Produces:
  - `db.New(pool *pgxpool.Pool) *db.Queries`
  - `(*db.Queries).GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{Hostname string, Slug string}) (db.GetLinkForRedirectRow, error)` — row fields: `ID uuid.UUID`, `TeamID uuid.UUID`, `DestinationURL string`, `RedirectType int16`, `State string`, `ExpiresAt *time.Time`, `AnalyticsEnabled bool`, `HasPassword bool`
  - `(*db.Queries).GetLinkForVerify(ctx, db.GetLinkForVerifyParams{Hostname, Slug string}) (db.GetLinkForVerifyRow, error)` — same fields but `PasswordHash *string` in place of `HasPassword`
  - `(*db.Queries).UpsertClickStats(ctx, []db.UpsertClickStatsParams) *db.UpsertClickStatsBatchResults` — params fields: `LinkID uuid.UUID`, `BucketStart time.Time`, `DimensionType string`, `DimensionValue *string`, `Clicks int64`, `UniqueVisitors int64`

- [ ] **Step 1: Install sqlc**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
sqlc version
```

- [ ] **Step 2: Write the auth stub sqlc needs**

sqlc parses the migration files to learn the schema, but `auth.users` is created by Supabase itself and appears in no migration — the foreign keys to it would fail to resolve. This file exists **only** for sqlc's parser and is never applied to a database.

Create `apps/api/internal/db/schema/0000_auth_stub.sql`:

```sql
-- Not a migration. sqlc reads this so the foreign keys to Supabase's
-- auth.users resolve during code generation. Supabase owns the real table;
-- this file is never applied to any database.
create schema if not exists auth;

create table auth.users (
  id uuid primary key
);
```

- [ ] **Step 3: Write sqlc.yaml**

Create `apps/api/sqlc.yaml`:

```yaml
version: '2'
sql:
  - engine: postgresql
    schema:
      - internal/db/schema
      - ../../supabase/migrations
    queries: internal/db/queries
    gen:
      go:
        package: db
        out: internal/db
        sql_package: pgx/v5
        emit_json_tags: false
        emit_pointers_for_null_types: true
        emit_empty_slices: true
        # sqlc's initialism handling produces DestinationUrl, not
        # DestinationURL. Rename rather than let every call site carry the
        # inconsistency.
        rename:
          destination_url: DestinationURL
          qr_logo_url: QRLogoURL
        overrides:
          - db_type: uuid
            go_type: github.com/google/uuid.UUID
          - db_type: uuid
            nullable: true
            go_type:
              import: github.com/google/uuid
              type: UUID
              pointer: true
          # Without these, sqlc's pgx/v5 mapping yields pgtype.Date and the
          # rollup's bucket_start would not be a time.Time.
          - db_type: date
            go_type: time.Time
          - db_type: date
            nullable: true
            go_type:
              type: time.Time
              pointer: true
          # Likewise: without these, a nullable timestamptz becomes
          # pgtype.Timestamptz and will not assign to link.Cached.ExpiresAt.
          - db_type: timestamptz
            go_type: time.Time
          - db_type: timestamptz
            nullable: true
            go_type:
              type: time.Time
              pointer: true
```

- [ ] **Step 4: Write the link queries**

Create `apps/api/internal/db/queries/link.sql`:

```sql
-- Redirect-path lookup. Only verified domains resolve: an unverified domain
-- must not serve links, or a team could claim a hostname it does not own.
-- Note password_hash is projected as a boolean here so the hash never leaves
-- the database on the hot path.

-- name: GetLinkForRedirect :one
select
  l.id,
  l.team_id,
  l.destination_url,
  l.redirect_type,
  l.state,
  l.expires_at,
  l.analytics_enabled,
  -- The cast is required: without it sqlc cannot infer the type of a bare
  -- boolean expression and generates interface{}.
  (l.password_hash is not null)::boolean as has_password
from link l
join domain d on d.id = l.domain_id
where d.hostname = $1
  and l.slug = $2
  and d.verification_status = 'verified';

-- name: GetLinkForVerify :one
select
  l.id,
  l.team_id,
  l.destination_url,
  l.redirect_type,
  l.state,
  l.expires_at,
  l.analytics_enabled,
  l.password_hash
from link l
join domain d on d.id = l.domain_id
where d.hostname = $1
  and l.slug = $2
  and d.verification_status = 'verified';
```

- [ ] **Step 5: Write the click-stats upsert**

Create `apps/api/internal/db/queries/click_stats.sql`:

```sql
-- Batched rollup upsert. The unique constraint is NULLS NOT DISTINCT, which
-- is what lets the dimension_type = 'total' row (dimension_value is null)
-- increment rather than duplicate.

-- name: UpsertClickStats :batchexec
insert into link_click_stats
  (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
values ($1, $2, $3, $4, $5, $6)
on conflict (link_id, bucket_start, dimension_type, dimension_value)
do update set
  clicks = link_click_stats.clicks + excluded.clicks,
  unique_visitors = link_click_stats.unique_visitors + excluded.unique_visitors;
```

- [ ] **Step 6: Generate and verify the code compiles**

```bash
cd apps/api
sqlc generate
go get github.com/google/uuid
go mod tidy
go build ./...
```

Expected: `sqlc generate` prints nothing and writes `internal/db/*.go`; `go build` succeeds.

- [ ] **Step 7: Write the failing query test**

Create `apps/api/internal/db/queries_test.go`:

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

func TestGetLinkForRedirectResolvesVerifiedDomainOnly(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	q := db.New(pool)

	row, err := q.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: "short.test",
		Slug:     "hello",
	})

	require.NoError(t, err, "the seeded link on the verified domain must resolve")
	require.Equal(t, "https://example.org/hello", row.DestinationURL)
	require.EqualValues(t, 302, row.RedirectType)
	require.Equal(t, "active", row.State)
	require.False(t, row.HasPassword)
	require.True(t, row.AnalyticsEnabled)
	require.Nil(t, row.ExpiresAt)
}

func TestGetLinkForRedirectMissesUnknownSlug(t *testing.T) {
	ctx := context.Background()
	q := db.New(testPool(t))

	_, err := q.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: "short.test",
		Slug:     "does-not-exist",
	})

	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestUpsertClickStatsAccumulatesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	q := db.New(pool)

	linkID := uuid.MustParse("00000000-0000-0000-0000-0000000000d1")
	day := time.Now().UTC().Truncate(24 * time.Hour)
	firefox := "Firefox"

	params := []db.UpsertClickStatsParams{
		{LinkID: linkID, BucketStart: day, DimensionType: "total", DimensionValue: nil, Clicks: 2, UniqueVisitors: 1},
		{LinkID: linkID, BucketStart: day, DimensionType: "browser", DimensionValue: &firefox, Clicks: 2, UniqueVisitors: 1},
	}

	require.NoError(t, execBatch(ctx, q, params))
	require.NoError(t, execBatch(ctx, q, params))

	var totalClicks, totalUnique int64
	require.NoError(t, pool.QueryRow(ctx,
		`select clicks, unique_visitors from link_click_stats
		 where link_id = $1 and bucket_start = $2 and dimension_type = 'total'`,
		linkID, day).Scan(&totalClicks, &totalUnique))

	require.EqualValues(t, 4, totalClicks)
	require.EqualValues(t, 2, totalUnique)

	// Leave the table as we found it so reruns stay deterministic.
	_, err := pool.Exec(ctx, `delete from link_click_stats where link_id = $1`, linkID)
	require.NoError(t, err)
}

func execBatch(ctx context.Context, q *db.Queries, params []db.UpsertClickStatsParams) error {
	var execErr error
	results := q.UpsertClickStats(ctx, params)
	results.Exec(func(_ int, err error) {
		if err != nil && execErr == nil {
			execErr = err
		}
	})
	if err := results.Close(); err != nil && execErr == nil {
		execErr = err
	}
	return execErr
}
```

- [ ] **Step 8: Run the query tests**

Run: `cd apps/api && go test ./internal/db/...` Expected: PASS (5 tests total, including Task 2's two)

- [ ] **Step 9: Commit**

```bash
git add apps/api
git commit -m "feat(db): generate sqlc queries for the redirect path and click rollups"
```

---

## Task 4: Redis client and the sliding-window rate limiter

**Files:**

- Create: `apps/api/internal/cache/client.go`
- Create: `apps/api/internal/cache/lua/ratelimit.lua`
- Test: `apps/api/internal/cache/ratelimit_test.go`
- Test: `apps/api/internal/cache/testhelper_test.go`

**Interfaces:**

- Consumes: `config.Config.RedisURL`
- Produces:
  - `cache.New(redisURL string) (*cache.Client, error)`
  - `(*cache.Client).Close() error`
  - `(*cache.Client).Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, err error)`

- [ ] **Step 1: Add dependencies**

```bash
cd apps/api
go get github.com/redis/go-redis/v9
go get github.com/testcontainers/testcontainers-go
go get github.com/testcontainers/testcontainers-go/modules/redis
go mod tidy
```

- [ ] **Step 2: Write the Redis test helper**

Create `apps/api/internal/cache/testhelper_test.go`:

```go
package cache_test

import (
	"context"
	"testing"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/cache"
)

// newTestClient starts a throwaway Redis container and returns a client
// pointed at it. Skips when Docker is unavailable so the suite stays usable
// on a machine without it.
func newTestClient(t *testing.T) *cache.Client {
	t.Helper()

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	client, err := cache.New(url)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}
```

- [ ] **Step 3: Write the failing rate-limit test**

Create `apps/api/internal/cache/ratelimit_test.go`:

```go
package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAllowPermitsUpToTheLimitThenRejects(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for i := range 5 {
		allowed, remaining, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
		require.NoError(t, err)
		require.True(t, allowed, "request %d of 5 must be allowed", i+1)
		require.Equal(t, 4-i, remaining)
	}

	allowed, remaining, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed, "the sixth request must be rejected")
	require.Zero(t, remaining)
}

func TestAllowIsScopedPerKey(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for range 5 {
		_, _, err := client.Allow(ctx, "rl:test:a", 5, time.Minute)
		require.NoError(t, err)
	}

	allowed, _, err := client.Allow(ctx, "rl:test:b", 5, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed, "a different key must have its own budget")
}

func TestAllowRecoversAfterTheWindowPasses(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	for range 2 {
		_, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
		require.NoError(t, err)
	}

	blocked, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
	require.NoError(t, err)
	require.False(t, blocked)

	// Two full windows clears both the current and the previous counter the
	// sliding-window estimate reads.
	time.Sleep(2100 * time.Millisecond)

	allowed, _, err := client.Allow(ctx, "rl:test:c", 2, time.Second)
	require.NoError(t, err)
	require.True(t, allowed, "the budget must recover once the window rolls over")
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/cache/...` Expected: FAIL — the `cache` package does not exist yet

- [ ] **Step 5: Write the rate-limit Lua script**

Create `apps/api/internal/cache/lua/ratelimit.lua`:

```lua
-- Sliding-window counter. Estimates the request rate from the current window's
-- counter plus a time-weighted share of the previous window's, which smooths
-- the burst a plain fixed window allows at a window boundary.
--
-- KEYS[1] base key (the two window counters are derived from it)
-- ARGV[1] limit
-- ARGV[2] window length in seconds
-- ARGV[3] current time in unix milliseconds
-- returns { allowed (0|1), remaining }

local limit  = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now    = tonumber(ARGV[3])

local windowMillis = window * 1000
local currentSlot  = math.floor(now / windowMillis)

local currentKey  = KEYS[1] .. ':' .. currentSlot
local previousKey = KEYS[1] .. ':' .. (currentSlot - 1)

local currentCount  = tonumber(redis.call('GET', currentKey))  or 0
local previousCount = tonumber(redis.call('GET', previousKey)) or 0

local elapsed  = (now % windowMillis) / windowMillis
local carried  = previousCount * (1 - elapsed)
local estimate = carried + currentCount

if estimate >= limit then
  return { 0, 0 }
end

currentCount = redis.call('INCR', currentKey)
redis.call('EXPIRE', currentKey, window * 2)

local remaining = math.floor(limit - (carried + currentCount))
if remaining < 0 then
  remaining = 0
end

return { 1, remaining }
```

- [ ] **Step 6: Implement the client**

Create `apps/api/internal/cache/client.go`:

```go
// Package cache owns every Redis interaction: the link cache that fronts the
// redirect path, unique-visitor deduplication, and rate limiting. No other
// package issues Redis commands.
package cache

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/ratelimit.lua
var rateLimitSource string

var rateLimitScript = redis.NewScript(rateLimitSource)

// Client wraps the Redis connection with the small set of operations the
// redirect path needs. Every method costs a known number of Redis commands —
// the free tier's 500K/month ceiling is the binding constraint on this project,
// so that cost is part of each method's contract.
type Client struct {
	rdb *redis.Client
}

// New dials Redis from a redis:// or rediss:// URL.
func New(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse redis url: %w", err)
	}
	return &Client{rdb: redis.NewClient(opts)}, nil
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

// Allow applies a sliding-window rate limit to key. Costs one Redis command
// (a single EVAL, regardless of how many operations the script performs).
func (c *Client) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	res, err := rateLimitScript.Run(ctx, c.rdb,
		[]string{key},
		limit,
		int(window.Seconds()),
		time.Now().UnixMilli(),
	).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("cache: rate limit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("cache: rate limit: unexpected reply length %d", len(res))
	}
	return res[0] == 1, int(res[1]), nil
}
```

- [ ] **Step 7: Run the rate-limit tests**

Run: `cd apps/api && go test ./internal/cache/...` Expected: PASS (3 tests)

- [ ] **Step 8: Commit**

```bash
git add apps/api
git commit -m "feat(cache): add Redis client with a sliding-window rate limiter"
```

---

## Task 5: The link cache and the redirect lookup script

**Files:**

- Create: `apps/api/internal/link/cached.go`
- Modify: `apps/api/internal/cache/client.go` (add the lookup, put and invalidate methods)
- Create: `apps/api/internal/cache/lua/redirect_lookup.lua`
- Test: `apps/api/internal/cache/lookup_test.go`

**Interfaces:**

- Consumes: `cache.New` from Task 4.
- Produces:
  - `link.Cached` struct: `ID uuid.UUID`, `TeamID uuid.UUID`, `DestinationURL string`, `RedirectType int`, `State string`, `ExpiresAt *time.Time`, `HasPassword bool`; plus `link.CacheKey(hostname, slug string) string` and `link.UniqueSetPrefix`
  - `cache.Lookup` struct: `Found bool`, `NegativelyCached bool`, `Link link.Cached`, `UniqueVisit bool`
  - `(*cache.Client).LookupForRedirect(ctx, cacheKey, visitorHash, day string, uniqueTTL time.Duration) (Lookup, error)` — one Redis command
  - `(*cache.Client).PutLink(ctx, cacheKey string, l link.Cached, ttl time.Duration) error`
  - `(*cache.Client).PutNotFound(ctx, cacheKey string, ttl time.Duration) error`
  - `(*cache.Client).MarkUniqueVisit(ctx, linkID, day string, visitorHash string, ttl time.Duration) (bool, error)`
  - `(*cache.Client).InvalidateLink(ctx, cacheKey string) error`

- [ ] **Step 1: Write the cached-link type**

Create `apps/api/internal/link/cached.go`:

```go
// Package link holds the link record shared between the cache and the HTTP
// handlers, and the key layout the redirect path depends on.
package link

import (
	"time"

	"github.com/google/uuid"
)

// UniqueSetPrefix is the Redis key prefix for a link's daily unique-visitor
// set. The redirect Lua script appends "<link id>:<day>" to it, so it must
// stay in sync with lua/redirect_lookup.lua.
const UniqueSetPrefix = "uniq:"

// NotFoundSentinel is the cached value meaning "no link resolves this key".
// Negative caching keeps a scanner walking random slugs off the database.
const NotFoundSentinel = "-"

// Cached is the link record the redirect path reads. It is deliberately
// minimal: everything needed to answer a redirect, nothing else. The password
// hash is never cached — only whether one exists.
type Cached struct {
	ID             uuid.UUID  `json:"id"`
	TeamID         uuid.UUID  `json:"team_id"`
	DestinationURL string     `json:"destination_url"`
	RedirectType   int        `json:"redirect_type"`
	State          string     `json:"state"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	HasPassword    bool       `json:"has_password"`
	// AnalyticsEnabled false means this link records no clicks at all.
	AnalyticsEnabled bool `json:"analytics_enabled"`
}

// CacheKey is the Redis key for a link, scoped by hostname because slugs are
// unique per domain, never globally.
func CacheKey(hostname, slug string) string {
	return "l:" + hostname + ":" + slug
}
```

- [ ] **Step 2: Write the failing lookup test**

Create `apps/api/internal/cache/lookup_test.go`:

```go
package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

func sample() link.Cached {
	return link.Cached{
		ID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TeamID:         uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		DestinationURL: "https://example.org/a|b?c=d",
		RedirectType:     302,
		State:            "active",
		HasPassword:      false,
		AnalyticsEnabled: true,
	}
}

func TestLookupReportsMissForAnUncachedKey(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)

	got, err := client.LookupForRedirect(ctx, link.CacheKey("short.test", "nope"), "v1", "2026-09-02", time.Hour)

	require.NoError(t, err)
	require.False(t, got.Found)
	require.False(t, got.NegativelyCached)
}

func TestLookupReturnsThePutLinkAndCountsTheFirstVisitAsUnique(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Hour))

	first, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, first.Found)
	require.False(t, first.NegativelyCached)
	require.Equal(t, sample(), first.Link, "a destination containing a pipe must survive the round trip")
	require.True(t, first.UniqueVisit)

	second, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, second.Found)
	require.False(t, second.UniqueVisit, "the same visitor on the same day is not unique again")

	other, err := client.LookupForRedirect(ctx, key, "visitor-b", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, other.UniqueVisit, "a different visitor is unique")

	nextDay, err := client.LookupForRedirect(ctx, key, "visitor-a", "2026-09-03", time.Hour)
	require.NoError(t, err)
	require.True(t, nextDay.UniqueVisit, "the same visitor on a new day is unique again")
}

func TestLookupReportsNegativeCache(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "gone")

	require.NoError(t, client.PutNotFound(ctx, key, time.Minute))

	got, err := client.LookupForRedirect(ctx, key, "v1", "2026-09-02", time.Hour)

	require.NoError(t, err)
	require.False(t, got.Found)
	require.True(t, got.NegativelyCached)
}

func TestInvalidateRemovesTheCachedLink(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	key := link.CacheKey("short.test", "hello")

	require.NoError(t, client.PutLink(ctx, key, sample(), time.Hour))
	require.NoError(t, client.InvalidateLink(ctx, key))

	got, err := client.LookupForRedirect(ctx, key, "v1", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.False(t, got.Found)
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/cache/...` Expected: FAIL — `client.LookupForRedirect undefined`

- [ ] **Step 4: Write the redirect lookup Lua script**

Create `apps/api/internal/cache/lua/redirect_lookup.lua`:

```lua
-- Reads the cached link and, when one is present, records the visitor in the
-- link's daily unique-visitor set — in a single Redis command, because the
-- free tier's command budget is the binding constraint on the redirect path.
--
-- The cached value is "<link id>|<json>", so the script can find the link id
-- (which it needs to build the set key) without parsing JSON. The link id is
-- a UUID and never contains a pipe, so splitting on the first one is safe even
-- when the destination URL contains pipes.
--
-- KEYS[1] link cache key
-- ARGV[1] unique-visitor set prefix
-- ARGV[2] visitor hash
-- ARGV[3] day, as YYYY-MM-DD
-- ARGV[4] unique-visitor set TTL in seconds
-- returns { cached value or false, isUniqueVisit (0|1) }

local cached = redis.call('GET', KEYS[1])
if not cached then
  return { false, 0 }
end

if cached == '-' then
  return { cached, 0 }
end

local separator = string.find(cached, '|', 1, true)
if not separator then
  return { cached, 0 }
end

local linkID = string.sub(cached, 1, separator - 1)
local setKey = ARGV[1] .. linkID .. ':' .. ARGV[3]

local added = redis.call('SADD', setKey, ARGV[2])
if added == 1 then
  redis.call('EXPIRE', setKey, tonumber(ARGV[4]))
end

return { cached, added }
```

- [ ] **Step 5: Add the cache methods**

Append to `apps/api/internal/cache/client.go` (and add the `encoding/json`, `strings` and `github.com/mheob/kurze-url/apps/api/internal/link` imports, plus the embed for the new script next to the existing one):

```go
//go:embed lua/redirect_lookup.lua
var redirectLookupSource string

var redirectLookupScript = redis.NewScript(redirectLookupSource)

// Lookup is the result of a redirect-path cache read.
type Lookup struct {
	// Found reports whether a live link record was cached.
	Found bool
	// NegativelyCached reports that a recent lookup already established there
	// is no such link — the caller must not hit Postgres again.
	NegativelyCached bool
	Link             link.Cached
	// UniqueVisit is true when this visitor hash had not been seen for this
	// link today. Meaningless unless Found is true.
	UniqueVisit bool
}

// LookupForRedirect reads the cached link and deduplicates the visitor in one
// Redis command. visitorHash must be the daily-rotating hash from
// analytics.VisitorHash — a raw IP must never reach this method.
func (c *Client) LookupForRedirect(
	ctx context.Context,
	cacheKey, visitorHash, day string,
	uniqueTTL time.Duration,
) (Lookup, error) {
	res, err := redirectLookupScript.Run(ctx, c.rdb,
		[]string{cacheKey},
		link.UniqueSetPrefix,
		visitorHash,
		day,
		int(uniqueTTL.Seconds()),
	).Slice()
	if err != nil {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: %w", err)
	}
	if len(res) != 2 {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: unexpected reply length %d", len(res))
	}

	raw, ok := res[0].(string)
	if !ok {
		return Lookup{}, nil // nil reply: cache miss
	}
	if raw == link.NotFoundSentinel {
		return Lookup{NegativelyCached: true}, nil
	}

	_, payload, found := strings.Cut(raw, "|")
	if !found {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: malformed cached value")
	}

	var cached link.Cached
	if err := json.Unmarshal([]byte(payload), &cached); err != nil {
		return Lookup{}, fmt.Errorf("cache: redirect lookup: decode: %w", err)
	}

	unique, _ := res[1].(int64)
	return Lookup{Found: true, Link: cached, UniqueVisit: unique == 1}, nil
}

// PutLink caches a link record. The stored value is "<link id>|<json>" so the
// lookup script can extract the id without parsing JSON.
func (c *Client) PutLink(ctx context.Context, cacheKey string, l link.Cached, ttl time.Duration) error {
	payload, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("cache: encode link: %w", err)
	}
	value := l.ID.String() + "|" + string(payload)
	if err := c.rdb.Set(ctx, cacheKey, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put link: %w", err)
	}
	return nil
}

// PutNotFound negatively caches an unresolvable key for a short TTL.
func (c *Client) PutNotFound(ctx context.Context, cacheKey string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, cacheKey, link.NotFoundSentinel, ttl).Err(); err != nil {
		return fmt.Errorf("cache: put not-found: %w", err)
	}
	return nil
}

// MarkUniqueVisit records a visitor against a link's daily set, for the
// cache-miss path where LookupForRedirect had no link id to work with.
func (c *Client) MarkUniqueVisit(
	ctx context.Context,
	linkID, day, visitorHash string,
	ttl time.Duration,
) (bool, error) {
	key := link.UniqueSetPrefix + linkID + ":" + day
	added, err := c.rdb.SAdd(ctx, key, visitorHash).Result()
	if err != nil {
		return false, fmt.Errorf("cache: mark unique visit: %w", err)
	}
	if added == 1 {
		if err := c.rdb.Expire(ctx, key, ttl).Err(); err != nil {
			return false, fmt.Errorf("cache: mark unique visit: expire: %w", err)
		}
	}
	return added == 1, nil
}

// InvalidateLink drops a cached link. Every mutation of a link in plan 2 must
// call this, or a 302's "destination changes take effect immediately" promise
// is only true after LinkCacheTTL elapses.
func (c *Client) InvalidateLink(ctx context.Context, cacheKey string) error {
	if err := c.rdb.Del(ctx, cacheKey).Err(); err != nil {
		return fmt.Errorf("cache: invalidate link: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Run the cache tests**

Run: `cd apps/api && go test ./internal/cache/...` Expected: PASS (7 tests)

- [ ] **Step 7: Commit**

```bash
git add apps/api
git commit -m "feat(cache): add link cache with single-command redirect lookup"
```

---

## Task 6: The daily-rotating visitor hash

**Files:**

- Create: `apps/api/internal/analytics/visitor.go`
- Test: `apps/api/internal/analytics/visitor_test.go`

**Interfaces:**

- Consumes: `config.Config.VisitorSalt`
- Produces:
  - `analytics.VisitorHash(secret, ip, userAgent string, at time.Time) string` — 32 lowercase hex characters
  - `analytics.Day(at time.Time) string` — `YYYY-MM-DD` in UTC, the bucket key both the Redis unique set and the Postgres rollup use

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/analytics/visitor_test.go`:

```go
package analytics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

const (
	secret = "test-secret"
	ip     = "203.0.113.42"
	ua     = "Mozilla/5.0 (X11; Linux x86_64) Firefox/141.0"
)

func at(day int) time.Time {
	return time.Date(2026, 9, day, 13, 37, 0, 0, time.UTC)
}

func TestVisitorHashIsStableWithinADay(t *testing.T) {
	morning := time.Date(2026, 9, 2, 0, 0, 1, 0, time.UTC)
	evening := time.Date(2026, 9, 2, 23, 59, 59, 0, time.UTC)

	require.Equal(t,
		analytics.VisitorHash(secret, ip, ua, morning),
		analytics.VisitorHash(secret, ip, ua, evening),
	)
}

func TestVisitorHashRotatesDaily(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, ip, ua, at(3)),
		"yesterday's hash must not be correlatable with today's",
	)
}

func TestVisitorHashSeparatesDifferentVisitors(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, "198.51.100.7", ua, at(2)),
	)
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash(secret, ip, "curl/8.7.1", at(2)),
	)
}

func TestVisitorHashDoesNotAmbiguateFieldBoundaries(t *testing.T) {
	// Without a separator between the fields, "1.2.3" + "4Firefox" and
	// "1.2.34" + "Firefox" would hash identically.
	require.NotEqual(t,
		analytics.VisitorHash(secret, "1.2.3", "4Firefox", at(2)),
		analytics.VisitorHash(secret, "1.2.34", "Firefox", at(2)),
	)
}

func TestVisitorHashRevealsNothingAboutTheInput(t *testing.T) {
	hash := analytics.VisitorHash(secret, ip, ua, at(2))

	require.Len(t, hash, 32)
	require.NotContains(t, hash, ip)
	require.NotContains(t, strings.ToLower(hash), "firefox")
}

func TestVisitorHashChangesWithTheSecret(t *testing.T) {
	require.NotEqual(t,
		analytics.VisitorHash(secret, ip, ua, at(2)),
		analytics.VisitorHash("another-secret", ip, ua, at(2)),
	)
}

func TestDayIsUTCDateOnly(t *testing.T) {
	// 00:30 in UTC+2 is still the previous day in UTC.
	berlin := time.FixedZone("CEST", 2*60*60)
	require.Equal(t, "2026-09-01",
		analytics.Day(time.Date(2026, 9, 2, 0, 30, 0, 0, berlin)))
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: FAIL — package `analytics` does not exist

- [ ] **Step 3: Implement the visitor hash**

Create `apps/api/internal/analytics/visitor.go`:

```go
// Package analytics turns a redirect request into aggregate rollup counts.
// It never stores, logs or returns a full IP address.
package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// VisitorHash produces the daily-rotating, salted, non-reversible identifier
// used to count unique visitors. The date is part of the HMAC input, so a
// visitor's identifier changes every day and yesterday's cannot be correlated
// with today's. The raw IP never leaves this function.
func VisitorHash(secret, ip, userAgent string, at time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	// A NUL separator keeps the field boundaries unambiguous, so no pair of
	// distinct (ip, userAgent) inputs can produce the same digest input.
	mac.Write([]byte(Day(at)))
	mac.Write([]byte{0})
	mac.Write([]byte(ip))
	mac.Write([]byte{0})
	mac.Write([]byte(userAgent))

	// 128 bits is far more than enough to keep collisions negligible within a
	// single link's daily visitor set, and halves the Redis memory each set
	// costs against the 256 MB free tier.
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// Day is the UTC date bucket a click is counted into. Both the Redis
// unique-visitor set and the Postgres rollup key off this, so they must agree.
func Day(at time.Time) string {
	return at.UTC().Format(time.DateOnly)
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api
git commit -m "feat(analytics): add daily-rotating salted visitor hash"
```

---

## Task 7: Request-to-dimension extraction

**Files:**

- Create: `apps/api/internal/analytics/dimensions.go`
- Test: `apps/api/internal/analytics/dimensions_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks
- Produces:
  - `analytics.Dimensions` struct: `Browser, OS, Device, Country, Referrer, UTMSource, BotStatus, Source string`
  - `analytics.ExtractDimensions(r *http.Request) Dimensions`
  - `(Dimensions).Rows() []DimensionRow` where `DimensionRow` is `{Type string; Value *string}` — the `total` row has a nil `Value`
  - `analytics.QRQueryParam = "qr"` — the marker plan 2's QR generator must append to the encoded short URL

- [ ] **Step 1: Add the user-agent parser**

```bash
cd apps/api
go get github.com/mileusna/useragent
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `apps/api/internal/analytics/dimensions_test.go`:

```go
package analytics_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

func request(t *testing.T, target, userAgent string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestExtractDimensionsFromADesktopBrowser(t *testing.T) {
	r := request(t, "/hello",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
		map[string]string{"X-Vercel-IP-Country": "de"})

	d := analytics.ExtractDimensions(r)

	require.Equal(t, "Chrome", d.Browser)
	require.Equal(t, "macOS", d.OS)
	require.Equal(t, "desktop", d.Device)
	require.Equal(t, "DE", d.Country)
	require.Equal(t, "direct", d.Referrer)
	require.Empty(t, d.UTMSource)
	require.Equal(t, "human", d.BotStatus)
	require.Equal(t, "regular", d.Source)
}

func TestExtractDimensionsDetectsBots(t *testing.T) {
	r := request(t, "/hello",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", nil)

	require.Equal(t, "bot", analytics.ExtractDimensions(r).BotStatus)
}

func TestExtractDimensionsDetectsQRScans(t *testing.T) {
	r := request(t, "/hello?qr=1", "Mozilla/5.0", nil)

	require.Equal(t, "qr", analytics.ExtractDimensions(r).Source)
}

func TestExtractDimensionsReducesReferrerToItsHost(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0",
		map[string]string{"Referer": "https://News.Example.ORG/some/deep/path?secret=1"})

	require.Equal(t, "news.example.org", analytics.ExtractDimensions(r).Referrer,
		"only the host is kept — the path and query could carry personal data")
}

func TestExtractDimensionsCapturesUTMSource(t *testing.T) {
	r := request(t, "/hello?utm_source=newsletter", "Mozilla/5.0", nil)

	require.Equal(t, "newsletter", analytics.ExtractDimensions(r).UTMSource)
}

func TestExtractDimensionsFallsBackToUnknown(t *testing.T) {
	r := request(t, "/hello", "", nil)

	d := analytics.ExtractDimensions(r)

	require.Equal(t, "unknown", d.Browser)
	require.Equal(t, "unknown", d.OS)
	require.Equal(t, "unknown", d.Device)
	require.Equal(t, "unknown", d.Country)
}

func TestExtractDimensionsBoundsValueLength(t *testing.T) {
	long := strings.Repeat("a", 501)
	r := request(t, "/hello?utm_source="+long, "Mozilla/5.0", nil)

	require.LessOrEqual(t, len(analytics.ExtractDimensions(r).UTMSource), 128,
		"unbounded dimension values would let anyone inflate the rollup table")
}

func TestExtractDimensionsNeverTruncatesMidRune(t *testing.T) {
	// 127 ASCII bytes then multi-byte runes, so byte 128 lands inside one.
	// A plain byte slice here yields invalid UTF-8, which Postgres rejects.
	long := strings.Repeat("a", 127) + strings.Repeat("ü", 40)
	r := request(t, "/hello?utm_source="+url.QueryEscape(long), "Mozilla/5.0", nil)

	got := analytics.ExtractDimensions(r).UTMSource

	require.LessOrEqual(t, len(got), 128)
	require.True(t, utf8.ValidString(got), "a truncated value must still be valid UTF-8")
}

func TestRowsAlwaysIncludeTotalWithANullValue(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0", nil)

	rows := analytics.ExtractDimensions(r).Rows()

	var total *analytics.DimensionRow
	for i := range rows {
		if rows[i].Type == "total" {
			total = &rows[i]
		}
	}

	require.NotNil(t, total)
	require.Nil(t, total.Value, "the total row's dimension_value must be null")
}

func TestRowsOmitUTMSourceWhenAbsent(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0", nil)

	for _, row := range analytics.ExtractDimensions(r).Rows() {
		require.NotEqual(t, "utm_source", row.Type,
			"an absent utm_source must produce no row rather than an 'unknown' one")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: FAIL — `undefined: analytics.ExtractDimensions`

- [ ] **Step 4: Implement dimension extraction**

Create `apps/api/internal/analytics/dimensions.go`:

```go
package analytics

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/mileusna/useragent"
)

// QRQueryParam marks a click as coming from a scanned QR code. The QR
// generator (plan 2) appends "?qr=1" to the short URL it encodes; nothing else
// can distinguish a scan from a normal click.
const QRQueryParam = "qr"

const (
	unknown = "unknown"
	// maxValueLength bounds a dimension value so an attacker cannot inflate
	// link_click_stats by sending long, distinct referrers or utm_sources.
	maxValueLength = 128
)

// Dimensions are the rollup axes a single click contributes to.
type Dimensions struct {
	Browser   string
	OS        string
	Device    string
	Country   string
	Referrer  string
	UTMSource string
	BotStatus string
	Source    string
}

// DimensionRow is one (dimension_type, dimension_value) pair. Value is nil
// only for the "total" row.
type DimensionRow struct {
	Type  string
	Value *string
}

// ExtractDimensions derives the rollup axes from a redirect request. It reads
// only the User-Agent, the Referer host, the utm_source query parameter and
// Vercel's geo header — never the client IP.
func ExtractDimensions(r *http.Request) Dimensions {
	ua := useragent.Parse(r.UserAgent())

	d := Dimensions{
		Browser:   fallback(ua.Name),
		OS:        fallback(ua.OS),
		Device:    device(ua),
		Country:   country(r),
		Referrer:  referrerHost(r.Referer()),
		UTMSource: truncate(r.URL.Query().Get("utm_source")),
		BotStatus: "human",
		Source:    "regular",
	}
	if ua.Bot {
		d.BotStatus = "bot"
	}
	if r.URL.Query().Get(QRQueryParam) == "1" {
		d.Source = "qr"
	}
	return d
}

// Rows flattens the dimensions into the rows a click writes. utm_source is
// omitted when absent: emitting "unknown" for every non-campaign click would
// double the table's row count for no analytical value.
func (d Dimensions) Rows() []DimensionRow {
	rows := []DimensionRow{
		{Type: "total", Value: nil},
		{Type: "browser", Value: &d.Browser},
		{Type: "os", Value: &d.OS},
		{Type: "device", Value: &d.Device},
		{Type: "country", Value: &d.Country},
		{Type: "referrer", Value: &d.Referrer},
		{Type: "bot_status", Value: &d.BotStatus},
		{Type: "qr_vs_regular", Value: &d.Source},
	}
	if d.UTMSource != "" {
		rows = append(rows, DimensionRow{Type: "utm_source", Value: &d.UTMSource})
	}
	return rows
}

func device(ua useragent.UserAgent) string {
	switch {
	case ua.Mobile:
		return "mobile"
	case ua.Tablet:
		return "tablet"
	case ua.Desktop:
		return "desktop"
	default:
		return unknown
	}
}

// country reads the geo header Vercel adds at the edge. Doing it this way
// keeps the promise of no per-request third-party geolocation call without
// shipping and updating a GeoIP database. Off Vercel the header is absent.
func country(r *http.Request) string {
	code := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Vercel-IP-Country")))
	if code == "" {
		return unknown
	}
	return truncate(code)
}

// referrerHost keeps only the host. A full referrer URL can carry personal
// data in its path or query, and its cardinality is unbounded.
func referrerHost(referer string) string {
	if referer == "" {
		return "direct"
	}
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" {
		return unknown
	}
	return truncate(strings.ToLower(u.Hostname()))
}

func fallback(v string) string {
	if strings.TrimSpace(v) == "" {
		return unknown
	}
	return truncate(v)
}

// truncate bounds a value's stored size without ever returning invalid UTF-8.
// A plain byte slice can cut inside a multi-byte rune, and Postgres rejects
// the result with "invalid byte sequence for encoding UTF8" — which would
// silently drop a click's row on the redirect hot path. utm_source is
// attacker-supplied free-form UTF-8, so this is reachable, not theoretical.
func truncate(v string) string {
	if len(v) <= maxValueLength {
		return v
	}
	v = v[:maxValueLength]
	for len(v) > 0 && !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return v
}
```

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: PASS (16 tests)

- [ ] **Step 6: Commit**

```bash
git add apps/api
git commit -m "feat(analytics): derive rollup dimensions from the redirect request"
```

---

## Task 8: The click recorder

**Files:**

- Create: `apps/api/internal/analytics/recorder.go`
- Test: `apps/api/internal/analytics/recorder_test.go`

**Interfaces:**

- Consumes: `analytics.Dimensions` (Task 7)
- Produces:
  - `analytics.Row` struct: `LinkID uuid.UUID`, `Day time.Time`, `DimType string`, `DimValue *string`, `Clicks int64`, `Unique int64`
  - `analytics.FlushFunc = func(context.Context, []Row) error`
  - `analytics.NewRecorder(flush FlushFunc, interval time.Duration, maxBuffer int, log *slog.Logger) *Recorder`
  - `(*Recorder).Record(linkID uuid.UUID, at time.Time, d Dimensions, unique bool)` — non-blocking, no I/O
  - `(*Recorder).Run(ctx context.Context)` — blocks until ctx is done, then flushes one last time
  - `(*Recorder).Flush(ctx context.Context) error`

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/analytics/recorder_test.go`:

```go
package analytics_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

type flushSpy struct {
	mu    sync.Mutex
	calls [][]analytics.Row
	err   error
}

func (s *flushSpy) flush(_ context.Context, rows []analytics.Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, rows)
	return s.err
}

func (s *flushSpy) rowsFor(dimType string) []analytics.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []analytics.Row
	for _, call := range s.calls {
		for _, row := range call {
			if row.DimType == dimType {
				out = append(out, row)
			}
		}
	}
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func desktopDimensions() analytics.Dimensions {
	return analytics.Dimensions{
		Browser: "Chrome", OS: "macOS", Device: "desktop", Country: "DE",
		Referrer: "direct", BotStatus: "human", Source: "regular",
	}
}

func TestRecorderAggregatesRepeatedClicksIntoOneRow(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	linkID := uuid.New()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	rec.Record(linkID, now, desktopDimensions(), true)
	rec.Record(linkID, now, desktopDimensions(), false)
	rec.Record(linkID, now, desktopDimensions(), false)

	require.NoError(t, rec.Flush(context.Background()))

	totals := spy.rowsFor("total")
	require.Len(t, totals, 1, "three clicks on one link on one day are one row")
	require.EqualValues(t, 3, totals[0].Clicks)
	require.EqualValues(t, 1, totals[0].Unique)
	require.Nil(t, totals[0].DimValue, "the total row's dimension_value is null")
}

func TestRecorderKeepsDifferentDaysApart(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	linkID := uuid.New()

	rec.Record(linkID, time.Date(2026, 9, 2, 23, 0, 0, 0, time.UTC), desktopDimensions(), true)
	rec.Record(linkID, time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC), desktopDimensions(), true)

	require.NoError(t, rec.Flush(context.Background()))

	require.Len(t, spy.rowsFor("total"), 2)
}

func TestRecorderTruncatesTheDayToAUTCDate(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	rec.Record(uuid.New(), time.Date(2026, 9, 2, 13, 37, 42, 99, time.UTC), desktopDimensions(), true)
	require.NoError(t, rec.Flush(context.Background()))

	day := spy.rowsFor("total")[0].Day
	require.Equal(t, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), day)
}

func TestFlushIsANoOpWhenNothingWasRecorded(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	require.NoError(t, rec.Flush(context.Background()))

	spy.mu.Lock()
	defer spy.mu.Unlock()
	require.Empty(t, spy.calls, "an empty buffer must not hit the database")
}

func TestFlushEmptiesTheBuffer(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	now := time.Now()

	rec.Record(uuid.New(), now, desktopDimensions(), true)
	require.NoError(t, rec.Flush(context.Background()))
	require.NoError(t, rec.Flush(context.Background()))

	require.Len(t, spy.rowsFor("total"), 1, "the second flush must not resend the first flush's rows")
}

func TestFlushReportsButDoesNotRetainRowsOnError(t *testing.T) {
	spy := &flushSpy{err: errors.New("database down")}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())

	rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)

	require.Error(t, rec.Flush(context.Background()))

	spy.err = nil
	require.NoError(t, rec.Flush(context.Background()))
	require.Len(t, spy.rowsFor("total"), 1,
		"a failed flush drops its rows rather than growing the buffer without bound")
}

func TestRunFlushesOnContextCancellation(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 1000, discardLogger())
	rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	require.Len(t, spy.rowsFor("total"), 1, "buffered clicks must survive shutdown")
}

func TestRunFlushesWhenTheBufferFills(t *testing.T) {
	spy := &flushSpy{}
	rec := analytics.NewRecorder(spy.flush, time.Hour, 2, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rec.Run(ctx)

	for range 5 {
		rec.Record(uuid.New(), time.Now(), desktopDimensions(), true)
	}

	require.Eventually(t, func() bool {
		return len(spy.rowsFor("total")) >= 2
	}, 5*time.Second, 20*time.Millisecond,
		"exceeding maxBuffer must trigger a flush without waiting for the interval")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: FAIL — `undefined: analytics.NewRecorder`

- [ ] **Step 3: Implement the recorder**

Create `apps/api/internal/analytics/recorder.go`:

```go
package analytics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Row is one rollup increment ready for the database.
type Row struct {
	LinkID   uuid.UUID
	Day      time.Time
	DimType  string
	DimValue *string
	Clicks   int64
	Unique   int64
}

// FlushFunc writes a batch of rollup increments. It is the only place the
// recorder touches the outside world.
type FlushFunc func(ctx context.Context, rows []Row) error

type bufferKey struct {
	linkID   uuid.UUID
	day      time.Time
	dimType  string
	dimValue string
	isNull   bool
}

type bufferCounts struct {
	clicks int64
	unique int64
}

// Recorder aggregates clicks in memory and writes them to the database out of
// band, so the redirect response never waits on analytics. Buffered rows are
// lost if the process dies unflushed — an accepted trade for keeping the hot
// path free of a synchronous write, and bounded by the flush interval.
type Recorder struct {
	flush    FlushFunc
	interval time.Duration
	maxSize  int
	log      *slog.Logger

	mu     sync.Mutex
	buffer map[bufferKey]bufferCounts

	wake chan struct{}
}

// NewRecorder builds a recorder. maxBuffer bounds how many distinct rows may
// accumulate before a flush is triggered early, independent of interval.
func NewRecorder(flush FlushFunc, interval time.Duration, maxBuffer int, log *slog.Logger) *Recorder {
	return &Recorder{
		flush:    flush,
		interval: interval,
		maxSize:  maxBuffer,
		log:      log,
		buffer:   make(map[bufferKey]bufferCounts),
		wake:     make(chan struct{}, 1),
	}
}

// Record accumulates one click. It performs no I/O and never blocks on
// anything but a brief in-memory lock, which is what keeps it safe to call
// from the redirect handler.
func (r *Recorder) Record(linkID uuid.UUID, at time.Time, d Dimensions, unique bool) {
	day := at.UTC().Truncate(24 * time.Hour)

	var uniqueCount int64
	if unique {
		uniqueCount = 1
	}

	r.mu.Lock()
	for _, row := range d.Rows() {
		key := bufferKey{linkID: linkID, day: day, dimType: row.Type, isNull: row.Value == nil}
		if row.Value != nil {
			key.dimValue = *row.Value
		}
		counts := r.buffer[key]
		counts.clicks++
		counts.unique += uniqueCount
		r.buffer[key] = counts
	}
	full := len(r.buffer) >= r.maxSize
	r.mu.Unlock()

	if full {
		select {
		case r.wake <- struct{}{}:
		default: // a flush is already pending
		}
	}
}

// Run flushes on the interval and whenever the buffer fills, then flushes once
// more when ctx is cancelled so a graceful shutdown does not drop counts.
func (r *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := r.Flush(shutdownCtx); err != nil {
				r.log.Error("final click-stats flush failed", "error", err)
			}
			cancel()
			return
		case <-ticker.C:
			r.flushAndLog(ctx)
		case <-r.wake:
			r.flushAndLog(ctx)
		}
	}
}

func (r *Recorder) flushAndLog(ctx context.Context) {
	if err := r.Flush(ctx); err != nil {
		r.log.Error("click-stats flush failed", "error", err)
	}
}

// Flush writes everything buffered so far. On failure the rows are dropped
// rather than retained: analytics are a rollup, and an unbounded retry buffer
// on a serverless instance is a worse failure than a gap in the counts.
func (r *Recorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	if len(r.buffer) == 0 {
		r.mu.Unlock()
		return nil
	}
	pending := r.buffer
	r.buffer = make(map[bufferKey]bufferCounts)
	r.mu.Unlock()

	rows := make([]Row, 0, len(pending))
	for key, counts := range pending {
		row := Row{
			LinkID:  key.linkID,
			Day:     key.day,
			DimType: key.dimType,
			Clicks:  counts.clicks,
			Unique:  counts.unique,
		}
		if !key.isNull {
			value := key.dimValue
			row.DimValue = &value
		}
		rows = append(rows, row)
	}

	return r.flush(ctx, rows)
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/analytics/...` Expected: PASS (24 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api
git commit -m "feat(analytics): add non-blocking click recorder with buffered rollup flush"
```

---

## Task 9: Server-rendered redirect-surface pages, in English and German

**Files:**

- Create: `apps/api/internal/pages/pages.go`
- Create: `apps/api/internal/pages/templates/error.html`
- Create: `apps/api/internal/pages/templates/password.html`
- Test: `apps/api/internal/pages/pages_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks
- Produces:
  - `pages.Locale` with `pages.LocaleEN` / `pages.LocaleDE`; `pages.Negotiate(acceptLanguage string) Locale`
  - `pages.Kind` with `KindNotFound`, `KindDisabled`, `KindExpired`, `KindFlagged`, `KindRateLimited`, `KindServerError`
  - `pages.RenderError(w http.ResponseWriter, status int, loc Locale, kind Kind)`
  - `pages.RenderPasswordPrompt(w http.ResponseWriter, status int, loc Locale, action string, wrongPassword bool)`

These pages sit on the redirect hot path, so they are plain `html/template` — deliberately framework-free, with no client-side JavaScript and no build step.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/pages/pages_test.go`:

```go
package pages_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

func TestNegotiatePicksGermanWhenPreferred(t *testing.T) {
	require.Equal(t, pages.LocaleDE, pages.Negotiate("de-DE,de;q=0.9,en;q=0.8"))
	require.Equal(t, pages.LocaleDE, pages.Negotiate("de"))
}

func TestNegotiateDefaultsToEnglish(t *testing.T) {
	require.Equal(t, pages.LocaleEN, pages.Negotiate(""))
	require.Equal(t, pages.LocaleEN, pages.Negotiate("en-GB,en;q=0.9"))
	require.Equal(t, pages.LocaleEN, pages.Negotiate("fr-FR,fr;q=0.9"))
}

func TestNegotiateRespectsQualityOrder(t *testing.T) {
	require.Equal(t, pages.LocaleEN, pages.Negotiate("en;q=0.9,de;q=0.5"))
	require.Equal(t, pages.LocaleDE, pages.Negotiate("en;q=0.4,de;q=0.8"))
}

func TestRenderErrorWritesTheStatusAndLocalisedCopy(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderError(rec, http.StatusNotFound, pages.LocaleDE, pages.KindNotFound)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	body := rec.Body.String()
	require.Contains(t, body, `lang="de"`)
	require.Contains(t, body, "nicht gefunden")
}

func TestRenderErrorHasDistinctCopyPerKind(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range []pages.Kind{
		pages.KindNotFound, pages.KindDisabled, pages.KindExpired,
		pages.KindFlagged, pages.KindRateLimited, pages.KindServerError,
	} {
		rec := httptest.NewRecorder()
		pages.RenderError(rec, http.StatusOK, pages.LocaleEN, kind)
		body := rec.Body.String()
		require.False(t, seen[body], "kind %q reuses another kind's copy", kind)
		seen[body] = true
	}
}

func TestRenderPasswordPromptPostsToTheGivenAction(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, "/hello/verify", false)

	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, body, `method="post"`)
	require.Contains(t, body, `action="/hello/verify"`)
	require.Contains(t, body, `type="password"`)
	require.Contains(t, body, `name="password"`)
	require.NotContains(t, body, "incorrect")
}

func TestRenderPasswordPromptShowsAnErrorAfterAWrongAttempt(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusUnauthorized, pages.LocaleEN, "/hello/verify", true)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, strings.ToLower(rec.Body.String()), "incorrect")
}

func TestRenderPasswordPromptEscapesTheAction(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, `/x"><script>alert(1)</script>/verify`, false)

	require.NotContains(t, rec.Body.String(), "<script>alert(1)</script>")
}

func TestPasswordPromptIsAccessible(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, "/hello/verify", true)

	body := rec.Body.String()
	require.Contains(t, body, `<label for="password"`, "the input needs a programmatic label")
	require.Contains(t, body, `id="password"`)
	require.Contains(t, body, `role="alert"`, "the error must be announced to screen readers")
	require.Contains(t, body, `autocomplete="current-password"`)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/pages/...` Expected: FAIL — package `pages` does not exist

- [ ] **Step 3: Write the templates**

Create `apps/api/internal/pages/templates/error.html`:

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
				max-width: 32rem;
				text-align: center;
			}
			h1 {
				font-size: 1.5rem;
				margin: 0 0 0.5rem;
			}
			p {
				margin: 0;
				opacity: 0.8;
			}
		</style>
	</head>
	<body>
		<main>
			<h1>{{ .Heading }}</h1>
			<p>{{ .Body }}</p>
		</main>
	</body>
</html>
```

Create `apps/api/internal/pages/templates/password.html`:

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
				max-width: 24rem;
				width: 100%;
			}
			h1 {
				font-size: 1.5rem;
				margin: 0 0 0.5rem;
			}
			p {
				margin: 0 0 1rem;
				opacity: 0.8;
			}
			label {
				display: block;
				font-weight: 600;
				margin-bottom: 0.375rem;
			}
			input,
			button {
				width: 100%;
				box-sizing: border-box;
				font: inherit;
				padding: 0.625rem 0.75rem;
				border-radius: 0.5rem;
				border: 1px solid currentColor;
			}
			button {
				margin-top: 0.75rem;
				cursor: pointer;
				font-weight: 600;
			}
			.error {
				margin: 0 0 1rem;
				padding: 0.625rem 0.75rem;
				border-radius: 0.5rem;
				border: 1px solid currentColor;
				font-weight: 600;
			}
		</style>
	</head>
	<body>
		<main>
			<h1>{{ .Heading }}</h1>
			<p>{{ .Body }}</p>
			{{ if .WrongPassword }}
			<p class="error" role="alert">{{ .ErrorMessage }}</p>
			{{ end }}
			<form method="post" action="{{ .Action }}">
				<label for="password">{{ .PasswordLabel }}</label>
				<input
					id="password"
					name="password"
					type="password"
					autocomplete="current-password"
					required
					autofocus
				/>
				<button type="submit">{{ .SubmitLabel }}</button>
			</form>
		</main>
	</body>
</html>
```

- [ ] **Step 4: Implement the pages package**

Create `apps/api/internal/pages/pages.go`:

```go
// Package pages renders the redirect surface's browser-facing HTML. These
// pages sit on the redirect hot path, so they are plain html/template with no
// framework, no build step and no client-side JavaScript.
//
// Every string here exists in both English and German. Nothing user-facing is
// hardcoded in a single language, including on this surface, which never
// passes through the React app's i18n layer.
package pages

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Locale is one of the two languages the redirect surface ships in.
type Locale string

const (
	LocaleEN Locale = "en"
	LocaleDE Locale = "de"
)

// Kind identifies which error page to show.
type Kind string

const (
	KindNotFound    Kind = "not_found"
	KindDisabled    Kind = "disabled"
	KindExpired     Kind = "expired"
	KindFlagged     Kind = "flagged"
	KindRateLimited Kind = "rate_limited"
	KindServerError Kind = "server_error"
)

type copyText struct {
	title   string
	heading string
	body    string
}

type localeStrings struct {
	errors        map[Kind]copyText
	passwordTitle string
	passwordHead  string
	passwordBody  string
	passwordLabel string
	submitLabel   string
	wrongPassword string
}

var localeCopy = map[Locale]localeStrings{
	LocaleEN: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link not found", "Link not found", "This short link does not exist, or it has been removed."},
			KindDisabled:    {"Link disabled", "Link disabled", "The owner of this short link has turned it off."},
			KindExpired:     {"Link expired", "Link expired", "This short link has passed its expiry date."},
			KindFlagged:     {"Link blocked", "Link blocked", "This short link was flagged as unsafe and is no longer forwarded."},
			KindRateLimited: {"Too many requests", "Too many requests", "You have opened links too quickly. Please wait a moment and try again."},
			KindServerError: {"Something went wrong", "Something went wrong", "This link could not be resolved right now. Please try again shortly."},
		},
		passwordTitle: "Password required",
		passwordHead:  "Password required",
		passwordBody:  "This short link is protected. Enter its password to continue.",
		passwordLabel: "Password",
		submitLabel:   "Continue",
		wrongPassword: "That password is incorrect.",
	},
	LocaleDE: {
		errors: map[Kind]copyText{
			KindNotFound:    {"Link nicht gefunden", "Link nicht gefunden", "Dieser Kurzlink existiert nicht oder wurde entfernt."},
			KindDisabled:    {"Link deaktiviert", "Link deaktiviert", "Die Inhaberin oder der Inhaber dieses Kurzlinks hat ihn deaktiviert."},
			KindExpired:     {"Link abgelaufen", "Link abgelaufen", "Dieser Kurzlink hat sein Ablaufdatum überschritten."},
			KindFlagged:     {"Link gesperrt", "Link gesperrt", "Dieser Kurzlink wurde als unsicher eingestuft und wird nicht mehr weitergeleitet."},
			KindRateLimited: {"Zu viele Anfragen", "Zu viele Anfragen", "Sie haben zu schnell zu viele Links geöffnet. Bitte warten Sie einen Moment."},
			KindServerError: {"Etwas ist schiefgelaufen", "Etwas ist schiefgelaufen", "Dieser Link konnte gerade nicht aufgelöst werden. Bitte versuchen Sie es gleich erneut."},
		},
		passwordTitle: "Passwort erforderlich",
		passwordHead:  "Passwort erforderlich",
		passwordBody:  "Dieser Kurzlink ist geschützt. Geben Sie das Passwort ein, um fortzufahren.",
		passwordLabel: "Passwort",
		submitLabel:   "Weiter",
		wrongPassword: "Das Passwort ist nicht korrekt.",
	},
}

// Negotiate picks a locale from an Accept-Language header, honouring q-values.
// Anything that is not a German preference falls back to English, the default.
func Negotiate(acceptLanguage string) Locale {
	best := LocaleEN
	bestQuality := -1.0

	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}

		quality := 1.0
		if _, raw, ok := strings.Cut(params, "q="); ok {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
				quality = parsed
			}
		}

		locale := LocaleEN
		switch {
		case strings.HasPrefix(tag, "de"):
			locale = LocaleDE
		case strings.HasPrefix(tag, "en"):
			locale = LocaleEN
		default:
			continue
		}

		if quality > bestQuality {
			best, bestQuality = locale, quality
		}
	}

	return best
}

type errorView struct {
	Lang    Locale
	Title   string
	Heading string
	Body    string
}

// RenderError writes a localised error page with the given HTTP status.
func RenderError(w http.ResponseWriter, status int, loc Locale, kind Kind) {
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

type passwordView struct {
	Lang          Locale
	Title         string
	Heading       string
	Body          string
	Action        string
	PasswordLabel string
	SubmitLabel   string
	WrongPassword bool
	ErrorMessage  string
}

// RenderPasswordPrompt writes the password interstitial. action is the path
// the form posts to; html/template escapes it as an attribute value.
func RenderPasswordPrompt(w http.ResponseWriter, status int, loc Locale, action string, wrongPassword bool) {
	s := localeCopy[loc]

	render(w, status, "password.html", passwordView{
		Lang:          loc,
		Title:         s.passwordTitle,
		Heading:       s.passwordHead,
		Body:          s.passwordBody,
		Action:        action,
		PasswordLabel: s.passwordLabel,
		SubmitLabel:   s.submitLabel,
		WrongPassword: wrongPassword,
		ErrorMessage:  s.wrongPassword,
	})
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// These pages are per-request and must never be cached by an intermediary.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		// The status is already written, so there is nothing to do but record it.
		slog.Error("rendering redirect-surface page failed", "template", name, "error", err)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/pages/...` Expected: PASS (8 tests)

- [ ] **Step 6: Commit**

```bash
git add apps/api
git commit -m "feat(pages): add localised server-rendered redirect-surface pages"
```

---

## Task 10: The redirect handler

**Files:**

- Create: `apps/api/internal/api/api.go`
- Create: `apps/api/internal/api/middleware.go`
- Create: `apps/api/internal/api/redirect.go`
- Test: `apps/api/internal/api/testhelper_test.go`
- Test: `apps/api/internal/api/redirect_test.go`

**Interfaces:**

- Consumes: `config.Config` (T1), `db.Queries` (T3), `cache.Client` (T4, T5), `analytics` (T6–T8), `pages` (T9)
- Produces:
  - `api.Deps` struct: `Config config.Config`, `Queries *db.Queries`, `Cache *cache.Client`, `Recorder *analytics.Recorder`, `Log *slog.Logger`, `Now func() time.Time`
  - `(Deps).HandleRedirect(w http.ResponseWriter, r *http.Request)` — reads the slug from chi's `{slug}` URL param
  - `api.ClientIP(r *http.Request) string`, `api.Hostname(hostHeader string) string`

- [ ] **Step 1: Write the shared dependency struct and request helpers**

Create `apps/api/internal/api/api.go`:

```go
// Package api owns the HTTP shape of both surfaces: the Huma-registered /v1
// JSON API and the public, unversioned redirect surface. It issues no SQL and
// no Redis commands of its own — those belong to internal/db and internal/cache.
package api

import (
	"log/slog"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Deps is everything the handlers need, constructed once in cmd/api.
type Deps struct {
	Config   config.Config
	Queries  *db.Queries
	Cache    *cache.Client
	Recorder *analytics.Recorder
	Log      *slog.Logger

	// Now is injectable so tests can pin expiry behaviour. Defaults to
	// time.Now when nil.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}
```

Create `apps/api/internal/api/middleware.go`:

```go
package api

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP resolves the caller's address. On Vercel the platform sets
// X-Forwarded-For, and the request cannot reach the process without passing
// through it, so the leftmost entry is trustworthy there. Outside a trusted
// proxy this header is spoofable — which is why it is only ever used as a
// rate-limit key and as visitor-hash input, never stored or logged.
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Hostname strips any port from a Host header so it can be compared with the
// domain.hostname column, which never carries one.
func Hostname(hostHeader string) string {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
```

- [ ] **Step 2: Write the integration test helpers**

Create `apps/api/internal/api/testhelper_test.go`:

```go
package api_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func testCache(t *testing.T) *cache.Client {
	t.Helper()

	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	client, err := cache.New(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}

type fixture struct {
	deps     api.Deps
	pool     *pgxpool.Pool
	hostname string
	linkID   uuid.UUID
	rows     *[]analytics.Row
}

// newFixture builds a fully wired Deps against a real Postgres and Redis, plus
// a throwaway team, verified domain and link. Everything it inserts is removed
// on cleanup so the suite can run repeatedly against one local database.
func newFixture(t *testing.T, opts ...func(*linkOptions)) *fixture {
	t.Helper()
	ctx := context.Background()

	pool := testPool(t)
	client := testCache(t)

	options := linkOptions{
		slug:         "hello",
		destination:  "https://example.org/hello",
		redirectType: 302,
		state:        "active",
	}
	for _, opt := range opts {
		opt(&options)
	}

	hostname := "t" + uuid.NewString()[:8] + ".test"

	var userID, teamID, domainID, linkID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('fixture') returning id`).Scan(&teamID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, $3, now()) returning id`,
		teamID, hostname, options.verification()).Scan(&domainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, redirect_type,
		                   state, expires_at, password_hash, analytics_enabled, created_by)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
		domainID, teamID, options.slug, options.destination, options.redirectType,
		options.state, options.expiresAt, options.passwordHash,
		!options.analyticsOff, userID).Scan(&linkID))

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	var recorded []analytics.Row
	recorder := analytics.NewRecorder(
		func(_ context.Context, rows []analytics.Row) error {
			recorded = append(recorded, rows...)
			return nil
		},
		time.Hour, 100000,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	cfg, err := config.Load()
	require.NoError(t, err)

	return &fixture{
		pool:     pool,
		hostname: hostname,
		linkID:   linkID,
		rows:     &recorded,
		deps: api.Deps{
			Config:   cfg,
			Queries:  db.New(pool),
			Cache:    client,
			Recorder: recorder,
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			Now:      func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) },
		},
	}
}

type linkOptions struct {
	slug         string
	destination  string
	redirectType int
	state        string
	expiresAt    *time.Time
	passwordHash *string
	unverified   bool
	analyticsOff bool
}

func (o linkOptions) verification() string {
	if o.unverified {
		return "pending"
	}
	return "verified"
}

func withSlug(s string) func(*linkOptions)  { return func(o *linkOptions) { o.slug = s } }
func withState(s string) func(*linkOptions) { return func(o *linkOptions) { o.state = s } }
func withRedirectType(t int) func(*linkOptions) {
	return func(o *linkOptions) { o.redirectType = t }
}
func withExpiry(at time.Time) func(*linkOptions) {
	return func(o *linkOptions) { o.expiresAt = &at }
}
func withPasswordHash(h string) func(*linkOptions) {
	return func(o *linkOptions) { o.passwordHash = &h }
}
func unverifiedDomain() func(*linkOptions)  { return func(o *linkOptions) { o.unverified = true } }
func analyticsDisabled() func(*linkOptions) { return func(o *linkOptions) { o.analyticsOff = true } }
```

The test package needs the required environment variables for `config.Load`. Add a `TestMain` to the same file:

```go
func TestMain(m *testing.M) {
	for name, value := range map[string]string{
		"DATABASE_URL": "postgres://postgres:postgres@127.0.0.1:54322/postgres",
		"REDIS_URL":    "redis://127.0.0.1:6379",
		"VISITOR_SALT": "test-salt",
		"API_HOSTNAME": "api.test",
	} {
		if os.Getenv(name) == "" {
			_ = os.Setenv(name, value)
		}
	}
	os.Exit(m.Run())
}
```

- [ ] **Step 3: Write the failing redirect test**

Create `apps/api/internal/api/redirect_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/link"
)

// redirectRouter mounts the handler the way the real router does, so chi's
// {slug} URL parameter resolves.
func redirectRouter(f *fixture) http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}", f.deps.HandleRedirect)
	return r
}

func get(t *testing.T, f *fixture, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = f.hostname
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/141.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	redirectRouter(f).ServeHTTP(rec, req)
	return rec
}

func TestRedirectResolvesFromPostgresOnACacheMissAndPopulatesTheCache(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a 302 must not be cached, or destination changes stop taking effect")

	cached, err := f.deps.Cache.LookupForRedirect(context.Background(),
		link.CacheKey(f.hostname, "hello"), "someone-else", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, cached.Found, "the miss must have populated the cache")
	require.Equal(t, "https://example.org/hello", cached.Link.DestinationURL)
}

func TestRedirectServesFromTheCacheOnTheSecondRequest(t *testing.T) {
	f := newFixture(t)

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	// Change the row behind the cache's back. A cache hit must not see it.
	_, err := f.pool.Exec(context.Background(),
		`update link set destination_url = 'https://example.org/changed' where id = $1`, f.linkID)
	require.NoError(t, err)

	rec := get(t, f, "/hello", nil)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"),
		"the second request must be served from cache")
}

func TestRedirectHonoursThePerLinkRedirectType(t *testing.T) {
	f := newFixture(t, withRedirectType(301))

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusMovedPermanently, rec.Code)
}

func TestRedirectReturns404AndNegativelyCachesAnUnknownSlug(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/nope", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)

	cached, err := f.deps.Cache.LookupForRedirect(context.Background(),
		link.CacheKey(f.hostname, "nope"), "v", "2026-09-02", time.Hour)
	require.NoError(t, err)
	require.True(t, cached.NegativelyCached,
		"an unknown slug must be negatively cached so a scanner cannot hammer Postgres")
}

func TestRedirectRefusesAnUnverifiedDomain(t *testing.T) {
	f := newFixture(t, unverifiedDomain())

	require.Equal(t, http.StatusNotFound, get(t, f, "/hello", nil).Code,
		"an unverified domain must not serve links — the team may not own the hostname")
}

func TestRedirectRefusesLinksThatAreNotActive(t *testing.T) {
	for _, tc := range []struct {
		state  string
		status int
	}{
		{"disabled", http.StatusGone},
		{"expired", http.StatusGone},
		{"flagged", http.StatusForbidden},
	} {
		t.Run(tc.state, func(t *testing.T) {
			f := newFixture(t, withState(tc.state))
			require.Equal(t, tc.status, get(t, f, "/hello", nil).Code)
		})
	}
}

func TestRedirectRefusesALinkPastItsExpiry(t *testing.T) {
	f := newFixture(t, withExpiry(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))

	require.Equal(t, http.StatusGone, get(t, f, "/hello", nil).Code)
}

func TestRedirectAllowsALinkBeforeItsExpiry(t *testing.T) {
	f := newFixture(t, withExpiry(time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)))

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
}

func TestRedirectShowsThePasswordPromptInsteadOfRedirecting(t *testing.T) {
	f := newFixture(t, withPasswordHash("$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"))

	rec := get(t, f, "/hello", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), `action="/hello/verify"`)

	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.Empty(t, *f.rows, "opening the interstitial is not a click")
}

func TestRedirectRecordsTheClickWithTheUniqueVisitorFlag(t *testing.T) {
	f := newFixture(t)

	get(t, f, "/hello", nil)
	get(t, f, "/hello", nil)

	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	var total *struct {
		clicks int64
		unique int64
	}
	for _, row := range *f.rows {
		if row.DimType == "total" {
			total = &struct {
				clicks int64
				unique int64
			}{row.Clicks, row.Unique}
		}
	}

	require.NotNil(t, total)
	require.EqualValues(t, 2, total.clicks)
	require.EqualValues(t, 1, total.unique, "the same visitor twice in a day counts once")
}

func TestRedirectRecordsNothingWhenAnalyticsAreDisabledForTheLink(t *testing.T) {
	f := newFixture(t, analyticsDisabled())

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	require.Empty(t, *f.rows, "a link with analytics disabled must record no click at all")
}

func TestRedirectRateLimitsPerClientIP(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RedirectRateLimitPerMin = 2

	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)
	require.Equal(t, http.StatusFound, get(t, f, "/hello", nil).Code)

	rec := get(t, f, "/hello", nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	other := httptest.NewRequest(http.MethodGet, "/hello", nil)
	other.Host = f.hostname
	other.Header.Set("X-Forwarded-For", "198.51.100.4")
	otherRec := httptest.NewRecorder()
	redirectRouter(f).ServeHTTP(otherRec, other)
	require.Equal(t, http.StatusFound, otherRec.Code, "a different IP has its own budget")
}

func TestRedirectRespondsInGermanWhenPreferred(t *testing.T) {
	f := newFixture(t)

	rec := get(t, f, "/nope", map[string]string{"Accept-Language": "de-DE,de;q=0.9"})

	require.Contains(t, rec.Body.String(), "nicht gefunden")
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/api/...` Expected: FAIL — `f.deps.HandleRedirect undefined`

- [ ] **Step 5: Implement the redirect handler**

Create `apps/api/internal/api/redirect.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

// HandleRedirect serves GET /{slug} on a short-link hostname. This is the hot
// path: it never waits on anything optional. Click recording goes into an
// in-memory buffer, and a Redis failure degrades the response rather than
// failing it.
func (d Deps) HandleRedirect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))

	slug := chi.URLParam(r, "slug")
	hostname := Hostname(r.Host)
	ip := ClientIP(r)
	now := d.now()

	if !d.allowRedirect(ctx, ip) {
		w.Header().Set("Retry-After", "60")
		pages.RenderError(w, http.StatusTooManyRequests, locale, pages.KindRateLimited)
		return
	}

	cacheKey := link.CacheKey(hostname, slug)
	visitor := analytics.VisitorHash(d.Config.VisitorSalt, ip, r.UserAgent(), now)
	day := analytics.Day(now)

	lookup, err := d.Cache.LookupForRedirect(ctx, cacheKey, visitor, day, d.Config.UniqueVisitorTTL)
	if err != nil {
		// Degrade to Postgres rather than failing the redirect.
		d.Log.Error("redirect cache lookup failed", "error", err, "hostname", hostname)
		lookup = cache.Lookup{}
	}

	if lookup.NegativelyCached {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return
	}

	resolved := lookup.Link
	unique := lookup.UniqueVisit

	if !lookup.Found {
		resolved, unique, err = d.resolveFromDatabase(ctx, hostname, slug, cacheKey, visitor, day)
		if errors.Is(err, pgx.ErrNoRows) {
			pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
			return
		}
		if err != nil {
			d.Log.Error("redirect database lookup failed", "error", err, "hostname", hostname, "slug", slug)
			pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
			return
		}
	}

	if status, kind, blocked := unavailable(resolved, now); blocked {
		pages.RenderError(w, status, locale, kind)
		return
	}

	if resolved.HasPassword {
		// Not a click yet — the click is recorded when the password verifies.
		pages.RenderPasswordPrompt(w, http.StatusOK, locale, "/"+slug+"/verify", false)
		return
	}

	if resolved.AnalyticsEnabled {
		d.Recorder.Record(resolved.ID, now, analytics.ExtractDimensions(r), unique)
	}
	d.writeRedirect(w, r, resolved)
}

// allowRedirect applies the per-IP redirect rate limit. It fails open: if
// Redis is unreachable the redirect still works, because availability of the
// redirect is the product. The failure is logged loudly instead.
func (d Deps) allowRedirect(ctx context.Context, ip string) bool {
	allowed, _, err := d.Cache.Allow(ctx, "rl:redirect:"+ip,
		d.Config.RedirectRateLimitPerMin, time.Minute)
	if err != nil {
		d.Log.Error("redirect rate limit unavailable, failing open", "error", err)
		return true
	}
	return allowed
}

func (d Deps) resolveFromDatabase(
	ctx context.Context,
	hostname, slug, cacheKey, visitor, day string,
) (link.Cached, bool, error) {
	row, err := d.Queries.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: hostname,
		Slug:     slug,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if putErr := d.Cache.PutNotFound(ctx, cacheKey, d.Config.NotFoundCacheTTL); putErr != nil {
			d.Log.Error("negative cache write failed", "error", putErr)
		}
		return link.Cached{}, false, err
	}
	if err != nil {
		return link.Cached{}, false, err
	}

	resolved := link.Cached{
		ID:             row.ID,
		TeamID:         row.TeamID,
		DestinationURL: row.DestinationURL,
		RedirectType:   int(row.RedirectType),
		State:          row.State,
		ExpiresAt:      row.ExpiresAt,
		HasPassword:    row.HasPassword,

		AnalyticsEnabled: row.AnalyticsEnabled,
	}

	if err := d.Cache.PutLink(ctx, cacheKey, resolved, d.Config.LinkCacheTTL); err != nil {
		d.Log.Error("link cache write failed", "error", err)
	}

	// The lookup script had no link id to deduplicate against on a miss, so
	// the visitor is recorded here instead.
	unique, err := d.Cache.MarkUniqueVisit(ctx, resolved.ID.String(), day, visitor, d.Config.UniqueVisitorTTL)
	if err != nil {
		d.Log.Error("unique-visitor dedup failed", "error", err)
	}

	return resolved, unique, nil
}

// unavailable reports whether a link must not be followed, and with what.
func unavailable(l link.Cached, now time.Time) (int, pages.Kind, bool) {
	if l.ExpiresAt != nil && !now.Before(*l.ExpiresAt) {
		return http.StatusGone, pages.KindExpired, true
	}

	switch l.State {
	case "active":
		return 0, "", false
	case "disabled":
		return http.StatusGone, pages.KindDisabled, true
	case "expired":
		return http.StatusGone, pages.KindExpired, true
	case "flagged":
		return http.StatusForbidden, pages.KindFlagged, true
	default:
		return http.StatusGone, pages.KindDisabled, true
	}
}

func (d Deps) writeRedirect(w http.ResponseWriter, r *http.Request, l link.Cached) {
	status := http.StatusFound
	if l.RedirectType == http.StatusMovedPermanently {
		status = http.StatusMovedPermanently
	} else {
		// A 302 promises a fresh lookup every time; say so explicitly rather
		// than leaving it to an intermediary's heuristics.
		w.Header().Set("Cache-Control", "no-store")
	}

	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, l.DestinationURL, status)
}
```

- [ ] **Step 6: Run the redirect tests**

Run: `cd apps/api && go test ./internal/api/...` Expected: PASS (14 tests, including the three state sub-tests)

- [ ] **Step 7: Run the whole suite and vet**

Run: `cd apps/api && go vet ./... && go test ./... && golangci-lint run` Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add apps/api
git commit -m "feat(api): add the cached, rate-limited redirect handler"
```

---

## Task 11: Argon2id password protection and the verify endpoints

**Files:**

- Create: `apps/api/internal/auth/password.go`
- Create: `apps/api/internal/api/verify.go`
- Test: `apps/api/internal/auth/password_test.go`
- Test: `apps/api/internal/api/verify_test.go`

**Interfaces:**

- Consumes: `pages` (T9), `Deps` (T10), `db.GetLinkForVerify` (T3)
- Produces:
  - `auth.HashPassword(plain string) (string, error)` — PHC-encoded Argon2id
  - `auth.VerifyPassword(encoded, plain string) (bool, error)`
  - `(Deps).HandleVerifyForm(w, r)` — `GET /{slug}/verify`
  - `(Deps).HandleVerifySubmit(w, r)` — `POST /{slug}/verify`

- [ ] **Step 1: Add the crypto dependency**

```bash
cd apps/api
go get golang.org/x/crypto
go mod tidy
```

- [ ] **Step 2: Write the failing password test**

Create `apps/api/internal/auth/password_test.go`:

```go
package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

func TestHashPasswordProducesAPHCEncodedArgon2idString(t *testing.T) {
	encoded, err := auth.HashPassword("correct horse battery staple")

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"), "got %q", encoded)
	require.Len(t, strings.Split(encoded, "$"), 6)
	require.NotContains(t, encoded, "correct horse battery staple")
}

func TestHashPasswordSaltsEveryHash(t *testing.T) {
	first, err := auth.HashPassword("same password")
	require.NoError(t, err)
	second, err := auth.HashPassword("same password")
	require.NoError(t, err)

	require.NotEqual(t, first, second, "two hashes of one password must differ")
}

func TestVerifyPasswordAcceptsTheCorrectPassword(t *testing.T) {
	encoded, err := auth.HashPassword("s3cret")
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "s3cret")

	require.NoError(t, err)
	require.True(t, ok)
}

func TestVerifyPasswordRejectsTheWrongPassword(t *testing.T) {
	encoded, err := auth.HashPassword("s3cret")
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "S3cret")

	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerifyPasswordRejectsAMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=19456,t=2,p=1$",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	} {
		ok, err := auth.VerifyPassword(encoded, "anything")

		require.Error(t, err, "encoded = %q", encoded)
		require.False(t, ok)
	}
}

func TestVerifyPasswordRoundTripsParametersFromTheEncodedHash(t *testing.T) {
	// A hash written with different parameters must still verify, so the
	// parameters can be raised later without invalidating existing hashes.
	encoded, err := auth.HashPasswordWithParams("legacy", auth.Params{Memory: 8192, Time: 1, Threads: 1, KeyLength: 32, SaltLength: 16})
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "legacy")

	require.NoError(t, err)
	require.True(t, ok)
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/auth/...` Expected: FAIL — package `auth` does not exist

- [ ] **Step 4: Implement Argon2id hashing**

Create `apps/api/internal/auth/password.go`:

```go
// Package auth owns credential verification: link passwords and Supabase JWTs.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash is returned when a stored hash cannot be parsed. It always
// means the stored value is wrong, never that the password was.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// Params are the Argon2id cost parameters. They are stored inside every hash,
// so raising them later does not invalidate existing passwords.
type Params struct {
	Memory     uint32
	Time       uint32
	Threads    uint8
	KeyLength  uint32
	SaltLength uint32
}

// DefaultParams follows OWASP's second recommended Argon2id configuration
// (19 MiB, 2 iterations, 1 degree of parallelism). The lower-memory variant is
// the right pick on serverless, where a 46 MiB allocation per verification
// would be a real cold-start and concurrency cost, and the tight per-link rate
// limit on the verify endpoint carries the rest of the brute-force defence.
var DefaultParams = Params{Memory: 19456, Time: 2, Threads: 1, KeyLength: 32, SaltLength: 16}

// HashPassword hashes a link password with the default parameters.
func HashPassword(plain string) (string, error) {
	return HashPasswordWithParams(plain, DefaultParams)
}

// HashPasswordWithParams hashes with explicit parameters, returning a
// PHC-format string: $argon2id$v=19$m=...,t=...,p=...$salt$hash
func HashPasswordWithParams(plain string, p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, p.Time, p.Memory, p.Threads, p.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a plaintext password against a PHC-encoded hash using
// the parameters recorded in the hash itself. The comparison is constant time.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}

	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}
	if len(salt) == 0 || len(want) == 0 {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(plain), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

- [ ] **Step 5: Run the password tests**

Run: `cd apps/api && go test ./internal/auth/...` Expected: PASS (6 tests)

- [ ] **Step 6: Write the failing verify-endpoint test**

Create `apps/api/internal/api/verify_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

func verifyRouter(f *fixture) http.Handler {
	r := chi.NewRouter()
	r.Get("/{slug}", f.deps.HandleRedirect)
	r.Get("/{slug}/verify", f.deps.HandleVerifyForm)
	r.Post("/{slug}/verify", f.deps.HandleVerifySubmit)
	return r
}

func protectedFixture(t *testing.T, password string) *fixture {
	t.Helper()
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	return newFixture(t, withPasswordHash(hash))
}

func postPassword(t *testing.T, f *fixture, slug, password, ip string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/"+slug+"/verify", strings.NewReader(form.Encode()))
	req.Host = f.hostname
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	verifyRouter(f).ServeHTTP(rec, req)
	return rec
}

func TestVerifyFormRendersThePrompt(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	req := httptest.NewRequest(http.MethodGet, "/hello/verify", nil)
	req.Host = f.hostname
	rec := httptest.NewRecorder()
	verifyRouter(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `action="/hello/verify"`)
}

func TestVerifySubmitRedirectsOnTheCorrectPassword(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "hunter2", "203.0.113.1")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"))
}

func TestVerifySubmitRecordsTheClickOnlyOnSuccess(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	postPassword(t, f, "hello", "wrong", "203.0.113.1")
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))
	require.Empty(t, *f.rows, "a failed attempt is not a click")

	postPassword(t, f, "hello", "hunter2", "203.0.113.1")
	require.NoError(t, f.deps.Recorder.Flush(context.Background()))

	var totals int64
	for _, row := range *f.rows {
		if row.DimType == "total" {
			totals += row.Clicks
		}
	}
	require.EqualValues(t, 1, totals)
}

func TestVerifySubmitRerendersWithAnErrorOnTheWrongPassword(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "nope", "203.0.113.1")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, strings.ToLower(rec.Body.String()), "incorrect")
}

func TestVerifySubmitNeverLeaksTheHash(t *testing.T) {
	f := protectedFixture(t, "hunter2")

	rec := postPassword(t, f, "hello", "nope", "203.0.113.1")

	require.NotContains(t, rec.Body.String(), "$argon2id$")
	require.NotContains(t, rec.Body.String(), "hunter2")
}

func TestVerifySubmitRateLimitsTightlyPerLinkAndIP(t *testing.T) {
	f := protectedFixture(t, "hunter2")
	f.deps.Config.PasswordRateLimitPerMin = 3

	for range 3 {
		require.Equal(t, http.StatusUnauthorized,
			postPassword(t, f, "hello", "wrong", "203.0.113.1").Code)
	}

	require.Equal(t, http.StatusTooManyRequests,
		postPassword(t, f, "hello", "wrong", "203.0.113.1").Code,
		"the fourth attempt from one IP must be blocked")

	require.Equal(t, http.StatusUnauthorized,
		postPassword(t, f, "hello", "wrong", "198.51.100.9").Code,
		"a different IP has its own budget")
}

func TestVerifyOnAnUnprotectedLinkIsNotFound(t *testing.T) {
	f := newFixture(t)

	require.Equal(t, http.StatusNotFound,
		postPassword(t, f, "hello", "anything", "203.0.113.1").Code)
}

func TestVerifyOnAnInactiveLinkIsRefusedBeforeCheckingThePassword(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	require.NoError(t, err)
	f := newFixture(t, withPasswordHash(hash), withState("flagged"))

	require.Equal(t, http.StatusForbidden,
		postPassword(t, f, "hello", "hunter2", "203.0.113.1").Code)
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/api/...` Expected: FAIL — `f.deps.HandleVerifyForm undefined`

- [ ] **Step 8: Implement the verify handlers**

Create `apps/api/internal/api/verify.go`:

```go
package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

// HandleVerifyForm serves GET /{slug}/verify. It exists so a bookmarked or
// reloaded interstitial still works; GET /{slug} renders the same form.
func (d Deps) HandleVerifyForm(w http.ResponseWriter, r *http.Request) {
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))
	slug := chi.URLParam(r, "slug")

	if _, _, ok := d.loadProtectedLink(w, r, locale, slug); !ok {
		return
	}

	pages.RenderPasswordPrompt(w, http.StatusOK, locale, "/"+slug+"/verify", false)
}

// HandleVerifySubmit serves POST /{slug}/verify. It carries its own, much
// tighter rate limit than the redirect path: Argon2id alone is not enough
// against a short password, so the number of guesses is capped per link and
// client address.
func (d Deps) HandleVerifySubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))
	slug := chi.URLParam(r, "slug")
	hostname := Hostname(r.Host)
	ip := ClientIP(r)
	now := d.now()

	rateKey := "rl:pwverify:" + hostname + ":" + slug + ":" + ip
	allowed, _, err := d.Cache.Allow(ctx, rateKey, d.Config.PasswordRateLimitPerMin, time.Minute)
	if err != nil {
		// Unlike the redirect path this fails closed: an unbounded number of
		// password guesses is a worse outcome than a temporarily unusable
		// protected link.
		d.Log.Error("password rate limit unavailable, failing closed", "error", err)
		allowed = false
	}
	if !allowed {
		w.Header().Set("Retry-After", "60")
		pages.RenderError(w, http.StatusTooManyRequests, locale, pages.KindRateLimited)
		return
	}

	resolved, hash, ok := d.loadProtectedLink(w, r, locale, slug)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}

	valid, err := auth.VerifyPassword(hash, r.PostFormValue("password"))
	if err != nil {
		// A malformed stored hash is our bug, not a wrong password.
		d.Log.Error("stored password hash is unusable", "link_id", resolved.ID, "error", err)
		pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
		return
	}
	if !valid {
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}

	if resolved.AnalyticsEnabled {
		unique, err := d.Cache.MarkUniqueVisit(ctx, resolved.ID.String(), analytics.Day(now),
			analytics.VisitorHash(d.Config.VisitorSalt, ip, r.UserAgent(), now),
			d.Config.UniqueVisitorTTL)
		if err != nil {
			d.Log.Error("unique-visitor dedup failed", "error", err)
		}
		d.Recorder.Record(resolved.ID, now, analytics.ExtractDimensions(r), unique)
	}

	d.writeRedirect(w, r, resolved)
}

// loadProtectedLink reads the link straight from Postgres — the password hash
// is deliberately never cached, so the verify path cannot use the link cache.
// It writes the response itself and reports false when the caller must stop.
func (d Deps) loadProtectedLink(
	w http.ResponseWriter,
	r *http.Request,
	locale pages.Locale,
	slug string,
) (link.Cached, string, bool) {
	row, err := d.Queries.GetLinkForVerify(r.Context(), db.GetLinkForVerifyParams{
		Hostname: Hostname(r.Host),
		Slug:     slug,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return link.Cached{}, "", false
	}
	if err != nil {
		d.Log.Error("verify database lookup failed", "error", err, "slug", slug)
		pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
		return link.Cached{}, "", false
	}

	// Verifying a password against a link that has none is meaningless; treat
	// it as a missing page rather than revealing that the link exists.
	if row.PasswordHash == nil || *row.PasswordHash == "" {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return link.Cached{}, "", false
	}

	resolved := link.Cached{
		ID:             row.ID,
		TeamID:         row.TeamID,
		DestinationURL: row.DestinationURL,
		RedirectType:   int(row.RedirectType),
		State:          row.State,
		ExpiresAt:      row.ExpiresAt,
		HasPassword:    true,

		AnalyticsEnabled: row.AnalyticsEnabled,
	}

	// State is checked before the password so a disabled or flagged link never
	// becomes an oracle for guessing its password.
	if status, kind, blocked := unavailable(resolved, d.now()); blocked {
		pages.RenderError(w, status, locale, kind)
		return link.Cached{}, "", false
	}

	return resolved, *row.PasswordHash, true
}
```

- [ ] **Step 9: Run the verify tests**

Run: `cd apps/api && go test ./internal/api/...` Expected: PASS (22 tests)

- [ ] **Step 10: Commit**

```bash
git add apps/api
git commit -m "feat(auth): add Argon2id link passwords and the verify interstitial"
```

---

## Task 12: JWKS verification and the Huma `/v1` surface

**Files:**

- Create: `apps/api/internal/auth/jwt.go`
- Create: `apps/api/internal/api/v1.go`
- Modify: `apps/api/internal/api/api.go` (add the `Verifier` field)
- Test: `apps/api/internal/auth/jwt_test.go`
- Test: `apps/api/internal/api/v1_test.go`

**Interfaces:**

- Consumes: `config.Config.JWKSURL/JWTIssuer/JWTAudience` (T1), `Deps` (T10)
- Produces:
  - `auth.Claims` struct: `UserID uuid.UUID`, `Email string`
  - `auth.NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*auth.Verifier, error)`
  - `(*auth.Verifier).Verify(ctx context.Context, rawToken string) (Claims, error)`
  - `(Deps).RegisterV1(api huma.API)` — registers `GET /v1/me` and the bearer security scheme
  - `api.NewHumaConfig() huma.Config` — the OpenAPI 3.1 document config, including the `bearerAuth` scheme
  - `api.UserFromContext(ctx context.Context) (auth.Claims, bool)`

Supabase issues **ES256** tokens signed with asymmetric keys published at the project's JWKS endpoint. The legacy HS256 shared secret is not used, so no signing secret ever reaches this backend.

- [ ] **Step 1: Add the JWT dependencies**

```bash
cd apps/api
go get github.com/golang-jwt/jwt/v5
go get github.com/MicahParks/keyfunc/v3
go get github.com/danielgtaylor/huma/v2
go mod tidy
```

- [ ] **Step 2: Write the failing JWT test**

Create `apps/api/internal/auth/jwt_test.go`:

```go
package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

const (
	testKID      = "test-key-1"
	testIssuer   = "https://project.supabase.co/auth/v1"
	testAudience = "authenticated"
)

// jwksServer serves a JWKS document for a freshly generated P-256 key and
// returns the private half for signing test tokens.
func jwksServer(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	document := map[string]any{"keys": []map[string]string{{
		"kty": "EC",
		"crv": "P-256",
		"kid": testKID,
		"alg": "ES256",
		"use": "sig",
		"x":   base64.RawURLEncoding.EncodeToString(key.PublicKey.X.FillBytes(make([]byte, 32))),
		"y":   base64.RawURLEncoding.EncodeToString(key.PublicKey.Y.FillBytes(make([]byte, 32))),
	}}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)

	return key, server.URL
}

func sign(t *testing.T, key *ecdsa.PrivateKey, claims jwt.MapClaims, alg jwt.SigningMethod) string {
	t.Helper()
	token := jwt.NewWithClaims(alg, claims)
	token.Header["kid"] = testKID
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func validClaims(subject string) jwt.MapClaims {
	return jwt.MapClaims{
		"sub":   subject,
		"iss":   testIssuer,
		"aud":   testAudience,
		"email": "member@verein.test",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
}

func newVerifier(t *testing.T, jwksURL string) *auth.Verifier {
	t.Helper()
	v, err := auth.NewVerifier(context.Background(), jwksURL, testIssuer, testAudience)
	require.NoError(t, err)
	return v
}

func TestVerifyAcceptsAValidES256Token(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)
	subject := uuid.NewString()

	claims, err := verifier.Verify(context.Background(),
		sign(t, key, validClaims(subject), jwt.SigningMethodES256))

	require.NoError(t, err)
	require.Equal(t, subject, claims.UserID.String())
	require.Equal(t, "member@verein.test", claims.Email)
}

func TestVerifyRejectsAnExpiredToken(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["exp"] = time.Now().Add(-time.Minute).Unix()

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheWrongIssuer(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["iss"] = "https://attacker.example/auth/v1"

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheWrongAudience(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims(uuid.NewString())
	claims["aud"] = "someone-else"

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsATokenSignedByAnotherKey(t *testing.T) {
	_, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	attackerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(),
		sign(t, attackerKey, validClaims(uuid.NewString()), jwt.SigningMethodES256))

	require.Error(t, err)
}

func TestVerifyRejectsTheNoneAlgorithm(t *testing.T) {
	_, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(uuid.NewString()))
	token.Header["kid"] = testKID
	raw, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = verifier.Verify(context.Background(), raw)

	require.Error(t, err, "alg=none must never be accepted")
}

func TestVerifyRejectsANonUUIDSubject(t *testing.T) {
	key, jwksURL := jwksServer(t)
	verifier := newVerifier(t, jwksURL)

	claims := validClaims("not-a-uuid")

	_, err := verifier.Verify(context.Background(), sign(t, key, claims, jwt.SigningMethodES256))

	require.Error(t, err)
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/auth/...` Expected: FAIL — `undefined: auth.NewVerifier`

- [ ] **Step 4: Implement the verifier**

Create `apps/api/internal/auth/jwt.go`:

```go
package auth

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the part of a Supabase access token this API acts on.
type Claims struct {
	UserID uuid.UUID
	Email  string
}

// Verifier validates Supabase-issued access tokens against the project's
// published JWKS. Keys are fetched and cached in the background, so a valid
// request costs no network call; the backend holds no signing secret at all.
type Verifier struct {
	keyfunc  keyfunc.Keyfunc
	issuer   string
	audience string
}

// NewVerifier starts the JWKS cache for jwksURL. The context governs the
// background refresh, so it should live as long as the process.
func NewVerifier(ctx context.Context, jwksURL, issuer, audience string) (*Verifier, error) {
	// All three are required together. Making issuer or audience optional
	// would silently disable that check for an operator who sets the JWKS URL
	// and forgets the rest — a validly-signed token from any issuer would then
	// be accepted. doc 06 requires issuer, audience and expiry to be checked.
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: jwks url is required")
	}
	if issuer == "" {
		return nil, fmt.Errorf("auth: jwt issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("auth: jwt audience is required")
	}

	k, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("auth: create jwks cache: %w", err)
	}

	return &Verifier{keyfunc: k, issuer: issuer, audience: audience}, nil
}

// supabaseClaims adds the one non-registered claim this API reads.
type supabaseClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

// Verify parses and validates a raw bearer token. Only ES256 is accepted:
// pinning the algorithm is what stops an attacker downgrading to alg=none or
// to HS256 keyed with the published public key.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	}

	claims := &supabaseClaims{}

	token, err := jwt.ParseWithClaims(rawToken, claims, v.keyfunc.KeyfuncCtx(ctx), options...)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify token: %w", err)
	}
	if !token.Valid {
		return Claims{}, fmt.Errorf("auth: token is not valid")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: subject is not a user id: %w", err)
	}

	return Claims{UserID: userID, Email: claims.Email}, nil
}
```

- [ ] **Step 5: Run the JWT tests**

Run: `cd apps/api && go test ./internal/auth/...` Expected: PASS (13 tests)

- [ ] **Step 6: Add the verifier to Deps**

In `apps/api/internal/api/api.go`, add the field and its import:

```go
	Recorder *analytics.Recorder
	Verifier *auth.Verifier
	Log      *slog.Logger
```

```go
	"github.com/mheob/kurze-url/apps/api/internal/auth"
```

- [ ] **Step 7: Write the failing `/v1` test**

Create `apps/api/internal/api/v1_test.go`:

```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func v1Router(f *fixture) http.Handler {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	f.deps.RegisterV1(humaAPI)
	return router
}

func TestMeRejectsAMissingBearerToken(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json",
		"errors must use Huma's RFC 9457 default, not a custom model")
}

func TestMeRejectsAGarbageBearerToken(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenAPIDocumentDeclaresTheBearerScheme(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "bearerAuth")
	require.Contains(t, rec.Body.String(), "3.1.")
}

func TestOpenAPIDocumentExcludesTheRedirectSurface(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	body := rec.Body.String()
	require.NotContains(t, body, "{slug}",
		"the public redirect surface is deliberately not part of the machine contract")
}

func TestHealthIsUnauthenticated(t *testing.T) {
	f := newFixture(t)

	rec := httptest.NewRecorder()
	v1Router(f).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
}
```

- [ ] **Step 8: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/api/...` Expected: FAIL — `undefined: api.NewHumaConfig`

- [ ] **Step 9: Implement the `/v1` surface**

Create `apps/api/internal/api/v1.go`:

```go
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

type contextKey struct{}

var userContextKey = contextKey{}

// NewHumaConfig builds the OpenAPI 3.1 document config. The bearer scheme is
// declared once here; individual operations opt in via Security, and the
// middleware enforces it only where declared.
func NewHumaConfig() huma.Config {
	config := huma.DefaultConfig("kurze-url API", "1.0.0")
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "A Supabase-issued access token (ES256, verified against the project's JWKS).",
		},
	}
	return config
}

// MeOutput is the body of GET /v1/me. Team memberships join it in plan 2.
type MeOutput struct {
	Body struct {
		UserID uuid.UUID `json:"user_id"`
		Email  string    `json:"email"`
	}
}

// HealthOutput is the body of GET /v1/health.
type HealthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// RegisterV1 mounts the versioned JSON API. Only routes registered here appear
// in the OpenAPI document; the redirect surface is deliberately absent.
func (d Deps) RegisterV1(api huma.API) {
	api.UseMiddleware(d.authMiddleware(api))

	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/v1/health",
		Summary:     "Liveness probe",
		Tags:        []string{"Meta"},
	}, func(_ context.Context, _ *struct{}) (*HealthOutput, error) {
		out := &HealthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-me",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "The authenticated user",
		Tags:        []string{"Session"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		claims, ok := UserFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		out := &MeOutput{}
		out.Body.UserID = claims.UserID
		out.Body.Email = claims.Email
		return out, nil
	})
}

// authMiddleware enforces the bearer scheme on exactly the operations that
// declare it. Operations without a Security block stay open by design.
func (d Deps) authMiddleware(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !requiresBearer(ctx.Operation()) {
			next(ctx)
			return
		}

		if d.Verifier == nil {
			d.Log.Error("authenticated operation reached with no JWT verifier configured",
				"operation", ctx.Operation().OperationID)
			huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication is not configured")
			return
		}

		token := bearerToken(ctx.Header("Authorization"))
		if token == "" {
			huma.WriteErr(api, ctx, http.StatusUnauthorized, "missing bearer token")
			return
		}

		claims, err := d.Verifier.Verify(ctx.Context(), token)
		if err != nil {
			// The reason is logged, never returned: a precise error tells an
			// attacker which part of the token to fix.
			d.Log.Warn("bearer token rejected", "error", err)
			huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid bearer token")
			return
		}

		next(huma.WithValue(ctx, userContextKey, claims))
	}
}

func requiresBearer(operation *huma.Operation) bool {
	for _, scheme := range operation.Security {
		if _, ok := scheme["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

func bearerToken(header string) string {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// UserFromContext returns the verified claims a bearer-authenticated operation
// was called with.
func UserFromContext(ctx context.Context) (auth.Claims, bool) {
	claims, ok := ctx.Value(userContextKey).(auth.Claims)
	return claims, ok
}
```

Add the `humachi` adapter dependency the test needs:

```bash
cd apps/api && go get github.com/danielgtaylor/huma/v2/adapters/humachi && go mod tidy
```

- [ ] **Step 10: Run the tests**

Run: `cd apps/api && go test ./internal/...` Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add apps/api
git commit -m "feat(api): verify Supabase JWTs via JWKS and register the /v1 surface"
```

---

## Task 13: Hostname routing, full wiring and the Vercel deployment config

**Files:**

- Create: `apps/api/internal/api/router.go`
- Modify: `apps/api/cmd/api/main.go` (replace `run` and delete `healthHandler`)
- Modify: `apps/api/cmd/api/main_test.go` (delete — the health route now lives in the router)
- Create: `apps/api/.env.example`
- Create: `apps/api/vercel.json`
- Modify: `README.md` (add a "Running the API locally" section)
- Test: `apps/api/internal/api/router_test.go`

**Interfaces:**

- Consumes: everything from Tasks 1–12
- Produces: `api.NewRouter(deps Deps) http.Handler` — dispatches on the request's Host

- [ ] **Step 1: Write the failing router test**

Create `apps/api/internal/api/router_test.go`:

```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func requestTo(t *testing.T, handler http.Handler, host, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRouterServesV1OnlyOnTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	require.Equal(t, http.StatusOK, requestTo(t, handler, "api.test", "/v1/health").Code)
}

func TestRouterDoesNotExposeV1OnAShortLinkHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, f.hostname, "/v1/health")

	require.NotEqual(t, http.StatusOK, rec.Code,
		"a team's custom domain must not serve the JSON API")
}

func TestRouterServesRedirectsOnAShortLinkHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, f.hostname, "/hello")

	require.Equal(t, http.StatusFound, rec.Code)
}

func TestRouterDoesNotServeRedirectsOnTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	rec := requestTo(t, handler, "api.test", "/hello")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRouterIgnoresThePortWhenMatchingTheAPIHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "localhost"
	handler := api.NewRouter(f.deps)

	require.Equal(t, http.StatusOK, requestTo(t, handler, "localhost:8080", "/v1/health").Code)
}

func TestRouterServesHealthOnEveryHostname(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.APIHostname = "api.test"
	handler := api.NewRouter(f.deps)

	// The uptime monitor and Vercel's own checks hit whichever hostname they
	// are pointed at, so /health must answer on all of them.
	require.Equal(t, http.StatusOK, requestTo(t, handler, "api.test", "/health").Code)
	require.Equal(t, http.StatusOK, requestTo(t, handler, f.hostname, "/health").Code)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/api/...` Expected: FAIL — `undefined: api.NewRouter`

- [ ] **Step 3: Implement the router**

Create `apps/api/internal/api/router.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the single handler the server runs. Two surfaces share one
// process, separated by hostname: the JSON API answers only on the configured
// API hostname, and every other hostname is treated as a short-link domain.
//
// The split is what keeps /v1 off a team's custom domain and keeps the
// redirect routes off the API's own domain, where a slug could otherwise
// shadow a future API path.
func NewRouter(deps Deps) http.Handler {
	apiHost := Hostname(deps.Config.APIHostname)

	apiSurface := chi.NewRouter()
	apiSurface.Use(middleware.RealIP, middleware.Recoverer)
	deps.RegisterV1(humachi.New(apiSurface, NewHumaConfig()))

	redirectSurface := chi.NewRouter()
	redirectSurface.Use(middleware.Recoverer)
	redirectSurface.Get("/{slug}", deps.HandleRedirect)
	redirectSurface.Get("/{slug}/verify", deps.HandleVerifyForm)
	redirectSurface.Post("/{slug}/verify", deps.HandleVerifySubmit)

	root := chi.NewRouter()
	// /health answers on every hostname: the uptime monitor and the platform's
	// own checks do not know which one they are hitting.
	root.Get("/health", plainHealth)
	root.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		if Hostname(r.Host) == apiHost {
			apiSurface.ServeHTTP(w, r)
			return
		}
		redirectSurface.ServeHTTP(w, r)
	})

	return root
}

func plainHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Run the router tests**

Run: `cd apps/api && go test ./internal/api/...` Expected: PASS

- [ ] **Step 5: Wire everything together in main.go**

Replace the whole of `apps/api/cmd/api/main.go` with:

```go
// Command api is the single entrypoint Vercel's Go Framework Preset detects.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

const (
	clickFlushInterval = 5 * time.Second
	clickBufferMax     = 5000
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redis, err := cache.New(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()

	queries := db.New(pool)
	recorder := analytics.NewRecorder(clickStatsFlush(queries), clickFlushInterval, clickBufferMax, log)

	deps := api.Deps{
		Config:   cfg,
		Queries:  queries,
		Cache:    redis,
		Recorder: recorder,
		Log:      log,
	}

	// Authentication is optional at startup so the redirect surface stays
	// runnable locally without a Supabase project. /v1 operations that declare
	// bearerAuth reject every request until it is configured.
	if cfg.JWKSURL != "" {
		verifier, err := auth.NewVerifier(ctx, cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
		if err != nil {
			return err
		}
		deps.Verifier = verifier
	} else {
		log.Warn("SUPABASE_JWKS_URL is unset — authenticated /v1 operations will reject all requests")
	}

	recorderDone := make(chan struct{})
	go func() {
		recorder.Run(ctx)
		close(recorderDone)
	}()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.NewRouter(deps),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "port", cfg.Port, "api_hostname", cfg.APIHostname)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)

	// The recorder flushes whatever is still buffered when ctx is cancelled;
	// wait for that before the process exits, or those clicks are lost.
	select {
	case <-recorderDone:
	case <-shutdownCtx.Done():
		log.Warn("timed out waiting for the final click-stats flush")
	}

	return shutdownErr
}

// clickStatsFlush adapts the recorder's rows onto the generated batch upsert.
func clickStatsFlush(queries *db.Queries) analytics.FlushFunc {
	return func(ctx context.Context, rows []analytics.Row) error {
		params := make([]db.UpsertClickStatsParams, 0, len(rows))
		for _, row := range rows {
			params = append(params, db.UpsertClickStatsParams{
				LinkID:         row.LinkID,
				BucketStart:    row.Day,
				DimensionType:  row.DimType,
				DimensionValue: row.DimValue,
				Clicks:         row.Clicks,
				UniqueVisitors: row.Unique,
			})
		}

		var firstErr error
		results := queries.UpsertClickStats(ctx, params)
		results.Exec(func(_ int, err error) {
			if err != nil && firstErr == nil {
				firstErr = err
			}
		})
		if err := results.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
}
```

- [ ] **Step 6: Delete the superseded health test**

```bash
rm apps/api/cmd/api/main_test.go
```

The health route is now covered by `TestRouterServesHealthOnEveryHostname`.

- [ ] **Step 7: Write the environment template**

Create `apps/api/.env.example`:

```bash
# Never commit real values. Production values live in Vercel's project
# environment variables; local values belong in apps/api/.env (gitignored).

PORT=8080

# The single hostname that serves /v1. Every other Host header is treated as a
# short-link domain and routed to the redirect surface.
API_HOSTNAME=localhost

# Supabase Postgres. Locally this is what `supabase start` prints; in
# production use the Supavisor transaction pooler (port 6543), not a direct
# connection — serverless instances open far more connections than the free
# tier's direct limit allows.
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres

# Upstash Redis. Use the rediss:// URL in production.
REDIS_URL=redis://127.0.0.1:6379

# Supabase auth. ES256 via JWKS — never the legacy HS256 shared secret.
SUPABASE_JWKS_URL=https://<project>.supabase.co/auth/v1/.well-known/jwks.json
SUPABASE_JWT_ISSUER=https://<project>.supabase.co/auth/v1
SUPABASE_JWT_AUDIENCE=authenticated

# Keys the daily-rotating visitor hash. Rotating this resets unique-visitor
# counts for the current day; it is never logged and never leaves the process.
VISITOR_SALT=

# Requests per minute. Redirect is per client IP; password is per link per IP.
RATE_LIMIT_REDIRECT_PER_MIN=60
RATE_LIMIT_PASSWORD_PER_MIN=5
RATE_LIMIT_LINK_CREATE_PER_MIN=20
```

- [ ] **Step 8: Write the Vercel config**

Create `apps/api/vercel.json`:

```json
{
	"$schema": "https://openapi.vercel.sh/vercel.json",
	"framework": null,
	"buildCommand": "go build -o bin/api ./cmd/api",
	"regions": ["fra1"]
}
```

Two settings that cannot live in this file and must be set in the Vercel project's dashboard, both documented in `docs/planning/07-repo-structure-and-tooling.md`:

- **Root Directory** = `apps/api`
- **Ignored Build Step** = `git diff --quiet HEAD^ HEAD -- apps/api supabase` — `apps/api` is Go, so it gets none of Vercel's automatic pnpm-workspace build skipping.

- [ ] **Step 9: Document local development in the README**

Append to `README.md`:

````markdown
## Running the API locally

Prerequisites: Go 1.27+, Docker, the [Supabase CLI](https://supabase.com/docs/guides/cli), and [sqlc](https://sqlc.dev).

```bash
# 1. Start Postgres (with auth) and Redis
supabase start
docker run -d --name kurze-url-redis -p 6379:6379 redis:7-alpine

# 2. Apply migrations and the local seed
supabase db reset

# 3. Configure and run the API
cp apps/api/.env.example apps/api/.env   # then set VISITOR_SALT
cd apps/api && go run ./cmd/api
```
````

The seed creates a verified `short.test` domain with a `hello` link, so:

```bash
curl -i -H 'Host: short.test' http://localhost:8080/hello   # 302 to example.org
curl -i http://localhost:8080/v1/health                     # 200, API surface
```

Regenerate the database layer after changing a migration or a query:

```bash
cd apps/api && sqlc generate
```

````

- [ ] **Step 10: Run the whole suite, vet and lint**

Run: `cd apps/api && go vet ./... && go test ./... && golangci-lint run`
Expected: PASS

- [ ] **Step 11: Verify the server actually starts and redirects**

```bash
cd apps/api && go run ./cmd/api &
sleep 2
curl -si -H 'Host: short.test' http://localhost:8080/hello | head -3
curl -si http://localhost:8080/v1/health | head -3
curl -si http://localhost:8080/openapi.json | head -3
kill %1
````

Expected: `HTTP/1.1 302 Found` with `Location: https://example.org/hello`; `HTTP/1.1 200 OK` for both others.

- [ ] **Step 12: Commit**

```bash
git add apps/api README.md
git commit -m "feat(api): route by hostname, wire the server and add the Vercel config"
```

---

## Definition of done

- [ ] `cd apps/api && go vet ./... && go test ./... && golangci-lint run` is clean.
- [ ] `GET /<slug>` on a short-link hostname redirects with the link's own status code, serves from Redis on the second request, and records a click without the response waiting on it.
- [ ] An unknown slug is negatively cached; an unverified domain never resolves.
- [ ] Disabled, expired and flagged links are refused with distinct statuses and localised pages.
- [ ] A password-protected link shows the interstitial, verifies against Argon2id, and is capped at 5 attempts per minute per link per IP.
- [ ] No raw IP address is written to Postgres, to a log line, or to Redis.
- [ ] `/v1` answers only on the API hostname; the redirect surface answers only off it; `/openapi.json` declares `bearerAuth` and contains no `{slug}` path.
- [ ] `.env.example` documents every variable with no real values in it.

## Explicitly not in this plan

These belong to plan 2 (core API) and must not be skipped there:

- **HTTPS-only URL scheme allowlist and SSRF protection with DNS-rebinding re-checks** — they attach to link creation, which does not exist yet. Plan 2 cannot ship link creation without them.
- **Async Google Safe Browsing scanning** — writes `link_scan_result` and flips `link.state` to `flagged`, which the redirect path built here already honours.
- **`cache.InvalidateLink` callers** — every link mutation in plan 2 must invalidate `link.CacheKey(hostname, slug)`, or a 302's promise of an immediate destination change is only true after `LinkCacheTTL`.
- **The `?qr=1` marker** — plan 2's QR generator must append it to the short URL it encodes, or the `qr_vs_regular` dimension records every scan as `regular`.
- **`audit_log` writes** — the table exists; the action taxonomy is still an open item in `CLAUDE.md` and falls out of plan 2's endpoint list.
- **`link_create` rate limiting** — the value is configured (`RATE_LIMIT_LINK_CREATE_PER_MIN=20`) and `cache.Allow` is built; the endpoint that consumes it is in plan 2.
- **The 90-day analytics retention job** — `docs/planning/01-architecture.md` commits to automatic deletion after 90 days. It is a scheduled `delete from link_click_stats where bucket_start < current_date - 90` behind the Vercel Cron surface that plan 5 owns (the same cron that does the Supabase keep-alive ping and the free-tier threshold checks), and it needs that surface's authentication designed alongside it. **Until plan 5 ships it, the retention promise is unmet** — it must not slip past the instance opening to real Vereine.

## Open items this plan does not resolve

Flagged rather than silently answered, per `CLAUDE.md`:

- **Backups.** Supabase's free tier provides none. A scheduled `supabase db dump` to off-site storage is the obvious mitigation, still undesigned.
- **Signup gate.** Open self-service versus maintainer approval for new teams. Affects plan 2's `POST /v1/teams`.
- **Alert notification channel** for the free-tier thresholds, Sentry and Better Stack.
- **Sentry.** Vercel Hobby keeps runtime logs for one hour, so Sentry is the only durable error record. This plan logs through `slog`; wiring the Sentry Go SDK is plan 5 and should not slip later than that.
