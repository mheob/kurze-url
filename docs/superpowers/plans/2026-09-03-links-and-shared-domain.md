# Links and the Shared Domain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team can create, find, change and delete short links on a hostname the instance owns, and every such link resolves through the existing redirect path at unchanged cost.

**Architecture:** One shared hostname is modelled as a `domain` row with `team_id IS NULL`, provisioned from configuration at boot. Routes that carry a link ID but no team ID authorize through per-entity scope structs that resolve the link, then delegate to plan 2's membership check — so authorization stays declared in the input type and a handler cannot forget it. Every link write invalidates the redirect cache after its transaction commits.

**Tech Stack:** Go 1.27, chi v5, Huma v2, pgx/v5 + sqlc, go-redis v9, Supabase CLI migrations, testcontainers-go.

**Spec:** `docs/superpowers/specs/2026-09-03-links-and-shared-domain-design.md`

## Global Constraints

Copied from the spec. Every task's requirements implicitly include this section.

- The tenant is called `team` in every identifier — tables, columns, Go types, API paths. "Verein" appears only in user-facing German copy. Never `verein_id`.
- `GET /<slug>` is the hot path. Nothing this plan adds may cost it a single extra Redis command or database round trip.
- There is no RLS. Every query that touches team data filters by `team_id` in Go. A query without that filter is a data-leak bug, not a style issue.
- Never store a full IP address, ever.
- Errors are Huma's default RFC 9457 `application/problem+json`. Do not build a custom error model.
- Pagination is offset/limit with the existing `api.Page[T]` envelope, `per_page` capped at 100. Never pagination headers.
- Filtering uses flat, explicitly typed query parameters per endpoint. Not a generic `filter=field:op:value` scheme.
- Operations needing authentication declare `Security: []map[string][]string{{"bearerAuth": {}}}`.
- Migrations are owned by the Supabase CLI: `supabase migration new <name>`, then `supabase db push`. No golang-migrate, no Atlas.
- Database access is sqlc-generated from raw SQL in `apps/api/internal/db/queries/`. No ORM. Regenerate with `sqlc generate` from `apps/api`.
- Go tests run against real Postgres (local Supabase at `postgres://postgres:postgres@127.0.0.1:54322/postgres`) and real Redis (testcontainers). Mocks are for external HTTP only.
- Conventional Commits. Commit description max 50 characters including type and scope.
- `password_hash` never appears in any response, in any form. Only `has_password`.

---

## File Structure

**New files**

| File | Responsibility |
| --- | --- |
| `supabase/migrations/<ts>_shared_domain.sql` | Drops `NOT NULL` from `domain.team_id` |
| `apps/api/internal/db/queries/domain.sql` | Shared-domain upsert and the linkable-domain check |
| `apps/api/internal/db/queries/link_crud.sql` | Link create/read/list/update/delete and the scope lookup |
| `apps/api/internal/db/shared_domain_test.go` | Database-level tests for the domain queries |
| `apps/api/internal/db/link_crud_test.go` | Database-level tenancy tests for the link queries |
| `apps/api/internal/slug/slug.go` | Slug alphabet, generation, normalization, validation, reserved list |
| `apps/api/internal/slug/slug_test.go` | Unit tests for the above |
| `apps/api/internal/destination/destination.go` | Destination URL validation |
| `apps/api/internal/destination/destination_test.go` | Unit tests for the above |
| `apps/api/internal/authz/link.go` | `LinkPath`, `ResolvedLink`, `LinkResolver`, the two link scopes |
| `apps/api/internal/authz/link_test.go` | Unit tests for the link scopes against a fake resolver |
| `apps/api/internal/api/bootstrap.go` | `SharedDomain` and `ProvisionSharedDomain` |
| `apps/api/internal/api/bootstrap_test.go` | Provisioning tests |
| `apps/api/internal/api/links.go` | The five link handlers, their input/output types and registration |
| `apps/api/internal/api/links_test.go` | End-to-end handler tests through the router |
| `apps/api/internal/api/links_isolation_test.go` | Cross-team isolation and falsification tests |

**Modified files**

| File | Change |
| --- | --- |
| `apps/api/internal/config/config.go` | `SharedDomainHostname`, `ShortURLScheme` |
| `apps/api/internal/config/config_test.go` | Tests for both |
| `apps/api/internal/api/api.go` | `Deps.SharedDomain` |
| `apps/api/internal/api/v1.go` | Install the link resolver; call `d.registerLinks(api)` |
| `apps/api/internal/api/redirect.go` | Lowercase the slug before the cache key |
| `apps/api/internal/api/verify.go` | Lowercase the slug in both handlers |
| `apps/api/internal/authz/scope.go` | Extract `resolveMembership` so link scopes can reuse it |
| `apps/api/internal/audit/audit.go` | Three link actions and the `link` entity |
| `apps/api/internal/api/matrix_test.go` | Five new matrix rows, `{link}` path rendering |
| `apps/api/internal/api/tenancy_test.go` | Fixture gains a shared domain and a team-owned link |
| `apps/api/cmd/api/main.go` | Provision the shared domain at boot |
| `docs/planning/05-database-schema.md` | Amend the denormalization note and the audit action example |
| `docs/planning/06-api-design.md` | Note that `scan_result` arrives with scanning |
| `CLAUDE.md` | Shared domain, entity scopes, slug rules |

---

### Task 1: Shared-domain schema and queries

**Files:**

- Create: `supabase/migrations/<timestamp>_shared_domain.sql`
- Create: `apps/api/internal/db/queries/domain.sql`
- Test: `apps/api/internal/db/shared_domain_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `db.UpsertSharedDomain(ctx, hostname string) (UpsertSharedDomainRow, error)` with fields `ID uuid.UUID`, `Hostname string`; `db.GetLinkableDomain(ctx, GetLinkableDomainParams{ID uuid.UUID, TeamID uuid.UUID}) (GetLinkableDomainRow, error)` with fields `ID uuid.UUID`, `Hostname string`.

- [ ] **Step 1: Create the migration**

Run: `supabase migration new shared_domain`

Write into the generated file:

```sql
-- A globally unique hostname cannot belong to one team while serving every
-- team. team_id IS NULL therefore means "shared": any team may create links
-- on this domain. link.team_id remains the sole authorization key, so no
-- authorization path changes.
alter table domain alter column team_id drop not null;
```

- [ ] **Step 2: Apply it**

Run: `supabase db push` Expected: the migration applies with no error.

- [ ] **Step 3: Write the failing database test**

Create `apps/api/internal/db/shared_domain_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestUpsertSharedDomainIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "shared-" + uuid.NewString()[:8] + ".test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where hostname = $1`, hostname)
	})

	first, err := queries.UpsertSharedDomain(ctx, hostname)
	require.NoError(t, err)

	second, err := queries.UpsertSharedDomain(ctx, hostname)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID, "a second boot must not create a second row")

	var teamID *uuid.UUID
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`select team_id, verification_status from domain where id = $1`, first.ID).
		Scan(&teamID, &status))
	require.Nil(t, teamID, "a shared domain belongs to no team")
	require.Equal(t, "verified", status, "an unverified domain serves no links")
}

func TestUpsertSharedDomainRefusesToHijackATeamsDomain(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "owned-" + uuid.NewString()[:8] + ".test"
	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('owner of a custom domain') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})
	_, err := pool.Exec(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now())`, teamID, hostname)
	require.NoError(t, err)

	_, err = queries.UpsertSharedDomain(ctx, hostname)
	require.Error(t, err, "a team's own hostname must never be converted into a shared one")

	var stillOwned uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`select team_id from domain where hostname = $1`, hostname).Scan(&stillOwned))
	require.Equal(t, teamID, stillOwned)
}

func TestGetLinkableDomainAcceptsSharedAndOwnRejectsOthers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	var mine, theirs uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `insert into team (name) values ('mine') returning id`).Scan(&mine))
	require.NoError(t, pool.QueryRow(ctx, `insert into team (name) values ('theirs') returning id`).Scan(&theirs))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = any($1)`,
			[]uuid.UUID{mine, theirs})
	})

	sharedHost := "shared-" + uuid.NewString()[:8] + ".test"
	shared, err := queries.UpsertSharedDomain(ctx, sharedHost)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where id = $1`, shared.ID)
	})

	var ownVerified, ownPending, otherTeams uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		mine, "own-"+uuid.NewString()[:8]+".test").Scan(&ownVerified))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status)
		 values ($1, $2, 'pending') returning id`,
		mine, "pending-"+uuid.NewString()[:8]+".test").Scan(&ownPending))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		theirs, "other-"+uuid.NewString()[:8]+".test").Scan(&otherTeams))

	for name, tc := range map[string]struct {
		domainID uuid.UUID
		ok       bool
	}{
		"shared domain":            {shared.ID, true},
		"own verified domain":      {ownVerified, true},
		"own unverified domain":    {ownPending, false},
		"another team's domain":    {otherTeams, false},
		"a domain that is not one": {uuid.New(), false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := queries.GetLinkableDomain(ctx, db.GetLinkableDomainParams{
				ID: tc.domainID, TeamID: mine,
			})
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}
```

- [ ] **Step 4: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/db/ -run 'SharedDomain|LinkableDomain'` Expected: FAIL — `queries.UpsertSharedDomain` and `queries.GetLinkableDomain` are undefined.

- [ ] **Step 5: Write the queries**

Create `apps/api/internal/db/queries/domain.sql`:

```sql
-- Domain queries. A row with team_id IS NULL is the instance's shared
-- hostname: every team may create links on it. Every other row belongs to
-- exactly one team.

-- UpsertSharedDomain provisions the instance's shared hostname at boot. The
-- WHERE clause on the conflict branch is the safety catch: if the hostname is
-- already registered as some team's verified custom domain, no row is updated
-- and no row is returned, so the :one query fails with pgx.ErrNoRows rather
-- than silently seizing a hostname a team owns.

-- name: UpsertSharedDomain :one
insert into domain (team_id, hostname, verification_status, verified_at)
values (null, $1, 'verified', now())
on conflict (hostname) do update
  set verification_status = 'verified',
      verified_at = coalesce(domain.verified_at, now())
  where domain.team_id is null
returning id, hostname;

-- GetLinkableDomain answers "may this team put a link on this domain?".
-- Both halves matter: an unverified domain must not serve links, or a team
-- could claim a hostname it does not own, and a domain belonging to another
-- team is not this team's to use.

-- name: GetLinkableDomain :one
select id, hostname
from domain
where id = $1
  and verification_status = 'verified'
  and (team_id is null or team_id = $2);
```

- [ ] **Step 6: Regenerate sqlc**

Run: `cd apps/api && sqlc generate` Expected: `internal/db/domain.sql.go` appears; `git status` shows no unexpected churn in the other generated files.

- [ ] **Step 7: Run the tests**

Run: `cd apps/api && go test ./internal/db/ -run 'SharedDomain|LinkableDomain' -v` Expected: PASS, all subtests.

- [ ] **Step 8: Run the whole db package**

Run: `cd apps/api && go test ./internal/db/` Expected: PASS — the nullable column must not have broken plan 1's or plan 2's queries.

- [ ] **Step 9: Commit**

```
feat(db): allow a team-less shared domain
```

---

### Task 2: Configuration for the shared hostname

**Files:**

- Modify: `apps/api/internal/config/config.go`
- Test: `apps/api/internal/config/config_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `config.Config.SharedDomainHostname string`, `config.Config.ShortURLScheme string`.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/config/config_test.go`:

```go
func TestSharedDomainHostnameDefaultsToLocalhost(t *testing.T) {
	withRequiredEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "localhost", cfg.SharedDomainHostname)
	require.Equal(t, "http", cfg.ShortURLScheme,
		"a local checkout must produce http:// short URLs, not https://")
}

func TestSharedDomainHostnameComesFromTheEnvironment(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SHARED_DOMAIN_HOSTNAME", "kurze.url")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "kurze.url", cfg.SharedDomainHostname)
	require.Equal(t, "https", cfg.ShortURLScheme)
}

func TestShortURLSchemeCanBeOverridden(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SHARED_DOMAIN_HOSTNAME", "kurze.url")
	t.Setenv("SHORT_URL_SCHEME", "http")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "http", cfg.ShortURLScheme)
}

func TestShortURLSchemeRejectsAnythingElse(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("SHORT_URL_SCHEME", "javascript")

	_, err := config.Load()
	require.Error(t, err)
}
```

If `withRequiredEnv` does not already exist in that file, add it — it sets the three variables `Load` requires:

```go
func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "test-salt")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/config/ -run 'SharedDomain|ShortURLScheme'` Expected: FAIL — `cfg.SharedDomainHostname` undefined.

- [ ] **Step 3: Add the fields**

In `apps/api/internal/config/config.go`, next to `APIHostname`:

```go
	// SharedDomainHostname is the instance's own short hostname — the domain
	// every team's links use until that team brings its own. It is
	// configuration rather than a seeded migration because it differs per
	// environment: localhost in a checkout, a preview hostname on Vercel, the
	// real short domain in production.
	SharedDomainHostname string

	// ShortURLScheme is the scheme used to compose a link's short_url. It is
	// derived from the hostname unless set explicitly, so a local checkout
	// does not advertise https:// URLs it cannot serve.
	ShortURLScheme string
```

- [ ] **Step 4: Populate them in Load**

Inside `Load`, in the struct literal next to `APIHostname`:

```go
		SharedDomainHostname: env("SHARED_DOMAIN_HOSTNAME", "localhost"),
```

And after the required-value loop, before the rate-limit block:

```go
	cfg.ShortURLScheme = env("SHORT_URL_SCHEME", defaultShortURLScheme(cfg.SharedDomainHostname))
	if cfg.ShortURLScheme != "http" && cfg.ShortURLScheme != "https" {
		return Config{}, fmt.Errorf(
			"config: SHORT_URL_SCHEME must be http or https, got %q", cfg.ShortURLScheme)
	}
```

And at the bottom of the file:

```go
// defaultShortURLScheme picks http for a local hostname and https everywhere
// else. Advertising an https:// short URL from a checkout that only serves
// http produces links nobody can open.
func defaultShortURLScheme(hostname string) string {
	host := hostname
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		host = h
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return "http"
	}
	return "https"
}
```

Add `"net"` to the imports.

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/config/ -v` Expected: PASS.

- [ ] **Step 6: Commit**

```
feat(config): add the shared domain hostname
```

---

### Task 3: Provision the shared domain at boot

**Files:**

- Create: `apps/api/internal/api/bootstrap.go`
- Create: `apps/api/internal/api/bootstrap_test.go`
- Modify: `apps/api/internal/api/api.go`
- Modify: `apps/api/cmd/api/main.go`

**Interfaces:**

- Consumes: `db.UpsertSharedDomain` from Task 1; `config.Config.SharedDomainHostname` from Task 2.
- Produces: `api.SharedDomain{ID uuid.UUID; Hostname string}`; `api.ProvisionSharedDomain(ctx context.Context, queries *db.Queries, hostname string) (SharedDomain, error)`; the field `api.Deps.SharedDomain SharedDomain`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/api/bootstrap_test.go`:

```go
package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestProvisionSharedDomainIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "boot-" + uuid.NewString()[:8] + ".test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where hostname = $1`, hostname)
	})

	first, err := api.ProvisionSharedDomain(ctx, queries, hostname)
	require.NoError(t, err)
	require.Equal(t, hostname, first.Hostname)
	require.NotEqual(t, uuid.Nil, first.ID)

	second, err := api.ProvisionSharedDomain(ctx, queries, hostname)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
}

func TestProvisionSharedDomainFailsOnATeamsHostname(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "claimed-" + uuid.NewString()[:8] + ".test"
	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('claimant') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})
	_, err := pool.Exec(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now())`, teamID, hostname)
	require.NoError(t, err)

	_, err = api.ProvisionSharedDomain(ctx, queries, hostname)
	require.ErrorIs(t, err, api.ErrHostnameClaimed)
}

func TestProvisionSharedDomainRejectsAnEmptyHostname(t *testing.T) {
	_, err := api.ProvisionSharedDomain(context.Background(), nil, "")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/api/ -run ProvisionSharedDomain` Expected: FAIL — `api.ProvisionSharedDomain` undefined.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/api/bootstrap.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrHostnameClaimed means the configured shared hostname is already some
// team's verified custom domain. Starting anyway would let every team create
// links on a hostname that team owns, so startup fails instead.
var ErrHostnameClaimed = errors.New("api: the shared hostname is already a team's custom domain")

// SharedDomain is the instance's own short hostname, resolved once at boot.
// Holding it on Deps means creating a link without an explicit domain_id
// costs no extra query.
type SharedDomain struct {
	ID       uuid.UUID
	Hostname string
}

// ProvisionSharedDomain makes sure the configured shared hostname exists as a
// verified, team-less domain row, and returns it. It is idempotent and runs on
// every boot.
func ProvisionSharedDomain(
	ctx context.Context, queries *db.Queries, hostname string,
) (SharedDomain, error) {
	host := strings.ToLower(strings.TrimSpace(hostname))
	if host == "" {
		return SharedDomain{}, errors.New("api: the shared domain hostname is empty")
	}

	row, err := queries.UpsertSharedDomain(ctx, host)
	if errors.Is(err, pgx.ErrNoRows) {
		return SharedDomain{}, fmt.Errorf("%w: %s", ErrHostnameClaimed, host)
	}
	if err != nil {
		return SharedDomain{}, fmt.Errorf("api: provision the shared domain: %w", err)
	}

	return SharedDomain{ID: row.ID, Hostname: row.Hostname}, nil
}
```

- [ ] **Step 4: Add the field to Deps**

In `apps/api/internal/api/api.go`, inside `Deps`, after `Config`:

```go
	// SharedDomain is the instance's own short hostname. A link created with
	// no explicit domain_id lands here.
	SharedDomain SharedDomain
```

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -run ProvisionSharedDomain -v` Expected: PASS.

- [ ] **Step 6: Wire it into main**

In `apps/api/cmd/api/main.go`, after `queries := db.New(pool)` and before the `deps := api.Deps{...}` literal:

```go
	sharedDomain, err := api.ProvisionSharedDomain(ctx, queries, cfg.SharedDomainHostname)
	if err != nil {
		return err
	}
	log.Info("shared domain ready", "hostname", sharedDomain.Hostname, "domain_id", sharedDomain.ID)
```

and add the field to the literal:

```go
		SharedDomain: sharedDomain,
```

- [ ] **Step 7: Build and start the binary against the local stack**

Run: `cd apps/api && go build ./... && go run ./cmd/api` Expected: the log line `shared domain ready` appears with `hostname=localhost`. Stop the process.

- [ ] **Step 8: Commit**

```
feat(api): provision the shared domain at boot
```

---

### Task 4: The slug package

**Files:**

- Create: `apps/api/internal/slug/slug.go`
- Test: `apps/api/internal/slug/slug_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `slug.Alphabet` (const string), `slug.Length` (const int), `slug.Generate() (string, error)`, `slug.Normalize(raw string) string`, `slug.Validate(normalized string) error`, `slug.ErrMalformed`, `slug.ErrReserved`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/slug/slug_test.go`:

```go
package slug_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/slug"
)

func TestGenerateProducesTheDocumentedShape(t *testing.T) {
	seen := map[string]bool{}

	for range 500 {
		got, err := slug.Generate()
		require.NoError(t, err)
		require.Len(t, got, slug.Length)

		for _, r := range got {
			require.True(t, strings.ContainsRune(slug.Alphabet, r),
				"generated slug %q contains %q, which is not in the alphabet", got, r)
		}
		seen[got] = true
	}

	require.Greater(t, len(seen), 490,
		"500 draws from a 1.1e12 space should almost never repeat; %d unique", len(seen))
}

func TestAlphabetExcludesLookalikes(t *testing.T) {
	require.Len(t, slug.Alphabet, 32)
	for _, excluded := range []rune{'0', '1', 'l', 'o'} {
		require.NotContains(t, slug.Alphabet, string(excluded),
			"%q is easy to misread on a printed flyer", string(excluded))
	}
	require.Equal(t, strings.ToLower(slug.Alphabet), slug.Alphabet,
		"slugs are case-insensitive, so the alphabet must be lowercase")
}

func TestNormalizeLowercasesAndTrims(t *testing.T) {
	require.Equal(t, "sommerfest", slug.Normalize("  SommerFest "))
	require.Equal(t, "abc", slug.Normalize("ABC"))
	require.Equal(t, "", slug.Normalize("   "))
}

func TestValidateAcceptsWhatTheSpecAllows(t *testing.T) {
	for _, ok := range []string{
		"abc",
		"sommerfest-2026",
		"jhv_2026",
		"a1b",
		strings.Repeat("a", 64),
	} {
		require.NoError(t, slug.Validate(ok), "%q must be accepted", ok)
	}
}

func TestValidateRejectsWhatTheSpecForbids(t *testing.T) {
	for name, bad := range map[string]string{
		"too short":            "ab",
		"too long":             strings.Repeat("a", 65),
		"leading hyphen":       "-abc",
		"trailing hyphen":      "abc-",
		"leading underscore":   "_abc",
		"trailing underscore":  "abc_",
		"uppercase":            "Abc",
		"a slash":              "a/b",
		"a dot":                "a.b",
		"a space":              "a b",
		"non-ascii":            "grüße",
		"empty":                "",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, slug.Validate(bad), slug.ErrMalformed)
		})
	}
}

func TestValidateRejectsReservedSlugs(t *testing.T) {
	for _, reserved := range []string{
		"health", "verify", "api", "admin", "login", "static", "assets",
		"robots.txt", "favicon.ico", "sitemap.xml", "apple-touch-icon.png",
		".well-known", "_next",
	} {
		t.Run(reserved, func(t *testing.T) {
			err := slug.Validate(reserved)
			require.Error(t, err, "%q must never become a link", reserved)
		})
	}
}

func TestHealthIsReservedBecauseTheRouterOwnsIt(t *testing.T) {
	require.ErrorIs(t, slug.Validate("health"), slug.ErrReserved,
		"/health is registered on the root router and would shadow this link")
}

func TestGeneratedSlugsAreAlwaysValid(t *testing.T) {
	for range 200 {
		got, err := slug.Generate()
		require.NoError(t, err)
		require.NoError(t, slug.Validate(got),
			"a generated slug must satisfy the same rules a custom alias does")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/slug/` Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/slug/slug.go`:

```go
// Package slug owns the shape of the short part of a short link: how one is
// generated, how a user-supplied alias is normalized, and which values are
// refused outright.
package slug

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Alphabet excludes 0, 1, l and o. These links travel on posters, flyers and
// printed newsletters, where a reader has to tell characters apart by eye.
const Alphabet = "23456789abcdefghijkmnpqrstuvwxyz"

// Length is the generated slug length. 32^8 is about 1.1e12 combinations.
const Length = 8

var (
	// ErrMalformed means the value does not match the permitted shape.
	ErrMalformed = errors.New("slug: not a permitted slug")

	// ErrReserved means the value is a path the service itself owns.
	ErrReserved = errors.New("slug: reserved")
)

// pattern is applied after Normalize, so it never needs to consider case.
// Three to sixty-four characters, alphanumeric at both ends, with hyphens and
// underscores permitted only inside.
var pattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$`)

// reserved names paths the service serves itself, plus the conventional
// browser and platform paths. It applies on every domain, not only the shared
// one: a single chi mux serves them all, so a custom domain reaches the same
// routes.
var reserved = map[string]struct{}{
	"health":               {}, // registered on the root router today
	"verify":               {}, // the password interstitial's own subpath
	"api":                  {},
	"admin":                {},
	"login":                {},
	"static":               {},
	"assets":               {},
	"robots.txt":           {},
	"favicon.ico":          {},
	"sitemap.xml":          {},
	"apple-touch-icon.png": {},
	".well-known":          {},
	"_next":                {},
}

// Generate draws a slug from crypto/rand.
func Generate() (string, error) {
	limit := big.NewInt(int64(len(Alphabet)))

	var b strings.Builder
	b.Grow(Length)
	for range Length {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("slug: draw a random character: %w", err)
		}
		b.WriteByte(Alphabet[n.Int64()])
	}
	return b.String(), nil
}

// Normalize is applied to every slug on the way in and on the way out of the
// redirect path, so /Abc and /abc are the same link.
func Normalize(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// Validate checks a normalized slug. Pass it the output of Normalize, never
// raw user input.
func Validate(normalized string) error {
	if _, ok := reserved[normalized]; ok {
		return fmt.Errorf("%w: %q", ErrReserved, normalized)
	}
	if !pattern.MatchString(normalized) {
		return fmt.Errorf("%w: %q", ErrMalformed, normalized)
	}
	return nil
}
```

Note the ordering inside `Validate`: the reserved check runs first, so `robots.txt` and `.well-known` — which the pattern would reject anyway — report the accurate reason rather than a shape complaint.

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/slug/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```
feat(slug): add slug generation and validation
```

---

### Task 5: Destination URL validation

**Files:**

- Create: `apps/api/internal/destination/destination.go`
- Test: `apps/api/internal/destination/destination_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `destination.MaxLength` (const int, 2048), `destination.Validate(raw string, selfHostnames []string) error`, `destination.ErrScheme`, `destination.ErrPrivateAddress`, `destination.ErrSelfReference`, `destination.ErrTooLong`, `destination.ErrMalformed`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/destination/destination_test.go`:

```go
package destination_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/destination"
)

var self = []string{"kurze.url", "api.kurze.url"}

func TestValidateAcceptsOrdinaryHTTPSURLs(t *testing.T) {
	for _, ok := range []string{
		"https://example.org",
		"https://example.org/sommerfest?utm_source=flyer#anfahrt",
		"https://sub.example.org:8443/path",
		"https://xn--grsse-loa.example",
	} {
		require.NoError(t, destination.Validate(ok, self), "%q must be accepted", ok)
	}
}

func TestValidateRejectsEverySchemeButHTTPS(t *testing.T) {
	for name, raw := range map[string]string{
		"plain http": "http://example.org",
		"javascript": "javascript:alert(1)",
		"data":       "data:text/html,<script>alert(1)</script>",
		"file":       "file:///etc/passwd",
		"ftp":        "ftp://example.org/x",
		"mailto":     "mailto:vorstand@verein.test",
		"scheme-relative": "//example.org/x",
		"relative":        "/sommerfest",
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, destination.Validate(raw, self))
		})
	}
}

func TestValidateRejectsPrivateAndLocalAddresses(t *testing.T) {
	for name, raw := range map[string]string{
		"loopback v4":  "https://127.0.0.1/admin",
		"loopback v6":  "https://[::1]/admin",
		"private 10":   "https://10.0.0.1/",
		"private 172":  "https://172.16.4.9/",
		"private 192":  "https://192.168.1.1/",
		"link-local":   "https://169.254.169.254/latest/meta-data/",
		"unique-local": "https://[fd00::1]/",
		"multicast":    "https://224.0.0.1/",
		"unspecified":  "https://0.0.0.0/",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, destination.Validate(raw, self), destination.ErrPrivateAddress)
		})
	}
}

func TestValidateRejectsOurOwnHostnames(t *testing.T) {
	for _, raw := range []string{
		"https://kurze.url/abcd",
		"https://KURZE.URL/abcd",
		"https://api.kurze.url/v1/links",
		"https://kurze.url:443/abcd",
	} {
		require.ErrorIs(t, destination.Validate(raw, self), destination.ErrSelfReference,
			"%q is a redirect loop", raw)
	}
}

func TestValidateRejectsAnOverlongURL(t *testing.T) {
	long := "https://example.org/" + strings.Repeat("a", destination.MaxLength)

	require.ErrorIs(t, destination.Validate(long, self), destination.ErrTooLong)
}

func TestValidateRejectsAHostlessURL(t *testing.T) {
	require.Error(t, destination.Validate("https://", self))
	require.Error(t, destination.Validate("", self))
}

func TestValidateAcceptsAPublicIPLiteral(t *testing.T) {
	require.NoError(t, destination.Validate("https://93.184.216.34/", self),
		"only private and local ranges are refused, not IP literals as such")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/destination/` Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/destination/destination.go`:

```go
// Package destination validates where a short link points. It is deliberately
// creation-time only: no DNS is resolved here, because a record can change
// between validation and the first click. The DNS-rebinding re-check belongs
// wherever the service itself fetches a URL, which nothing in the link
// endpoints does.
package destination

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MaxLength caps a destination. Long enough for any real campaign URL, short
// enough that a row stays small.
const MaxLength = 2048

var (
	// ErrMalformed means the value is not a parseable absolute URL.
	ErrMalformed = errors.New("destination: not an absolute URL")

	// ErrScheme means the scheme is not https.
	ErrScheme = errors.New("destination: only https:// destinations are allowed")

	// ErrPrivateAddress means the host is a literal address inside a range
	// that is not reachable from the public internet.
	ErrPrivateAddress = errors.New("destination: private and local addresses are not allowed")

	// ErrSelfReference means the destination points back at this service.
	ErrSelfReference = errors.New("destination: a link may not point at this service")

	// ErrTooLong means the URL exceeds MaxLength.
	ErrTooLong = errors.New("destination: url is too long")
)

// Validate checks a destination URL. selfHostnames is the set of hostnames
// this instance answers on; a destination naming any of them is a loop.
func Validate(raw string, selfHostnames []string) error {
	if len(raw) > MaxLength {
		return fmt.Errorf("%w: %d characters, limit is %d", ErrTooLong, len(raw), MaxLength)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	// The scheme is checked by allowlist, never by blocklist. A blocklist is a
	// promise to enumerate every dangerous scheme forever.
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: got %q", ErrScheme, parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("%w: no host", ErrMalformed)
	}

	if ip := net.ParseIP(host); ip != nil && !isPublic(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
	}

	for _, own := range selfHostnames {
		if host == strings.ToLower(strings.TrimSpace(own)) {
			return fmt.Errorf("%w: %s", ErrSelfReference, host)
		}
	}

	return nil
}

// isPublic reports whether an address literal is one a browser could
// meaningfully be sent to across the internet.
func isPublic(ip net.IP) bool {
	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(),
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast(),
		ip.IsUnspecified():
		return false
	}
	// fc00::/7 — unique local addresses. net.IP.IsPrivate covers fc00::/7
	// already, but only for 16-byte forms; the explicit check costs nothing
	// and documents the intent.
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil && v6[0]&0xfe == 0xfc {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/destination/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```
feat(api): validate link destination urls
```

---

### Task 6: Case-insensitive slugs on the redirect path

**Files:**

- Modify: `apps/api/internal/api/redirect.go:28`
- Modify: `apps/api/internal/api/verify.go:26`, `apps/api/internal/api/verify.go:49`
- Test: `apps/api/internal/api/redirect_test.go`

**Interfaces:**

- Consumes: `slug.Normalize` from Task 4.
- Produces: nothing new; changes behaviour of the existing handlers.

This is the only task in the plan that touches the hot path. It must add no Redis command and no query — only a `strings.ToLower` on a short string.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/api/redirect_test.go`:

```go
func TestRedirectIsCaseInsensitiveInTheSlug(t *testing.T) {
	f := newFixture(t) // seeds slug "hello"

	for _, requested := range []string{"hello", "HELLO", "HeLLo"} {
		t.Run(requested, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://"+f.hostname+"/"+requested, nil)
			f.router().ServeHTTP(rec, req)

			require.Equal(t, http.StatusFound, rec.Code,
				"a link printed on a flyer must resolve however its case was typed")
			require.Equal(t, "https://example.org/hello", rec.Header().Get("Location"))
		})
	}
}

func TestRedirectCaseFoldingSharesOneCacheEntry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	f.router().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "http://"+f.hostname+"/HELLO", nil))
	require.Equal(t, http.StatusFound, rec.Code)

	// The uppercase request must have populated the lowercase key, not a
	// second one: two cache entries for one link would double the hot path's
	// memory and let them disagree after an update.
	value, err := f.deps.Cache.Raw().Get(ctx, link.CacheKey(f.hostname, "hello")).Result()
	require.NoError(t, err, "the lowercase key must be the one that got populated")
	require.NotEmpty(t, value)

	_, err = f.deps.Cache.Raw().Get(ctx, link.CacheKey(f.hostname, "HELLO")).Result()
	require.Error(t, err, "no uppercase key may exist")
}
```

If `fixture` has no `router()` helper and no way to reach the raw Redis client, add both. `router()` builds the same chi router the existing redirect tests build; copy that construction out of the nearest existing test into a method. For the Redis client, add to `apps/api/internal/cache/client.go`:

```go
// Raw exposes the underlying client. It exists for tests that need to assert
// on exact keys; handlers must use the methods above.
func (c *Client) Raw() *redis.Client { return c.rdb }
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/api/ -run 'CaseInsensitive|CaseFolding' -v` Expected: FAIL — the uppercase request 404s, because `HELLO` matches no row.

- [ ] **Step 3: Normalize in the redirect handler**

In `apps/api/internal/api/redirect.go`, replace line 28:

```go
	slug := chi.URLParam(r, "slug")
```

with:

```go
	// Slugs are case-insensitive: they are stored lowercase, so the incoming
	// one is folded before it becomes a cache key or a query parameter. This
	// is a string operation on a short string — no extra Redis command, no
	// extra query, so the hot path's cost is unchanged.
	slug := slugpkg.Normalize(chi.URLParam(r, "slug"))
```

Import the package under an alias, because `slug` is already a local variable name in these handlers:

```go
	slugpkg "github.com/mheob/kurze-url/apps/api/internal/slug"
```

- [ ] **Step 4: Normalize in both verify handlers**

Apply the identical change at `apps/api/internal/api/verify.go:26` and `apps/api/internal/api/verify.go:49`. The interstitial must resolve the same link the redirect did, or a password-protected link would 404 when its slug was typed in a different case.

- [ ] **Step 5: Run the redirect and verify suites**

Run: `cd apps/api && go test ./internal/api/ -run 'Redirect|Verify' -v` Expected: PASS, including plan 1's existing cases.

- [ ] **Step 6: Confirm the hot path's command count is unchanged**

Run: `cd apps/api && go test ./internal/api/ -run 'CommandCount|Redis' -v` Expected: PASS. If no such test exists in the package, run the full redirect suite instead and confirm no new Redis call was introduced by reading the diff: the change must be one function call on a string, nothing else.

- [ ] **Step 7: Commit**

```
feat(api): make slugs case-insensitive
```

---

### Task 7: Link CRUD queries

**Files:**

- Create: `apps/api/internal/db/queries/link_crud.sql`
- Test: `apps/api/internal/db/link_crud_test.go`

**Interfaces:**

- Consumes: the nullable `domain.team_id` from Task 1.
- Produces, all on `*db.Queries`:
  - `GetLinkScope(ctx, id uuid.UUID) (GetLinkScopeRow, error)` — fields `ID`, `TeamID`, `DomainID uuid.UUID`, `Hostname string`, `Slug string`
  - `CreateLink(ctx, CreateLinkParams) (CreateLinkRow, error)`
  - `GetLinkForAPI(ctx, GetLinkForAPIParams{ID, TeamID uuid.UUID}) (GetLinkForAPIRow, error)`
  - `ListLinksForTeam(ctx, ListLinksForTeamParams) ([]ListLinksForTeamRow, error)`
  - `CountLinksForTeam(ctx, CountLinksForTeamParams) (int64, error)`
  - `UpdateLink(ctx, UpdateLinkParams) (UpdateLinkRow, error)`
  - `DeleteLink(ctx, DeleteLinkParams{ID, TeamID uuid.UUID}) (int64, error)`
- Every row type carries the same columns: `ID`, `DomainID`, `TeamID`, `Hostname`, `Slug`, `DestinationURL`, `RedirectType int16`, `State string`, `ExpiresAt *time.Time`, `HasPassword bool`, `AnalyticsEnabled bool`, `CreatedBy uuid.UUID`, `CreatedAt`, `UpdatedAt time.Time`. `ListLinksForTeamRow` adds `TotalCount int64`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/db/link_crud_test.go`:

```go
package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// linkFixture is one team with one verified domain, plus a second team with
// its own domain and link, so every tenancy assertion has something to leak to.
type linkFixture struct {
	queries  *db.Queries
	userID   uuid.UUID
	teamID   uuid.UUID
	domainID uuid.UUID
	hostname string

	otherTeamID uuid.UUID
	otherLinkID uuid.UUID
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()

	f := &linkFixture{queries: db.New(pool)}
	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&f.userID))

	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('links') returning id`).Scan(&f.teamID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('other links') returning id`).Scan(&f.otherTeamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = any($1)`,
			[]uuid.UUID{f.teamID, f.otherTeamID})
	})

	f.hostname = "l" + uuid.NewString()[:8] + ".test"
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.teamID, f.hostname).Scan(&f.domainID))

	var otherDomainID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.otherTeamID, "o"+uuid.NewString()[:8]+".test").Scan(&otherDomainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 'secret', 'https://example.org/secret', $3) returning id`,
		otherDomainID, f.otherTeamID, f.userID).Scan(&f.otherLinkID))

	return f
}

func (f *linkFixture) create(t *testing.T, slug string) db.CreateLinkRow {
	t.Helper()
	row, err := f.queries.CreateLink(context.Background(), db.CreateLinkParams{
		DomainID:         f.domainID,
		TeamID:           f.teamID,
		Slug:             slug,
		DestinationURL:   "https://example.org/" + slug,
		RedirectType:     302,
		AnalyticsEnabled: true,
		CreatedBy:        f.userID,
	})
	require.NoError(t, err)
	return row
}

func TestCreateLinkReturnsTheFullRepresentation(t *testing.T) {
	f := newLinkFixture(t)

	row := f.create(t, "sommerfest")

	require.Equal(t, "sommerfest", row.Slug)
	require.Equal(t, f.teamID, row.TeamID)
	require.Equal(t, f.hostname, row.Hostname)
	require.Equal(t, "active", row.State)
	require.False(t, row.HasPassword)
	require.Nil(t, row.ExpiresAt)
}

func TestCreateLinkRefusesADuplicateSlugOnOneDomain(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "jhv")

	_, err := f.queries.CreateLink(context.Background(), db.CreateLinkParams{
		DomainID:         f.domainID,
		TeamID:           f.teamID,
		Slug:             "jhv",
		DestinationURL:   "https://example.org/other",
		RedirectType:     302,
		AnalyticsEnabled: true,
		CreatedBy:        f.userID,
	})
	require.Error(t, err, "(domain_id, slug) is unique")
}

func TestGetLinkForAPIFiltersByTeam(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "mine")
	ctx := context.Background()

	got, err := f.queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{ID: mine.ID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, mine.ID, got.ID)

	_, err = f.queries.GetLinkForAPI(ctx,
		db.GetLinkForAPIParams{ID: f.otherLinkID, TeamID: f.teamID})
	require.Error(t, err, "another team's link must not be readable")
}

func TestGetLinkScopeResolvesWithoutATeamFilter(t *testing.T) {
	f := newLinkFixture(t)
	ctx := context.Background()

	got, err := f.queries.GetLinkScope(ctx, f.otherLinkID)
	require.NoError(t, err,
		"the scope lookup is what determines the team, so it cannot filter by one")
	require.Equal(t, f.otherTeamID, got.TeamID)
}

func TestListLinksForTeamNeverReturnsAnotherTeamsLinks(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "one")
	f.create(t, "two")

	rows, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID:  f.teamID,
		SortAsc: false,
		Limit:   50,
		Offset:  0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, f.teamID, row.TeamID)
		require.NotEqual(t, f.otherLinkID, row.ID)
	}
	require.Equal(t, int64(2), rows[0].TotalCount)
}

func TestListLinksForTeamFilters(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "sommerfest")
	disabled := f.create(t, "winterfeier")
	_, err := f.queries.UpdateLink(context.Background(), db.UpdateLinkParams{
		ID:               disabled.ID,
		TeamID:           f.teamID,
		Slug:             disabled.Slug,
		DestinationURL:   disabled.DestinationURL,
		RedirectType:     disabled.RedirectType,
		State:            "disabled",
		AnalyticsEnabled: true,
	})
	require.NoError(t, err)

	state := "disabled"
	rows, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, State: &state, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "winterfeier", rows[0].Slug)

	query := "sommer"
	rows, err = f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, Q: &query, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "sommerfest", rows[0].Slug)

	domainID := f.domainID
	rows, err = f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, DomainID: &domainID, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestListLinksForTeamSortsBothWays(t *testing.T) {
	f := newLinkFixture(t)
	first := f.create(t, "first")
	time.Sleep(10 * time.Millisecond)
	second := f.create(t, "second")

	desc, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, SortAsc: false, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, second.ID, desc[0].ID, "newest first is the default")

	asc, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, SortAsc: true, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, asc[0].ID)
}

func TestUpdateLinkFiltersByTeamAndTouchesUpdatedAt(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "changeme")
	ctx := context.Background()

	updated, err := f.queries.UpdateLink(ctx, db.UpdateLinkParams{
		ID:               mine.ID,
		TeamID:           f.teamID,
		Slug:             "changed",
		DestinationURL:   "https://example.org/changed",
		RedirectType:     301,
		State:            "active",
		AnalyticsEnabled: false,
	})
	require.NoError(t, err)
	require.Equal(t, "changed", updated.Slug)
	require.Equal(t, int16(301), updated.RedirectType)
	require.False(t, updated.AnalyticsEnabled)
	require.True(t, updated.UpdatedAt.After(mine.UpdatedAt))

	_, err = f.queries.UpdateLink(ctx, db.UpdateLinkParams{
		ID:               f.otherLinkID,
		TeamID:           f.teamID,
		Slug:             "hijacked",
		DestinationURL:   "https://evil.test/",
		RedirectType:     302,
		State:            "active",
		AnalyticsEnabled: true,
	})
	require.Error(t, err, "another team's link must not be writable")
}

func TestDeleteLinkFiltersByTeam(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "goodbye")
	ctx := context.Background()

	affected, err := f.queries.DeleteLink(ctx, db.DeleteLinkParams{ID: mine.ID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	affected, err = f.queries.DeleteLink(ctx,
		db.DeleteLinkParams{ID: f.otherLinkID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, int64(0), affected, "another team's link must survive")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/db/ -run 'Link' ` Expected: FAIL — `db.CreateLinkParams` and the rest are undefined.

- [ ] **Step 3: Write the queries**

Create `apps/api/internal/db/queries/link_crud.sql`:

```sql
-- Link CRUD. There is no RLS: every query here except GetLinkScope filters by
-- team_id, because Postgres enforces nothing about tenancy. password_hash is
-- never selected — only whether one exists.

-- GetLinkScope is the one deliberate exception to the team_id rule. It is what
-- the LinkViewerScope/LinkEditorScope resolver calls to *discover* which team a
-- link belongs to, so it cannot filter by the answer. Everything the handler
-- does afterwards is filtered by the team_id this returns.

-- name: GetLinkScope :one
select l.id, l.team_id, l.domain_id, d.hostname, l.slug
from link l
join domain d on d.id = l.domain_id
where l.id = $1;

-- name: CreateLink :one
with inserted as (
  insert into link (domain_id, team_id, slug, destination_url, redirect_type,
                    expires_at, analytics_enabled, created_by)
  values ($1, $2, $3, $4, $5, $6, $7, $8)
  returning *
)
select i.id, i.domain_id, i.team_id, d.hostname, i.slug, i.destination_url,
       i.redirect_type, i.state, i.expires_at,
       (i.password_hash is not null)::boolean as has_password,
       i.analytics_enabled, i.created_by, i.created_at, i.updated_at
from inserted i
join domain d on d.id = i.domain_id;

-- name: GetLinkForAPI :one
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.created_by, l.created_at, l.updated_at
from link l
join domain d on d.id = l.domain_id
where l.id = $1 and l.team_id = $2;

-- Paginated. count(*) over () gives the total in the same scan, so the list
-- and its total_count cannot disagree the way two separate queries can. The
-- two CASE branches in ORDER BY implement a direction switch: whichever branch
-- is not selected evaluates to NULL for every row and therefore orders nothing.
-- l.id is the tie-break, so paging is stable when two links share a timestamp.

-- name: ListLinksForTeam :many
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.created_by, l.created_at, l.updated_at,
       count(*) over () as total_count
from link l
join domain d on d.id = l.domain_id
where l.team_id = sqlc.arg('team_id')
  and (sqlc.narg('state')::text is null or l.state = sqlc.narg('state')::text)
  and (sqlc.narg('domain_id')::uuid is null or l.domain_id = sqlc.narg('domain_id')::uuid)
  and (sqlc.narg('q')::text is null
       or l.slug ilike '%' || sqlc.narg('q')::text || '%'
       or l.destination_url ilike '%' || sqlc.narg('q')::text || '%')
order by
  case when sqlc.arg('sort_asc')::boolean then l.created_at end asc,
  case when not sqlc.arg('sort_asc')::boolean then l.created_at end desc,
  l.id
limit sqlc.arg('limit') offset sqlc.arg('offset');

-- CountLinksForTeam repeats the filters because the page-past-the-end fallback
-- needs a total when the windowed count returned no rows to carry it.

-- name: CountLinksForTeam :one
select count(*)
from link l
where l.team_id = sqlc.arg('team_id')
  and (sqlc.narg('state')::text is null or l.state = sqlc.narg('state')::text)
  and (sqlc.narg('domain_id')::uuid is null or l.domain_id = sqlc.narg('domain_id')::uuid)
  and (sqlc.narg('q')::text is null
       or l.slug ilike '%' || sqlc.narg('q')::text || '%'
       or l.destination_url ilike '%' || sqlc.narg('q')::text || '%');

-- UpdateLink writes every mutable column. The handler reads the row first,
-- inside the same transaction, merges the request onto it, and passes the
-- merged result here — the same read-then-write shape RenameTeam uses, and the
-- reason the audit entry can list exactly which fields changed.

-- name: UpdateLink :one
with updated as (
  update link set
    slug = $3,
    destination_url = $4,
    redirect_type = $5,
    state = $6,
    expires_at = $7,
    analytics_enabled = $8,
    updated_at = now()
  where id = $1 and team_id = $2
  returning *
)
select u.id, u.domain_id, u.team_id, d.hostname, u.slug, u.destination_url,
       u.redirect_type, u.state, u.expires_at,
       (u.password_hash is not null)::boolean as has_password,
       u.analytics_enabled, u.created_by, u.created_at, u.updated_at
from updated u
join domain d on d.id = u.domain_id;

-- name: DeleteLink :execrows
delete from link where id = $1 and team_id = $2;
```

- [ ] **Step 4: Regenerate sqlc**

Run: `cd apps/api && sqlc generate` Expected: `internal/db/link_crud.sql.go` appears.

If sqlc reports that `sqlc.arg('limit')` collides with a reserved word or that `ListLinksForTeamParams` fields are not named as this task's Interfaces block promises, rename the SQL arguments rather than the Go call sites — the Interfaces block is the contract later tasks were written against.

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/db/ -run 'Link' -v` Expected: PASS.

- [ ] **Step 6: Falsify the tenancy filter**

Temporarily delete `and l.team_id = $2` from `GetLinkForAPI`, run `sqlc generate`, and run `go test ./internal/db/ -run GetLinkForAPIFiltersByTeam`. Expected: FAIL. Restore the filter, regenerate, and confirm PASS.

A tenancy test that passes with the filter removed is testing nothing. Record in the task report that this falsification was performed and what it showed.

- [ ] **Step 7: Commit**

```
feat(db): add link crud queries
```

---

### Task 8: Link-scoped authorization

**Files:**

- Create: `apps/api/internal/authz/link.go`
- Create: `apps/api/internal/authz/link_test.go`
- Modify: `apps/api/internal/authz/scope.go`
- Modify: `apps/api/internal/api/v1.go:91-94`

**Interfaces:**

- Consumes: `db.GetLinkScope` from Task 7; `authz.Membership`, `authz.Role`, `authz.RoleViewer`, `authz.RoleEditor` from plan 2.
- Produces:
  - `authz.LinkPath{LinkID uuid.UUID}`
  - `authz.ResolvedLink{ID, TeamID, DomainID uuid.UUID; Hostname, Slug string}`
  - `authz.ErrLinkNotFound`
  - `authz.LinkResolver` interface with `Link(ctx context.Context, linkID uuid.UUID) (ResolvedLink, error)`
  - `authz.WithLinkResolver(ctx context.Context, r LinkResolver) context.Context`
  - `authz.NewQueryLinkResolver(queries *db.Queries) QueryLinkResolver`
  - `authz.LinkViewerScope` and `authz.LinkEditorScope`, each with `Member() Membership` and `Link() ResolvedLink`

- [ ] **Step 1: Extract the membership half of resolveScope**

In `apps/api/internal/authz/scope.go`, split `resolveScope` so the link scopes can reuse the membership decision without re-checking a `team_id` path parameter that link routes do not have. Keep the existing behaviour exactly:

```go
// resolveScope is the team-path entry point: it guards the path parameter,
// then defers the actual decision to resolveMembership.
func resolveScope(ctx huma.Context, teamID uuid.UUID, required Role, out *Membership) []error {
	// (keep the existing claims check and the existing team_id re-parse guard
	// here, unchanged, including their comments)

	return resolveMembership(ctx, teamID, required, "team not found", out)
}

// resolveMembership is the whole authorization decision, shared by the
// team-path scopes and the entity scopes. notFound is the message used when
// the caller is not a member: the wording differs per route so a 404 never
// says "team not found" on a link route.
func resolveMembership(
	ctx huma.Context, teamID uuid.UUID, required Role, notFound string, out *Membership,
) []error {
	resolver, ok := resolverFromContext(ctx.Context())
	if !ok {
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	membership, err := resolver.Membership(ctx.Context(), teamID, claimsUserID(ctx))
	switch {
	case errors.Is(err, ErrNotMember):
		return []error{huma.Error404NotFound(notFound)}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve team membership")}
	}

	if !membership.Role.AtLeast(required) {
		return []error{huma.Error403Forbidden(
			fmt.Sprintf("this action requires the %s role or higher", required))}
	}

	*out = membership
	return nil
}
```

Add the small helper the split needs, so both entry points read the caller the same way:

```go
func claimsUserID(ctx huma.Context) uuid.UUID {
	claims, _ := auth.ClaimsFromContext(ctx.Context())
	return claims.UserID
}
```

- [ ] **Step 2: Confirm plan 2's scope tests still pass**

Run: `cd apps/api && go test ./internal/authz/ -v` Expected: PASS. This step is a refactor; a red test here means the split changed behaviour, which it must not.

- [ ] **Step 3: Write the failing test**

Create `apps/api/internal/authz/link_test.go`:

```go
package authz_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

type fakeLinkResolver struct {
	link authz.ResolvedLink
	err  error
}

func (f fakeLinkResolver) Link(context.Context, uuid.UUID) (authz.ResolvedLink, error) {
	return f.link, f.err
}

type fakeMembershipResolver struct {
	membership authz.Membership
	err        error
}

func (f fakeMembershipResolver) Membership(
	context.Context, uuid.UUID, uuid.UUID,
) (authz.Membership, error) {
	return f.membership, f.err
}

type linkEditorInput struct {
	authz.LinkEditorScope
}

// linkScopeCase wires one request through a registered operation whose input
// embeds LinkEditorScope, so the scope is exercised exactly as Huma runs it.
func linkScopeCase(
	t *testing.T, links authz.LinkResolver, members authz.Resolver, userID uuid.UUID, linkID string,
) *httptest.ResponseRecorder { // humatest.TestAPI.Get returns this
	t.Helper()

	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if userID != uuid.Nil {
			inner = auth.WithClaims(inner, auth.Claims{UserID: userID})
		}
		if members != nil {
			inner = authz.WithResolver(inner, members)
		}
		if links != nil {
			inner = authz.WithLinkResolver(inner, links)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/links/{link_id}",
	}, func(_ context.Context, _ *linkEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/links/" + linkID)
}

func TestLinkScopeAllowsAnEditor(t *testing.T) {
	linkID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, linkID.String())

	require.Less(t, rec.Code, 400, "body: %s", rec.Body.String())
}

func TestLinkScopeRefusesAViewerWith403(t *testing.T) {
	linkID, teamID, userID := uuid.New(), uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, linkID.String())

	require.Equal(t, http.StatusForbidden, rec.Code,
		"a member who may see the link but not change it learns nothing new from a 403")
}

func TestLinkScopeHidesAnotherTeamsLinkWith404(t *testing.T) {
	linkID, userID := uuid.New(), uuid.New()

	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{ID: linkID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, linkID.String())

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to probe link IDs for existence")
	require.NotContains(t, rec.Body.String(), "team",
		"the 404 on a link route must be worded about the link")
}

func TestLinkScopeReturns404ForAMissingLink(t *testing.T) {
	rec := linkScopeCase(t,
		fakeLinkResolver{err: authz.ErrLinkNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLinkScopeReturns422ForAMalformedID(t *testing.T) {
	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a malformed ID is a malformed request, not a missing link")
}

func TestLinkScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	rec := linkScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"without a resolver there is no way to know whose link this is; refusing is the only safe answer")
}

func TestLinkScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	rec := linkScopeCase(t,
		fakeLinkResolver{link: authz.ResolvedLink{}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, uuid.New().String())

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

`humatest.New` returns a router and a `humatest.TestAPI`; `TestAPI` embeds `huma.API`, so `UseMiddleware` and `huma.Register` work on it directly, and `api.Get(path)` returns the `*httptest.ResponseRecorder` these assertions read. If the pinned Huma version's `humatest` differs, build the same thing with chi plus `humachi.New`, exactly as `tenancy_test.go` does. The assertions are what matter; the transport is not.

- [ ] **Step 4: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/authz/ -run LinkScope` Expected: FAIL — `authz.LinkEditorScope` undefined.

- [ ] **Step 5: Write the implementation**

Create `apps/api/internal/authz/link.go`:

```go
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrLinkNotFound means no link has that ID. It is answered with 404 — as is a
// link belonging to a team the caller is not in, so the two are
// indistinguishable from outside and link IDs cannot be probed.
var ErrLinkNotFound = errors.New("authz: link does not exist")

// LinkPath carries the link ID every link-scoped operation takes in its path.
// Exported and embedded by value for the same reason TeamPath is: reflection
// cannot reliably set fields promoted through an unexported embedded struct.
type LinkPath struct {
	LinkID uuid.UUID `path:"link_id" doc:"The link this request operates on."`
}

// ResolvedLink is what the scope loaded on the way to its decision. Handlers
// read it instead of querying the link a second time.
type ResolvedLink struct {
	ID       uuid.UUID
	TeamID   uuid.UUID
	DomainID uuid.UUID
	Hostname string
	Slug     string
}

// LinkResolver loads the tenancy facts about a link. Implemented by
// QueryLinkResolver against Postgres, and by fakes in tests.
type LinkResolver interface {
	Link(ctx context.Context, linkID uuid.UUID) (ResolvedLink, error)
}

type linkResolverKey struct{}

// WithLinkResolver returns a context carrying the link resolver. The /v1 auth
// middleware installs it once per request, beside the membership resolver.
func WithLinkResolver(ctx context.Context, r LinkResolver) context.Context {
	return context.WithValue(ctx, linkResolverKey{}, r)
}

func linkResolverFromContext(ctx context.Context) (LinkResolver, bool) {
	r, ok := ctx.Value(linkResolverKey{}).(LinkResolver)
	return r, ok
}

// QueryLinkResolver is the production LinkResolver: one primary-key lookup
// joined to domain, per link-scoped request.
type QueryLinkResolver struct {
	queries *db.Queries
}

// NewQueryLinkResolver builds a LinkResolver backed by queries.
func NewQueryLinkResolver(queries *db.Queries) QueryLinkResolver {
	return QueryLinkResolver{queries: queries}
}

// Link implements LinkResolver.
func (r QueryLinkResolver) Link(ctx context.Context, linkID uuid.UUID) (ResolvedLink, error) {
	row, err := r.queries.GetLinkScope(ctx, linkID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedLink{}, ErrLinkNotFound
	}
	if err != nil {
		return ResolvedLink{}, fmt.Errorf("authz: load link scope: %w", err)
	}
	return ResolvedLink{
		ID:       row.ID,
		TeamID:   row.TeamID,
		DomainID: row.DomainID,
		Hostname: row.Hostname,
		Slug:     row.Slug,
	}, nil
}

// LinkViewerScope is embedded by link operations any team member may call.
type LinkViewerScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

// LinkEditorScope is embedded by link operations requiring at least editor.
type LinkEditorScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

// Resolve loads the link and checks the caller's membership before the handler runs.
func (s *LinkViewerScope) Resolve(ctx huma.Context) []error {
	return resolveLinkScope(ctx, s.LinkID, RoleViewer, &s.member, &s.link)
}

// Resolve loads the link and checks the caller's membership before the handler runs.
func (s *LinkEditorScope) Resolve(ctx huma.Context) []error {
	return resolveLinkScope(ctx, s.LinkID, RoleEditor, &s.member, &s.link)
}

// Member returns the membership Resolve loaded.
func (s *LinkViewerScope) Member() Membership { return s.member }

// Member returns the membership Resolve loaded.
func (s *LinkEditorScope) Member() Membership { return s.member }

// Link returns the link Resolve loaded.
func (s *LinkViewerScope) Link() ResolvedLink { return s.link }

// Link returns the link Resolve loaded.
func (s *LinkEditorScope) Link() ResolvedLink { return s.link }

// resolveLinkScope turns a link ID into an authorization decision: who is
// calling, which team owns the link, and whether that caller's role in that
// team is enough. The team is discovered here rather than taken from the path,
// which is the whole reason this scope exists.
func resolveLinkScope(
	ctx huma.Context, linkID uuid.UUID, required Role, member *Membership, out *ResolvedLink,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed link_id would otherwise be reported as a plain 404 — the wrong
	// defect — so the raw value is re-checked here. Same guard, same reason, as
	// the team_id one in resolveScope.
	if raw := ctx.Param("link_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("link_id must be a valid UUID")}
		}
	}

	resolver, ok := linkResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this link.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Link(ctx.Context(), linkID)
	switch {
	case errors.Is(err, ErrLinkNotFound):
		return []error{huma.Error404NotFound("link not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the link")}
	}

	// A non-member gets the same 404 a missing link gets. An insufficient role
	// gets 403: that caller already knows the link exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "link not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
```

- [ ] **Step 6: Install the resolver in the /v1 middleware**

In `apps/api/internal/api/v1.go`, in `authMiddleware`, replace:

```go
		if d.Queries != nil {
			inner = authz.WithResolver(inner, authz.NewQueryResolver(d.Queries))
		}
```

with:

```go
		if d.Queries != nil {
			inner = authz.WithResolver(inner, authz.NewQueryResolver(d.Queries))
			inner = authz.WithLinkResolver(inner, authz.NewQueryLinkResolver(d.Queries))
		}
```

- [ ] **Step 7: Run the tests**

Run: `cd apps/api && go test ./internal/authz/ ./internal/api/ -v` Expected: PASS.

- [ ] **Step 8: Commit**

```
feat(authz): add link-scoped authorization
```

---

### Task 9: Link audit actions

**Files:**

- Modify: `apps/api/internal/audit/audit.go:26-57`
- Test: `apps/api/internal/audit/audit_test.go`

**Interfaces:**

- Consumes: the existing `audit.Action`, `audit.Entry`, `audit.Log`.
- Produces: `audit.ActionLinkCreated`, `audit.ActionLinkUpdated`, `audit.ActionLinkDeleted`, `audit.EntityLink`.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/audit/audit_test.go`:

```go
func TestLinkActionsAreInTheTaxonomy(t *testing.T) {
	for _, action := range []audit.Action{
		audit.ActionLinkCreated,
		audit.ActionLinkUpdated,
		audit.ActionLinkDeleted,
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
	require.Equal(t, "link", audit.EntityLink)
}

func TestLinkUpdateMetadataMayNotCarryAPassword(t *testing.T) {
	err := audit.Log(context.Background(), nil, audit.Entry{
		Action:     audit.ActionLinkUpdated,
		EntityType: audit.EntityLink,
		Metadata: map[string]any{
			"changed":  []any{"password"},
			"password": "hunter2",
		},
	})

	require.ErrorIs(t, err, audit.ErrForbiddenMetadata,
		"the denylist must still fire for a link entry, not only a team one")
}
```

If `audit.CheckAction` does not exist, add it — a tiny exported predicate so a test can ask the taxonomy a question without writing a row:

```go
// CheckAction reports whether an action is part of the taxonomy. Log applies
// the same check; this exists so callers and tests can ask without writing.
func CheckAction(a Action) error {
	if _, ok := knownActions[a]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, a)
	}
	return nil
}
```

and have `Log` call it instead of repeating the map lookup.

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/audit/ -run Link` Expected: FAIL — `audit.ActionLinkCreated` undefined.

- [ ] **Step 3: Add the actions**

In `apps/api/internal/audit/audit.go`, in the action const block:

```go
	ActionLinkCreated Action = "link.created"
	ActionLinkUpdated Action = "link.updated"
	ActionLinkDeleted Action = "link.deleted"
```

In the entity const block:

```go
	EntityLink = "link"
```

In `knownActions`:

```go
	ActionLinkCreated: {},
	ActionLinkUpdated: {},
	ActionLinkDeleted: {},
```

Add a comment above the three actions recording the decision, so the next reader does not "fix" it back:

```go
	// One row per PATCH, not one per changed field. doc 05 sketches a
	// link.destination_changed action, but a single PATCH can change several
	// fields atomically, and splitting that into several rows would
	// misrepresent one request as several. Which fields moved lives in
	// metadata.changed.
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/audit/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```
feat(audit): add the link action taxonomy
```

---

### Task 10: Create a link

**Files:**

- Create: `apps/api/internal/api/links.go`
- Create: `apps/api/internal/api/links_test.go`
- Modify: `apps/api/internal/api/v1.go:52-57`
- Modify: `apps/api/internal/api/tenancy_test.go`
- Modify: `apps/api/internal/api/matrix_test.go`

**Interfaces:**

- Consumes: `slug.Generate`, `slug.Normalize`, `slug.Validate` (Task 4); `destination.Validate`, `destination.MaxLength` (Task 5); `db.CreateLink`, `db.GetLinkableDomain` (Tasks 1 and 7); `audit.ActionLinkCreated`, `audit.EntityLink` (Task 9); `api.SharedDomain` (Task 3); `cache.Client.InvalidateLink`, `cache.Client.Allow`; `link.CacheKey`.
- Produces: `api.Link` (the JSON representation), `api.LinkOutput`, `Deps.registerLinks(api huma.API)`, and the unexported helpers `Deps.linkResponse`, `Deps.invalidateLink`, `Deps.selfHostnames`, `Deps.resolveLinkDomain` that Tasks 11 and 12 reuse.

- [ ] **Step 1: Extend the tenancy fixture with a shared domain and a link**

In `apps/api/internal/api/tenancy_test.go`, add three fields to `tenancyFixture`:

```go
	sharedDomainID uuid.UUID
	teamDomainID   uuid.UUID
	teamHostname   string
	linkID         uuid.UUID
```

and, in `newTenancyFixture`, after the team and members exist and before the `api.Deps` literal is built:

```go
	sharedHostname := "shared-" + suffix + ".test"
	var sharedDomainID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values (null, $1, 'verified', now()) returning id`, sharedHostname).Scan(&sharedDomainID))
	// A team-less domain is not reached by the team cascade, so it needs its
	// own cleanup or the suite leaks a row per fixture.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where id = $1`, sharedDomainID)
	})

	teamHostname := "team-" + suffix + ".test"
	var teamDomainID, linkID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		teamID, teamHostname).Scan(&teamDomainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 'fixture', 'https://example.org/fixture', $3) returning id`,
		teamDomainID, teamID, members[authz.RoleOwner].id).Scan(&linkID))
```

Set the new fields on the returned fixture, and add to the `api.Deps` literal:

```go
			SharedDomain: api.SharedDomain{ID: sharedDomainID, Hostname: sharedHostname},
```

Also set, next to the existing `cfg.InviteRateLimitPerHour = 20`:

```go
	cfg.SharedDomainHostname = sharedHostname
	cfg.LinkCreateRateLimitPerMin = 100
```

- [ ] **Step 2: Write the failing test**

Create `apps/api/internal/api/links_test.go`:

```go
package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/slug"
)

type linkBody struct {
	ID               uuid.UUID  `json:"id"`
	TeamID           uuid.UUID  `json:"team_id"`
	DomainID         uuid.UUID  `json:"domain_id"`
	Hostname         string     `json:"hostname"`
	Slug             string     `json:"slug"`
	ShortURL         string     `json:"short_url"`
	DestinationURL   string     `json:"destination_url"`
	RedirectType     int        `json:"redirect_type"`
	State            string     `json:"state"`
	ExpiresAt        *time.Time `json:"expires_at"`
	HasPassword      bool       `json:"has_password"`
	AnalyticsEnabled bool       `json:"analytics_enabled"`
	CreatedBy        uuid.UUID  `json:"created_by"`
}

func TestCreateLinkGeneratesASlugOnTheSharedDomain(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/sommerfest"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)

	require.Len(t, body.Slug, slug.Length)
	require.NoError(t, slug.Validate(body.Slug))
	require.Equal(t, f.sharedDomainID, body.DomainID, "no domain_id means the shared domain")
	require.Equal(t, f.teamID, body.TeamID)
	require.Equal(t, "active", body.State)
	require.Equal(t, 302, body.RedirectType)
	require.False(t, body.HasPassword)
	require.True(t, body.AnalyticsEnabled)
	require.Equal(t, f.members[authz.RoleEditor].id, body.CreatedBy)
	require.Contains(t, body.ShortURL, body.Slug)
	require.NotContains(t, rec.Body.String(), "password_hash")
}

func TestCreateLinkAcceptsACustomAliasAndLowercasesIt(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/jhv", "slug": "JHV-2026"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "jhv-2026", decode[linkBody](t, rec).Slug)
}

func TestCreateLinkRejectsATakenAliasWith409(t *testing.T) {
	f := newTenancyFixture(t)
	path := "/v1/teams/" + f.teamID.String() + "/links"
	body := map[string]any{"destination_url": "https://example.org/x", "slug": "sommerfest"}

	require.Equal(t, http.StatusCreated, f.do(t, f.members[authz.RoleEditor], http.MethodPost, path, body).Code)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost, path, body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestCreateLinkRejectsAReservedAlias(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/x", "slug": "health"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"/health is the router's own path and must never become a link")
}

func TestCreateLinkRejectsANonHTTPSDestination(t *testing.T) {
	f := newTenancyFixture(t)

	for _, bad := range []string{
		"http://example.org", "javascript:alert(1)", "https://127.0.0.1/admin",
	} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
			"/v1/teams/"+f.teamID.String()+"/links",
			map[string]any{"destination_url": bad})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%q must be refused", bad)
	}
}

func TestCreateLinkRejectsAPastExpiry(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"expires_at":      time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"creating an already-dead link is never intentional")
}

func TestCreateLinkRefusesAnotherTeamsDomain(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"domain_id":       other.teamDomainID.String(),
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.NotContains(t, rec.Body.String(), other.teamHostname,
		"the error must not disclose a hostname the caller does not own")
}

func TestCreateLinkClearsANegativeCacheEntry(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()

	// Somebody probed this slug before the link existed, so the redirect path
	// cached "no such link". Creating the link must clear that, or the new
	// link 404s for up to NotFoundCacheTTL for no visible reason.
	cacheKey := link.CacheKey(f.deps.SharedDomain.Hostname, "sommerfest")
	require.NoError(t, f.deps.Cache.PutNotFound(ctx, cacheKey, time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/s", "slug": "sommerfest"})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err, "the not-found sentinel must be gone after the link is created")
}

func TestCreateLinkWritesOneAuditRow(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/audit", "slug": "audited"})
	require.Equal(t, http.StatusCreated, rec.Code)
	created := decode[linkBody](t, rec)

	var count int
	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*), coalesce(max(metadata::text), '') from audit_log
		 where team_id = $1 and action = 'link.created' and entity_id = $2`,
		f.teamID, created.ID).Scan(&count, &metadata))

	require.Equal(t, 1, count)
	require.Contains(t, metadata, "audited")
	require.NotContains(t, metadata, "password")
}

func TestCreateLinkIsRefusedForAViewer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/x"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/api/ -run CreateLink` Expected: FAIL — 404, because no create operation is registered.

- [ ] **Step 4: Write the representation and the shared helpers**

Create `apps/api/internal/api/links.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/destination"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	slugpkg "github.com/mheob/kurze-url/apps/api/internal/slug"
)

// generatedSlugAttempts bounds the retry loop. At 32^8 combinations, five
// collisions in a row means something is wrong that a sixth draw will not fix.
const generatedSlugAttempts = 5

// Link is a link as the API reports it. password_hash never appears here in
// any form; HasPassword is the only projection of it that leaves the database.
type Link struct {
	ID               uuid.UUID  `json:"id"`
	TeamID           uuid.UUID  `json:"team_id"`
	DomainID         uuid.UUID  `json:"domain_id"`
	Hostname         string     `json:"hostname"`
	Slug             string     `json:"slug"`
	ShortURL         string     `json:"short_url"`
	DestinationURL   string     `json:"destination_url"`
	RedirectType     int        `json:"redirect_type"`
	State            string     `json:"state"`
	ExpiresAt        *time.Time `json:"expires_at"`
	HasPassword      bool       `json:"has_password"`
	AnalyticsEnabled bool       `json:"analytics_enabled"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// linkRow is the shape every link query returns. sqlc generates a distinct Go
// type per query even when the columns are identical, so the converters below
// funnel all of them through one place — and one place is where short_url gets
// composed.
type linkRow struct {
	ID               uuid.UUID
	TeamID           uuid.UUID
	DomainID         uuid.UUID
	Hostname         string
	Slug             string
	DestinationURL   string
	RedirectType     int16
	State            string
	ExpiresAt        *time.Time
	HasPassword      bool
	AnalyticsEnabled bool
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (d Deps) linkResponse(r linkRow) Link {
	return Link{
		ID:               r.ID,
		TeamID:           r.TeamID,
		DomainID:         r.DomainID,
		Hostname:         r.Hostname,
		Slug:             r.Slug,
		ShortURL:         fmt.Sprintf("%s://%s/%s", d.Config.ShortURLScheme, r.Hostname, r.Slug),
		DestinationURL:   r.DestinationURL,
		RedirectType:     int(r.RedirectType),
		State:            r.State,
		ExpiresAt:        r.ExpiresAt,
		HasPassword:      r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled,
		CreatedBy:        r.CreatedBy,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func rowFromCreate(r db.CreateLinkRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromGet(r db.GetLinkForAPIRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromUpdate(r db.UpdateLinkRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func rowFromList(r db.ListLinksForTeamRow) linkRow {
	return linkRow{
		ID: r.ID, TeamID: r.TeamID, DomainID: r.DomainID, Hostname: r.Hostname,
		Slug: r.Slug, DestinationURL: r.DestinationURL, RedirectType: r.RedirectType,
		State: r.State, ExpiresAt: r.ExpiresAt, HasPassword: r.HasPassword,
		AnalyticsEnabled: r.AnalyticsEnabled, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// selfHostnames is the set of hostnames a destination may not point at,
// because doing so is a redirect loop.
func (d Deps) selfHostnames() []string {
	return []string{d.Config.SharedDomainHostname, d.Config.APIHostname}
}

// invalidateLink drops a link's redirect-cache entry after its transaction has
// committed. Best-effort by design: the database is the source of truth and
// LinkCacheTTL bounds the staleness, so a Redis failure here is worth an error
// log, not a failed request whose write already landed.
func (d Deps) invalidateLink(ctx context.Context, hostname, slug string) {
	if d.Cache == nil {
		return
	}
	if err := d.Cache.InvalidateLink(ctx, link.CacheKey(hostname, slug)); err != nil {
		d.Log.Error("invalidate redirect cache",
			"error", err, "hostname", hostname, "slug", slug)
	}
}

// resolveLinkDomain answers which domain a new link goes on. No domain_id
// means the shared one, which costs no query because it was resolved at boot.
func (d Deps) resolveLinkDomain(
	ctx context.Context, teamID uuid.UUID, requested *uuid.UUID,
) (uuid.UUID, string, error) {
	if requested == nil {
		if d.SharedDomain.ID == uuid.Nil {
			return uuid.Nil, "", huma.Error500InternalServerError("no shared domain is configured")
		}
		return d.SharedDomain.ID, d.SharedDomain.Hostname, nil
	}

	row, err := d.Queries.GetLinkableDomain(ctx, db.GetLinkableDomainParams{
		ID: *requested, TeamID: teamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// One message for "unverified", "another team's" and "no such domain".
		// Telling them apart would confirm the existence of a hostname the
		// caller does not own.
		return uuid.Nil, "", huma.Error422UnprocessableEntity("that domain is not available to this team")
	}
	if err != nil {
		d.Log.Error("resolve link domain", "error", err)
		return uuid.Nil, "", huma.Error500InternalServerError("could not resolve the domain")
	}
	return row.ID, row.Hostname, nil
}

// isUniqueViolation reports whether err is Postgres' 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

- [ ] **Step 5: Write the create handler**

Append to `apps/api/internal/api/links.go`:

```go
// CreateLinkInput declares its authorization in its type: EditorScope resolves
// and checks the caller's role before this handler's body runs.
type CreateLinkInput struct {
	authz.EditorScope
	Body struct {
		DestinationURL string `json:"destination_url" maxLength:"2048" doc:"Where the link points. https:// only."`
		Slug           string `json:"slug,omitempty" maxLength:"64" doc:"Optional custom alias. Lowercased on input; generated when omitted."`
		DomainID       *uuid.UUID `json:"domain_id,omitempty" doc:"Optional. Defaults to the instance's shared domain."`
		RedirectType   int        `json:"redirect_type,omitempty" enum:"301,302" default:"302" doc:"301 is cached by browsers: clicks go uncounted and destination changes stop taking effect."`
		ExpiresAt      *time.Time `json:"expires_at,omitempty" doc:"Must be in the future."`
		AnalyticsEnabled *bool    `json:"analytics_enabled,omitempty" doc:"Defaults to true."`
	}
}

type LinkOutput struct {
	Status int
	Body   Link
}

func (d Deps) registerLinks(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-link",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/links",
		Summary:       "Create a link",
		Tags:          []string{"Links"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createLink)
}

func (d Deps) createLink(ctx context.Context, in *CreateLinkInput) (*LinkOutput, error) {
	member := in.Member()

	if err := d.allowLinkCreate(ctx, member.UserID); err != nil {
		return nil, err
	}

	if err := destination.Validate(in.Body.DestinationURL, d.selfHostnames()); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	if in.Body.ExpiresAt != nil && !in.Body.ExpiresAt.After(d.now()) {
		return nil, huma.Error422UnprocessableEntity("expires_at must be in the future")
	}

	domainID, hostname, err := d.resolveLinkDomain(ctx, member.TeamID, in.Body.DomainID)
	if err != nil {
		return nil, err
	}

	custom := slugpkg.Normalize(in.Body.Slug)
	if custom != "" {
		if err := slugpkg.Validate(custom); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
	}

	redirectType := in.Body.RedirectType
	if redirectType == 0 {
		redirectType = http.StatusFound
	}
	analyticsEnabled := true
	if in.Body.AnalyticsEnabled != nil {
		analyticsEnabled = *in.Body.AnalyticsEnabled
	}

	var created db.CreateLinkRow
	for attempt := range generatedSlugAttempts {
		candidate := custom
		if candidate == "" {
			candidate, err = slugpkg.Generate()
			if err != nil {
				d.Log.Error("generate slug", "error", err)
				return nil, huma.Error500InternalServerError("could not create the link")
			}
		}

		err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
			row, err := q.CreateLink(ctx, db.CreateLinkParams{
				DomainID:         domainID,
				TeamID:           member.TeamID,
				Slug:             candidate,
				DestinationURL:   in.Body.DestinationURL,
				RedirectType:     int16(redirectType),
				ExpiresAt:        in.Body.ExpiresAt,
				AnalyticsEnabled: analyticsEnabled,
				CreatedBy:        member.UserID,
			})
			if err != nil {
				return err
			}
			created = row

			return audit.Log(ctx, q, audit.Entry{
				TeamID:      member.TeamID,
				ActorUserID: member.UserID,
				Action:      audit.ActionLinkCreated,
				EntityType:  audit.EntityLink,
				EntityID:    row.ID,
				Metadata: map[string]any{
					"slug":            row.Slug,
					"hostname":        row.Hostname,
					"destination_url": row.DestinationURL,
					"redirect_type":   int(row.RedirectType),
				},
			})
		})

		switch {
		case err == nil:
			// Creating a link must clear the redirect cache, not only changing
			// one: a probe of this slug before it existed may have stored the
			// not-found sentinel under exactly this key.
			d.invalidateLink(ctx, created.Hostname, created.Slug)
			return &LinkOutput{Status: http.StatusCreated, Body: d.linkResponse(rowFromCreate(created))}, nil

		case isUniqueViolation(err) && custom != "":
			// The caller asked for this exact slug, so there is nothing to
			// retry. On the shared hostname this does disclose that some other
			// team holds it — inherent to one shared namespace.
			return nil, huma.Error409Conflict("that slug is already taken on this domain")

		case isUniqueViolation(err):
			continue

		default:
			d.Log.Error("create link", "error", err, "attempt", attempt)
			return nil, huma.Error500InternalServerError("could not create the link")
		}
	}

	d.Log.Error("exhausted slug generation attempts", "attempts", generatedSlugAttempts)
	return nil, huma.Error500InternalServerError("could not create the link")
}

// allowLinkCreate applies the per-user creation limit. The subject is the
// authenticated user, so no IP is involved. A Redis failure degrades open: an
// outage in the rate limiter must not stop a Verein publishing a link.
func (d Deps) allowLinkCreate(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.LinkCreateRateLimitPerMin <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:link-create:"+userID.String(), d.Config.LinkCreateRateLimitPerMin, time.Minute)
	if err != nil {
		d.Log.Error("link create rate limit check failed", "error", err)
		return nil
	}
	if !ok {
		return huma.Error429TooManyRequests("too many links created; try again shortly")
	}
	return nil
}
```

- [ ] **Step 6: Register the group**

In `apps/api/internal/api/v1.go`, in `RegisterV1`, after `d.registerAuditLog(api)`:

```go
	d.registerLinks(api)
```

- [ ] **Step 7: Add the matrix row**

In `apps/api/internal/api/matrix_test.go`, append to `teamScopedCases`:

```go
	{"create-link", http.MethodPost, "/v1/teams/{team}/links",
		map[string]string{"destination_url": "https://example.org/matrix"}, authz.RoleEditor},
```

Without this row `TestEveryOperationIsAccountedFor` fails the moment the operation is registered — which is the guard working as intended.

- [ ] **Step 8: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -run 'CreateLink|Matrix|AccountedFor' -v` Expected: PASS.

- [ ] **Step 9: Falsify the create-path invalidation**

Comment out the `d.invalidateLink(...)` call in the success branch and run `go test ./internal/api/ -run CreateLinkClearsANegativeCacheEntry`. Expected: FAIL. Restore it and confirm PASS. Record the result in the report.

- [ ] **Step 10: Commit**

```
feat(api): add link creation
```

---

### Task 11: List a team's links

**Files:**

- Modify: `apps/api/internal/api/links.go`
- Modify: `apps/api/internal/api/links_test.go`
- Modify: `apps/api/internal/api/matrix_test.go`

**Interfaces:**

- Consumes: `db.ListLinksForTeam`, `db.CountLinksForTeam` (Task 7); `PageParams`, `NewPage`, `NeedsTotalFallback`, `Page[T]`; `Deps.linkResponse`, `rowFromList` (Task 10).
- Produces: `api.ListLinksInput`, `api.ListLinksOutput` with `Body Page[Link]`, operation ID `list-links`.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/api/links_test.go`:

```go
func (f *tenancyFixture) createLink(t *testing.T, slug, dest string) linkBody {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": dest, "slug": slug})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[linkBody](t, rec)
}

type linkPage struct {
	Items      []linkBody `json:"items"`
	Page       int        `json:"page"`
	PerPage    int        `json:"per_page"`
	TotalCount int        `json:"total_count"`
}

func TestListLinksReturnsThePageEnvelope(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "eins", "https://example.org/eins")
	f.createLink(t, "zwei", "https://example.org/zwei")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[linkPage](t, rec)

	// The fixture itself seeds one link, so two created here makes three.
	require.Equal(t, 3, page.TotalCount)
	require.Len(t, page.Items, 3)
	require.Equal(t, 1, page.Page)
	for _, item := range page.Items {
		require.Equal(t, f.teamID, item.TeamID)
		require.NotEmpty(t, item.ShortURL)
	}
}

func TestListLinksDefaultsToNewestFirst(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aelter", "https://example.org/a")
	time.Sleep(10 * time.Millisecond)
	newest := f.createLink(t, "neuer", "https://example.org/b")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, newest.ID, decode[linkPage](t, rec).Items[0].ID)
}

func TestListLinksSortsAscendingOnRequest(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aelter", "https://example.org/a")
	time.Sleep(10 * time.Millisecond)
	f.createLink(t, "neuer", "https://example.org/b")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?sort=created_at", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "fixture", decode[linkPage](t, rec).Items[0].Slug,
		"ascending starts with the fixture's own, oldest link")
}

func TestListLinksFiltersBySubstring(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "sommerfest", "https://example.org/sommer")
	f.createLink(t, "winterfeier", "https://example.org/winter")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?q=sommer", nil)

	page := decode[linkPage](t, rec)
	require.Len(t, page.Items, 1)
	require.Equal(t, "sommerfest", page.Items[0].Slug)
}

func TestListLinksFiltersByDomain(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aufshared", "https://example.org/s")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?domain_id="+f.sharedDomainID.String(), nil)

	page := decode[linkPage](t, rec)
	require.Len(t, page.Items, 1, "the fixture's own link is on the team domain, not the shared one")
	require.Equal(t, "aufshared", page.Items[0].Slug)
}

func TestListLinksRejectsAnUnknownSort(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?sort=clicks", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"click-count sorting needs a join this plan does not build; refuse rather than ignore")
}

func TestListLinksNeverShowsAnotherTeamsLinks(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	other.createLink(t, "geheim", "https://example.org/geheim")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?per_page=100", nil)

	for _, item := range decode[linkPage](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
		require.NotEqual(t, "geheim", item.Slug)
	}
}

func TestListLinksReportsATotalPastTheLastPage(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "eins", "https://example.org/eins")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?page=9&per_page=10", nil)

	page := decode[linkPage](t, rec)
	require.Empty(t, page.Items)
	require.Equal(t, 2, page.TotalCount,
		"a page past the end still has to report how many there are")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/api/ -run ListLinks` Expected: FAIL — 404, no list operation registered.

- [ ] **Step 3: Write the handler**

Append to `apps/api/internal/api/links.go`:

```go
// ListLinksInput takes flat, explicitly typed filters — not a generic
// filter=field:op:value scheme, which is impossible to type in OpenAPI and
// impossible to index for.
type ListLinksInput struct {
	authz.ViewerScope
	PageParams
	Q        string     `query:"q" maxLength:"200" doc:"Substring match across the slug and the destination URL."`
	State    string     `query:"state" enum:"active,disabled,expired,flagged" doc:"Restrict to one state."`
	DomainID *uuid.UUID `query:"domain_id" doc:"Restrict to one domain."`
	Sort     string     `query:"sort" enum:"created_at,-created_at" default:"-created_at" doc:"Newest first by default."`
}

type ListLinksOutput struct {
	Body Page[Link]
}

func (d Deps) listLinks(ctx context.Context, in *ListLinksInput) (*ListLinksOutput, error) {
	member := in.Member()

	params := db.ListLinksForTeamParams{
		TeamID:  member.TeamID,
		SortAsc: in.Sort == "created_at",
		Limit:   in.Limit(),
		Offset:  in.Offset(),
	}
	countParams := db.CountLinksForTeamParams{TeamID: member.TeamID}

	if in.Q != "" {
		params.Q, countParams.Q = &in.Q, &in.Q
	}
	if in.State != "" {
		params.State, countParams.State = &in.State, &in.State
	}
	if in.DomainID != nil {
		params.DomainID, countParams.DomainID = in.DomainID, in.DomainID
	}

	rows, err := d.Queries.ListLinksForTeam(ctx, params)
	if err != nil {
		d.Log.Error("list links", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not list links")
	}

	items := make([]Link, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, d.linkResponse(rowFromList(row)))
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountLinksForTeam(ctx, countParams)
		if err != nil {
			d.Log.Error("count links", "error", err, "team_id", member.TeamID)
			return nil, huma.Error500InternalServerError("could not list links")
		}
	}

	return &ListLinksOutput{Body: NewPage(items, in.PageParams, total)}, nil
}
```

`in.Sort` carries the direction; `SortAsc` is the only thing the query needs, because `created_at` is the only sortable column this plan supports. Huma's `enum` tag is what refuses `sort=clicks` with a 422 — there is no extra check to write, and no silent fallback to a default the caller did not ask for.

- [ ] **Step 4: Register it**

In `registerLinks`, after the create registration:

```go
	huma.Register(api, huma.Operation{
		OperationID: "list-links",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/links",
		Summary:     "List a team's links",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listLinks)
```

- [ ] **Step 5: Add the matrix row**

Append to `teamScopedCases`:

```go
	{"list-links", http.MethodGet, "/v1/teams/{team}/links", nil, authz.RoleViewer},
```

- [ ] **Step 6: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -run 'ListLinks|AccountedFor' -v` Expected: PASS.

- [ ] **Step 7: Commit**

```
feat(api): add the link list endpoint
```

---

### Task 12: Read, update and delete a link

**Files:**

- Modify: `apps/api/internal/api/links.go`
- Modify: `apps/api/internal/api/links_test.go`
- Modify: `apps/api/internal/api/matrix_test.go`

**Interfaces:**

- Consumes: `authz.LinkViewerScope`, `authz.LinkEditorScope` (Task 8); `db.GetLinkForAPI`, `db.UpdateLink`, `db.DeleteLink` (Task 7); `audit.ActionLinkUpdated`, `audit.ActionLinkDeleted` (Task 9); the helpers from Task 10.
- Produces: operation IDs `get-link`, `update-link`, `delete-link`; `api.DeleteLinkOutput` with `Status int`.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/api/links_test.go`:

```go
func TestGetLinkReturnsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "lesen", "https://example.org/lesen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+created.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, created.ID, decode[linkBody](t, rec).ID)
	require.NotContains(t, rec.Body.String(), "password_hash")
}

func TestGetLinkIs404ForAnUnknownID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+uuid.NewString(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetLinkIs422ForAMalformedID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestUpdateLinkChangesTheDestinationAndInvalidatesTheCache(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "aendern", "https://example.org/alt")

	// Warm the cache the way a real visit would.
	cacheKey := link.CacheKey(created.Hostname, created.Slug)
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: created.ID, TeamID: created.TeamID, DestinationURL: created.DestinationURL,
		RedirectType: 302, State: "active", AnalyticsEnabled: true,
	}, time.Hour))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "https://example.org/neu", decode[linkBody](t, rec).DestinationURL)

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err,
		"a 302 promises destination changes take effect immediately, not after LinkCacheTTL")
}

func TestUpdateLinkChangingTheSlugInvalidatesBothKeys(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "alt", "https://example.org/x")

	oldKey := link.CacheKey(created.Hostname, "alt")
	newKey := link.CacheKey(created.Hostname, "neu")
	require.NoError(t, f.deps.Cache.PutLink(ctx, oldKey, link.Cached{ID: created.ID}, time.Hour))
	require.NoError(t, f.deps.Cache.PutNotFound(ctx, newKey, time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"slug": "NEU"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "neu", decode[linkBody](t, rec).Slug)

	_, err := f.deps.Cache.Raw().Get(ctx, oldKey).Result()
	require.Error(t, err, "the old slug must stop resolving")
	_, err = f.deps.Cache.Raw().Get(ctx, newKey).Result()
	require.Error(t, err, "the new slug's cached not-found sentinel must be cleared too")
}

func TestUpdateLinkRefusesAPasswordField(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "kennwort", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"password": "hunter2"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"passwords have their own endpoint, their own audit action and a tighter rate limit")
}

func TestUpdateLinkRefusesMovingItToAnotherDomain(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "umziehen", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"domain_id": f.teamDomainID.String()})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"moving a link changes its short URL, breaking every printed copy of it")
}

func TestUpdateLinkAcceptsOnlyActiveAndDisabled(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "zustand", "https://example.org/x")
	path := "/v1/links/" + created.ID.String()

	require.Equal(t, http.StatusOK,
		f.do(t, f.members[authz.RoleEditor], http.MethodPatch, path,
			map[string]any{"state": "disabled"}).Code)

	for _, systemState := range []string{"flagged", "expired"} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, path,
			map[string]any{"state": systemState})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"%q is set by the system, never by a caller", systemState)
	}
}

func TestUpdateLinkWritesOneAuditRowNamingWhatChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "protokoll", "https://example.org/alt")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu", "redirect_type": 301})
	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*), coalesce(max(metadata::text), '') from audit_log
		 where team_id = $1 and action = 'link.updated' and entity_id = $2`,
		f.teamID, created.ID).Scan(&count, &metadata))

	require.Equal(t, 1, count, "one PATCH is one audit row, however many fields it touched")
	require.Contains(t, metadata, "destination_url")
	require.Contains(t, metadata, "redirect_type")
	require.Contains(t, metadata, "changed")
	require.NotContains(t, metadata, "slug", "a field the request did not change must not be listed")
}

func TestUpdateLinkWritesNoAuditRowWhenNothingChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "unveraendert", "https://example.org/gleich")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/gleich"})
	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'link.updated' and entity_id = $1`,
		created.ID).Scan(&count))
	require.Zero(t, count, "a no-op PATCH must not write a misleading audit entry")
}

func TestDeleteLinkRemovesItAndTheCacheEntry(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "weg", "https://example.org/weg")

	cacheKey := link.CacheKey(created.Hostname, created.Slug)
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{ID: created.ID}, time.Hour))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete, "/v1/links/"+created.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Equal(t, http.StatusNotFound,
		f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+created.ID.String(), nil).Code)

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err, "a deleted link must stop resolving immediately")

	var count int
	require.NoError(t, f.pool.QueryRow(ctx,
		`select count(*) from audit_log where action = 'link.deleted' and entity_id = $1`,
		created.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestUpdateAndDeleteAreRefusedForAViewer(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nurlesen", "https://example.org/x")
	path := "/v1/links/" + created.ID.String()

	require.Equal(t, http.StatusForbidden,
		f.do(t, f.members[authz.RoleViewer], http.MethodPatch, path,
			map[string]any{"state": "disabled"}).Code)
	require.Equal(t, http.StatusForbidden,
		f.do(t, f.members[authz.RoleViewer], http.MethodDelete, path, nil).Code)
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/api/ -run 'GetLink|UpdateLink|DeleteLink'` Expected: FAIL — 404, none of the three operations are registered.

- [ ] **Step 3: Write the three handlers**

Append to `apps/api/internal/api/links.go`:

```go
type GetLinkInput struct {
	authz.LinkViewerScope
}

// UpdateLinkInput deliberately omits two fields the body might be expected to
// carry. password has its own endpoint, so it maps to its own audit action and
// its own tighter rate limit. domain_id is omitted because moving a link
// between domains changes its short URL, silently breaking every printed copy,
// and across teams it would break the link.team_id denormalization. Huma
// rejects unknown body fields, so both are 422 rather than silently ignored.
type UpdateLinkInput struct {
	authz.LinkEditorScope
	Body struct {
		DestinationURL   *string    `json:"destination_url,omitempty" maxLength:"2048"`
		Slug             *string    `json:"slug,omitempty" maxLength:"64"`
		RedirectType     *int       `json:"redirect_type,omitempty" enum:"301,302"`
		State            *string    `json:"state,omitempty" enum:"active,disabled" doc:"expired follows from expires_at and flagged is set by scanning; neither is a caller's to write."`
		ExpiresAt        *time.Time `json:"expires_at,omitempty"`
		AnalyticsEnabled *bool      `json:"analytics_enabled,omitempty"`
	}
}

type DeleteLinkInput struct {
	authz.LinkEditorScope
}

type DeleteLinkOutput struct {
	Status int
}

func (d Deps) getLink(ctx context.Context, in *GetLinkInput) (*LinkOutput, error) {
	member := in.Member()

	// The scope already authorized this caller. The team filter is here anyway:
	// it is what a reviewer can see, and the matrix test cannot see a missing one.
	row, err := d.Queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
		ID: in.Link().ID, TeamID: member.TeamID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("link not found")
	}
	if err != nil {
		d.Log.Error("get link", "error", err, "link_id", in.LinkID)
		return nil, huma.Error500InternalServerError("could not load the link")
	}

	return &LinkOutput{Status: http.StatusOK, Body: d.linkResponse(rowFromGet(row))}, nil
}

func (d Deps) updateLink(ctx context.Context, in *UpdateLinkInput) (*LinkOutput, error) {
	member := in.Member()

	if in.Body.DestinationURL != nil {
		if err := destination.Validate(*in.Body.DestinationURL, d.selfHostnames()); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
	}
	if in.Body.ExpiresAt != nil && !in.Body.ExpiresAt.After(d.now()) {
		return nil, huma.Error422UnprocessableEntity("expires_at must be in the future")
	}

	var newSlug string
	if in.Body.Slug != nil {
		newSlug = slugpkg.Normalize(*in.Body.Slug)
		if err := slugpkg.Validate(newSlug); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
	}

	// updated is a linkRow rather than a db.UpdateLinkRow because the no-op
	// branch below has only a db.GetLinkForAPIRow to offer. The two generated
	// structs happen to have identical fields today, but converting between
	// them would silently break the first time a column is added to one query
	// and not the other.
	var (
		updated  linkRow
		previous db.GetLinkForAPIRow
		changed  []string
	)

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		before, err := q.GetLinkForAPI(ctx, db.GetLinkForAPIParams{
			ID: in.Link().ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}
		previous = before

		params := db.UpdateLinkParams{
			ID:               before.ID,
			TeamID:           member.TeamID,
			Slug:             before.Slug,
			DestinationURL:   before.DestinationURL,
			RedirectType:     before.RedirectType,
			State:            before.State,
			ExpiresAt:        before.ExpiresAt,
			AnalyticsEnabled: before.AnalyticsEnabled,
		}
		metadata := map[string]any{}

		if newSlug != "" && newSlug != before.Slug {
			params.Slug = newSlug
			changed = append(changed, "slug")
			metadata["slug"] = map[string]any{"from": before.Slug, "to": newSlug}
		}
		if in.Body.DestinationURL != nil && *in.Body.DestinationURL != before.DestinationURL {
			params.DestinationURL = *in.Body.DestinationURL
			changed = append(changed, "destination_url")
			metadata["destination_url"] = map[string]any{
				"from": before.DestinationURL, "to": *in.Body.DestinationURL,
			}
		}
		if in.Body.RedirectType != nil && int16(*in.Body.RedirectType) != before.RedirectType {
			params.RedirectType = int16(*in.Body.RedirectType)
			changed = append(changed, "redirect_type")
			metadata["redirect_type"] = map[string]any{
				"from": int(before.RedirectType), "to": *in.Body.RedirectType,
			}
		}
		if in.Body.State != nil && *in.Body.State != before.State {
			params.State = *in.Body.State
			changed = append(changed, "state")
			metadata["state"] = map[string]any{"from": before.State, "to": *in.Body.State}
		}
		if in.Body.ExpiresAt != nil && (before.ExpiresAt == nil || !before.ExpiresAt.Equal(*in.Body.ExpiresAt)) {
			params.ExpiresAt = in.Body.ExpiresAt
			changed = append(changed, "expires_at")
			metadata["expires_at"] = map[string]any{"to": in.Body.ExpiresAt}
		}
		if in.Body.AnalyticsEnabled != nil && *in.Body.AnalyticsEnabled != before.AnalyticsEnabled {
			params.AnalyticsEnabled = *in.Body.AnalyticsEnabled
			changed = append(changed, "analytics_enabled")
			metadata["analytics_enabled"] = map[string]any{
				"from": before.AnalyticsEnabled, "to": *in.Body.AnalyticsEnabled,
			}
		}

		if len(changed) == 0 {
			// Nothing changed; do not write a misleading audit entry, and do
			// not bump updated_at either.
			updated = rowFromGet(before)
			return nil
		}

		row, err := q.UpdateLink(ctx, params)
		if err != nil {
			return err
		}
		updated = rowFromUpdate(row)
		metadata["changed"] = changed

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionLinkUpdated,
			EntityType:  audit.EntityLink,
			EntityID:    row.ID,
			Metadata:    metadata,
		})
	})

	switch {
	case isUniqueViolation(err):
		return nil, huma.Error409Conflict("that slug is already taken on this domain")
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("link not found")
	case err != nil:
		d.Log.Error("update link", "error", err, "link_id", in.LinkID)
		return nil, huma.Error500InternalServerError("could not update the link")
	}

	if len(changed) > 0 {
		d.invalidateLink(ctx, previous.Hostname, previous.Slug)
		if updated.Slug != previous.Slug {
			// The new key may hold a not-found sentinel from a probe.
			d.invalidateLink(ctx, updated.Hostname, updated.Slug)
		}
	}

	return &LinkOutput{Status: http.StatusOK, Body: d.linkResponse(updated)}, nil
}

func (d Deps) deleteLink(ctx context.Context, in *DeleteLinkInput) (*DeleteLinkOutput, error) {
	member := in.Member()
	resolved := in.Link()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		affected, err := q.DeleteLink(ctx, db.DeleteLinkParams{
			ID: resolved.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return pgx.ErrNoRows
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionLinkDeleted,
			EntityType:  audit.EntityLink,
			EntityID:    resolved.ID,
			Metadata: map[string]any{
				"slug":     resolved.Slug,
				"hostname": resolved.Hostname,
			},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("link not found")
	case err != nil:
		d.Log.Error("delete link", "error", err, "link_id", in.LinkID)
		return nil, huma.Error500InternalServerError("could not delete the link")
	}

	d.invalidateLink(ctx, resolved.Hostname, resolved.Slug)

	return &DeleteLinkOutput{Status: http.StatusNoContent}, nil
}
```

An unknown body field such as `password` or `domain_id` is refused with 422 because Huma emits `additionalProperties: false` for a struct body schema. If the pinned Huma version does not, add an explicit rejection rather than letting the field be silently ignored — silently ignoring `password` would be the worst of the three possible behaviours.

- [ ] **Step 4: Register all three**

Append to `registerLinks`:

```go
	huma.Register(api, huma.Operation{
		OperationID: "get-link",
		Method:      http.MethodGet,
		Path:        "/v1/links/{link_id}",
		Summary:     "Get a link",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.getLink)

	huma.Register(api, huma.Operation{
		OperationID: "update-link",
		Method:      http.MethodPatch,
		Path:        "/v1/links/{link_id}",
		Summary:     "Update a link",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateLink)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-link",
		Method:        http.MethodDelete,
		Path:          "/v1/links/{link_id}",
		Summary:       "Delete a link",
		Tags:          []string{"Links"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.deleteLink)
```

- [ ] **Step 5: Teach the matrix about link paths**

In `apps/api/internal/api/matrix_test.go`, extend `renderPath`:

```go
	path = strings.ReplaceAll(path, "{link}", f.linkID.String())
```

and append three rows to `teamScopedCases`:

```go
	{"get-link", http.MethodGet, "/v1/links/{link}", nil, authz.RoleViewer},
	{"update-link", http.MethodPatch, "/v1/links/{link}",
		map[string]string{"state": "disabled"}, authz.RoleEditor},
	{"delete-link", http.MethodDelete, "/v1/links/{link}", nil, authz.RoleEditor},
```

The non-member subtest expects 404 for these rows, which is exactly what the link scope produces: the link resolves, the membership does not, and a 404 is returned so link IDs cannot be probed.

- [ ] **Step 6: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -v` Expected: PASS, including every plan 1 and plan 2 test.

- [ ] **Step 7: Commit**

```
feat(api): add link read, update and delete
```

---

### Task 13: Isolation, falsification and the documentation amendments

**Files:**

- Create: `apps/api/internal/api/links_isolation_test.go`
- Modify: `docs/planning/05-database-schema.md`
- Modify: `docs/planning/06-api-design.md`
- Modify: `CLAUDE.md`

**Interfaces:**

- Consumes: everything the previous twelve tasks produced.
- Produces: no new code interface. This task's deliverable is the evidence that the tenancy boundary holds, and the documentation that stops the next plan from re-litigating settled decisions.

- [ ] **Step 1: Write the cross-team isolation tests**

Create `apps/api/internal/api/links_isolation_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// TestALinkIsInvisibleToEveryOtherTeam is the whole point of the entity scope.
// Two independent fixtures means two independent teams, and the owner of one is
// a total stranger to the other.
func TestALinkIsInvisibleToEveryOtherTeam(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	victim := theirs.createLink(t, "vertraulich", "https://example.org/vertraulich")
	path := "/v1/links/" + victim.ID.String()

	for name, tc := range map[string]struct {
		method string
		body   any
	}{
		"read":   {http.MethodGet, nil},
		"update": {http.MethodPatch, map[string]any{"state": "disabled"}},
		"delete": {http.MethodDelete, nil},
	} {
		t.Run(name, func(t *testing.T) {
			rec := mine.do(t, mine.members[authz.RoleOwner], tc.method, path, tc.body)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"an owner of another team must not learn that this link exists")
			require.NotContains(t, rec.Body.String(), "vertraulich")
			require.NotContains(t, rec.Body.String(), "403")
		})
	}

	// And nothing was actually done to it.
	rec := theirs.do(t, theirs.members[authz.RoleViewer], http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "active", decode[linkBody](t, rec).State)
}

func TestAStrangerSeesNoLinksAtAll(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "intern", "https://example.org/intern")

	require.Equal(t, http.StatusNotFound,
		f.do(t, f.stranger, http.MethodGet, "/v1/links/"+created.ID.String(), nil).Code)
	require.Equal(t, http.StatusNotFound,
		f.do(t, f.stranger, http.MethodGet, "/v1/teams/"+f.teamID.String()+"/links", nil).Code)
}

// TestCreatedLinkResolvesThroughTheRedirectPath closes the loop: the endpoints
// this plan adds and the hot path plan 1 built have to agree about the same
// link, including the case-insensitivity Task 6 introduced.
func TestCreatedLinkResolvesThroughTheRedirectPath(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sommerfest", "https://example.org/sommerfest")

	rec := f.redirect(t, created.Hostname, "SommerFest")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/sommerfest", rec.Header().Get("Location"))
}

// TestDeletedLinkStopsResolving is the other half of that loop.
func TestDeletedLinkStopsResolving(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "abgesagt", "https://example.org/abgesagt")
	require.Equal(t, http.StatusFound, f.redirect(t, created.Hostname, created.Slug).Code)

	require.Equal(t, http.StatusNoContent,
		f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
			"/v1/links/"+created.ID.String(), nil).Code)

	require.Equal(t, http.StatusNotFound,
		f.redirect(t, created.Hostname, created.Slug).Code)
}
```

Add the `redirect` helper to `tenancyFixture` in `tenancy_test.go` — the fixture currently only speaks to `/v1`, and these two tests need the public surface:

```go
// redirect issues a request to the public redirect surface on a short-link
// hostname, using the same router the API serves.
func (f *tenancyFixture) redirect(t *testing.T, hostname, slug string) *httptest.ResponseRecorder {
	t.Helper()

	router := chi.NewRouter()
	router.Get("/{slug}", f.deps.HandleRedirect)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://"+hostname+"/"+slug, nil)
	router.ServeHTTP(rec, req)
	return rec
}
```

- [ ] **Step 2: Run them**

Run: `cd apps/api && go test ./internal/api/ -run 'Invisible|Stranger|ResolvesThrough|StopsResolving' -v` Expected: PASS.

- [ ] **Step 3: Falsify the reserved-slug list**

Comment out the reserved-map lookup in `slug.Validate` and run `go test ./internal/api/ -run CreateLinkRejectsAReservedAlias` and `go test ./internal/slug/ -run Reserved`. Expected: both FAIL. Restore and confirm both PASS.

- [ ] **Step 4: Falsify the link scope's tenancy decision**

In `resolveLinkScope`, temporarily replace the `resolveMembership` call with `*member = Membership{TeamID: resolved.TeamID, Role: RoleOwner}` — the shape of the bug where a resolver authorizes nothing — and run `go test ./internal/api/ -run Invisible`. Expected: FAIL, with another team's link readable. Restore and confirm PASS.

This is the single most valuable falsification in the plan. The permission matrix cannot catch this class of bug, because the operation still declares a scope and still returns a plausible status.

- [ ] **Step 5: Run the whole suite**

Run: `cd apps/api && go test ./... && go vet ./... && golangci-lint run` Expected: PASS, zero issues, zero skips. A skipped test means Docker or the local Supabase stack is not running — start them rather than accepting the skip.

- [ ] **Step 6: Exercise the binary by hand**

Run: `cd apps/api && go run ./cmd/api` and, against it, create a link, read it back, change its destination, follow the short URL, and delete it. Confirm the redirect follows the new destination immediately after the change rather than an hour later. A green suite is not the same as a working binary.

- [ ] **Step 7: Amend doc 05**

In `docs/planning/05-database-schema.md`, next to the `link.team_id` denormalization note, add:

```markdown
Amended 2026-09-03 (see `docs/superpowers/specs/2026-09-03-links-and-shared-domain-design.md`): `domain.team_id` is nullable, and a row with `team_id IS NULL` is the instance's shared hostname that every team may use. So `link.team_id` equals `domain.team_id` only for custom domains; on a shared domain it is simply the creating team. The invariant that matters is unchanged: `link.team_id` never moves, and no authorization check needs a join.
```

And next to the audit-action example:

```markdown
Amended 2026-09-03: the example action `link.destination_changed` is superseded by `link.updated`. One PATCH can change several fields atomically, and one row per request keeps the log a faithful record of what was asked; which fields moved lives in `metadata.changed`.
```

- [ ] **Step 8: Amend doc 06**

In `docs/planning/06-api-design.md`, under the `GET /v1/links/{link_id}` bullet:

```markdown
As of 2026-09-03 this ships without the nested `link_scan_result` verdict: scanning does not exist yet, so there is nothing to nest. The field arrives with Safe Browsing scanning.
```

- [ ] **Step 9: Update CLAUDE.md**

Add to the "Non-obvious constraints" list:

```markdown
- **The shared default hostname is a `domain` row with `team_id IS NULL`**, upserted at boot from `SHARED_DOMAIN_HOSTNAME`. Any team may create links on it; slugs there are one global, first-come-first-served namespace.
- **Slugs are case-insensitive**: stored lowercase, folded on the redirect path. Generated slugs are 8 characters from `23456789abcdefghijkmnpqrstuvwxyz`.
- **Entity-scoped routes** (`/v1/links/{link_id}` and, later, domains, folders and tags) authorize through per-entity scope structs in `internal/authz` that resolve the entity, then reuse the membership check. A non-member gets 404, never 403.
- **Creating a link must invalidate the redirect cache**, not only updating one — a probe may have cached the not-found sentinel under the new slug's key.
```

Add to the "Data model (summary)" section:

```markdown
- `domain.team_id` is nullable; `NULL` means the shared instance hostname.
```

- [ ] **Step 10: Regenerate and check the OpenAPI-derived client**

Run: `pnpm format:check && pnpm lint && pnpm typecheck` Expected: PASS. If `packages/api-client` is generated from the OpenAPI document in this repository, regenerate it so the five new operations appear, and run the three checks again.

- [ ] **Step 11: Commit**

```
test(api): prove link tenancy isolation
```

and, separately, because it is a different kind of change:

```
docs: record the shared domain decisions
```

---

## Notes for the executor

**What the permission matrix cannot see.** It observes HTTP status per operation and role. A handler with the correct scope whose SQL forgot its `team_id` filter passes it. This plan is almost entirely new queries, so every task review must read the SQL and confirm the filter, and Task 13's step 4 is the falsification that proves the scope itself is load-bearing.

**Order matters in two places.** Task 6 must land before Task 10, or a link created with a lowercase slug will not resolve when its slug is typed in mixed case. Each endpoint task must add its matrix row in the same commit that registers the operation, or `TestEveryOperationIsAccountedFor` fails the build — which is the guard working, not a problem to route around.

**A skipped test is a failed test here.** The suite skips when Docker or the local Supabase stack is unavailable. Start both (`supabase start`, Docker running) before reporting a task done, and state the skip count in the report.

**What this plan deliberately leaves for later.** Folders and tags, and the list filters that depend on them, are plan 4. Custom domains, the domain endpoints, the Vercel Domain API and DNS verification are plan 5. Password endpoints, Safe Browsing scanning, QR codes and the stats endpoint are plan 6.
