# Custom Domains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Verein can put its short links on its own hostname, and the app tells it the truth about whether that hostname works yet.

**Architecture:** A team claims a hostname; the row carries a random token the Verein publishes as a TXT record. `POST /v1/domains/{id}/verify` proves two things — the token is in DNS, and the hostname reaches this API over TLS — and only then does the row become `verified`, which is what `GetLinkableDomain` already gates link creation on. Adding the hostname to the Vercel project stays a manual maintainer step; the backend never calls Vercel.

**Tech Stack:** Go (chi + Huma + sqlc + pgx), Postgres via Supabase, Upstash Redis for rate limiting, TanStack Start + Router/Query/Form on the frontend, Vitest + RTL + MSW, Playwright + axe-core.

**Spec:** `docs/superpowers/specs/2026-09-06-custom-domains-design.md`

## Global Constraints

Copied from the spec and from `CLAUDE.md`; every task's requirements include these.

- **There is no RLS.** Every query filters by `team_id`, even when an authorization scope already checked the caller. The permission matrix in `apps/api/internal/api/matrix_test.go` checks status per operation and role and **cannot** see a missing filter.
- **A non-member gets 404, never 403.** An insufficient role gets 403 — that caller already knows the entity exists.
- **The tenant is called `team`** in every identifier. "Verein" appears only in German user-facing copy.
- **The redirect path is untouched.** No task in this plan edits `GET /{slug}`, and no new query runs on it.
- **No hardcoded user-facing string.** Every new string lands in both `apps/web/src/i18n/locales/en.json` and `de.json`. `catalogues.test.ts` proves the two stay in step.
- **WCAG 2.1 AA**, gated in CI by the Storybook a11y addon and by axe-core in the e2e suite.
- **Version control goes through GitButler.** Every commit step uses `but commit`, never `git commit`. Conventional Commits, **max 50 characters including type and scope**. No co-author or generator footer.
- **`audit_log.metadata` may not carry a key whose word segments include `token`.** `audit.checkMetadata` rejects it, and `verification_token` matches. Audit entries in this plan carry `hostname`, never the token.
- **Lint and format are oxlint and oxfmt**, never ESLint or Prettier. `pnpm format` before every commit that touches JS/TS.

---

## File Structure

**API**

| File | Responsibility |
| --- | --- |
| `supabase/migrations/<ts>_custom_domains.sql` | Partial unique index, `verification_token`, comment on `vercel_domain_ref` |
| `apps/api/internal/db/queries/domain.sql` | Extended: claim, list, scope lookup, verify transition, link count, delete |
| `apps/api/internal/domainverify/domainverify.go` | Hostname normalisation, TXT lookup, reachability probe |
| `apps/api/internal/destination/destination.go` | `isPublic` becomes `IsPublic` — one predicate, two callers |
| `apps/api/internal/authz/domain.go` | `DomainAdminScope`, resolver, 404-not-403 |
| `apps/api/internal/api/domains.go` | The five handlers |
| `apps/api/internal/audit/audit.go` | Three new actions, one new entity constant |
| `apps/api/internal/api/v1.go` | Registers the domain resolver and the domain routes |
| `apps/api/internal/config/config.go` | `DOMAIN_DNS_TARGET`, two rate limits |

**Web**

| File | Responsibility |
| --- | --- |
| `apps/web/src/server/domains.ts` | Server functions over the five endpoints |
| `apps/web/src/components/domain-list.tsx` | Presentational list, records, verify control |
| `apps/web/src/components/domain-form.tsx` | Presentational claim form |
| `apps/web/src/routes/_authed/teams.$teamId.domains.tsx` | Route: loader, mutations, wiring |
| `apps/web/src/components/authed-shell.tsx` | Team navigation |
| `apps/web/src/components/link-form.tsx` | Domain picker |
| `apps/web/e2e/domains.spec.ts` | Claim, records shown, verify reports `token_missing` |

---

### Task 1: The migration

**Files:**

- Create: `supabase/migrations/<timestamp>_custom_domains.sql`
- Test: manual assertion against the local database (steps below)

**Interfaces:**

- Consumes: nothing.
- Produces: `domain.verification_token text` (nullable); the unique index `domain_hostname_verified_key` on `hostname` where `verification_status = 'verified'`; the constraint `domain_hostname_key` no longer exists.

- [ ] **Step 1: Create the migration file**

```bash
supabase migration new custom_domains
```

- [ ] **Step 2: Write the migration**

Put this in the file the command created:

```sql
-- A hostname may be claimed by several teams at once. Global uniqueness made
-- the first INSERT a lock: claim verein-xy.de and its actual owner can never
-- try. Uniqueness belongs on the outcome, not on the attempt — only one team
-- can hold a hostname once it is verified, and GetLinkableDomain already
-- refuses to put a link on anything else.
alter table domain drop constraint domain_hostname_key;

create unique index domain_hostname_verified_key
  on domain (hostname)
  where verification_status = 'verified';

-- The value the claiming team publishes as a TXT record under
-- _kurze-url-challenge.<hostname>. Not a secret — it is published in public
-- DNS — so it is stored and returned in the clear. Null for the shared
-- hostname, which is verified at boot and never proves anything.
alter table domain add column verification_token text;

-- Reserved, and deliberately never written: provisioning keeps the maintainer
-- in the loop, so this service never calls Vercel's Domain API and has no
-- reference to record. Kept so switching to self-service later is a code
-- change rather than a migration.
comment on column domain.vercel_domain_ref is
  'Unused under maintainer-in-the-loop provisioning; see the 2026-09-06 custom-domains design.';
```

- [ ] **Step 3: Apply it locally and verify the index**

Run:

```bash
supabase migration up --local
```

Then:

```bash
psql postgres://postgres:postgres@127.0.0.1:54322/postgres -c "\d domain"
```

Expected: `domain_hostname_verified_key` listed as a partial unique index with the `WHERE (verification_status = 'verified'::text)` predicate, no `domain_hostname_key`, and a `verification_token` column.

- [ ] **Step 4: Prove the new rule holds both ways**

Run:

```bash
psql postgres://postgres:postgres@127.0.0.1:54322/postgres <<'SQL'
begin;
insert into team (name) values ('A') returning id \gset a_
insert into team (name) values ('B') returning id \gset b_
insert into domain (team_id, hostname) values (:'a_id', 'dup.test');
insert into domain (team_id, hostname) values (:'b_id', 'dup.test');
select count(*) from domain where hostname = 'dup.test';
update domain set verification_status = 'verified' where hostname = 'dup.test' and team_id = :'a_id';
update domain set verification_status = 'verified' where hostname = 'dup.test' and team_id = :'b_id';
rollback;
SQL
```

Expected: the two inserts succeed and the count is 2; the first update succeeds; the **second update fails** with `duplicate key value violates unique constraint "domain_hostname_verified_key"`. That failure is the point of the index — if it does not appear, the predicate is wrong.

- [ ] **Step 5: Regenerate sqlc so the new column is visible**

```bash
cd apps/api && sqlc generate && go build ./...
```

- [ ] **Step 6: Commit**

```bash
but commit -b feat/custom-domains -m "feat(db): claim a hostname without locking it"
```

---

### Task 2: The queries, and a tenancy test for each

**Files:**

- Modify: `apps/api/internal/db/queries/domain.sql`
- Test: `apps/api/internal/db/tenancy_test.go`

**Interfaces:**

- Consumes: Task 1's schema.
- Produces, all on `*db.Queries`:
  - `CreateDomainClaim(ctx, CreateDomainClaimParams{TeamID uuid.UUID, Hostname string, VerificationToken *string}) (Domain, error)`
  - `ListDomainsForTeam(ctx, ListDomainsForTeamParams{TeamID uuid.UUID, Limit, Offset int32}) ([]ListDomainsForTeamRow, error)` — row carries `TotalCount int64`
  - `GetDomainForTeam(ctx, GetDomainForTeamParams{ID, TeamID uuid.UUID}) (Domain, error)`
  - `GetDomainScope(ctx, id uuid.UUID) (GetDomainScopeRow, error)` — row is `{ID uuid.UUID; TeamID *uuid.UUID}`
  - `MarkDomainVerified(ctx, MarkDomainVerifiedParams{ID, TeamID uuid.UUID}) (Domain, error)`
  - `FailCompetingClaims(ctx, FailCompetingClaimsParams{Hostname string, KeepID uuid.UUID}) error`
  - `CountLinksForDomain(ctx, CountLinksForDomainParams{DomainID, TeamID uuid.UUID}) (int64, error)`
  - `DeleteDomain(ctx, DeleteDomainParams{ID, TeamID uuid.UUID}) (int64, error)`

- [ ] **Step 1: Write the failing tenancy tests**

Append to `apps/api/internal/db/tenancy_test.go`. These run against the real database through the existing fixture in that file — read its top before writing, and reuse whatever it names its team/other-team helpers.

```go
func TestDomainQueriesFilterByTeam(t *testing.T) {
	f := newDBFixture(t)

	mine, err := f.q.CreateDomainClaim(t.Context(), db.CreateDomainClaimParams{
		TeamID: f.teamID, Hostname: "mine.test", VerificationToken: ptr("tok-a"),
	})
	require.NoError(t, err)

	theirs, err := f.q.CreateDomainClaim(t.Context(), db.CreateDomainClaimParams{
		TeamID: f.otherTeamID, Hostname: "theirs.test", VerificationToken: ptr("tok-b"),
	})
	require.NoError(t, err)

	t.Run("GetDomainForTeam hides another team's domain", func(t *testing.T) {
		_, err := f.q.GetDomainForTeam(t.Context(), db.GetDomainForTeamParams{
			ID: theirs.ID, TeamID: f.teamID,
		})
		require.ErrorIs(t, err, pgx.ErrNoRows,
			"without the team_id filter this returns another team's domain")
	})

	t.Run("ListDomainsForTeam returns only this team's domains", func(t *testing.T) {
		rows, err := f.q.ListDomainsForTeam(t.Context(), db.ListDomainsForTeamParams{
			TeamID: f.teamID, Limit: 100, Offset: 0,
		})
		require.NoError(t, err)
		for _, row := range rows {
			require.Equal(t, f.teamID, row.TeamID)
		}
	})

	t.Run("DeleteDomain refuses another team's domain", func(t *testing.T) {
		affected, err := f.q.DeleteDomain(t.Context(), db.DeleteDomainParams{
			ID: theirs.ID, TeamID: f.teamID,
		})
		require.NoError(t, err)
		require.Zero(t, affected, "a delete that ignores team_id would report 1")
	})

	t.Run("MarkDomainVerified refuses another team's domain", func(t *testing.T) {
		_, err := f.q.MarkDomainVerified(t.Context(), db.MarkDomainVerifiedParams{
			ID: theirs.ID, TeamID: f.teamID,
		})
		require.ErrorIs(t, err, pgx.ErrNoRows)
	})

	t.Run("CountLinksForDomain counts only this team's links", func(t *testing.T) {
		count, err := f.q.CountLinksForDomain(t.Context(), db.CountLinksForDomainParams{
			DomainID: mine.ID, TeamID: f.otherTeamID,
		})
		require.NoError(t, err)
		require.Zero(t, count)
	})
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run them and watch them fail to compile**

Run: `cd apps/api && go test ./internal/db/ -run TestDomainQueries -count=1` Expected: FAIL — `undefined: db.CreateDomainClaimParams` and the rest. The queries do not exist yet.

- [ ] **Step 3: Write the queries**

Append to `apps/api/internal/db/queries/domain.sql`:

```sql
-- CreateDomainClaim records a team's claim on a hostname. It is a claim, not
-- a reservation: several teams may hold one on the same hostname, and the
-- partial unique index decides the winner at verification time.

-- name: CreateDomainClaim :one
insert into domain (team_id, hostname, verification_token)
values ($1, $2, $3)
returning *;

-- name: ListDomainsForTeam :many
select *, count(*) over () as total_count
from domain
where team_id = $1
order by hostname
limit $2 offset $3;

-- name: GetDomainForTeam :one
select *
from domain
where id = $1 and team_id = sqlc.arg(team_id)::uuid;

-- GetDomainScope discovers the owning team from a domain ID alone, so it
-- cannot filter by the answer — the same exception GetTagScope is. team_id is
-- nullable because the shared hostname has none; the resolver treats that null
-- as "not found", because nobody administers the shared domain through this
-- API.

-- name: GetDomainScope :one
select id, team_id
from domain
where id = $1;

-- MarkDomainVerified is the transition. It fails with a unique violation when
-- another team already holds this hostname as verified, which is exactly the
-- race the partial index exists to lose safely.

-- name: MarkDomainVerified :one
update domain
set verification_status = 'verified',
    verified_at = now()
where id = $1 and team_id = sqlc.arg(team_id)::uuid
returning *;

-- FailCompetingClaims settles the other claims on a hostname once one wins.
-- They are marked rather than deleted so the losing team sees an answer
-- instead of a vanished row. A failed row blocks nothing: the unique index
-- covers verified rows only.

-- name: FailCompetingClaims :exec
update domain
set verification_status = 'failed'
where hostname = $1
  and id <> sqlc.arg(keep_id)::uuid
  and verification_status <> 'verified';

-- CountLinksForDomain answers "would deleting this domain destroy anything?".
-- link.team_id is denormalized precisely so this needs no join, and it is
-- filtered here even though the scope already authorized the caller.

-- name: CountLinksForDomain :one
select count(*)
from link
where domain_id = sqlc.arg(domain_id)::uuid
  and team_id = sqlc.arg(team_id)::uuid;

-- name: DeleteDomain :execrows
delete from domain
where id = $1 and team_id = sqlc.arg(team_id)::uuid;
```

- [ ] **Step 4: Generate and run**

Run: `cd apps/api && sqlc generate && go test ./internal/db/ -run TestDomainQueries -count=1` Expected: PASS.

- [ ] **Step 5: Falsify every filter**

For each of `GetDomainForTeam`, `DeleteDomain`, `MarkDomainVerified` and `CountLinksForDomain`, delete its `team_id` condition from the SQL, run `sqlc generate`, run the test, and confirm the matching subtest fails. Restore between each. Record the four results in the task report.

This step is not optional and it is not ceremony. Plan 4 shipped six tenancy tests that could not fail, because the scope layer intercepts before the query runs. A test that passes with the filter removed is testing nothing.

- [ ] **Step 6: Commit**

```bash
but commit -b feat/custom-domains -m "feat(db): add the domain claim queries"
```

---

### Task 3: `IsPublic` becomes shared

**Files:**

- Modify: `apps/api/internal/destination/destination.go`
- Test: `apps/api/internal/destination/destination_test.go`

**Interfaces:**

- Produces: `destination.IsPublic(ip net.IP) bool`.

- [ ] **Step 1: Write the failing test**

Append to `apps/api/internal/destination/destination_test.go`:

```go
func TestIsPublicIsReachableByOtherPackages(t *testing.T) {
	// internal/domainverify checks the address a probe is about to connect to
	// with this same predicate. Two copies of "is this address routable" would
	// drift, and the copy that drifts is the one that stops rejecting
	// link-local addresses.
	require.False(t, destination.IsPublic(net.ParseIP("169.254.169.254")))
	require.False(t, destination.IsPublic(net.ParseIP("127.0.0.1")))
	require.False(t, destination.IsPublic(net.ParseIP("10.0.0.1")))
	require.False(t, destination.IsPublic(net.ParseIP("::1")))
	require.True(t, destination.IsPublic(net.ParseIP("93.184.216.34")))
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/destination/ -run TestIsPublicIsReachable -count=1` Expected: FAIL — `undefined: destination.IsPublic`.

- [ ] **Step 3: Rename the function**

In `destination.go`, rename `isPublic` to `IsPublic`, update its one existing call site inside `Validate`, and put the reason in its doc comment:

```go
// IsPublic reports whether an address literal is one a browser could
// meaningfully be sent to across the internet. Exported because
// internal/domainverify applies the same predicate to the address a
// verification probe is about to connect to — the same question, so the same
// answer, rather than a second copy that drifts.
func IsPublic(ip net.IP) bool {
```

- [ ] **Step 4: Run the whole package**

Run: `cd apps/api && go test ./internal/destination/ -count=1` Expected: PASS, including the existing tests.

- [ ] **Step 5: Commit**

```bash
but commit -b feat/custom-domains -m "refactor(api): export IsPublic for reuse"
```

---

### Task 4: `internal/domainverify` — hostname rules

**Files:**

- Create: `apps/api/internal/domainverify/hostname.go`
- Test: `apps/api/internal/domainverify/hostname_test.go`

**Interfaces:**

- Produces: `domainverify.NormalizeHostname(raw string, reserved []string) (string, error)`, plus `ErrMalformed`, `ErrApex`, `ErrReserved`.

- [ ] **Step 1: Write the failing test**

```go
package domainverify_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

func TestNormalizeHostname(t *testing.T) {
	reserved := []string{"api.kurze-url.app", "go.kurze-url.app"}

	t.Run("lowercases and trims", func(t *testing.T) {
		got, err := domainverify.NormalizeHostname("  Links.Verein.DE ", reserved)
		require.NoError(t, err)
		require.Equal(t, "links.verein.de", got)
	})

	t.Run("converts IDN to punycode", func(t *testing.T) {
		got, err := domainverify.NormalizeHostname("links.münchen.de", reserved)
		require.NoError(t, err)
		require.Equal(t, "links.xn--mnchen-3ya.de", got)
	})

	for _, bad := range []string{
		"https://links.verein.de",
		"links.verein.de/path",
		"links.verein.de:8443",
		"user@links.verein.de",
		"192.0.2.1",
		"localhost",
		"",
	} {
		t.Run("rejects "+bad, func(t *testing.T) {
			_, err := domainverify.NormalizeHostname(bad, reserved)
			require.ErrorIs(t, err, domainverify.ErrMalformed)
		})
	}

	t.Run("rejects an apex", func(t *testing.T) {
		// An apex cannot be a CNAME, so serving it here means A records and
		// taking the Verein's own website offline.
		_, err := domainverify.NormalizeHostname("verein.de", reserved)
		require.ErrorIs(t, err, domainverify.ErrApex)
	})

	t.Run("rejects this instance's own names", func(t *testing.T) {
		// Not load-bearing — nobody outside the maintainer can place the TXT
		// record under kurze-url.app, so such a claim could never verify.
		// Failing here is honest; failing at a check the caller could never
		// pass is not.
		for _, own := range []string{"api.kurze-url.app", "GO.kurze-url.app", "anything.vercel.app"} {
			_, err := domainverify.NormalizeHostname(own, reserved)
			require.ErrorIs(t, err, domainverify.ErrReserved, own)
		}
	})
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/domainverify/ -count=1` Expected: FAIL — the package does not exist.

- [ ] **Step 3: Implement**

```go
// Package domainverify decides whether a team may serve links on a hostname.
// It answers two separate questions: does the claimant control the DNS zone
// (a TXT token), and does the hostname actually reach this API (a probe).
// Only both together make a domain usable, because a link on a hostname that
// does not resolve is a link that 404s for everyone who clicks it.
package domainverify

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

var (
	// ErrMalformed means the value is not a bare hostname.
	ErrMalformed = errors.New("domainverify: not a bare hostname")

	// ErrApex means the value is a registrable domain rather than a subdomain
	// of one.
	ErrApex = errors.New("domainverify: an apex domain cannot be used")

	// ErrReserved means the hostname belongs to this instance.
	ErrReserved = errors.New("domainverify: this hostname belongs to the instance")
)

// NormalizeHostname turns user input into the exact string stored in
// domain.hostname, or explains why it cannot. reserved is this instance's own
// hostnames — Deps.selfHostnames supplies them.
func NormalizeHostname(raw string, reserved []string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")

	if host == "" {
		return "", fmt.Errorf("%w: empty", ErrMalformed)
	}
	if strings.ContainsAny(host, ":/@ \t") {
		return "", fmt.Errorf("%w: %q carries a scheme, port, path or credentials", ErrMalformed, raw)
	}
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("%w: %q is an address, not a name", ErrMalformed, raw)
	}

	// Punycode before counting labels: an IDN's label count does not change,
	// but the stored value must be the ASCII form the resolver will be asked
	// about.
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%w: %q has no dot", ErrMalformed, raw)
	}
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("%w: %q has an empty label", ErrMalformed, raw)
		}
	}
	// Two labels is a registrable domain in the common case. This is a
	// deliberate approximation, not a public-suffix lookup: getting it wrong
	// rejects a claim that could have worked, which the maintainer can settle
	// by hand, whereas accepting an apex takes a Verein's website offline.
	if len(labels) == 2 {
		return "", fmt.Errorf("%w: %q", ErrApex, ascii)
	}

	if strings.HasSuffix(ascii, ".vercel.app") {
		return "", fmt.Errorf("%w: %q", ErrReserved, ascii)
	}
	for _, own := range reserved {
		if ascii == strings.ToLower(strings.TrimSpace(own)) {
			return "", fmt.Errorf("%w: %q", ErrReserved, ascii)
		}
	}

	return ascii, nil
}
```

- [ ] **Step 4: Add the dependency and run**

Run:

```bash
cd apps/api && go get golang.org/x/net@latest && go mod tidy && go test ./internal/domainverify/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): normalize claimed hostnames"
```

---

### Task 5: `internal/domainverify` — the two checks

**Files:**

- Create: `apps/api/internal/domainverify/verify.go`
- Test: `apps/api/internal/domainverify/verify_test.go`

**Interfaces:**

- Consumes: `destination.IsPublic` (Task 3).
- Produces:
  - `type Reason string` with `ReasonNone`, `ReasonTokenMissing = "token_missing"`, `ReasonTokenMismatch = "token_mismatch"`, `ReasonUnreachable = "unreachable"`
  - `type Resolver interface { LookupTXT(ctx context.Context, name string) ([]string, error) }`
  - `type Verifier struct { Resolver Resolver; Client *http.Client }`
  - `func NewVerifier() *Verifier`
  - `func (v *Verifier) Check(ctx context.Context, hostname, token string) (Reason, error)`
  - `func ChallengeName(hostname string) string`

- [ ] **Step 1: Write the failing test**

```go
package domainverify_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

type fakeResolver struct {
	values []string
	err    error
}

func (f fakeResolver) LookupTXT(context.Context, string) ([]string, error) {
	return f.values, f.err
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okProbe() *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
}

func TestCheck(t *testing.T) {
	t.Run("passes when the token is present and the host answers", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonNone, reason)
	})

	t.Run("finds the token among several TXT values", func(t *testing.T) {
		// A zone commonly carries SPF and other TXT records on the same name.
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"v=spf1 -all", "tok-a"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonNone, reason)
	})

	t.Run("reports a missing record", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{err: &net.DNSError{IsNotFound: true}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonTokenMissing, reason)
	})

	t.Run("reports a wrong record", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-b"}},
			Client:   okProbe(),
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonTokenMismatch, reason)
	})

	t.Run("reports unreachable when the probe fails", func(t *testing.T) {
		v := &domainverify.Verifier{
			Resolver: fakeResolver{values: []string{"tok-a"}},
			Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection refused")
			})},
		}
		reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
		require.NoError(t, err)
		require.Equal(t, domainverify.ReasonUnreachable, reason)
	})

	t.Run("asks for the challenge name, not the hostname", func(t *testing.T) {
		require.Equal(t,
			"_kurze-url-challenge.links.verein.de",
			domainverify.ChallengeName("links.verein.de"))
	})
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/domainverify/ -run TestCheck -count=1` Expected: FAIL — `undefined: domainverify.Verifier`.

- [ ] **Step 3: Implement**

```go
package domainverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/destination"
)

// Reason says which half of verification is not satisfied yet. It is returned
// to the caller and never stored: domain.verification_status has three values
// and gains no fourth.
type Reason string

const (
	// ReasonNone means both checks passed.
	ReasonNone Reason = ""

	// ReasonTokenMissing means no TXT record exists at the challenge name.
	ReasonTokenMissing Reason = "token_missing"

	// ReasonTokenMismatch means TXT records exist but none matches.
	ReasonTokenMismatch Reason = "token_mismatch"

	// ReasonUnreachable means the hostname does not reach this API. Under
	// maintainer-in-the-loop provisioning this is the normal state until the
	// maintainer has added the hostname to the Vercel project.
	ReasonUnreachable Reason = "unreachable"
)

// challengePrefix is a convention, not a standard. It only has to stay stable
// once a Verein has been told to create the record.
const challengePrefix = "_kurze-url-challenge."

// probeTimeout bounds the whole reachability check. This endpoint waits on a
// network nobody here controls.
const probeTimeout = 5 * time.Second

// maxProbeBody caps what is read from a third-party server. Nothing from the
// response is returned to the caller; the cap exists so a hostile server
// cannot stream forever.
const maxProbeBody = 4 << 10

// Resolver is the DNS half, an interface so tests need no network.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Verifier performs both checks.
type Verifier struct {
	Resolver Resolver
	Client   *http.Client
}

// ChallengeName is the record the claiming team must create.
func ChallengeName(hostname string) string { return challengePrefix + hostname }

// NewVerifier builds the production Verifier: the system resolver, and an HTTP
// client that refuses redirects and validates the address it is about to
// connect to.
func NewVerifier() *Verifier {
	dialer := &net.Dialer{Timeout: probeTimeout}

	// Control runs after the resolver and before the socket connects, and is
	// handed the address actually being dialed. Validating a resolved address
	// and then dialing the hostname again would re-open DNS rebinding: first
	// lookup public, second lookup internal. This is the check
	// internal/destination's package comment defers to "wherever the service
	// itself fetches a URL" — this is that place.
	dialer.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("domainverify: unparseable dial address %q", address)
		}
		ip := net.ParseIP(host)
		if ip == nil || !destination.IsPublic(ip) {
			return fmt.Errorf("domainverify: refusing to connect to %s", host)
		}
		return nil
	}

	return &Verifier{
		Resolver: net.DefaultResolver,
		Client: &http.Client{
			Timeout:   probeTimeout,
			Transport: &http.Transport{DialContext: dialer.DialContext},
			// A 302 to 169.254.169.254 would walk straight past the address
			// check above, because the redirect is followed by a fresh dial the
			// caller never sees.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("domainverify: redirects are not followed")
			},
		},
	}
}

// Check answers whether hostname may serve this team's links. It returns the
// first unsatisfied condition; an error means the check could not be performed
// at all, which is different from a check that ran and said no.
func (v *Verifier) Check(ctx context.Context, hostname, token string) (Reason, error) {
	values, err := v.Resolver.LookupTXT(ctx, ChallengeName(hostname))
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return ReasonTokenMissing, nil
	case err != nil:
		// A resolver failure is not proof of absence, but it is also not
		// something the caller can act on differently, and it must not become a
		// 500 for a Verein whose DNS is briefly slow.
		return ReasonTokenMissing, nil
	case len(values) == 0:
		return ReasonTokenMissing, nil
	}

	found := false
	for _, value := range values {
		if strings.TrimSpace(value) == token {
			found = true
			break
		}
	}
	if !found {
		return ReasonTokenMismatch, nil
	}

	if !v.reachable(ctx, hostname) {
		return ReasonUnreachable, nil
	}
	return ReasonNone, nil
}

// reachable asks whether this API answers on the hostname. It is a readiness
// check, not a security boundary: a third party could answer this on a host
// they control and would gain nothing, because the token already proved
// ownership and their links still would not be served here.
func (v *Verifier) reachable(ctx context.Context, hostname string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostname+"/health", nil)
	if err != nil {
		return false
	}

	resp, err := v.Client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// Read and discard under a cap. Nothing from this response is returned to
	// the caller — an endpoint that echoed what it fetched would be a reading
	// primitive for everything reachable from this network.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return false
	}
	return resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"status":"ok"`)
}
```

- [ ] **Step 4: Run**

Run: `cd apps/api && go test ./internal/domainverify/ -count=1` Expected: PASS. Add `"io"` to the test file's imports if the compiler asks.

- [ ] **Step 5: Write the SSRF tests, which need a real listener**

Append to `verify_test.go`. These use the production `NewVerifier` so the `Control` hook is actually exercised — a fake client would skip the thing under test.

```go
func TestProductionVerifierRefusesPrivateAddresses(t *testing.T) {
	v := domainverify.NewVerifier()
	v.Resolver = fakeResolver{values: []string{"tok-a"}}

	// localhost resolves to a loopback address, so Control must refuse the
	// dial. Without the hook this connects to whatever is listening locally.
	reason, err := v.Check(t.Context(), "localhost", "tok-a")
	require.NoError(t, err)
	require.Equal(t, domainverify.ReasonUnreachable, reason)
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	// A hostile server answering the probe with a redirect to an internal
	// address would otherwise get a fresh dial that Control's earlier decision
	// says nothing about.
	redirected := false
	v := &domainverify.Verifier{
		Resolver: fakeResolver{values: []string{"tok-a"}},
		Client: &http.Client{
			CheckRedirect: domainverify.NewVerifier().Client.CheckRedirect,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				redirected = true
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"http://169.254.169.254/"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    r,
				}, nil
			}),
		},
	}

	reason, err := v.Check(t.Context(), "links.verein.de", "tok-a")
	require.NoError(t, err)
	require.True(t, redirected, "the probe should have been attempted")
	require.Equal(t, domainverify.ReasonUnreachable, reason)
}
```

- [ ] **Step 6: Run, then falsify**

Run: `cd apps/api && go test ./internal/domainverify/ -count=1` — expected PASS.

Then remove `dialer.Control` from `NewVerifier` and re-run. Expected: `TestProductionVerifierRefusesPrivateAddresses` fails or hangs, because the probe now reaches whatever is on localhost. Restore it. Then replace `CheckRedirect` with `nil` and re-run: `TestRedirectsAreNotFollowed` must fail. Restore. Record both in the task report.

- [ ] **Step 7: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): verify a domain's txt and reach"
```

---

### Task 6: `DomainAdminScope`

**Files:**

- Create: `apps/api/internal/authz/domain.go`
- Test: `apps/api/internal/authz/domain_test.go`
- Modify: `apps/api/internal/api/v1.go` (register the resolver)

**Interfaces:**

- Consumes: `db.GetDomainScope` (Task 2).
- Produces: `authz.DomainPath{DomainID uuid.UUID}`, `authz.ResolvedDomain{ID, TeamID uuid.UUID}`, `authz.DomainResolver`, `authz.QueryDomainResolver`, `authz.NewQueryDomainResolver`, `authz.WithDomainResolver`, `authz.DomainViewerScope` and `authz.DomainAdminScope`, each with `Member()` and `Domain()`, `authz.ErrDomainNotFound`.

- [ ] **Step 1: Write the failing test**

Model it on `apps/api/internal/authz/tag_test.go` — read that file first and mirror its fake-resolver shape. The cases that must appear:

```go
func TestDomainAdminScope(t *testing.T) {
	t.Run("a stranger gets 404, not 403", func(t *testing.T) {
		// The API must not confirm that a domain exists to someone outside its
		// team. Same rule as every other entity scope.
	})

	t.Run("a member below admin gets 403", func(t *testing.T) {
		// An editor already knows the domain exists — they can see it in the
		// list — so hiding it from them would be theatre.
	})

	t.Run("an admin is allowed through", func(t *testing.T) {})

	t.Run("the shared domain is not administrable", func(t *testing.T) {
		// domain.team_id is null for the instance's own hostname. A null owner
		// cannot match any membership, and treating it as "not found" is what
		// keeps a team from deleting the hostname every other team is using.
		resolver := fakeDomainResolver{teamID: nil}
		// ... expect 404
	})

	t.Run("a malformed domain_id is 422, not 404", func(t *testing.T) {
		// Huma runs every resolver even when its own binding failed and reports
		// the last error's status; without the guard this surfaces as a 404,
		// which is the wrong defect.
	})
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/authz/ -run TestDomainAdminScope -count=1` Expected: FAIL — the scope does not exist.

- [ ] **Step 3: Implement, copying `tag.go` structurally**

`apps/api/internal/authz/domain.go` follows `tag.go` line for line, with three differences that matter:

```go
// ResolvedDomain is what the scope loaded on the way to its decision.
type ResolvedDomain struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// Domain implements DomainResolver.
func (r QueryDomainResolver) Domain(ctx context.Context, domainID uuid.UUID) (ResolvedDomain, error) {
	row, err := r.queries.GetDomainScope(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedDomain{}, ErrDomainNotFound
	}
	if err != nil {
		return ResolvedDomain{}, fmt.Errorf("authz: load domain scope: %w", err)
	}
	// A null team_id is the instance's shared hostname. It belongs to no team,
	// so no membership can authorize it, and no team may delete the hostname
	// every other team's links are on. Reported as not-found rather than as a
	// separate error: from outside, an unadministrable domain and a
	// nonexistent one are the same thing.
	if row.TeamID == nil {
		return ResolvedDomain{}, ErrDomainNotFound
	}
	return ResolvedDomain{ID: row.ID, TeamID: *row.TeamID}, nil
}

// DomainAdminScope is embedded by domain operations. Admin, not editor:
// links, folders and tags are content, and a domain is the namespace that
// content lives in — losing it takes every link along. That belongs with
// member management.
type DomainAdminScope struct {
	DomainPath
	member Membership
	domain ResolvedDomain
}

func (s *DomainAdminScope) Resolve(ctx huma.Context) []error {
	return resolveDomainScope(ctx, s.DomainID, RoleAdmin, &s.member, &s.domain)
}

// DomainViewerScope is the read-only sibling, for GET /v1/domains/{domain_id}.
// Separate rather than one scope with a role parameter, because the role a
// route requires belongs in its type where a reviewer reads it.
type DomainViewerScope struct {
	DomainPath
	member Membership
	domain ResolvedDomain
}

func (s *DomainViewerScope) Resolve(ctx huma.Context) []error {
	return resolveDomainScope(ctx, s.DomainID, RoleViewer, &s.member, &s.domain)
}
```

`resolveDomainScope` is `resolveTagScope` with `domain_id` in the parameter guard and `"domain not found"` as the membership failure message.

- [ ] **Step 4: Register the resolver**

In `apps/api/internal/api/v1.go`, beside the other three:

```go
inner = authz.WithDomainResolver(inner, authz.NewQueryDomainResolver(d.Queries))
```

- [ ] **Step 5: Run**

Run: `cd apps/api && go test ./internal/authz/ -count=1` Expected: PASS.

- [ ] **Step 6: Falsify the null-owner rule**

Delete the `row.TeamID == nil` branch, replace it with `uuid.UUID{}` as the team, and re-run. Expected: `the shared domain is not administrable` fails. Restore, and record the result.

- [ ] **Step 7: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): authorize domain operations"
```

---

### Task 7: Audit actions and configuration

**Files:**

- Modify: `apps/api/internal/audit/audit.go`
- Modify: `apps/api/internal/config/config.go`
- Test: `apps/api/internal/audit/audit_test.go`

**Interfaces:**

- Produces: `audit.ActionDomainClaimed`, `audit.ActionDomainVerified`, `audit.ActionDomainDeleted`, `audit.EntityDomain`; `Config.DomainDNSTarget string`, `Config.DomainClaimRateLimitPerHour int`, `Config.DomainVerifyRateLimitPerHour int`.

- [ ] **Step 1: Write the failing test**

```go
func TestDomainActionsAreInTheTaxonomy(t *testing.T) {
	for _, action := range []audit.Action{
		audit.ActionDomainClaimed,
		audit.ActionDomainVerified,
		audit.ActionDomainDeleted,
	} {
		require.NoError(t, audit.CheckAction(action), action)
	}
}

func TestDomainMetadataMayNotCarryTheToken(t *testing.T) {
	// forbiddenMetadataKeys matches the word segment "token", and
	// verification_token matches it. This is a trap rather than a nuisance: the
	// obvious metadata for a claim is the whole row.
	err := audit.Log(t.Context(), nil, audit.Entry{
		Action:     audit.ActionDomainClaimed,
		EntityType: audit.EntityDomain,
		Metadata:   map[string]any{"verification_token": "tok-a"},
	})
	require.ErrorIs(t, err, audit.ErrForbiddenMetadata)
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/api && go test ./internal/audit/ -count=1` Expected: FAIL — `undefined: audit.ActionDomainClaimed`.

- [ ] **Step 3: Add the actions, the entity and the config**

In `audit.go`, beside the existing constants:

```go
	ActionDomainClaimed  Action = "domain.claimed"
	ActionDomainVerified Action = "domain.verified"
	ActionDomainDeleted  Action = "domain.deleted"
```

Add `EntityDomain = "domain"` to the entity constants, and add all three actions to `knownActions` — the map is the taxonomy, and a constant missing from it fails `CheckAction`.

In `config.go`, beside the other limits:

```go
	// DomainDNSTarget is what a Verein is told to point their CNAME at. It is
	// configuration rather than a constant because Vercel assigns per-project
	// DNS targets alongside the generic cname.vercel-dns.com, and which one to
	// publish is unconfirmed until the first real domain is set up.
	DomainDNSTarget string
```

and, in `Load`:

```go
	cfg.DomainDNSTarget = env("DOMAIN_DNS_TARGET", "cname.vercel-dns.com")
	if cfg.DomainClaimRateLimitPerHour, err = envInt("RATE_LIMIT_DOMAIN_CLAIM_PER_HOUR", 5); err != nil {
		return Config{}, err
	}
	if cfg.DomainVerifyRateLimitPerHour, err = envInt("RATE_LIMIT_DOMAIN_VERIFY_PER_HOUR", 20); err != nil {
		return Config{}, err
	}
```

- [ ] **Step 4: Run**

Run: `cd apps/api && go test ./internal/audit/ ./internal/config/ -count=1` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): add domain audit actions"
```

---

### Task 8: Claim, list and read

**Files:**

- Create: `apps/api/internal/api/domains.go`
- Modify: `apps/api/internal/api/v1.go` (call `d.registerDomains(api)`)
- Modify: `apps/api/internal/api/api.go` (add `Verifier *domainverify.Verifier` to `Deps`)
- Test: `apps/api/internal/api/domains_test.go`

**Interfaces:**

- Consumes: Tasks 2, 4, 6, 7.
- Produces: `api.Domain` (the response type), `api.DNSRecord`, and the operations `create-domain`, `list-domains`, `get-domain`.

- [ ] **Step 1: Write the failing tests**

In `domains_test.go`, using `newTenancyFixture` exactly as `teams_test.go` does:

```go
func TestClaimDomainReturnsTheRecordsToCreate(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[api.Domain](t, rec)
	require.Equal(t, "links.verein.test", body.Hostname)
	require.Equal(t, "pending", body.VerificationStatus)
	require.NotEmpty(t, body.VerificationToken)
	require.Equal(t, "_kurze-url-challenge.links.verein.test", body.Records.TXT.Name)
	require.Equal(t, body.VerificationToken, body.Records.TXT.Value)
	require.Equal(t, "links.verein.test", body.Records.CNAME.Name)
	require.NotEmpty(t, body.Records.CNAME.Value)
}

func TestClaimDomainIsRefusedBelowAdmin(t *testing.T) {
	// A domain is the namespace a team's links live in, not content.
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestClaimDomainRejectsAnApex(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "verein.test"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestTwoTeamsMayClaimTheSameHostname(t *testing.T) {
	// The whole point of the partial index: a claim is not a reservation.
	f := newTenancyFixture(t)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, first.Code)

	second := f.do(t, f.otherAdmin, http.MethodPost,
		"/v1/teams/"+f.otherTeamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, second.Code, "body: %s", second.Body.String())

	require.NotEqual(t,
		decode[api.Domain](t, first).VerificationToken,
		decode[api.Domain](t, second).VerificationToken,
		"each claim needs its own token, or the first claimant could verify the second's")
}

func TestListDomainsHidesAnotherTeamsDomains(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/domains", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, item := range decode[api.Page[api.Domain]](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
	}
}
```

If `newTenancyFixture` has no `otherAdmin`/`otherTeamID`, add them there rather than inlining a second fixture — `members_test.go` already needs a second team, so follow whatever it does.

- [ ] **Step 2: Run and watch them fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestClaimDomain|TestTwoTeams|TestListDomains' -count=1` Expected: FAIL — 404 on every route, because nothing is registered.

- [ ] **Step 3: Implement the types and the three handlers**

```go
// Domain is a domain as the API reports it. VerificationToken and Records are
// included on every read, not just on creation: the screen that shows a Verein
// what to put in DNS has to be able to show it again tomorrow.
type Domain struct {
	ID                 uuid.UUID  `json:"id"`
	TeamID             uuid.UUID  `json:"team_id"`
	Hostname           string     `json:"hostname"`
	VerificationStatus string     `json:"verification_status"`
	VerificationToken  string     `json:"verification_token"`
	VerifiedAt         *time.Time `json:"verified_at"`
	Records            DNSRecords `json:"records"`
}

// DNSRecords are the two entries the claiming team must create.
type DNSRecords struct {
	TXT   DNSRecord `json:"txt"`
	CNAME DNSRecord `json:"cname"`
}

// DNSRecord is one entry, in the shape a DNS provider's form asks for.
type DNSRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CreateDomainInput declares its authorization in its type: AdminScope
// resolves and checks the caller's role before this handler's body runs.
type CreateDomainInput struct {
	authz.AdminScope
	Body struct {
		Hostname string `json:"hostname" maxLength:"253" doc:"A subdomain you control, e.g. links.verein.de. Not an apex."`
	}
}
```

The `createDomain` handler, in order: rate-limit by user; `domainverify.NormalizeHostname(in.Body.Hostname, d.selfHostnames())`, mapping `ErrApex`, `ErrReserved` and `ErrMalformed` each to a 422 carrying that error's own message; generate a token with the same alphabet and generator `internal/slug` already uses, at 32 characters; `db.InTx` around `CreateDomainClaim` plus `audit.Log` with `Metadata: map[string]any{"hostname": hostname}` — **not** the token, which `checkMetadata` rejects; return 201.

`listDomains` mirrors `listTags` exactly, including the comment explaining why it filters by `team_id` when the scope already authorized the caller.

The scope on each operation, decided once here so no task has to guess:

| Operation       | Scope                     | Role   |
| --------------- | ------------------------- | ------ |
| `create-domain` | `authz.AdminScope`        | admin  |
| `list-domains`  | `authz.ViewerScope`       | viewer |
| `get-domain`    | `authz.DomainViewerScope` | viewer |
| `verify-domain` | `authz.DomainAdminScope`  | admin  |
| `delete-domain` | `authz.DomainAdminScope`  | admin  |

`get-domain` reads, so it takes the viewer scope — `DomainViewerScope` sits beside `DomainAdminScope` the way `LinkViewerScope` sits beside `LinkEditorScope`, and both come from Task 6.

`domainResponse(row db.Domain, target string) Domain` builds the records:

```go
func domainResponse(row db.Domain, dnsTarget string) Domain {
	token := ""
	if row.VerificationToken != nil {
		token = *row.VerificationToken
	}
	return Domain{
		ID:                 row.ID,
		TeamID:             *row.TeamID,
		Hostname:           row.Hostname,
		VerificationStatus: row.VerificationStatus,
		VerificationToken:  token,
		VerifiedAt:         row.VerifiedAt,
		Records: DNSRecords{
			TXT:   DNSRecord{Name: domainverify.ChallengeName(row.Hostname), Value: token},
			CNAME: DNSRecord{Name: row.Hostname, Value: dnsTarget},
		},
	}
}
```

- [ ] **Step 4: Add the helpers Tasks 9 and 10 rely on**

Put these in `domains_test.go` now, so the later tasks find them rather than inventing a second set:

```go
// claimDomain claims a hostname for the fixture's own team as its admin.
func claimDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	return claimDomainAs(t, f, f.members[authz.RoleAdmin], f.teamID, hostname)
}

func claimDomainAs(
	t *testing.T, f *tenancyFixture, as testUser, teamID uuid.UUID, hostname string,
) api.Domain {
	t.Helper()
	rec := f.do(t, as, http.MethodPost, "/v1/teams/"+teamID.String()+"/domains",
		map[string]string{"hostname": hostname})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[api.Domain](t, rec)
}

// verifiedDomain claims a hostname and marks it verified directly, because the
// HTTP path to verified needs DNS this test has no control over.
func verifiedDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	claimed := claimDomain(t, f, hostname)
	_, err := f.pool.Exec(t.Context(),
		`update domain set verification_status = 'verified', verified_at = now() where id = $1`,
		claimed.ID)
	require.NoError(t, err)
	claimed.VerificationStatus = "verified"
	return claimed
}

// createLinkOn puts one link on a domain, so the delete guard has something to
// refuse over.
func createLinkOn(t *testing.T, f *tenancyFixture, domainID uuid.UUID) {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/", "domain_id": domainID})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}
```

- [ ] **Step 4: Run**

Run: `cd apps/api && go test ./internal/api/ -run 'TestClaimDomain|TestTwoTeams|TestListDomains' -count=1` Expected: PASS.

- [ ] **Step 6: Run the permission matrix**

Run: `cd apps/api && go test ./internal/api/ -run TestPermissionMatrix -count=1` Expected: FAIL, listing the three new operations as unaccounted for. Add them to the matrix with the roles this task implemented, then re-run to PASS. The matrix failing here is the feature working — it fails the build for any registered operation nobody has classified.

- [ ] **Step 7: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): claim and list team domains"
```

---

### Task 9: Verify

**Files:**

- Modify: `apps/api/internal/api/domains.go`
- Modify: `apps/api/cmd/api/main.go` (construct the verifier into `Deps`)
- Test: `apps/api/internal/api/domains_test.go`

**Interfaces:**

- Consumes: `domainverify.Verifier` (Task 5), `MarkDomainVerified` and `FailCompetingClaims` (Task 2).
- Produces: the `verify-domain` operation, and `VerifyDomainOutput{Body struct{ Domain Domain; Reason string }}`.

- [ ] **Step 1: Write the failing tests**

The fixture must inject a stub verifier, so give `tenancyFixture` a settable one — the same way it already lets tests set config fields:

```go
func TestVerifyReportsWhichHalfIsMissing(t *testing.T) {
	f := newTenancyFixture(t)
	f.verifier.reason = domainverify.ReasonTokenMissing

	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode[struct {
		Domain api.Domain `json:"domain"`
		Reason string     `json:"reason"`
	}](t, rec)
	require.Equal(t, "pending", body.Domain.VerificationStatus)
	require.Equal(t, "token_missing", body.Reason,
		"a Verein that cannot see which half failed cannot fix it")
}

func TestVerifySucceedsAndSettlesCompetingClaims(t *testing.T) {
	f := newTenancyFixture(t)
	f.verifier.reason = domainverify.ReasonNone

	mine := claimDomain(t, f, "contested.verein.test")
	theirs := claimDomainAs(t, f, f.otherAdmin, f.otherTeamID, "contested.verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+mine.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "verified", decode[struct {
		Domain api.Domain `json:"domain"`
	}](t, rec).Domain.VerificationStatus)

	var status string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select verification_status from domain where id = $1`, theirs.ID).Scan(&status))
	require.Equal(t, "failed", status,
		"the losing claim must be answered, not left pending forever")
}

func TestVerifyIsRefusedBelowAdmin(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `cd apps/api && go test ./internal/api/ -run TestVerify -count=1` Expected: FAIL — 404, the route does not exist.

- [ ] **Step 3: Implement**

The handler, in order: rate-limit by domain id **and** by user, because a DNS lookup plus a TLS connection to a third party must not be free to trigger in a loop; load the domain with `GetDomainForTeam` keyed by the scope's team; if it is already `verified`, return it unchanged with `ReasonNone` rather than re-probing; call `d.Verifier.Check(ctx, row.Hostname, token)`; on a non-empty reason, return 200 with the unchanged domain and that reason — **not** an error status, because "not ready yet" is the expected answer, not a failure; on `ReasonNone`, run `db.InTx` with `MarkDomainVerified`, then `FailCompetingClaims`, then `audit.Log` with `ActionDomainVerified`.

Handle the unique violation from `MarkDomainVerified` explicitly:

```go
	case isUniqueViolation(err):
		// Another team verified this hostname first. Both proved they control
		// the zone — which can happen legitimately during a handover — and the
		// index makes the second one lose rather than producing two verified
		// rows the redirect path would have to choose between.
		return nil, huma.Error409Conflict("another team has already verified this hostname")
```

In `cmd/api/main.go`, build the verifier once and put it on `Deps`:

```go
	deps.Verifier = domainverify.NewVerifier()
```

- [ ] **Step 4: Run**

Run: `cd apps/api && go test ./internal/api/ -count=1` Expected: PASS, matrix included after adding `verify-domain` to it.

- [ ] **Step 5: Falsify the competing-claim settlement**

Remove the `FailCompetingClaims` call and re-run. Expected: `TestVerifySucceedsAndSettlesCompetingClaims` fails on the status check. Restore, and record it.

- [ ] **Step 6: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): verify a claimed domain"
```

---

### Task 10: Delete, refused while links exist

**Files:**

- Modify: `apps/api/internal/api/domains.go`
- Test: `apps/api/internal/api/domains_test.go`

**Interfaces:**

- Consumes: `CountLinksForDomain`, `DeleteDomain` (Task 2).
- Produces: the `delete-domain` operation.

- [ ] **Step 1: Write the failing tests**

```go
func TestDeleteDomainIsRefusedWhileLinksExist(t *testing.T) {
	// link.domain_id is on delete cascade, and link_click_stats has no raw
	// click table behind it — those rollups cannot be recomputed from
	// anything. "Impossible" is worth more here than "warned".
	f := newTenancyFixture(t)
	claim := verifiedDomain(t, f, "links.verein.test")
	createLinkOn(t, f, claim.ID)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		"/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "1",
		"the count is what tells the team how much work removing it is")

	var stillThere int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from domain where id = $1`, claim.ID).Scan(&stillThere))
	require.Equal(t, 1, stillThere)
}

func TestDeleteDomainSucceedsWhenEmpty(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		"/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code)
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `cd apps/api && go test ./internal/api/ -run TestDeleteDomain -count=1` Expected: FAIL — 404.

- [ ] **Step 3: Implement**

Declare the sentinel beside the handler, the way `errTagCapReached` sits beside `createTag`:

```go
// errDomainHasLinks travels out of the transaction so the refusal becomes a
// 409 rather than a 500. It never reaches the client.
var errDomainHasLinks = errors.New("api: domain still has links")
```

Count first, inside the transaction, then delete:

```go
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		count, err := q.CountLinksForDomain(ctx, db.CountLinksForDomainParams{
			DomainID: resolved.ID, TeamID: resolved.TeamID,
		})
		if err != nil {
			return err
		}
		if count > 0 {
			linkCount = count
			return errDomainHasLinks
		}

		if _, err := q.DeleteDomain(ctx, db.DeleteDomainParams{
			ID: resolved.ID, TeamID: resolved.TeamID,
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      resolved.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionDomainDeleted,
			EntityType:  audit.EntityDomain,
			EntityID:    resolved.ID,
			Metadata:    map[string]any{"hostname": hostname},
		})
	})

	if errors.Is(err, errDomainHasLinks) {
		return nil, huma.Error409Conflict(fmt.Sprintf(
			"%d link(s) still use this domain; delete them first", linkCount))
	}
```

- [ ] **Step 4: Run**

Run: `cd apps/api && go test ./internal/api/ -count=1` Expected: PASS with `delete-domain` added to the matrix.

- [ ] **Step 5: Falsify**

Remove the `count > 0` branch and re-run. Expected: `TestDeleteDomainIsRefusedWhileLinksExist` fails, and the link is gone. Restore, and record it.

- [ ] **Step 6: Commit**

```bash
but commit -b feat/custom-domains -m "feat(api): refuse to delete a used domain"
```

---

### Task 11: Regenerate the spec and the client

**Files:**

- Modify: `apps/api/openapi.json`, `packages/api-client/src/generated/*`

- [ ] **Step 1: Regenerate**

```bash
pnpm generate:api
```

- [ ] **Step 2: Check the diff says what you expect**

Run: `git diff --stat apps/api/openapi.json packages/api-client` Expected: five new operations and the `Domain`, `DNSRecords`, `DNSRecord` schemas. If anything else moved, a handler changed something it should not have.

- [ ] **Step 3: Typecheck**

Run: `pnpm typecheck` Expected: clean.

- [ ] **Step 4: Commit**

```bash
but commit -b feat/custom-domains -m "chore(api-client): regenerate for domains"
```

---

### Task 12: The web server functions

**Files:**

- Create: `apps/web/src/server/domains.ts`
- Test: `apps/web/src/server/domains.test.ts`

**Interfaces:**

- Produces: `listDomainsFor(request, teamId)`, `listDomainsFn`, `claimDomainFor(request, teamId, hostname)`, `claimDomainFn`, `verifyDomainFor(request, domainId)`, `verifyDomainFn`, `deleteDomainFor(request, domainId)`, `deleteDomainFn`, and `domainsQueryOptions(teamId)`.

- [ ] **Step 1: Write the failing test**

Copy the whole mock scaffold from `apps/web/src/server/teams.test.ts` — the `FakeSupabaseClient`, `FakeResponse`, `vi.hoisted` block, `withSession`, and the two `vi.mock` calls — and then:

```ts
describe('claimDomainFor', () => {
	it('posts the hostname as the signed-in caller', async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');

		let seenBody: unknown = null;
		server.use(
			http.post('http://api.test/v1/teams/team-a/domains', async ({ request: apiRequest }) => {
				seenBody = await apiRequest.json();
				return HttpResponse.json({ hostname: 'links.verein.test', id: 'd1' }, { status: 201 });
			}),
		);

		await claimDomainFor(request, 'team-a', 'links.verein.test');

		expect(seenBody).toEqual({ hostname: 'links.verein.test' });
	});

	it('rejects when the API refuses', async () => {
		// throwOnError. Without it a 422 for an apex resolves to
		// { data: undefined, error } and the route reports a claim that was
		// never created.
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');
		server.use(
			http.post('http://api.test/v1/teams/team-a/domains', () =>
				HttpResponse.json({ detail: 'apex' }, { status: 422 }),
			),
		);

		await expect(claimDomainFor(request, 'team-a', 'verein.test')).rejects.toBeDefined();
	});

	it('carries a refreshed session cookie onto the response', async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');
		const appended: string[] = [];
		mocks.getResponse.mockReturnValueOnce({
			headers: { append: (_name, value) => appended.push(value) },
		});
		server.use(
			http.post('http://api.test/v1/teams/team-a/domains', () =>
				HttpResponse.json({ hostname: 'links.verein.test', id: 'd1' }, { status: 201 }),
			),
		);

		await claimDomainFor(request, 'team-a', 'links.verein.test');

		expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
	});
});
```

- [ ] **Step 2: Run and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/domains.test.ts` Expected: FAIL — the module does not exist.

- [ ] **Step 3: Implement**

`apps/web/src/server/domains.ts` follows `server/links.ts` exactly: every function takes `request: Request` rather than calling `getRequest()`, wraps in `createServerOnlyFn`, calls `requireSession` then `flushSessionCookies`, and passes `throwOnError: true`. `domainsQueryOptions(teamId)` mirrors the links one with the query key `['domains', teamId]`.

- [ ] **Step 4: Run**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/domains.test.ts` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/custom-domains -m "feat(web): add the domain server functions"
```

---

### Task 13: The domains screen

**Files:**

- Create: `apps/web/src/components/domain-list.tsx`, `apps/web/src/components/domain-list.test.tsx`, `apps/web/src/components/domain-list.stories.tsx`
- Create: `apps/web/src/routes/_authed/teams.$teamId.domains.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `de.json`

**Interfaces:**

- Consumes: Task 12's server functions, `CopyButton`, `classifyApiError`.
- Produces: `DomainList` taking `{ domains, onVerify, verifyingId, pendingReason }`.

- [ ] **Step 1: Add the strings, both languages**

Under `domains` in `en.json`:

```json
{
	"heading": "Your domains",
	"empty": "No domains yet. Your links use the shared domain.",
	"hostname": "Hostname",
	"hostnameHint": "A subdomain you control, for example links.verein.de",
	"hostnameRequired": "A hostname is required.",
	"claim": "Add domain",
	"status": "Status",
	"pending": "Waiting for DNS",
	"verified": "Working",
	"failed": "Another team verified this hostname first",
	"recordsHeading": "Create these two DNS records",
	"recordName": "Name",
	"recordValue": "Value",
	"verify": "Check now",
	"reason": {
		"token_missing": "The TXT record is not visible yet. DNS changes can take a few hours.",
		"token_mismatch": "A TXT record exists but its value does not match. Check for a typo.",
		"unreachable": "The hostname does not reach us yet. This is expected until the instance maintainer has added it."
	}
}
```

and the German equivalents in `de.json`. Every key must exist in both — `catalogues.test.ts` fails otherwise. No string may be identical across the two languages, or the e2e i18n crawl fails.

- [ ] **Step 2: Write the failing component test**

```tsx
describe('DomainList', () => {
	it('shows both DNS records for a pending domain', () => {
		// A Verein that cannot see what to put in DNS cannot proceed, and this
		// is the only screen that tells them.
		renderList([pendingDomain]);

		expect(screen.getByText('_kurze-url-challenge.links.verein.test')).toBeInTheDocument();
		expect(screen.getByText('tok-a')).toBeInTheDocument();
		expect(screen.getByText('cname.vercel-dns.com')).toBeInTheDocument();
	});

	it('hides the records once the domain works', () => {
		renderList([verifiedDomain]);
		expect(screen.queryByText(/_kurze-url-challenge/)).not.toBeInTheDocument();
	});

	it('explains which half of verification is missing', () => {
		// The reason is the whole reason the endpoint returns one. Showing a
		// bare "not verified" would leave a Verein guessing between a DNS typo
		// and a step that is not theirs to take.
		renderList([pendingDomain], { pendingReason: 'unreachable' });
		expect(screen.getByText(/does not reach us yet/i)).toBeInTheDocument();
	});

	it('puts the records in a table, not in divs', () => {
		// Accessibility is a CI gate at two levels. A grid of divs passes a
		// visual review and fails axe.
		renderList([pendingDomain]);
		expect(screen.getAllByRole('row').length).toBeGreaterThan(1);
	});
});
```

- [ ] **Step 3: Run and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/domain-list.test.tsx` Expected: FAIL — the component does not exist.

- [ ] **Step 4: Implement the component and the route**

`DomainList` is presentational and prop-driven, the same contract as `LinkList`: it renders a `<table>` of the two records with a `CopyButton` per value, a status, a "Check now" button per pending domain, and the translated reason when one is supplied. The route owns the loader (`domainsQueryOptions`), the mutations, `assertMembership(context.me.memberships, params.teamId)` in `beforeLoad`, and the `unauthenticated` → `/login` redirect that every other authed route performs.

- [ ] **Step 5: Run, and check accessibility**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/domain-list.test.tsx`, then `pnpm --filter @kurze-url/web test:storybook`. Expected: both PASS. The a11y addon is a real gate — if the story fails, fix the markup rather than the story.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/custom-domains -m "feat(web): add the team domains screen"
```

---

### Task 14: Navigation, and the domain picker

**Files:**

- Modify: `apps/web/src/components/authed-shell.tsx`, `apps/web/src/components/authed-shell.test.tsx`
- Modify: `apps/web/src/components/link-form.tsx`, `apps/web/src/components/link-form.test.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamId.links.new.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `de.json`

**Interfaces:**

- Consumes: Task 13's route, Task 12's `domainsQueryOptions`.
- Produces: `LinkForm` gains `domains?: readonly { id: string; hostname: string }[]`, and `LinkFormValues` gains `domain_id: string`.

- [ ] **Step 1: Write the failing tests**

```tsx
it('offers a domain picker when the team has a verified domain', async () => {
	// The API has accepted an explicit domain_id since plan 3; the form never
	// asked, so every link landed on the shared hostname. A verified domain
	// with no way to put a link on it is not a feature.
	renderForm({ domains: [{ hostname: 'links.verein.test', id: 'd1' }] });

	await userEvent.selectOptions(screen.getByLabelText(/domain/i), 'd1');
	await userEvent.type(screen.getByLabelText(/destination/i), 'https://example.org/');
	await userEvent.click(screen.getByRole('button', { name: /save/i }));

	expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ domain_id: 'd1' }));
});

it('omits the picker when there is nothing to pick', () => {
	// A select with one option is furniture.
	renderForm({ domains: [] });
	expect(screen.queryByLabelText(/domain/i)).not.toBeInTheDocument();
});
```

and in `authed-shell.test.tsx`:

```tsx
it('links to both team pages', async () => {
	// Before this the shell had a team switcher and a sign-out control, so a
	// second team page was unreachable by clicking.
	renderShell({});
	expect(await screen.findByRole('link', { name: 'Links' })).toBeInTheDocument();
	expect(screen.getByRole('link', { name: 'Domains' })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/link-form.test.tsx src/components/authed-shell.test.tsx` Expected: FAIL on all three.

- [ ] **Step 3: Implement**

`LinkForm` gains a `domain_id` field defaulting to `''`, rendered as a `<select>` only when `domains` is non-empty, with the shared domain as the empty-valued default option. `toRequestBody` in the create route maps `''` to `undefined`, exactly as it already does for `slug` and `expires_at`, so an unset picker keeps the current behaviour of falling through to the shared domain. The route loads the team's verified domains alongside the form.

`AuthedShell` gains two `<Link>`s in the existing `<header>`, requiring `currentTeamId` — omit them when it is undefined, the same condition the team switcher already uses.

- [ ] **Step 4: Run**

Run: `pnpm --filter @kurze-url/web test` Expected: PASS, whole suite.

- [ ] **Step 5: Falsify the picker**

Remove `domain_id` from `toRequestBody` and re-run. Expected: the picker test fails. Restore, and record it.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/custom-domains -m "feat(web): pick a domain when creating a link"
```

---

### Task 15: End to end

**Files:**

- Create: `apps/web/e2e/domains.spec.ts`
- Modify: `apps/web/e2e/i18n.spec.ts` (exclude the hostname from the language crawl)

**Interfaces:**

- Consumes: the `test` fixture from `apps/web/e2e/fixtures/auth.ts`, `waitForHydration` from `fixtures/hydration.ts`.

- [ ] **Step 1: Write the spec**

```ts
import { expect, type Page } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

test('claims a domain and shows the records to create', async ({ page, teamId }) => {
	await page.goto(`/teams/${teamId}/domains`);

	// `goto` resolves on `load`, which this server-rendered form reaches well
	// before React wires it up, and a value typed in that window never reaches
	// React's state. See `waitForHydration`.
	const hostname = page.getByLabel(/hostname/i);
	await waitForHydration(hostname);

	const claimed = `links-${Date.now()}.e2e.test`;
	await hostname.fill(claimed);
	await page.getByRole('button', { name: /add domain/i }).click();

	await expect(page.getByText(`_kurze-url-challenge.${claimed}`)).toBeVisible();
	await expect(page.getByText(claimed)).toBeVisible();
});

test('says which half of verification is missing', async ({ page, teamId }) => {
	// The success path cannot be exercised — it needs real DNS under our
	// control — but the failure path proves the whole chain from the form to
	// the DNS lookup is connected, which is the part that breaks.
	await page.goto(`/teams/${teamId}/domains`);

	const hostname = page.getByLabel(/hostname/i);
	await waitForHydration(hostname);
	await hostname.fill(`links-${Date.now()}.e2e.test`);
	await page.getByRole('button', { name: /add domain/i }).click();

	await page.getByRole('button', { name: /check now/i }).click();
	await expect(page.getByText(/TXT record is not visible yet/i)).toBeVisible();
});

test('has no accessibility violations on the domains screen', async ({ page, teamId }) => {
	await page.goto(`/teams/${teamId}/domains`);
	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});
```

- [ ] **Step 2: Exclude the hostname from the i18n crawl**

`i18n.spec.ts` asserts that no user-facing string is identical across languages. A hostname is data, not copy, and it is identical in both — the same exclusion the link destination and team name already have. Add it the way those two are added, not with a new mechanism.

- [ ] **Step 3: Run against a preview**

E2E runs on `deployment_status`, not locally against production. Push the branch, let the preview deploy, and read the run. `e2e/global-setup.ts` refuses to start if the paired API preview was not built — if it does, that is the guard working, not a bug in this task.

- [ ] **Step 4: Commit**

```bash
pnpm format
but commit -b feat/custom-domains -m "test(e2e): cover claiming a domain"
```

---

### Task 16: Documentation and the pull request

**Files:**

- Modify: `CLAUDE.md`
- Modify: `docs/planning/02-external-services-and-hosting.md`

- [ ] **Step 1: Update `CLAUDE.md`**

Two edits, both of which the spec's header already names as amendments:

- The shared-hostname bullet says `domain.hostname` is unique. It is now unique only among verified rows; say so, and say why (a claim is not a reservation).
- Add `DOMAIN_DNS_TARGET`, `RATE_LIMIT_DOMAIN_CLAIM_PER_HOUR` and `RATE_LIMIT_DOMAIN_VERIFY_PER_HOUR` wherever the other environment variables are described.

- [ ] **Step 2: Update the planning doc**

`02-external-services-and-hosting.md` still describes custom-domain provisioning as "Self-service via Vercel's Domain API". Replace that row's decision with maintainer-in-the-loop and point at the design doc for the reasoning. Leave the SDK call sequence in place — it is what a later switch would use.

- [ ] **Step 3: Run every gate**

```bash
pnpm format:check && pnpm lint && pnpm typecheck && pnpm test
pnpm --filter @kurze-url/web build
pnpm --filter @kurze-url/web test:storybook
cd apps/api && go vet ./... && go test ./... -count=1
```

Expected: all clean.

- [ ] **Step 4: Open the pull request**

Use the `create-pr` skill. Sections: Summary, Changes, Motivation, Testing, Breaking Changes. The Testing section names each falsification from Tasks 2, 5, 6, 9, 10 and 14 with what failed and how — a property with no recorded mutation is not verified.

---

## Closing checklist

- [ ] `pnpm format:check`, `pnpm lint`, `pnpm typecheck`, `pnpm test` clean from the repo root.
- [ ] `pnpm --filter @kurze-url/web build` clean — it catches an import-protection failure the other four gates do not.
- [ ] `pnpm --filter @kurze-url/web test:storybook` passes; the a11y addon is a real gate.
- [ ] `go test ./...` in `apps/api` passes against the local Supabase database.
- [ ] Playwright passes against a preview.
- [ ] Every new string exists in **both** `en.json` and `de.json`, and no string is identical across them.
- [ ] Every falsification recorded. Tasks 2, 5, 6, 9, 10 and 14 each name one.
- [ ] Every new query filters by `team_id`, and Task 2 proves it by removing the filter.
- [ ] The permission matrix accounts for all five new operations.
- [ ] No task edited `GET /{slug}`, and no new query runs on the redirect path.
- [ ] `verification_token` appears in no `audit_log.metadata` — `checkMetadata` rejects the key, so a violation is a failing test, not a silent leak.
