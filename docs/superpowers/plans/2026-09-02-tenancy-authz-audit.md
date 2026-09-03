# Tenancy, Authorization and Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the tenancy core of the `/v1` API — a reusable authorization layer, an atomic audit-log write path, the paginated list envelope, and the team and team-member endpoints — so every later resource can answer "is the caller a member of this team, with a sufficient role?" in one place.

**Architecture:** Authorization is declared in the handler's _input type_. Each team-scoped operation embeds one of four scope structs (`ViewerScope`, `EditorScope`, `AdminScope`, `OwnerScope`); Huma calls the scope's `Resolve` before the handler body, which loads the caller's `team_member` row and rejects with 404 (non-member) or 403 (insufficient role). Mutations run through `db.InTx`, which performs the mutation and its `audit_log` insert in one pgx transaction. Identity data is read from `auth.users` by SQL; Supabase's Admin API is called only to send an invitation email.

**Tech Stack:** Go 1.27 · chi v5 · Huma v2.39 · pgx/v5 + sqlc v1.31 · go-redis v9 · testify · local Supabase Postgres for query tests

**Spec:** `docs/superpowers/specs/2026-09-02-tenancy-authz-audit-design.md` (and `CLAUDE.md`, plus `docs/planning/05-database-schema.md` and `06-api-design.md`)

**Preceded by:** `docs/superpowers/plans/2026-09-02-foundation-and-redirect-path.md`, shipped in PR #3. That plan built config, the schema migration, sqlc, the Redis client, the redirect path, Argon2id verification, JWKS verification and hostname routing. This plan adds to that codebase; it does not restate it.

## Global Constraints

Every task's requirements implicitly include this section.

- **The tenant is called `team`** in every identifier — tables, columns, Go types, API paths. `verein` never appears in an identifier.
- **There is no RLS.** Postgres enforces nothing about tenancy. Every query in this plan filters by `team_id`, and every team-scoped operation carries a scope embed. A query without a tenancy filter is a data-leak bug.
- **Never store or log a full IP address.** This plan touches no IP-derived data at all; the only rate-limit key it adds is a team UUID.
- **Never write a plaintext password or a password hash into `audit_log.metadata`.** `audit.Log` enforces this with a key denylist.
- **Errors are Huma's default RFC 9457 `application/problem+json`.** No custom error model.
- **Pagination is offset/limit** with the typed `Page[T]` envelope, `per_page` capped at 100. Never pagination headers.
- **Migrations are owned by the Supabase CLI.** This plan needs no migration — the schema from plan 1 already has every table it uses.
- **`internal/api` owns HTTP shape only.** No SQL and no Redis commands inline; those live in `internal/db` and `internal/cache`.
- **Commits follow Conventional Commits**, one logical change each.
- Go module path is `github.com/mheob/kurze-url/apps/api`; all commands run from `apps/api`.

## The role permission matrix

This table is the contract. Task 14 turns it into an executable test.

| Action               | viewer | editor | admin | owner |
| -------------------- | ------ | ------ | ----- | ----- |
| Read team            | yes    | yes    | yes   | yes   |
| Rename team          | no     | no     | yes   | yes   |
| List members         | yes    | yes    | yes   | yes   |
| Invite or add member | no     | no     | yes   | yes   |
| Change member role   | no     | no     | yes   | yes   |
| Remove member        | no     | no     | yes   | yes   |
| Read audit log       | no     | no     | yes   | yes   |

Extra rules that are not role comparisons:

- Granting or revoking the `owner` role requires `owner`. An admin may grant up to `admin`.
- An admin may not remove an owner.
- A team always has at least one owner: the last owner can be neither demoted nor removed.
- `POST /v1/teams` is restricted to the maintainer allowlist, not to any team role.

## File Structure

Created:

```
internal/authz/roles.go          Role type, ordering, parsing
internal/authz/roles_test.go
internal/authz/scope.go          Membership, Resolver, context plumbing, the four scope types
internal/authz/scope_test.go
internal/authz/resolver.go       QueryResolver: the db-backed Resolver implementation
internal/audit/audit.go          Action constants, Entry, Log
internal/audit/audit_test.go
internal/supabase/admin.go       Admin API client (invite only)
internal/supabase/admin_test.go
internal/api/page.go             Page[T], PageParams
internal/api/page_test.go
internal/api/me.go               GET /v1/me
internal/api/teams.go            POST|GET /v1/teams, GET|PATCH /v1/teams/{team_id}
internal/api/teams_test.go
internal/api/members.go          the four /v1/teams/{team_id}/members operations
internal/api/members_test.go
internal/api/auditlog.go         GET /v1/teams/{team_id}/audit-log
internal/api/auditlog_test.go
internal/api/matrix_test.go      the permission matrix + operation registry tests
internal/api/tenancy_test.go     shared test helpers for the tenancy suite
internal/db/tx.go                InTx (hand-written; sqlc rewrites only its own files)
internal/db/queries/team.sql     team, team_member and auth.users queries
internal/db/queries/audit.sql    audit insert and filtered list
internal/db/tenancy_test.go      query tests for the above
```

Modified:

```
internal/config/config.go        maintainer allowlist, Supabase auth URL + service-role key, invite rate limit
internal/config/config_test.go
internal/auth/jwt.go             claims context helpers move here from internal/api
internal/api/api.go              Deps gains Pool and Admin
internal/api/v1.go               installs claims + resolver into context; /v1/me moves to me.go
internal/db/schema/0000_auth_stub.sql   gains the nullable email column sqlc needs
.env.example                     the three new variables, valueless
README.md                        the new local-development variables
```

## Task boundaries

Fourteen tasks. Each ends with a green test run and one commit. Tasks 1–6 build the substrate (config, roles, scopes, queries, audit, pagination); tasks 7–13 build one endpoint group each; task 14 wires everything and makes the matrix executable.

---

## Task 1: Configuration for the maintainer allowlist, Supabase Admin API and invite limit

**Files:**

- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `.env.example`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `config.Config` gains `MaintainerUserIDs []uuid.UUID`, `SupabaseAuthURL string`, `SupabaseServiceRoleKey string`, `InviteRateLimitPerHour int`, and the method `func (c Config) IsMaintainer(id uuid.UUID) bool`.

- [ ] **Step 1: Write the failing config tests**

Append to `internal/config/config_test.go`. Match the existing file's style — it already sets and clears environment variables around `config.Load()`; reuse whatever helper it defines rather than inventing a second one. If it has no helper, use `t.Setenv`, which restores the previous value automatically.

```go
func TestLoadParsesTheMaintainerAllowlist(t *testing.T) {
	first := uuid.New()
	second := uuid.New()

	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", first.String()+", "+second.String())

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{first, second}, cfg.MaintainerUserIDs)
	require.True(t, cfg.IsMaintainer(first))
	require.True(t, cfg.IsMaintainer(second))
	require.False(t, cfg.IsMaintainer(uuid.New()))
}

func TestLoadTreatsAnUnsetAllowlistAsNobody(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", "")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Empty(t, cfg.MaintainerUserIDs)
	require.False(t, cfg.IsMaintainer(uuid.New()),
		"an unset allowlist must close team creation, never open it")
}

func TestLoadRejectsAMalformedMaintainerID(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("MAINTAINER_USER_IDS", "not-a-uuid")

	_, err := config.Load()

	require.Error(t, err, "a typo in the allowlist must fail startup, not silently drop an entry")
	require.Contains(t, err.Error(), "MAINTAINER_USER_IDS")
}

func TestSupabaseAuthURLDefaultsToTheJWTIssuer(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("SUPABASE_JWT_ISSUER", "https://project.supabase.co/auth/v1")
	t.Setenv("SUPABASE_AUTH_URL", "")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "https://project.supabase.co/auth/v1", cfg.SupabaseAuthURL,
		"the issuer is the auth base URL for a Supabase project; do not make operators set both")
}

func TestInviteRateLimitDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/postgres")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("VISITOR_SALT", "salt")
	t.Setenv("RATE_LIMIT_INVITE_PER_HOUR", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 20, cfg.InviteRateLimitPerHour)

	t.Setenv("RATE_LIMIT_INVITE_PER_HOUR", "5")
	cfg, err = config.Load()
	require.NoError(t, err)
	require.Equal(t, 5, cfg.InviteRateLimitPerHour)
}
```

Add `"github.com/google/uuid"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run 'Maintainer|SupabaseAuthURL|InviteRateLimit' -v` Expected: FAIL — compilation errors, `cfg.MaintainerUserIDs undefined`.

- [ ] **Step 3: Extend the Config struct**

In `internal/config/config.go`, add these fields to `Config` after `VisitorSalt`:

```go
	// Only these user IDs may create teams. Empty means nobody can: this is a
	// shared instance, and an open POST /v1/teams is the classic URL-shortener
	// abuse vector. A misconfigured deployment must close team creation, not
	// open it, so this is deliberately not a required variable.
	MaintainerUserIDs []uuid.UUID

	// Supabase's auth base URL and service-role key, used for exactly one
	// call: POST {SupabaseAuthURL}/invite. Both empty means invitations are
	// unavailable and the members endpoint refuses the new-address branch.
	SupabaseAuthURL        string
	SupabaseServiceRoleKey string
```

Add `InviteRateLimitPerHour int` to the rate-limit block:

```go
	RedirectRateLimitPerMin   int
	PasswordRateLimitPerMin   int
	LinkCreateRateLimitPerMin int
	InviteRateLimitPerHour    int
```

Add the imports `"strings"` and `"github.com/google/uuid"`.

- [ ] **Step 4: Parse the new variables in Load**

In `Load`, after the existing required-variable loop and before the rate-limit block, add:

```go
	cfg.SupabaseAuthURL = env("SUPABASE_AUTH_URL", cfg.JWTIssuer)
	cfg.SupabaseServiceRoleKey = os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	maintainers, err := envUUIDs("MAINTAINER_USER_IDS")
	if err != nil {
		return Config{}, err
	}
	cfg.MaintainerUserIDs = maintainers
```

Note `err` is already declared below by the existing `var err error`; move that declaration above this block, or use `:=` here and drop the later `var err error`. Keep exactly one declaration — `golangci-lint` will flag a shadowed one.

Add to the rate-limit block:

```go
	if cfg.InviteRateLimitPerHour, err = envInt("RATE_LIMIT_INVITE_PER_HOUR", 20); err != nil {
		return Config{}, err
	}
```

Add the two new helpers at the bottom of the file:

```go
// envUUIDs parses a comma-separated list of UUIDs. A malformed entry fails
// startup: silently dropping it would quietly change who may create teams.
func envUUIDs(name string) ([]uuid.UUID, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}

	var ids []uuid.UUID
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			return nil, fmt.Errorf("config: %s contains an invalid uuid %q: %w", name, part, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// IsMaintainer reports whether the user may create teams.
func (c Config) IsMaintainer(id uuid.UUID) bool {
	for _, allowed := range c.MaintainerUserIDs {
		if allowed == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v` Expected: PASS, including the pre-existing tests.

- [ ] **Step 6: Document the variables in `.env.example`**

Append to `apps/api/.env.example`:

```bash
# Comma-separated Supabase user IDs allowed to create teams. Empty (the
# default) means nobody can: this is one shared instance, and self-service team
# creation is deliberately not enabled. A Verein asks the maintainer, who
# creates the team and invites its first owner.
MAINTAINER_USER_IDS=

# Supabase Admin API, used for exactly one call: sending a team invitation
# email. SUPABASE_AUTH_URL defaults to SUPABASE_JWT_ISSUER and rarely needs
# setting. The service-role key bypasses every database policy — never log it,
# never expose it to the frontend or the CLI.
SUPABASE_AUTH_URL=
SUPABASE_SERVICE_ROLE_KEY=

# Invitations per hour per team. Invitations spend real email quota (Resend's
# free tier is 3,000 per month across all Supabase auth email).
RATE_LIMIT_INVITE_PER_HOUR=20
```

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "feat(api): configure maintainer allowlist and invite settings"
```

---

## Task 2: The role type and its ordering

**Files:**

- Create: `internal/authz/roles.go`
- Test: `internal/authz/roles_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `authz.Role` (a `string` type) with constants `RoleViewer`, `RoleEditor`, `RoleAdmin`, `RoleOwner`; `func ParseRole(string) (Role, error)`; `func (r Role) AtLeast(min Role) bool`; `func (r Role) String() string`; `var ErrInvalidRole error`.

- [ ] **Step 1: Write the failing test**

Create `internal/authz/roles_test.go`:

```go
package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestRolesAreOrderedViewerToOwner(t *testing.T) {
	require.True(t, authz.RoleOwner.AtLeast(authz.RoleAdmin))
	require.True(t, authz.RoleAdmin.AtLeast(authz.RoleEditor))
	require.True(t, authz.RoleEditor.AtLeast(authz.RoleViewer))
	require.True(t, authz.RoleViewer.AtLeast(authz.RoleViewer))

	require.False(t, authz.RoleViewer.AtLeast(authz.RoleEditor))
	require.False(t, authz.RoleEditor.AtLeast(authz.RoleAdmin))
	require.False(t, authz.RoleAdmin.AtLeast(authz.RoleOwner))
}

func TestAnUnknownRoleSatisfiesNothing(t *testing.T) {
	unknown := authz.Role("superuser")

	require.False(t, unknown.AtLeast(authz.RoleViewer),
		"an unrecognised role must never pass a check — failing open is a data leak")
}

func TestParseRoleAcceptsExactlyTheFourSchemaRoles(t *testing.T) {
	for _, name := range []string{"viewer", "editor", "admin", "owner"} {
		role, err := authz.ParseRole(name)
		require.NoError(t, err)
		require.Equal(t, name, role.String())
	}
}

func TestParseRoleRejectsAnythingElse(t *testing.T) {
	for _, name := range []string{"", "Owner", "root", "admin "} {
		_, err := authz.ParseRole(name)
		require.ErrorIs(t, err, authz.ErrInvalidRole, "input %q", name)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/authz/ -v` Expected: FAIL — `no Go files in .../internal/authz`.

- [ ] **Step 3: Implement the role type**

Create `internal/authz/roles.go`:

```go
// Package authz owns tenancy authorization: the role ordering and the scope
// types that every team-scoped operation embeds. Postgres enforces nothing
// about tenancy — there is no RLS and the API connects with a service role —
// so this package is the only thing standing between one team's data and
// another's.
package authz

import (
	"errors"
	"fmt"
)

// Role mirrors the team_member.role check constraint.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// ErrInvalidRole is returned by ParseRole for anything outside the four
// schema roles.
var ErrInvalidRole = errors.New("authz: invalid role")

// rank orders the roles. A role absent from this map ranks 0 and therefore
// satisfies no requirement, which is the safe direction to fail.
var rank = map[Role]int{
	RoleViewer: 1,
	RoleEditor: 2,
	RoleAdmin:  3,
	RoleOwner:  4,
}

// ParseRole converts a request or database value into a Role.
func ParseRole(s string) (Role, error) {
	role := Role(s)
	if _, ok := rank[role]; !ok {
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, s)
	}
	return role, nil
}

// AtLeast reports whether r is min or higher.
func (r Role) AtLeast(min Role) bool {
	return rank[r] >= rank[min] && rank[r] > 0
}

func (r Role) String() string { return string(r) }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/authz/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/roles.go internal/authz/roles_test.go
git commit -m "feat(api): add the team role type and ordering"
```

---

## Task 3: Scope types, the membership resolver and the claims-context move

**Files:**

- Create: `internal/authz/scope.go`
- Test: `internal/authz/scope_test.go`
- Modify: `internal/auth/jwt.go` (claims context helpers move here)
- Modify: `internal/api/v1.go` (use the moved helpers)

**Interfaces:**

- Consumes: `authz.Role` and `authz.RoleViewer`…`RoleOwner` from Task 2; `auth.Claims` from plan 1.
- Produces:
  - `auth.WithClaims(ctx context.Context, c Claims) context.Context` and `auth.ClaimsFromContext(ctx context.Context) (Claims, bool)`.
  - `authz.Membership{TeamID, UserID uuid.UUID; Role Role}`.
  - `authz.Resolver` interface: `Membership(ctx context.Context, teamID, userID uuid.UUID) (Membership, error)`.
  - `authz.ErrNotMember`.
  - `authz.WithResolver(ctx context.Context, r Resolver) context.Context`.
  - `authz.TeamPath{TeamID uuid.UUID}` plus `ViewerScope`, `EditorScope`, `AdminScope`, `OwnerScope`, each with `Resolve(huma.Context) []error` on a pointer receiver and `Member() Membership`.

- [ ] **Step 1: Move the claims context helpers into `internal/auth`**

`internal/authz` needs the caller's user ID, and it cannot import `internal/api` (which will import `authz`). The claims context key therefore moves down a layer.

Append to `internal/auth/jwt.go`:

```go
// claimsKey is the context key the verified claims travel under. It lives in
// this package rather than internal/api because internal/authz needs to read
// the caller's identity and must not import the HTTP layer.
type claimsKey struct{}

// WithClaims returns a context carrying the verified claims.
func WithClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// ClaimsFromContext returns the claims a bearer-authenticated request was
// verified with.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(Claims)
	return claims, ok
}
```

In `internal/api/v1.go`, delete `type contextKey struct{}` and `var userContextKey = contextKey{}`, and rewrite the last line of `authMiddleware` and `UserFromContext`:

```go
		next(huma.WithContext(ctx, auth.WithClaims(ctx.Context(), claims)))
```

```go
// UserFromContext returns the verified claims a bearer-authenticated operation
// was called with. It is a thin wrapper over auth.ClaimsFromContext so handlers
// need not import internal/auth for this one call.
func UserFromContext(ctx context.Context) (auth.Claims, bool) {
	return auth.ClaimsFromContext(ctx)
}
```

- [ ] **Step 2: Run the existing suite to prove the move changed no behaviour**

Run: `go test ./internal/api/ ./internal/auth/` Expected: PASS — the `/v1/me` tests from plan 1 still pass. If Postgres or Docker is unavailable, some tests skip; that is expected, but nothing may fail.

- [ ] **Step 3: Write the failing scope test**

Create `internal/authz/scope_test.go`:

```go
package authz_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// stubResolver answers with a fixed role, or ErrNotMember when role is empty.
type stubResolver struct {
	role authz.Role
	err  error
}

func (s stubResolver) Membership(_ context.Context, teamID, userID uuid.UUID) (authz.Membership, error) {
	if s.err != nil {
		return authz.Membership{}, s.err
	}
	return authz.Membership{TeamID: teamID, UserID: userID, Role: s.role}, nil
}

// scopeTestServer mounts one operation whose input embeds an AdminScope and
// which echoes the resolved role, so a test can assert both the status code
// and that the handler saw the membership.
func scopeTestServer(t *testing.T, resolver authz.Resolver, claims *auth.Claims) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "1.0.0"))

	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if claims != nil {
			inner = auth.WithClaims(inner, *claims)
		}
		if resolver != nil {
			inner = authz.WithResolver(inner, resolver)
		}
		next(huma.WithContext(ctx, inner))
	})

	type input struct {
		authz.AdminScope
	}
	type output struct {
		Body struct {
			Role string `json:"role"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "scope-probe",
		Method:      http.MethodGet,
		Path:        "/teams/{team_id}/probe",
	}, func(_ context.Context, in *input) (*output, error) {
		out := &output{}
		out.Body.Role = in.Member().Role.String()
		return out, nil
	})

	return mux
}

func get(t *testing.T, handler http.Handler, teamID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/teams/"+teamID.String()+"/probe", nil))
	return rec
}

func TestScopeAllowsASufficientRoleAndExposesTheMembership(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New(), Email: "member@example.org"}
	handler := scopeTestServer(t, stubResolver{role: authz.RoleOwner}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"role":"owner"}`, rec.Body.String(),
		"the handler must see the membership Resolve loaded, not query it again")
}

func TestScopeRejectsAnInsufficientRoleWith403(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{role: authz.RoleEditor}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}

func TestScopeHidesTheTeamFromANonMemberWith404(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{err: authz.ErrNotMember}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to tell an existing team from a missing one")
}

func TestScopeRejectsAnUnauthenticatedCallerWith401(t *testing.T) {
	handler := scopeTestServer(t, stubResolver{role: authz.RoleOwner}, nil)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestScopeFailsClosedWhenNoResolverIsInstalled(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, nil, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a misconfigured server must refuse the request, never serve it unauthorized")
}

func TestScopeSurfacesAResolverFailureAs500(t *testing.T) {
	claims := auth.Claims{UserID: uuid.New()}
	handler := scopeTestServer(t, stubResolver{err: errors.New("database is down")}, &claims)

	rec := get(t, handler, uuid.New())

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/authz/ -run Scope -v` Expected: FAIL — `undefined: authz.AdminScope`.

- [ ] **Step 5: Implement the scopes**

Create `internal/authz/scope.go`:

```go
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

// ErrNotMember means the caller has no team_member row for the team. It is
// answered with 404, not 403: a team's existence is not disclosed to people
// outside it, so team IDs cannot be enumerated.
var ErrNotMember = errors.New("authz: caller is not a member of the team")

// Membership is the caller's relationship to one team.
type Membership struct {
	TeamID uuid.UUID
	UserID uuid.UUID
	Role   Role
}

// Resolver loads a caller's membership. Implemented by QueryResolver against
// Postgres, and by fakes in tests.
type Resolver interface {
	Membership(ctx context.Context, teamID, userID uuid.UUID) (Membership, error)
}

type resolverKey struct{}

// WithResolver returns a context carrying the membership resolver. The /v1
// auth middleware installs it once per request.
func WithResolver(ctx context.Context, r Resolver) context.Context {
	return context.WithValue(ctx, resolverKey{}, r)
}

func resolverFromContext(ctx context.Context) (Resolver, bool) {
	r, ok := ctx.Value(resolverKey{}).(Resolver)
	return r, ok
}

// TeamPath carries the team ID every team-scoped operation takes in its path.
// It is exported and embedded by value in each scope because reflection cannot
// reliably set fields promoted through an unexported embedded struct.
type TeamPath struct {
	TeamID uuid.UUID `path:"team_id" doc:"The team this request operates on."`
}

// The four scopes below are what an operation embeds to declare the role it
// requires. Huma calls Resolve before the handler body runs, so a handler can
// never execute without the check having passed. Embedding one of these is the
// only way a team-scoped operation is allowed to reach team data; the registry
// test in internal/api fails the build if an authenticated operation omits it.

type ViewerScope struct {
	TeamPath
	member Membership
}

type EditorScope struct {
	TeamPath
	member Membership
}

type AdminScope struct {
	TeamPath
	member Membership
}

type OwnerScope struct {
	TeamPath
	member Membership
}

func (s *ViewerScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleViewer, &s.member)
}

func (s *EditorScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleEditor, &s.member)
}

func (s *AdminScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleAdmin, &s.member)
}

func (s *OwnerScope) Resolve(ctx huma.Context) []error {
	return resolveScope(ctx, s.TeamID, RoleOwner, &s.member)
}

// Member returns the membership Resolve loaded. Handlers read it instead of
// querying team_member a second time.
func (s *ViewerScope) Member() Membership { return s.member }
func (s *EditorScope) Member() Membership { return s.member }
func (s *AdminScope) Member() Membership  { return s.member }
func (s *OwnerScope) Member() Membership  { return s.member }

// resolveScope is the whole authorization decision, in one place. The
// membership travels out through the scope struct rather than the context
// because Resolve returns only errors — it cannot replace the context the
// handler receives — but it can mutate the input it is part of.
func resolveScope(ctx huma.Context, teamID uuid.UUID, min Role, out *Membership) []error {
	claims, ok := auth.ClaimsFromContext(ctx.Context())
	if !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	resolver, ok := resolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know whether this caller belongs to the team.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	membership, err := resolver.Membership(ctx.Context(), teamID, claims.UserID)
	switch {
	case errors.Is(err, ErrNotMember):
		return []error{huma.Error404NotFound("team not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve team membership")}
	}

	if !membership.Role.AtLeast(min) {
		return []error{huma.Error403Forbidden(
			fmt.Sprintf("this action requires the %s role or higher", min))}
	}

	*out = membership
	return nil
}
```

- [ ] **Step 6: Add the humago adapter dependency if it is missing**

Run: `go get github.com/danielgtaylor/huma/v2/adapters/humago@v2.39.1 && go mod tidy`

(The adapter ships inside the same module, so this usually resolves to a no-op. Run `go mod tidy` regardless so the test file's import is recorded.)

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/authz/ -v` Expected: PASS, all six scope tests plus Task 2's role tests.

- [ ] **Step 8: Commit**

```bash
git add internal/authz/scope.go internal/authz/scope_test.go internal/auth/jwt.go internal/api/v1.go go.mod go.sum
git commit -m "feat(api): add team scope resolution for /v1 operations"
```

---

## Task 4: Tenancy queries, the auth stub's email column and the transaction helper

**Files:**

- Modify: `internal/db/schema/0000_auth_stub.sql`
- Create: `internal/db/queries/team.sql`
- Create: `internal/db/queries/audit.sql`
- Create: `internal/db/tx.go`
- Test: `internal/db/tenancy_test.go`
- Generated (do not hand-edit): `internal/db/team.sql.go`, `internal/db/audit.sql.go`, `internal/db/models.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces (sqlc-generated method names later tasks call): `GetTeamMembership`, `CreateTeam`, `GetTeam`, `RenameTeam`, `ListTeamsForUser`, `ListMembershipsForUser`, `InsertTeamMember`, `ListTeamMembers`, `UpdateTeamMemberRole`, `DeleteTeamMember`, `LockTeamOwners`, `GetUserIDByEmail`, `InsertAuditLog`, `ListAuditLog`. Plus hand-written `db.InTx(ctx, pool, func(*db.Queries) error) error`.

- [ ] **Step 1: Give the sqlc auth stub its email column**

`auth.users.email` is nullable in Supabase (phone-only accounts exist), so the generated Go type must be `*string`. Replace the table in `internal/db/schema/0000_auth_stub.sql`:

```sql
create table auth.users (
  id     uuid primary key,
  -- Nullable, matching Supabase: an account created by phone has no email.
  -- Reads must handle nil.
  email  text
);
```

- [ ] **Step 2: Write the team queries**

Create `internal/db/queries/team.sql`:

```sql
-- Tenancy queries. There is no RLS: every one of these filters by team_id (or
-- by the caller's user_id), because Postgres enforces nothing about tenancy.

-- name: GetTeamMembership :one
select team_id, user_id, role
from team_member
where team_id = $1 and user_id = $2;

-- name: CreateTeam :one
insert into team (name) values ($1)
returning id, name, created_at;

-- name: GetTeam :one
select id, name, created_at from team where id = $1;

-- name: RenameTeam :one
update team set name = $2 where id = $1
returning id, name, created_at;

-- Paginated. count(*) over () gives the total in the same scan, so the list
-- and its total_count cannot disagree the way two separate queries can.

-- name: ListTeamsForUser :many
select t.id, t.name, t.created_at, tm.role, count(*) over () as total_count
from team t
join team_member tm on tm.team_id = t.id
where tm.user_id = $1
order by t.name, t.id
limit $2 offset $3;

-- Unpaginated on purpose: this drives the frontend's team switcher, and a
-- person belongs to a handful of Vereine.

-- name: ListMembershipsForUser :many
select tm.team_id, t.name as team_name, tm.role
from team_member tm
join team t on t.id = tm.team_id
where tm.user_id = $1
order by t.name, t.id;

-- Returns created_at because POST /v1/teams/{team_id}/members echoes the
-- created member back, and a fabricated timestamp would be a lie.

-- name: InsertTeamMember :one
insert into team_member (team_id, user_id, role) values ($1, $2, $3)
returning created_at;

-- name: ListTeamMembers :many
select tm.user_id, u.email, tm.role, tm.created_at, count(*) over () as total_count
from team_member tm
join auth.users u on u.id = tm.user_id
where tm.team_id = $1
order by u.email, tm.user_id
limit $2 offset $3;

-- name: UpdateTeamMemberRole :exec
update team_member set role = $3 where team_id = $1 and user_id = $2;

-- name: DeleteTeamMember :exec
delete from team_member where team_id = $1 and user_id = $2;

-- Locks the team's owner rows for the rest of the transaction. Without this,
-- two concurrent demotions both read "there are two owners" and both succeed,
-- leaving the team ownerless.

-- name: LockTeamOwners :many
select user_id from team_member
where team_id = $1 and role = 'owner'
for update;

-- name: GetUserIDByEmail :one
select id from auth.users where lower(email) = lower($1) limit 1;
```

- [ ] **Step 3: Write the audit queries**

Create `internal/db/queries/audit.sql`:

```sql
-- Audit log. Writes always share the transaction of the mutation they record;
-- see db.InTx.

-- name: InsertAuditLog :exec
insert into audit_log (team_id, actor_user_id, action, entity_type, entity_id, metadata)
values ($1, $2, $3, $4, $5, $6);

-- name: ListAuditLog :many
select id, team_id, actor_user_id, action, entity_type, entity_id, metadata, created_at,
       count(*) over () as total_count
from audit_log
where team_id = sqlc.arg('team_id')
  and (sqlc.narg('entity_type')::text is null or entity_type = sqlc.narg('entity_type')::text)
  and (sqlc.narg('action')::text is null or action = sqlc.narg('action')::text)
  and (sqlc.narg('actor_user_id')::uuid is null or actor_user_id = sqlc.narg('actor_user_id')::uuid)
  and (sqlc.narg('from')::timestamptz is null or created_at >= sqlc.narg('from')::timestamptz)
  and (sqlc.narg('to')::timestamptz is null or created_at <= sqlc.narg('to')::timestamptz)
order by created_at desc, id desc
limit sqlc.arg('result_limit') offset sqlc.arg('result_offset');
```

- [ ] **Step 4: Write the transaction helper**

Create `internal/db/tx.go`:

```go
// This file is hand-written. sqlc rewrites only its own outputs — db.go,
// models.go, batch.go and *.sql.go — so a hand-written file is safe in this
// package. Do not add queries here; they belong in queries/*.sql.

package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn inside one transaction. Every mutating handler uses it so the
// mutation and its audit_log insert commit together: an audited action either
// happened and is recorded, or neither.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this is safe
	// unconditionally and covers every early return inside fn.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit transaction: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Generate and check it compiles**

Run: `sqlc generate && go build ./...` Expected: no output from either. If sqlc errors on `auth.users`, the stub edit in Step 1 was not saved.

Inspect the generated `ListAuditLogParams` and `ListTeamMembersRow` in `internal/db/`. Note the exact field names and types now — later tasks reference them, and sqlc's naming (`ResultLimit`, `ResultOffset`, `TotalCount`, `Email *string`) must match what those tasks assume.

- [ ] **Step 6: Write the failing query tests**

Create `internal/db/tenancy_test.go`. It uses the existing `testPool` helper from `schema_test.go` and rolls everything back, so the suite can run repeatedly against one local database.

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

// seedTeamWithOwner inserts a team plus one owner, inside tx, and returns both
// IDs. It borrows an existing auth.users row: the local Supabase stack seeds
// one, and this suite must not invent auth users of its own.
func seedTeamWithOwner(ctx context.Context, t *testing.T, tx pgx.Tx) (teamID, userID uuid.UUID) {
	t.Helper()

	require.NoError(t, tx.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('tenancy fixture') returning id`).Scan(&teamID))
	_, err := tx.Exec(ctx,
		`insert into team_member (team_id, user_id, role) values ($1, $2, 'owner')`, teamID, userID)
	require.NoError(t, err)

	return teamID, userID
}

func TestGetTeamMembershipReturnsTheCallersRole(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	member, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{TeamID: teamID, UserID: userID})

	require.NoError(t, err)
	require.Equal(t, "owner", member.Role)
}

func TestGetTeamMembershipReturnsNoRowsForANonMember(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, _ := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	_, err = q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: teamID,
		UserID: uuid.New(),
	})

	require.ErrorIs(t, err, pgx.ErrNoRows,
		"the caller of this query turns ErrNoRows into 404; it must not return an empty row")
}

func TestListTeamMembersJoinsTheEmailAndCarriesTheTotal(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	rows, err := q.ListTeamMembers(ctx, db.ListTeamMembersParams{
		TeamID: teamID,
		Limit:  25,
		Offset: 0,
	})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, userID, rows[0].UserID)
	require.EqualValues(t, 1, rows[0].TotalCount)
}

func TestListTeamsForUserOnlyReturnsTeamsTheUserBelongsTo(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('someone else') returning id`).Scan(&otherTeamID))

	q := db.New(tx)
	rows, err := q.ListTeamsForUser(ctx, db.ListTeamsForUserParams{
		UserID: userID,
		Limit:  25,
		Offset: 0,
	})

	require.NoError(t, err)
	for _, row := range rows {
		require.NotEqual(t, otherTeamID, row.ID,
			"a team the caller has no membership in must never appear")
	}
	require.Contains(t, teamIDs(rows), teamID)
}

func teamIDs(rows []db.ListTeamsForUserRow) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestGetUserIDByEmailIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var email *string
	err := pool.QueryRow(ctx,
		`select email from auth.users where email is not null limit 1`).Scan(&email)
	if err != nil {
		t.Skip("the local Supabase stack seeded no user with an email address")
	}

	q := db.New(pool)
	id, err := q.GetUserIDByEmail(ctx, upper(*email))

	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, id)
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - ('a' - 'A')
		}
	}
	return string(out)
}

func TestInTxRollsBackEverythingWhenTheCallbackFails(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var teamID uuid.UUID
	wantErr := errTest

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, "rolled back")
		require.NoError(t, err)
		teamID = team.ID
		return wantErr
	})

	require.ErrorIs(t, err, wantErr)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from team where id = $1`, teamID).Scan(&count))
	require.Zero(t, count, "InTx must roll back every write the callback made")
}

func TestInTxCommitsWhenTheCallbackSucceeds(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	var teamID uuid.UUID
	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, "committed")
		if err != nil {
			return err
		}
		teamID = team.ID
		return nil
	}))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	var name string
	require.NoError(t, pool.QueryRow(ctx,
		`select name from team where id = $1`, teamID).Scan(&name))
	require.Equal(t, "committed", name)
}

func TestListAuditLogFiltersByActionAndPaginates(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)
	q := db.New(tx)

	for _, action := range []string{"team.created", "team.renamed", "team.renamed"} {
		require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			TeamID:      &teamID,
			ActorUserID: &userID,
			Action:      action,
			EntityType:  "team",
			EntityID:    &teamID,
			Metadata:    []byte(`{}`),
		}))
	}

	renamed := "team.renamed"
	rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{
		TeamID:       teamID,
		Action:       &renamed,
		ResultLimit:  25,
		ResultOffset: 0,
	})

	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.EqualValues(t, 2, rows[0].TotalCount)
	for _, row := range rows {
		require.Equal(t, "team.renamed", row.Action)
	}
}

func TestListAuditLogNeverCrossesTeams(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	teamID, userID := seedTeamWithOwner(ctx, t, tx)

	var otherTeamID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('other') returning id`).Scan(&otherTeamID))

	q := db.New(tx)
	require.NoError(t, q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &otherTeamID,
		ActorUserID: &userID,
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    &otherTeamID,
		Metadata:    []byte(`{}`),
	}))

	rows, err := q.ListAuditLog(ctx, db.ListAuditLogParams{
		TeamID:       teamID,
		ResultLimit:  25,
		ResultOffset: 0,
	})

	require.NoError(t, err)
	require.Empty(t, rows, "an audit entry belonging to another team must never be listed")
}
```

Add `var errTest = errors.New("test failure")` and the `errors` import at the top of the file. Delete the unused `time` import if the final file does not need it.

- [ ] **Step 7: Run the query tests**

Run: `supabase start` (if it is not already running), then `go test ./internal/db/ -v` Expected: PASS. Tests skip — never fail — when Postgres is unavailable.

If a generated field name differs from what the test assumes (for example `Limit` versus `ResultLimit`), fix the _test_ to match the generated code, or rename the sqlc parameter in the `.sql` file and regenerate. Do not hand-edit generated Go.

- [ ] **Step 8: Commit**

```bash
git add internal/db/
git commit -m "feat(api): add tenancy and audit queries with a tx helper"
```

---

## Task 5: The audit writer and its action taxonomy

**Files:**

- Create: `internal/audit/audit.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**

- Consumes: `db.InsertAuditLog` and `db.InTx` from Task 4.
- Produces: `audit.Action` with constants `ActionTeamCreated`, `ActionTeamRenamed`, `ActionMemberInvited`, `ActionMemberAdded`, `ActionMemberRoleChanged`, `ActionMemberRemoved`; `audit.EntityTeam`, `audit.EntityTeamMember`; `audit.Entry`; `audit.Log(ctx context.Context, q *db.Queries, e Entry) error`; `audit.ErrForbiddenMetadata`; `audit.ErrUnknownAction`.

- [ ] **Step 1: Write the failing test**

Create `internal/audit/audit_test.go`:

```go
package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
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

// seedTeam creates a throwaway team owned by an existing auth user.
func seedTeam(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (teamID, userID uuid.UUID) {
	t.Helper()

	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('audit fixture') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	return teamID, userID
}

func TestLogWritesTheEntry(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
			Metadata:    map[string]any{"from": "Alte Verein", "to": "Neue Verein"},
		})
	}))

	var action, entityType string
	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`select action, entity_type, metadata from audit_log where team_id = $1`,
		teamID).Scan(&action, &entityType, &raw))

	require.Equal(t, "team.renamed", action)
	require.Equal(t, "team", entityType)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(raw, &metadata))
	require.Equal(t, "Neue Verein", metadata["to"])
}

func TestLogWritesAnEmptyObjectForNilMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	require.NoError(t, db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamCreated,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		})
	}))

	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`select metadata from audit_log where team_id = $1`, teamID).Scan(&raw))
	require.JSONEq(t, `{}`, string(raw), "metadata must be an object, never SQL null")
}

func TestLogRefusesPasswordishMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	for _, key := range []string{"password", "Password", "password_hash", "ip", "ip_address"} {
		err := db.InTx(ctx, pool, func(q *db.Queries) error {
			return audit.Log(ctx, q, audit.Entry{
				TeamID:      teamID,
				ActorUserID: userID,
				Action:      audit.ActionTeamRenamed,
				EntityType:  audit.EntityTeam,
				EntityID:    teamID,
				Metadata:    map[string]any{key: "secret"},
			})
		})

		require.ErrorIs(t, err, audit.ErrForbiddenMetadata, "key %q", key)
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from audit_log where team_id = $1`, teamID).Scan(&count))
	require.Zero(t, count, "a refused entry must leave no row behind")
}

func TestLogRefusesAnActionOutsideTheTaxonomy(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.Action("team.frobnicated"),
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		})
	})

	require.ErrorIs(t, err, audit.ErrUnknownAction,
		"the taxonomy is the contract; a typo must fail loudly rather than land in the log")
}

func TestLogRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	teamID, userID := seedTeam(ctx, t, pool)
	boom := errors.New("mutation failed after the audit insert")

	err := db.InTx(ctx, pool, func(q *db.Queries) error {
		if err := audit.Log(ctx, q, audit.Entry{
			TeamID:      teamID,
			ActorUserID: userID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    teamID,
		}); err != nil {
			return err
		}
		return boom
	})

	require.ErrorIs(t, err, boom)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`select count(*) from audit_log where team_id = $1`, teamID).Scan(&count))
	require.Zero(t, count,
		"an audited action either happened and is recorded, or neither")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/audit/ -v` Expected: FAIL — `no Go files in .../internal/audit`.

- [ ] **Step 3: Implement the audit writer**

Create `internal/audit/audit.go`:

```go
// Package audit owns the audit_log write path and the action taxonomy. Every
// mutating endpoint records exactly one entry, in the same transaction as the
// mutation itself, so the log cannot disagree with the data.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Action is an audit_log.action value: one per mutating endpoint, shaped
// "entity.verb". Plan 3 adds domain.*, folder.*, tag.* and link.* here
// alongside the endpoints that emit them.
type Action string

const (
	ActionTeamCreated       Action = "team.created"
	ActionTeamRenamed       Action = "team.renamed"
	ActionMemberInvited     Action = "team_member.invited"
	ActionMemberAdded       Action = "team_member.added"
	ActionMemberRoleChanged Action = "team_member.role_changed"
	ActionMemberRemoved     Action = "team_member.removed"
)

// Entity types match the table names.
const (
	EntityTeam       = "team"
	EntityTeamMember = "team_member"
)

var (
	// ErrUnknownAction guards against typos: an action outside the taxonomy is
	// a bug, and a silently-written unknown value makes the log unqueryable.
	ErrUnknownAction = errors.New("audit: action is not part of the taxonomy")

	// ErrForbiddenMetadata enforces the schema comment on audit_log.metadata:
	// it never carries a plaintext password, a password hash, or an IP address.
	ErrForbiddenMetadata = errors.New("audit: metadata may not carry secrets or IP addresses")
)

var knownActions = map[Action]struct{}{
	ActionTeamCreated:       {},
	ActionTeamRenamed:       {},
	ActionMemberInvited:     {},
	ActionMemberAdded:       {},
	ActionMemberRoleChanged: {},
	ActionMemberRemoved:     {},
}

// Matched case-insensitively against every metadata key.
var forbiddenMetadataKeys = []string{
	"password", "password_hash", "hash", "secret", "token", "ip", "ip_address",
}

// Entry is one audit record. Every field is required except Metadata.
type Entry struct {
	TeamID      uuid.UUID
	ActorUserID uuid.UUID
	Action      Action
	EntityType  string
	EntityID    uuid.UUID
	Metadata    map[string]any
}

// Log writes the entry through q. Pass the *db.Queries that db.InTx handed to
// the callback, never the pool-backed one, or the entry will not share the
// mutation's transaction.
func Log(ctx context.Context, q *db.Queries, e Entry) error {
	if _, ok := knownActions[e.Action]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}
	if err := checkMetadata(e.Metadata); err != nil {
		return err
	}

	raw := []byte(`{}`)
	if len(e.Metadata) > 0 {
		encoded, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("audit: encode metadata: %w", err)
		}
		raw = encoded
	}

	teamID, actorUserID, entityID := e.TeamID, e.ActorUserID, e.EntityID
	if err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		TeamID:      &teamID,
		ActorUserID: &actorUserID,
		Action:      string(e.Action),
		EntityType:  e.EntityType,
		EntityID:    &entityID,
		Metadata:    raw,
	}); err != nil {
		return fmt.Errorf("audit: insert entry: %w", err)
	}
	return nil
}

func checkMetadata(metadata map[string]any) error {
	for key := range metadata {
		lower := strings.ToLower(key)
		for _, forbidden := range forbiddenMetadataKeys {
			if lower == forbidden {
				return fmt.Errorf("%w: %q", ErrForbiddenMetadata, key)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/audit/ -v` Expected: PASS (or skip if Postgres is down).

- [ ] **Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat(api): add the audit log writer and action taxonomy"
```

---

## Task 6: The pagination envelope

**Files:**

- Create: `internal/api/page.go`
- Test: `internal/api/page_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `api.Page[T]` with fields `Items []T`, `Page int`, `PerPage int`, `TotalCount int`; `api.PageParams` with `Page`/`PerPage` query fields plus `Limit() int32`, `Offset() int32`, `Normalized() (page, perPage int)`; `api.NewPage[T](items []T, params PageParams, total int64) Page[T]`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/page_test.go`:

```go
package api_test

import (
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func TestPageParamsClampsAndDefaults(t *testing.T) {
	cases := []struct {
		name        string
		in          api.PageParams
		wantLimit   int32
		wantOffset  int32
		wantPage    int
		wantPerPage int
	}{
		{"defaults when zero", api.PageParams{}, 25, 0, 1, 25},
		{"second page", api.PageParams{Page: 2, PerPage: 10}, 10, 10, 2, 10},
		{"per_page is capped at 100", api.PageParams{Page: 1, PerPage: 5000}, 100, 0, 1, 100},
		{"negative page is treated as the first", api.PageParams{Page: -3, PerPage: 10}, 10, 0, 1, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantLimit, tc.in.Limit())
			require.Equal(t, tc.wantOffset, tc.in.Offset())

			page, perPage := tc.in.Normalized()
			require.Equal(t, tc.wantPage, page)
			require.Equal(t, tc.wantPerPage, perPage)
		})
	}
}

func TestNewPageReportsTheNormalisedParamsAndTotal(t *testing.T) {
	page := api.NewPage([]string{"a", "b"}, api.PageParams{Page: 0, PerPage: 0}, 7)

	require.Equal(t, []string{"a", "b"}, page.Items)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 25, page.PerPage)
	require.Equal(t, 7, page.TotalCount)
}

func TestNewPageNeverEmitsANullItemsArray(t *testing.T) {
	page := api.NewPage(nil, api.PageParams{}, 0)

	require.NotNil(t, page.Items,
		"a generated TypeScript client should get [], never null")
	require.Empty(t, page.Items)
}

// The generated TypeScript client in plan 4 inherits these schema names, so a
// mangled generic name would propagate into the frontend's types.
func TestGenericEnvelopeSchemaNamesAreReadable(t *testing.T) {
	name := huma.DefaultSchemaNamer(reflect.TypeOf(api.Page[api.Team]{}), "")

	require.Equal(t, "PageTeam", name)
}
```

`api.Team` is defined in Task 8. Until then this last test will not compile; write the test now, and expect the run in Step 2 to fail on that. If you are executing tasks strictly in order, temporarily assert against `api.Page[string]{}` (`"PageString"`) and switch it to `api.Page[api.Team]{}` in Task 8.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run 'Page' -v` Expected: FAIL — `undefined: api.PageParams`.

- [ ] **Step 3: Implement the envelope**

Create `internal/api/page.go`:

```go
package api

// Page is the envelope every list endpoint returns. Pagination metadata lives
// in the body, not in headers: Huma's value is typed response bodies, and a
// generated TypeScript client cannot see headers.
type Page[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalCount int `json:"total_count"`
}

const (
	defaultPerPage = 25
	maxPerPage     = 100
)

// PageParams is embedded by every list operation's input. Huma applies the
// default and range tags; the methods below normalise again so a value built
// directly in a test behaves identically to one parsed from a request.
type PageParams struct {
	Page    int `query:"page" default:"1" minimum:"1" doc:"1-based page number."`
	PerPage int `query:"per_page" default:"25" minimum:"1" maximum:"100" doc:"Items per page, capped at 100."`
}

// Normalized returns the effective page and per-page values.
func (p PageParams) Normalized() (page, perPage int) {
	page, perPage = p.Page, p.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	return page, perPage
}

// Limit is the SQL LIMIT for these params.
func (p PageParams) Limit() int32 {
	_, perPage := p.Normalized()
	return int32(perPage)
}

// Offset is the SQL OFFSET for these params.
func (p PageParams) Offset() int32 {
	page, perPage := p.Normalized()
	return int32((page - 1) * perPage)
}

// NewPage wraps a query's rows. total comes from the count(*) over () column
// the list queries project, so the items and the total are always consistent.
func NewPage[T any](items []T, params PageParams, total int64) Page[T] {
	page, perPage := params.Normalized()
	if items == nil {
		items = []T{}
	}
	return Page[T]{
		Items:      items,
		Page:       page,
		PerPage:    perPage,
		TotalCount: int(total),
	}
}
```

If Task 4's generated queries take `int64` for `Limit`/`Offset` rather than `int32`, change the two method signatures to `int64` and drop the conversions. Check `internal/db/team.sql.go` before assuming.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'Page' -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/page.go internal/api/page_test.go
git commit -m "feat(api): add the paginated list envelope"
```

---

## Task 7: The db-backed resolver, Deps wiring and `/v1/me` memberships

**Files:**

- Create: `internal/authz/resolver.go`
- Create: `internal/api/me.go`
- Create: `internal/api/tenancy_test.go` (shared helpers for tasks 7–14)
- Modify: `internal/api/api.go` (Deps gains `Pool` and `Admin`)
- Modify: `internal/api/v1.go` (install the resolver; move `/v1/me` out)

**Interfaces:**

- Consumes: `authz.Membership`, `authz.Resolver`, `authz.ErrNotMember`, `authz.ParseRole` (Tasks 2–3); `db.GetTeamMembership`, `db.ListMembershipsForUser` (Task 4).
- Produces:
  - `authz.NewQueryResolver(q *db.Queries) QueryResolver`, implementing `authz.Resolver`.
  - `api.Deps.Pool *pgxpool.Pool` and `api.Deps.Admin Inviter`.
  - `api.Inviter` interface: `InviteUser(ctx context.Context, email string, data map[string]any) (uuid.UUID, error)`.
  - `api.TeamMembership{TeamID uuid.UUID, Name string, Role string}` and the extended `api.MeOutput`.
  - `(d Deps) registerMe(api huma.API)`.

- [ ] **Step 1: Implement the resolver**

Create `internal/authz/resolver.go`:

```go
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// QueryResolver is the production Resolver: one indexed primary-key lookup
// against team_member per team-scoped request.
type QueryResolver struct {
	queries *db.Queries
}

func NewQueryResolver(queries *db.Queries) QueryResolver {
	return QueryResolver{queries: queries}
}

func (r QueryResolver) Membership(
	ctx context.Context, teamID, userID uuid.UUID,
) (Membership, error) {
	row, err := r.queries.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: teamID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotMember
	}
	if err != nil {
		return Membership{}, fmt.Errorf("authz: load membership: %w", err)
	}

	role, err := ParseRole(row.Role)
	if err != nil {
		// The check constraint should make this impossible; if it happens,
		// refusing is safer than guessing a role.
		return Membership{}, fmt.Errorf("authz: team %s member %s: %w", teamID, userID, err)
	}

	return Membership{TeamID: row.TeamID, UserID: row.UserID, Role: role}, nil
}
```

- [ ] **Step 2: Extend Deps**

In `internal/api/api.go`, add to the `Deps` struct:

```go
	// Pool backs db.InTx. Queries above is pool-backed too, but a transaction
	// needs the pool itself.
	Pool *pgxpool.Pool

	// Admin sends team invitation emails. Nil disables the invite branch of
	// POST /v1/teams/{team_id}/members, which then refuses unknown addresses.
	Admin Inviter
```

and declare the interface this package consumes:

```go
// Inviter is the slice of Supabase's Admin API this package needs. It is
// declared here, next to its consumer, so handler tests can fake it without
// touching HTTP.
type Inviter interface {
	InviteUser(ctx context.Context, email string, data map[string]any) (uuid.UUID, error)
}
```

Add the imports `"context"`, `"github.com/google/uuid"` and `"github.com/jackc/pgx/v5/pgxpool"`.

- [ ] **Step 3: Install the resolver in the `/v1` middleware**

In `internal/api/v1.go`, build the resolver once in `RegisterV1` and hand it to the middleware, so no allocation happens per request:

```go
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

	d.registerMe(api)
}
```

and in `authMiddleware`, replace the final `next(...)` line:

```go
		inner := auth.WithClaims(ctx.Context(), claims)
		if d.Queries != nil {
			inner = authz.WithResolver(inner, authz.NewQueryResolver(d.Queries))
		}
		next(huma.WithContext(ctx, inner))
```

Delete `MeOutput` from `v1.go` — it moves to `me.go` in the next step. Add the `authz` import.

- [ ] **Step 4: Write the failing `/v1/me` test**

Create `internal/api/tenancy_test.go` with the helpers the rest of the plan uses, plus the first test:

```go
package api_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/config"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// fakeInviter stands in for Supabase's Admin API. It records what it was asked
// to send so a test can assert an email was or was not triggered.
type fakeInviter struct {
	calls  []string
	userID uuid.UUID
	err    error
}

func (f *fakeInviter) InviteUser(
	_ context.Context, email string, _ map[string]any,
) (uuid.UUID, error) {
	f.calls = append(f.calls, email)
	if f.err != nil {
		return uuid.Nil, f.err
	}
	return f.userID, nil
}

type testUser struct {
	id    uuid.UUID
	email string
}

// tenancyFixture is one team with one member per role, a stranger who belongs
// to no team, a real JWKS-backed verifier and a wired /v1 router.
type tenancyFixture struct {
	deps     api.Deps
	pool     *pgxpool.Pool
	key      *ecdsa.PrivateKey
	router   http.Handler
	teamID   uuid.UUID
	members  map[authz.Role]testUser
	stranger testUser
	invites  *fakeInviter
}

// seedAuthUser inserts a Supabase auth user. The column list mirrors
// supabase/seed.sql — auth.users belongs to Supabase, and tests must not
// invent a different shape for it.
func seedAuthUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, email string) testUser {
	t.Helper()

	id := uuid.New()
	_, err := pool.Exec(ctx,
		`insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
		                         email_confirmed_at, created_at, updated_at)
		 values ($1, '00000000-0000-0000-0000-000000000000', 'authenticated',
		         'authenticated', $2, '', now(), now(), now())`, id, email)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, id)
	})

	return testUser{id: id, email: email}
}

func newTenancyFixture(t *testing.T) *tenancyFixture {
	t.Helper()
	ctx := context.Background()

	pool := testPool(t)
	redis := testCache(t)

	suffix := uuid.NewString()[:8]
	members := map[authz.Role]testUser{}
	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleEditor, authz.RoleAdmin, authz.RoleOwner} {
		members[role] = seedAuthUser(ctx, t, pool, role.String()+"-"+suffix+"@verein.test")
	}
	stranger := seedAuthUser(ctx, t, pool, "stranger-"+suffix+"@verein.test")

	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ($1) returning id`, "Verein "+suffix).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	for role, user := range members {
		_, err := pool.Exec(ctx,
			`insert into team_member (team_id, user_id, role) values ($1, $2, $3)`,
			teamID, user.id, role.String())
		require.NoError(t, err)
	}

	key, jwksURL := startAuthenticatedJWKSServer(t)
	verifier, err := auth.NewVerifier(ctx, jwksURL, meTestIssuer, meTestAudience)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	// The owner is the instance maintainer in these tests; every other user
	// must be refused by POST /v1/teams.
	cfg.MaintainerUserIDs = []uuid.UUID{members[authz.RoleOwner].id}
	cfg.InviteRateLimitPerHour = 20

	invites := &fakeInviter{userID: uuid.New()}

	f := &tenancyFixture{
		pool:     pool,
		key:      key,
		teamID:   teamID,
		members:  members,
		stranger: stranger,
		invites:  invites,
		deps: api.Deps{
			Config:   cfg,
			Queries:  db.New(pool),
			Pool:     pool,
			Cache:    redis,
			Verifier: verifier,
			Admin:    invites,
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}

	router := chi.NewRouter()
	f.deps.RegisterV1(humachi.New(router, api.NewHumaConfig()))
	f.router = router

	return f
}

// do issues a request to the /v1 surface as the given user. Pass an empty
// testUser to send it unauthenticated.
func (f *tenancyFixture) do(
	t *testing.T, as testUser, method, path string, body any,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if as.id != uuid.Nil {
		req.Header.Set("Authorization",
			"Bearer "+signMeToken(t, f.key, as.id.String(), as.email))
	}

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// decode unmarshals a successful JSON response body.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

func TestMeListsTheCallersTeamMemberships(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodGet, "/v1/me", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode[struct {
		UserID      uuid.UUID `json:"user_id"`
		Email       string    `json:"email"`
		Memberships []struct {
			TeamID uuid.UUID `json:"team_id"`
			Name   string    `json:"name"`
			Role   string    `json:"role"`
		} `json:"memberships"`
	}](t, rec)

	require.Equal(t, f.members[authz.RoleEditor].id, body.UserID)
	require.Len(t, body.Memberships, 1)
	require.Equal(t, f.teamID, body.Memberships[0].TeamID)
	require.Equal(t, "editor", body.Memberships[0].Role)
}

func TestMeReturnsAnEmptyMembershipListForANewUser(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/me", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"memberships":[]`,
		"a user with no teams must get [], not null")
}
```

Delete the `var _ = cache.Client{}` line if the `cache` import ends up used by `testCache` only — it exists so the file compiles either way; remove whichever is redundant.

- [ ] **Step 5: Run it to verify it fails**

Run: `go test ./internal/api/ -run 'TestMeLists|TestMeReturnsAnEmpty' -v` Expected: FAIL — `d.registerMe undefined` or a missing `memberships` field.

- [ ] **Step 6: Implement `/v1/me`**

Create `internal/api/me.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// TeamMembership is one entry in GET /v1/me — it drives the frontend's team
// switcher, which needs the team's name and the caller's role in it.
type TeamMembership struct {
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
	Role   string    `json:"role"`
}

// MeOutput is the body of GET /v1/me.
type MeOutput struct {
	Body struct {
		UserID      uuid.UUID        `json:"user_id"`
		Email       string           `json:"email"`
		Memberships []TeamMembership `json:"memberships"`
	}
}

func (d Deps) registerMe(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-me",
		Method:      http.MethodGet,
		Path:        "/v1/me",
		Summary:     "The authenticated user and their teams",
		Tags:        []string{"Session"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		claims, ok := UserFromContext(ctx)
		if !ok {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		rows, err := d.Queries.ListMembershipsForUser(ctx, claims.UserID)
		if err != nil {
			d.Log.Error("list memberships", "error", err)
			return nil, huma.Error500InternalServerError("could not load team memberships")
		}

		memberships := make([]TeamMembership, 0, len(rows))
		for _, row := range rows {
			memberships = append(memberships, TeamMembership{
				TeamID: row.TeamID,
				Name:   row.TeamName,
				Role:   row.Role,
			})
		}

		out := &MeOutput{}
		out.Body.UserID = claims.UserID
		out.Body.Email = claims.Email
		out.Body.Memberships = memberships
		return out, nil
	})
}
```

- [ ] **Step 7: Run the API suite**

Run: `go test ./internal/api/ -v` Expected: PASS, including plan 1's redirect, verify, router and `/v1/me` tests. The pre-existing `TestMeReturnsTheVerifiedClaimsForAValidBearerToken` signs a token for a random UUID that has no `auth.users` row; `ListMembershipsForUser` returns no rows for it, so it still passes with an empty membership list.

- [ ] **Step 8: Commit**

```bash
git add internal/authz/resolver.go internal/api/me.go internal/api/tenancy_test.go internal/api/api.go internal/api/v1.go
git commit -m "feat(api): resolve memberships and return them from /v1/me"
```

---

## Task 8: Team endpoints

**Files:**

- Create: `internal/api/teams.go`
- Test: `internal/api/teams_test.go`
- Modify: `internal/api/v1.go` (call `d.registerTeams(api)`)

**Interfaces:**

- Consumes: `authz.ViewerScope`, `authz.AdminScope` (Task 3); `db.InTx`, `db.CreateTeam`, `db.GetTeam`, `db.RenameTeam`, `db.ListTeamsForUser`, `db.InsertTeamMember` (Task 4); `audit.Log` and the team actions (Task 5); `Page[T]`, `PageParams`, `NewPage` (Task 6); `config.Config.IsMaintainer` (Task 1).
- Produces: `api.Team{ID uuid.UUID, Name string, CreatedAt time.Time, Role string}`; `(d Deps) registerTeams(api huma.API)`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/teams_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestCreateTeamIsRefusedForANonMaintainer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"team creation is maintainer-only; an admin of another team is still a stranger here")
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}

func TestCreateTeamMakesTheMaintainerTheOwnerAndAuditsIt(t *testing.T) {
	f := newTenancyFixture(t)
	maintainer := f.members[authz.RoleOwner]

	rec := f.do(t, maintainer, http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	created := decode[api.Team](t, rec)
	require.Equal(t, "Neuer Verein", created.Name)
	require.Equal(t, "owner", created.Role)

	t.Cleanup(func() {
		_, _ = f.pool.Exec(t.Context(), `delete from team where id = $1`, created.ID)
	})

	var role string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select role from team_member where team_id = $1 and user_id = $2`,
		created.ID, maintainer.id).Scan(&role))
	require.Equal(t, "owner", role)

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log where team_id = $1`, created.ID).Scan(&action))
	require.Equal(t, "team.created", action)
}

func TestCreateTeamRejectsAnEmptyName(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams",
		map[string]string{"name": ""})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListTeamsReturnsOnlyTheCallersTeams(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Team]](t, rec)
	require.Len(t, page.Items, 1)
	require.Equal(t, f.teamID, page.Items[0].ID)
	require.Equal(t, "viewer", page.Items[0].Role)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 25, page.PerPage)
}

func TestListTeamsIsEmptyForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/teams", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Team]](t, rec)
	require.Empty(t, page.Items)
	require.Zero(t, page.TotalCount)
}

func TestListTeamsRejectsAnOversizedPerPage(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams?per_page=1000", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"per_page is capped at 100 by the schema, so an oversized value is a validation error")
}

func TestGetTeamIsVisibleToAViewer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code)
	team := decode[api.Team](t, rec)
	require.Equal(t, f.teamID, team.ID)
	require.WithinDuration(t, time.Now(), team.CreatedAt, time.Hour)
}

func TestGetTeamHidesItFromAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to distinguish an existing team from a missing one")
}

func TestGetTeamRejectsAnUnauthenticatedCaller(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, testUser{}, http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRenameTeamRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusForbidden, rec.Code)

	var name string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select name from team where id = $1`, f.teamID).Scan(&name))
	require.NotEqual(t, "Umbenannt", name, "a refused request must change nothing")
}

func TestRenameTeamAuditsTheOldAndNewName(t *testing.T) {
	f := newTenancyFixture(t)

	var before string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select name from team where id = $1`, f.teamID).Scan(&before))

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "Umbenannt", decode[api.Team](t, rec).Name)

	var action string
	var metadata []byte
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action, metadata from audit_log where team_id = $1 and action = 'team.renamed'`,
		f.teamID).Scan(&action, &metadata))
	require.Contains(t, string(metadata), before)
	require.Contains(t, string(metadata), "Umbenannt")
}

func TestRenameTeamIs404ForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetTeamRejectsAMalformedTeamID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.NotEqual(t, uuid.Nil.String(), rec.Body.String())
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run 'Team' -v` Expected: FAIL — `undefined: api.Team`.

- [ ] **Step 3: Implement the team handlers**

Create `internal/api/teams.go`:

```go
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Team is the API's representation of a team. Role is the *caller's* role in
// it, which is what the frontend needs to decide which controls to render.
type Team struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Role      string    `json:"role"`
}

type CreateTeamInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200" doc:"The Verein's display name."`
	}
}

type TeamOutput struct {
	Body Team
}

type ListTeamsInput struct {
	PageParams
}

type ListTeamsOutput struct {
	Body Page[Team]
}

type GetTeamInput struct {
	authz.ViewerScope
}

type UpdateTeamInput struct {
	authz.AdminScope
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200"`
	}
}

func (d Deps) registerTeams(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-team",
		Method:        http.MethodPost,
		Path:          "/v1/teams",
		Summary:       "Create a team",
		Description:   "Restricted to the instance maintainers. A Verein asks the maintainer, who creates the team and invites its first owner.",
		Tags:          []string{"Teams"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createTeam)

	huma.Register(api, huma.Operation{
		OperationID: "list-teams",
		Method:      http.MethodGet,
		Path:        "/v1/teams",
		Summary:     "List the teams the caller belongs to",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listTeams)

	huma.Register(api, huma.Operation{
		OperationID: "get-team",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}",
		Summary:     "Get a team",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.getTeam)

	huma.Register(api, huma.Operation{
		OperationID: "update-team",
		Method:      http.MethodPatch,
		Path:        "/v1/teams/{team_id}",
		Summary:     "Rename a team",
		Tags:        []string{"Teams"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateTeam)
}

func (d Deps) createTeam(ctx context.Context, in *CreateTeamInput) (*TeamOutput, error) {
	claims, ok := UserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !d.Config.IsMaintainer(claims.UserID) {
		return nil, huma.Error403Forbidden("team creation is limited to the instance maintainers")
	}

	var created db.CreateTeamRow
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		team, err := q.CreateTeam(ctx, in.Body.Name)
		if err != nil {
			return err
		}
		created = team

		// The timestamp is unused here; the response reports the team, not the
		// membership.
		if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{
			TeamID: team.ID,
			UserID: claims.UserID,
			Role:   authz.RoleOwner.String(),
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      team.ID,
			ActorUserID: claims.UserID,
			Action:      audit.ActionTeamCreated,
			EntityType:  audit.EntityTeam,
			EntityID:    team.ID,
			Metadata:    map[string]any{"name": team.Name},
		})
	})
	if err != nil {
		d.Log.Error("create team", "error", err)
		return nil, huma.Error500InternalServerError("could not create the team")
	}

	return &TeamOutput{Body: Team{
		ID:        created.ID,
		Name:      created.Name,
		CreatedAt: created.CreatedAt,
		Role:      authz.RoleOwner.String(),
	}}, nil
}

func (d Deps) listTeams(ctx context.Context, in *ListTeamsInput) (*ListTeamsOutput, error) {
	claims, ok := UserFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("not authenticated")
	}

	rows, err := d.Queries.ListTeamsForUser(ctx, db.ListTeamsForUserParams{
		UserID: claims.UserID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list teams", "error", err)
		return nil, huma.Error500InternalServerError("could not list teams")
	}

	items := make([]Team, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Team{
			ID:        row.ID,
			Name:      row.Name,
			CreatedAt: row.CreatedAt,
			Role:      row.Role,
		})
	}

	return &ListTeamsOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) getTeam(ctx context.Context, in *GetTeamInput) (*TeamOutput, error) {
	team, err := d.Queries.GetTeam(ctx, in.TeamID)
	if err != nil {
		d.Log.Error("get team", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not load the team")
	}

	return &TeamOutput{Body: Team{
		ID:        team.ID,
		Name:      team.Name,
		CreatedAt: team.CreatedAt,
		Role:      in.Member().Role.String(),
	}}, nil
}

func (d Deps) updateTeam(ctx context.Context, in *UpdateTeamInput) (*TeamOutput, error) {
	member := in.Member()

	var renamed db.RenameTeamRow
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		before, err := q.GetTeam(ctx, in.TeamID)
		if err != nil {
			return err
		}

		after, err := q.RenameTeam(ctx, db.RenameTeamParams{ID: in.TeamID, Name: in.Body.Name})
		if err != nil {
			return err
		}
		renamed = after

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      in.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTeamRenamed,
			EntityType:  audit.EntityTeam,
			EntityID:    in.TeamID,
			Metadata:    map[string]any{"from": before.Name, "to": after.Name},
		})
	})
	if err != nil {
		d.Log.Error("rename team", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not rename the team")
	}

	return &TeamOutput{Body: Team{
		ID:        renamed.ID,
		Name:      renamed.Name,
		CreatedAt: renamed.CreatedAt,
		Role:      member.Role.String(),
	}}, nil
}
```

Register it: add `d.registerTeams(api)` to `RegisterV1`, after `d.registerMe(api)`.

The generated row types may be named `db.CreateTeamRow`/`db.RenameTeamRow` or just `db.Team` depending on whether sqlc considers the projection identical to the model. Check `internal/db/team.sql.go` and use whatever it generated.

- [ ] **Step 4: Run the team tests**

Run: `go test ./internal/api/ -run 'Team' -v` Expected: PASS.

- [ ] **Step 5: Switch the schema-name assertion to the real type**

In `internal/api/page_test.go`, change `TestGenericEnvelopeSchemaNamesAreReadable` to use `api.Page[api.Team]{}` and expect `"PageTeam"` (if you used the `string` placeholder in Task 6).

Run: `go test ./internal/api/ -run 'Page' -v` Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/teams.go internal/api/teams_test.go internal/api/v1.go internal/api/page_test.go
git commit -m "feat(api): add team endpoints"
```

---

## Task 9: Member listing

**Files:**

- Create: `internal/api/members.go`
- Test: `internal/api/members_test.go`
- Modify: `internal/api/v1.go` (call `d.registerMembers(api)`)

**Interfaces:**

- Consumes: `authz.ViewerScope` (Task 3); `db.ListTeamMembers` (Task 4); `Page[T]`, `PageParams`, `NewPage` (Task 6).
- Produces: `api.Member{UserID uuid.UUID, Email string, Role string, CreatedAt time.Time}`; `(d Deps) registerMembers(api huma.API)` — tasks 11 and 12 add operations to this same function.

- [ ] **Step 1: Write the failing test**

Create `internal/api/members_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestListMembersIsVisibleToAViewerAndCarriesEmails(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.Page[api.Member]](t, rec)
	require.Equal(t, 4, page.TotalCount, "the fixture seeds one member per role")

	byEmail := map[string]string{}
	for _, member := range page.Items {
		byEmail[member.Email] = member.Role
	}
	require.Equal(t, "owner", byEmail[f.members[authz.RoleOwner].email])
	require.Equal(t, "viewer", byEmail[f.members[authz.RoleViewer].email])
}

func TestListMembersPaginates(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members?page=2&per_page=2", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Member]](t, rec)
	require.Len(t, page.Items, 2)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 2, page.PerPage)
	require.Equal(t, 4, page.TotalCount)
}

func TestListMembersHidesTheTeamFromAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run 'ListMembers' -v` Expected: FAIL — `undefined: api.Member`.

- [ ] **Step 3: Implement the listing**

Create `internal/api/members.go`:

```go
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Member is a team membership as the API exposes it. Email comes from
// auth.users; there is no display name, because public.profile is an open
// question, not a decision.
type Member struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type ListMembersInput struct {
	authz.ViewerScope
	PageParams
}

type ListMembersOutput struct {
	Body Page[Member]
}

func (d Deps) registerMembers(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-team-members",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/members",
		Summary:     "List a team's members",
		Tags:        []string{"Members"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listMembers)
}

func (d Deps) listMembers(ctx context.Context, in *ListMembersInput) (*ListMembersOutput, error) {
	rows, err := d.Queries.ListTeamMembers(ctx, db.ListTeamMembersParams{
		TeamID: in.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list team members", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not list the team's members")
	}

	items := make([]Member, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Member{
			UserID:    row.UserID,
			Email:     derefString(row.Email),
			Role:      row.Role,
			CreatedAt: row.CreatedAt,
		})
	}

	return &ListMembersOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

// derefString flattens a nullable column. auth.users.email is nullable because
// Supabase allows phone-only accounts.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

Register it: add `d.registerMembers(api)` to `RegisterV1`.

- [ ] **Step 4: Run the member tests**

Run: `go test ./internal/api/ -run 'ListMembers' -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/members.go internal/api/members_test.go internal/api/v1.go
git commit -m "feat(api): add team member listing"
```

---

## Task 10: The Supabase Admin API client

**Files:**

- Create: `internal/supabase/admin.go`
- Test: `internal/supabase/admin_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: `supabase.NewClient(baseURL, serviceRoleKey string) (*Client, error)`; `(*Client) InviteUser(ctx context.Context, email string, data map[string]any) (uuid.UUID, error)`, satisfying `api.Inviter`; `supabase.ErrUserExists`; `supabase.ErrNotConfigured`.

- [ ] **Step 1: Write the failing test**

Create `internal/supabase/admin_test.go`:

```go
package supabase_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/supabase"
)

func TestInviteUserPostsToTheAuthInviteEndpoint(t *testing.T) {
	invitedID := uuid.New()

	var gotPath, gotAPIKey, gotAuth string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("apikey")
		gotAuth = r.Header.Get("Authorization")

		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": invitedID.String()})
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "service-role-key")
	require.NoError(t, err)

	id, err := client.InviteUser(context.Background(), "neu@verein.test",
		map[string]any{"team_id": "abc", "role": "editor"})

	require.NoError(t, err)
	require.Equal(t, invitedID, id)
	require.Equal(t, "/invite", gotPath)
	require.Equal(t, "service-role-key", gotAPIKey)
	require.Equal(t, "Bearer service-role-key", gotAuth)
	require.Equal(t, "neu@verein.test", gotBody["email"])
	require.Equal(t, map[string]any{"team_id": "abc", "role": "editor"}, gotBody["data"])
}

func TestInviteUserReportsAnExistingAddressDistinctly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       422,
			"error_code": "email_exists",
			"msg":        "A user with this email address has already been registered",
		})
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "alt@verein.test", nil)

	require.ErrorIs(t, err, supabase.ErrUserExists,
		"the caller falls back to the direct-add branch for an address that already has an account")
}

func TestInviteUserSurfacesAServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "neu@verein.test", nil)

	require.Error(t, err)
	require.NotErrorIs(t, err, supabase.ErrUserExists)
}

func TestNewClientRefusesAnIncompleteConfiguration(t *testing.T) {
	_, err := supabase.NewClient("", "key")
	require.ErrorIs(t, err, supabase.ErrNotConfigured)

	_, err = supabase.NewClient("https://project.supabase.co/auth/v1", "")
	require.ErrorIs(t, err, supabase.ErrNotConfigured)
}

func TestInviteUserDoesNotLeakTheServiceRoleKeyIntoErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "super-secret-service-role-key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "neu@verein.test", nil)

	require.Error(t, err)
	require.NotContains(t, err.Error(), "super-secret-service-role-key",
		"the service-role key bypasses every database policy; it must never reach a log line")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/supabase/ -v` Expected: FAIL — `no Go files in .../internal/supabase`.

- [ ] **Step 3: Implement the client**

Create `internal/supabase/admin.go`:

```go
// Package supabase talks to Supabase's Admin API. It exists for exactly one
// call — sending a team invitation email — because that is the only thing this
// backend cannot do in SQL. Identity reads go through internal/db instead.
package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrUserExists means the address already has an account, so the caller
	// should add the membership directly instead of inviting.
	ErrUserExists = errors.New("supabase: a user with that email already exists")

	// ErrNotConfigured means the base URL or the service-role key is missing.
	ErrNotConfigured = errors.New("supabase: admin api is not configured")
)

// Client is a minimal Admin API client.
type Client struct {
	baseURL        string
	serviceRoleKey string
	http           *http.Client
}

// NewClient builds a client for {baseURL}/invite. baseURL is the project's
// auth base — the same value as the JWT issuer.
func NewClient(baseURL, serviceRoleKey string) (*Client, error) {
	if baseURL == "" || serviceRoleKey == "" {
		return nil, ErrNotConfigured
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		serviceRoleKey: serviceRoleKey,
		http:           &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// InviteUser creates an unconfirmed auth.users row and sends the invitation
// email, returning the new user's ID. data becomes the invite's user metadata,
// which is how the team and role travel with the invitation.
func (c *Client) InviteUser(
	ctx context.Context, email string, data map[string]any,
) (uuid.UUID, error) {
	payload := map[string]any{"email": email}
	if len(data) > 0 {
		payload["data"] = data
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: encode invite: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/invite",
		bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: build invite request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Supabase wants the service-role key in both places.
	req.Header.Set("apikey", c.serviceRoleKey)
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)

	res, err := c.http.Do(req)
	if err != nil {
		// %w on the transport error is safe: net/http redacts nothing, but the
		// key is a header, never part of the URL.
		return uuid.Nil, fmt.Errorf("supabase: send invite: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Cap the read: an upstream that streams garbage must not exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: read invite response: %w", err)
	}

	if res.StatusCode >= http.StatusBadRequest {
		if isExistingUser(raw) {
			return uuid.Nil, ErrUserExists
		}
		// The body is echoed, the key is not — errors reach logs and Sentry.
		return uuid.Nil, fmt.Errorf("supabase: invite failed with status %d: %s",
			res.StatusCode, truncate(string(raw), 200))
	}

	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return uuid.Nil, fmt.Errorf("supabase: decode invite response: %w", err)
	}

	id, err := uuid.Parse(decoded.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("supabase: invite returned an unusable user id: %w", err)
	}
	return id, nil
}

// isExistingUser recognises the "already registered" answer. Supabase has
// changed both the status code and the wording across versions, so match on
// the stable error_code first and the message as a fallback.
func isExistingUser(body []byte) bool {
	var decoded struct {
		ErrorCode string `json:"error_code"`
		Msg       string `json:"msg"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		if decoded.ErrorCode == "email_exists" || decoded.ErrorCode == "user_already_exists" {
			return true
		}
		for _, msg := range []string{decoded.Msg, decoded.Message} {
			if strings.Contains(strings.ToLower(msg), "already been registered") ||
				strings.Contains(strings.ToLower(msg), "already exists") {
				return true
			}
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/supabase/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/supabase/
git commit -m "feat(api): add the Supabase invite client"
```

---

## Task 11: Adding and inviting members

**Files:**

- Modify: `internal/api/members.go`
- Test: `internal/api/members_test.go`

**Interfaces:**

- Consumes: `authz.AdminScope`, `authz.ParseRole` (Tasks 2–3); `db.GetUserIDByEmail`, `db.GetTeamMembership`, `db.InsertTeamMember` (Task 4); `audit.ActionMemberAdded`, `audit.ActionMemberInvited` (Task 5); `Inviter` (Task 7); `supabase.ErrUserExists` (Task 10); `cache.Client.Allow` from plan 1.
- Produces: `(d Deps) addMember(...)` registered as `add-team-member`, returning 201 with a `Member` body.

- [ ] **Step 1: Add the failing tests**

Append to `internal/api/members_test.go`:

```go
func TestAddMemberRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "neu@verein.test", "role": "editor"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, f.invites.calls, "a refused request must not send an email")
}

func TestAddMemberAddsAnExistingAccountWithoutSendingEmail(t *testing.T) {
	f := newTenancyFixture(t)
	existing := f.stranger

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": existing.email, "role": "editor"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	added := decode[api.Member](t, rec)
	require.Equal(t, existing.id, added.UserID)
	require.Equal(t, "editor", added.Role)
	require.Empty(t, f.invites.calls,
		"an address that already has an account gets a membership, not an invitation")

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log
		 where team_id = $1 and entity_id = $2`, f.teamID, existing.id).Scan(&action))
	require.Equal(t, "team_member.added", action)
}

func TestAddMemberInvitesAnUnknownAddress(t *testing.T) {
	f := newTenancyFixture(t)
	// The fake inviter reports the ID Supabase would have created; the row has
	// to exist for the foreign key, so seed it under that ID.
	invited := seedAuthUserWithID(t.Context(), t, f.pool, f.invites.userID, "invited@verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "invited@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, []string{"invited@verein.test"}, f.invites.calls)

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log where team_id = $1 and entity_id = $2`,
		f.teamID, invited.id).Scan(&action))
	require.Equal(t, "team_member.invited", action)
}

func TestAddMemberRejectsSomeoneAlreadyInTheTeam(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.members[authz.RoleViewer].email, "role": "editor"})

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Empty(t, f.invites.calls)
}

func TestAddMemberRefusesAnAdminGrantingOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "owner"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"granting the owner role requires owner")
}

func TestAddMemberLetsAnOwnerGrantOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "owner"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}

func TestAddMemberRejectsAnUnknownRole(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "superuser"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestAddMemberIsRateLimited(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.InviteRateLimitPerHour = 1
	f.rebuildRouter()

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "viewer"})
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "another@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusTooManyRequests, second.Code,
			"invitations spend real email quota, so the endpoint is capped per team")
}

func TestAddMemberSurfacesAnInviteFailureAs502(t *testing.T) {
	f := newTenancyFixture(t)
	f.invites.err = errors.New("supabase is unreachable")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "unknown@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusBadGateway, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from audit_log where team_id = $1`, f.teamID).Scan(&count))
	require.Zero(t, count, "a failed invitation must leave no membership and no audit entry")
}
```

Add `"errors"` to the test file's imports.

- [ ] **Step 2: Add the two helpers the tests need**

In `internal/api/tenancy_test.go`, add:

```go
// seedAuthUserWithID seeds an auth user under a caller-chosen ID, for the
// invite path where the fake inviter decides the new user's ID.
func seedAuthUserWithID(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool, id uuid.UUID, email string,
) testUser {
	t.Helper()

	_, err := pool.Exec(ctx,
		`insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
		                         email_confirmed_at, created_at, updated_at)
		 values ($1, '00000000-0000-0000-0000-000000000000', 'authenticated',
		         'authenticated', $2, '', now(), now(), now())`, id, email)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from auth.users where id = $1`, id)
	})

	return testUser{id: id, email: email}
}

// rebuildRouter re-registers /v1 after a test mutated f.deps.Config.
func (f *tenancyFixture) rebuildRouter() {
	router := chi.NewRouter()
	f.deps.RegisterV1(humachi.New(router, api.NewHumaConfig()))
	f.router = router
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/api/ -run 'AddMember' -v` Expected: FAIL — 404 from chi, because the route does not exist yet.

- [ ] **Step 4: Implement the handler**

Add to `internal/api/members.go`:

```go
type AddMemberInput struct {
	authz.AdminScope
	Body struct {
		Email string `json:"email" format:"email" doc:"The person's email address."`
		Role  string `json:"role" enum:"viewer,editor,admin,owner" doc:"The role to grant."`
	}
}

type MemberOutput struct {
	Body Member
}
```

and register it inside `registerMembers`:

```go
	huma.Register(api, huma.Operation{
		OperationID:   "add-team-member",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/members",
		Summary:       "Invite or add a member",
		Description:   "An address without an account is invited by email; an address that already has one is added directly and sees the team on next login.",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.addMember)
```

and the handler:

```go
func (d Deps) addMember(ctx context.Context, in *AddMemberInput) (*MemberOutput, error) {
	actor := in.Member()

	role, err := authz.ParseRole(in.Body.Role)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("unknown role")
	}
	// Only an owner may create another owner. The scope has already proved the
	// caller is at least an admin.
	if role == authz.RoleOwner && !actor.Role.AtLeast(authz.RoleOwner) {
		return nil, huma.Error403Forbidden("granting the owner role requires the owner role")
	}

	// Invitations spend real email quota, so cap them per team.
	allowed, _, err := d.Cache.Allow(ctx, "rl:invite:"+in.TeamID.String(),
		d.Config.InviteRateLimitPerHour, time.Hour)
	if err != nil {
		d.Log.Error("invite rate limit", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not check the invitation rate limit")
	}
	if !allowed {
		return nil, huma.Error429TooManyRequests("too many invitations for this team; try again later")
	}

	userID, invited, err := d.resolveInvitee(ctx, in)
	if err != nil {
		return nil, err
	}

	if _, err := d.Queries.GetTeamMembership(ctx, db.GetTeamMembershipParams{
		TeamID: in.TeamID,
		UserID: userID,
	}); err == nil {
		return nil, huma.Error409Conflict("that person is already a member of this team")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		d.Log.Error("check existing membership", "error", err)
		return nil, huma.Error500InternalServerError("could not add the member")
	}

	action := audit.ActionMemberAdded
	if invited {
		action = audit.ActionMemberInvited
	}

	var createdAt time.Time
	if err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		created, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{
			TeamID: in.TeamID,
			UserID: userID,
			Role:   role.String(),
		})
		if err != nil {
			return err
		}
		createdAt = created

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      in.TeamID,
			ActorUserID: actor.UserID,
			Action:      action,
			EntityType:  audit.EntityTeamMember,
			EntityID:    userID,
			Metadata:    map[string]any{"email": in.Body.Email, "role": role.String()},
		})
	}); err != nil {
		d.Log.Error("add team member", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not add the member")
	}

	return &MemberOutput{Body: Member{
		UserID:    userID,
		Email:     in.Body.Email,
		Role:      role.String(),
		CreatedAt: createdAt,
	}}, nil
}

// resolveInvitee returns the user ID to add and whether an invitation email
// was sent. The invite is deliberately performed BEFORE the transaction: an
// email cannot be rolled back, so a failure here must leave no membership.
func (d Deps) resolveInvitee(ctx context.Context, in *AddMemberInput) (uuid.UUID, bool, error) {
	userID, err := d.Queries.GetUserIDByEmail(ctx, in.Body.Email)
	switch {
	case err == nil:
		return userID, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		d.Log.Error("look up invitee", "error", err)
		return uuid.Nil, false, huma.Error500InternalServerError("could not look up that address")
	}

	if d.Admin == nil {
		return uuid.Nil, false, huma.Error503ServiceUnavailable(
			"invitations are not configured on this instance")
	}

	invitedID, err := d.Admin.InviteUser(ctx, in.Body.Email, map[string]any{
		"team_id": in.TeamID.String(),
		"role":    in.Body.Role,
	})
	switch {
	case errors.Is(err, supabase.ErrUserExists):
		// Raced with another signup, or Supabase knows an account the SQL
		// lookup missed. Re-read and fall through to the direct-add path.
		existing, lookupErr := d.Queries.GetUserIDByEmail(ctx, in.Body.Email)
		if lookupErr != nil {
			d.Log.Error("invitee exists but could not be looked up", "error", lookupErr)
			return uuid.Nil, false, huma.Error502BadGateway("could not resolve that address")
		}
		return existing, false, nil
	case err != nil:
		d.Log.Error("send invitation", "error", err, "team_id", in.TeamID)
		return uuid.Nil, false, huma.Error502BadGateway("could not send the invitation")
	}

	return invitedID, true, nil
}
```

Add the imports `"errors"`, `"time"`, `"github.com/jackc/pgx/v5"`, `"github.com/mheob/kurze-url/apps/api/internal/audit"` and `"github.com/mheob/kurze-url/apps/api/internal/supabase"`.

- [ ] **Step 5: Confirm `InsertTeamMember` returns its timestamp**

Task 4 declared the query as `:one … returning created_at`, so `createdAt` above compiles. If it was written as `:exec`, change it now in `internal/db/queries/team.sql`, run `sqlc generate`, and update the call in `createTeam` (`internal/api/teams.go`) to `if _, err := q.InsertTeamMember(...)`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'Member|Team' -v` Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api/members.go internal/api/members_test.go internal/api/teams.go internal/api/tenancy_test.go internal/db/
git commit -m "feat(api): add and invite team members"
```

---

## Task 12: Role changes, removal and the last-owner invariant

**Files:**

- Modify: `internal/api/members.go`
- Test: `internal/api/members_test.go`

**Interfaces:**

- Consumes: `db.LockTeamOwners`, `db.UpdateTeamMemberRole`, `db.DeleteTeamMember`, `db.GetTeamMembership` (Task 4); `audit.ActionMemberRoleChanged`, `audit.ActionMemberRemoved` (Task 5).
- Produces: `(d Deps) updateMember(...)` and `(d Deps) removeMember(...)`, registered as `update-team-member` and `remove-team-member`, both returning 204.

- [ ] **Step 1: Add the failing tests**

Append to `internal/api/members_test.go`:

```go
func memberPath(f *tenancyFixture, user testUser) string {
	return "/v1/teams/" + f.teamID.String() + "/members/" + user.id.String()
}

func TestUpdateMemberRoleRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		memberPath(f, f.members[authz.RoleViewer]), map[string]string{"role": "admin"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUpdateMemberRolePromotesAndAudits(t *testing.T) {
	f := newTenancyFixture(t)
	target := f.members[authz.RoleViewer]

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		memberPath(f, target), map[string]string{"role": "editor"})

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	var role string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select role from team_member where team_id = $1 and user_id = $2`,
		f.teamID, target.id).Scan(&role))
	require.Equal(t, "editor", role)

	var metadata []byte
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select metadata from audit_log
		 where team_id = $1 and action = 'team_member.role_changed'`, f.teamID).Scan(&metadata))
	require.Contains(t, string(metadata), "viewer")
	require.Contains(t, string(metadata), "editor")
}

func TestUpdateMemberRoleRefusesAnAdminGrantingOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		memberPath(f, f.members[authz.RoleEditor]), map[string]string{"role": "owner"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUpdateMemberRoleRefusesAnAdminDemotingAnOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		memberPath(f, f.members[authz.RoleOwner]), map[string]string{"role": "admin"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"changing an owner's role requires owner")
}

func TestUpdateMemberRoleRefusesDemotingTheLastOwner(t *testing.T) {
	f := newTenancyFixture(t)
	owner := f.members[authz.RoleOwner]

	rec := f.do(t, owner, http.MethodPatch,
		memberPath(f, owner), map[string]string{"role": "admin"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"a team must always have at least one owner")

	var role string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select role from team_member where team_id = $1 and user_id = $2`,
		f.teamID, owner.id).Scan(&role))
	require.Equal(t, "owner", role)
}

func TestUpdateMemberRoleAllowsDemotingAnOwnerWhenAnotherRemains(t *testing.T) {
	f := newTenancyFixture(t)
	owner := f.members[authz.RoleOwner]
	second := f.stranger

	_, err := f.pool.Exec(t.Context(),
		`insert into team_member (team_id, user_id, role) values ($1, $2, 'owner')`,
		f.teamID, second.id)
	require.NoError(t, err)

	rec := f.do(t, owner, http.MethodPatch,
		memberPath(f, owner), map[string]string{"role": "admin"})

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
}

func TestUpdateMemberRoleIs404ForANonMemberTarget(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		memberPath(f, f.stranger), map[string]string{"role": "editor"})

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRemoveMemberRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		memberPath(f, f.members[authz.RoleViewer]), nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRemoveMemberDeletesAndAudits(t *testing.T) {
	f := newTenancyFixture(t)
	target := f.members[authz.RoleViewer]

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete, memberPath(f, target), nil)

	require.Equal(t, http.StatusNoContent, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from team_member where team_id = $1 and user_id = $2`,
		f.teamID, target.id).Scan(&count))
	require.Zero(t, count)

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log where team_id = $1 and entity_id = $2`,
		f.teamID, target.id).Scan(&action))
	require.Equal(t, "team_member.removed", action)
}

func TestRemoveMemberRefusesAnAdminRemovingAnOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		memberPath(f, f.members[authz.RoleOwner]), nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRemoveMemberRefusesRemovingTheLastOwner(t *testing.T) {
	f := newTenancyFixture(t)
	owner := f.members[authz.RoleOwner]

	rec := f.do(t, owner, http.MethodDelete, memberPath(f, owner), nil)

	require.Equal(t, http.StatusForbidden, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from team_member where team_id = $1 and user_id = $2`,
		f.teamID, owner.id).Scan(&count))
	require.Equal(t, 1, count)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/api/ -run 'UpdateMember|RemoveMember' -v` Expected: FAIL — the routes are not registered.

- [ ] **Step 3: Implement both handlers**

Add to `internal/api/members.go`:

```go
type UpdateMemberInput struct {
	authz.AdminScope
	UserID uuid.UUID `path:"user_id" doc:"The member to change."`
	Body   struct {
		Role string `json:"role" enum:"viewer,editor,admin,owner"`
	}
}

type RemoveMemberInput struct {
	authz.AdminScope
	UserID uuid.UUID `path:"user_id" doc:"The member to remove."`
}
```

Register both inside `registerMembers`:

```go
	huma.Register(api, huma.Operation{
		OperationID:   "update-team-member",
		Method:        http.MethodPatch,
		Path:          "/v1/teams/{team_id}/members/{user_id}",
		Summary:       "Change a member's role",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.updateMember)

	huma.Register(api, huma.Operation{
		OperationID:   "remove-team-member",
		Method:        http.MethodDelete,
		Path:          "/v1/teams/{team_id}/members/{user_id}",
		Summary:       "Remove a member",
		Tags:          []string{"Members"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.removeMember)
```

and the handlers:

```go
func (d Deps) updateMember(ctx context.Context, in *UpdateMemberInput) (*struct{}, error) {
	actor := in.Member()

	newRole, err := authz.ParseRole(in.Body.Role)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("unknown role")
	}

	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		current, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
			TeamID: in.TeamID,
			UserID: in.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return huma.Error404NotFound("that person is not a member of this team")
		}
		if err != nil {
			return err
		}

		currentRole, err := authz.ParseRole(current.Role)
		if err != nil {
			return err
		}

		// Anything involving the owner role — granting it or taking it away —
		// is an owner's decision, not an admin's.
		if (currentRole == authz.RoleOwner || newRole == authz.RoleOwner) &&
			!actor.Role.AtLeast(authz.RoleOwner) {
			return huma.Error403Forbidden("changing the owner role requires the owner role")
		}

		if currentRole == authz.RoleOwner && newRole != authz.RoleOwner {
			if err := d.refuseLastOwner(ctx, q, in.TeamID); err != nil {
				return err
			}
		}

		if currentRole == newRole {
			return nil // Nothing changed; do not write a misleading audit entry.
		}

		if err := q.UpdateTeamMemberRole(ctx, db.UpdateTeamMemberRoleParams{
			TeamID: in.TeamID,
			UserID: in.UserID,
			Role:   newRole.String(),
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      in.TeamID,
			ActorUserID: actor.UserID,
			Action:      audit.ActionMemberRoleChanged,
			EntityType:  audit.EntityTeamMember,
			EntityID:    in.UserID,
			Metadata:    map[string]any{"from": currentRole.String(), "to": newRole.String()},
		})
	})
	if err != nil {
		return nil, d.mutationError(err, "could not change the member's role",
			"update team member role", in.TeamID)
	}

	return nil, nil
}

func (d Deps) removeMember(ctx context.Context, in *RemoveMemberInput) (*struct{}, error) {
	actor := in.Member()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		current, err := q.GetTeamMembership(ctx, db.GetTeamMembershipParams{
			TeamID: in.TeamID,
			UserID: in.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return huma.Error404NotFound("that person is not a member of this team")
		}
		if err != nil {
			return err
		}

		currentRole, err := authz.ParseRole(current.Role)
		if err != nil {
			return err
		}

		if currentRole == authz.RoleOwner {
			if !actor.Role.AtLeast(authz.RoleOwner) {
				return huma.Error403Forbidden("removing an owner requires the owner role")
			}
			if err := d.refuseLastOwner(ctx, q, in.TeamID); err != nil {
				return err
			}
		}

		if err := q.DeleteTeamMember(ctx, db.DeleteTeamMemberParams{
			TeamID: in.TeamID,
			UserID: in.UserID,
		}); err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      in.TeamID,
			ActorUserID: actor.UserID,
			Action:      audit.ActionMemberRemoved,
			EntityType:  audit.EntityTeamMember,
			EntityID:    in.UserID,
			Metadata:    map[string]any{"role": currentRole.String()},
		})
	})
	if err != nil {
		return nil, d.mutationError(err, "could not remove the member",
			"remove team member", in.TeamID)
	}

	return nil, nil
}

// refuseLastOwner locks the team's owner rows and refuses if only one is left.
// The lock is what makes this safe: without it, two concurrent demotions both
// read "two owners" and both succeed, leaving the team ownerless.
func (d Deps) refuseLastOwner(ctx context.Context, q *db.Queries, teamID uuid.UUID) error {
	owners, err := q.LockTeamOwners(ctx, teamID)
	if err != nil {
		return err
	}
	if len(owners) <= 1 {
		return huma.Error403Forbidden("a team must always have at least one owner")
	}
	return nil
}

// mutationError passes a deliberate huma status error through unchanged and
// turns anything else into a logged 500, so a handler's InTx callback can
// return either kind.
func (d Deps) mutationError(err error, message, operation string, teamID uuid.UUID) error {
	var status huma.StatusError
	if errors.As(err, &status) {
		return err
	}
	d.Log.Error(operation, "error", err, "team_id", teamID)
	return huma.Error500InternalServerError(message)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'UpdateMember|RemoveMember' -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/members.go internal/api/members_test.go
git commit -m "feat(api): change and remove team members safely"
```

---

## Task 13: The audit-log endpoint

**Files:**

- Create: `internal/api/auditlog.go`
- Test: `internal/api/auditlog_test.go`
- Modify: `internal/api/v1.go` (call `d.registerAuditLog(api)`)

**Interfaces:**

- Consumes: `authz.AdminScope` (Task 3); `db.ListAuditLog` (Task 4); `Page[T]`, `PageParams` (Task 6).
- Produces: `api.AuditEntry`; `(d Deps) registerAuditLog(api huma.API)` registering `list-audit-log`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/auditlog_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func auditLogPath(f *tenancyFixture) string {
	return "/v1/teams/" + f.teamID.String() + "/audit-log"
}

func TestAuditLogIsHiddenFromEditorsAndViewers(t *testing.T) {
	f := newTenancyFixture(t)

	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleEditor} {
		rec := f.do(t, f.members[role], http.MethodGet, auditLogPath(f), nil)
		require.Equal(t, http.StatusForbidden, rec.Code, "role %s", role)
	}
}

func TestAuditLogListsEntriesForAnAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	// Produce two entries through the API itself, so the test covers the real
	// write path rather than hand-inserted rows.
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Erst"}).Code)
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Zweit"}).Code)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet, auditLogPath(f), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Equal(t, 2, page.TotalCount)
	require.Equal(t, "team.renamed", page.Items[0].Action)
	require.Equal(t, f.members[authz.RoleAdmin].id, *page.Items[0].ActorUserID)
	require.NotEmpty(t, page.Items[0].Metadata)
}

func TestAuditLogFiltersByAction(t *testing.T) {
	f := newTenancyFixture(t)

	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Erst"}).Code)
	require.Equal(t, http.StatusNoContent, f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		memberPath(f, f.members[authz.RoleViewer]), nil).Code)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?action=team_member.removed", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, "team_member.removed", page.Items[0].Action)
}

func TestAuditLogRejectsAMalformedActorFilter(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?actor_user_id=not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestAuditLogIs404ForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, auditLogPath(f), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run 'AuditLog' -v` Expected: FAIL — `undefined: api.AuditEntry`.

- [ ] **Step 3: Implement the endpoint**

Create `internal/api/auditlog.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// AuditEntry is one audit_log row. Metadata is passed through verbatim: the
// writer already guarantees it carries no secret and no IP address.
type AuditEntry struct {
	ID          int64           `json:"id"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *uuid.UUID      `json:"entity_id,omitempty"`
	ActorUserID *uuid.UUID      `json:"actor_user_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

type ListAuditLogInput struct {
	authz.AdminScope
	PageParams
	EntityType  string    `query:"entity_type" enum:"team,team_member,domain,folder,tag,link" doc:"Restrict to one entity type."`
	Action      string    `query:"action" maxLength:"64" doc:"Restrict to one action, e.g. team_member.removed."`
	ActorUserID string    `query:"actor_user_id" doc:"Restrict to one actor, as a UUID."`
	From        time.Time `query:"from" doc:"Only entries at or after this instant (RFC 3339)."`
	To          time.Time `query:"to" doc:"Only entries at or before this instant (RFC 3339)."`
}

type ListAuditLogOutput struct {
	Body Page[AuditEntry]
}

func (d Deps) registerAuditLog(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/audit-log",
		Summary:     "Read a team's audit log",
		Description: "Administrative history, so it is restricted to admins and owners.",
		Tags:        []string{"Audit"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listAuditLog)
}

func (d Deps) listAuditLog(
	ctx context.Context, in *ListAuditLogInput,
) (*ListAuditLogOutput, error) {
	params := db.ListAuditLogParams{
		TeamID:       in.TeamID,
		ResultLimit:  in.Limit(),
		ResultOffset: in.Offset(),
	}

	if in.EntityType != "" {
		entityType := in.EntityType
		params.EntityType = &entityType
	}
	if in.Action != "" {
		action := in.Action
		params.Action = &action
	}
	if in.ActorUserID != "" {
		actor, err := uuid.Parse(in.ActorUserID)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("actor_user_id must be a UUID")
		}
		params.ActorUserID = &actor
	}
	if !in.From.IsZero() {
		from := in.From
		params.From = &from
	}
	if !in.To.IsZero() {
		to := in.To
		params.To = &to
	}

	rows, err := d.Queries.ListAuditLog(ctx, params)
	if err != nil {
		d.Log.Error("list audit log", "error", err, "team_id", in.TeamID)
		return nil, huma.Error500InternalServerError("could not read the audit log")
	}

	items := make([]AuditEntry, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, AuditEntry{
			ID:          row.ID,
			Action:      row.Action,
			EntityType:  row.EntityType,
			EntityID:    row.EntityID,
			ActorUserID: row.ActorUserID,
			Metadata:    json.RawMessage(row.Metadata),
			CreatedAt:   row.CreatedAt,
		})
	}

	return &ListAuditLogOutput{Body: NewPage(items, in.PageParams, total)}, nil
}
```

Register it: add `d.registerAuditLog(api)` to `RegisterV1`.

If `row.Metadata` generated as `[]byte`, the conversion above is right. If sqlc produced `pgtype.JSONB`, add a `jsonb` override to `sqlc.yaml` mapping it to `[]byte` and regenerate rather than importing pgtype into the API layer.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'AuditLog' -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/auditlog.go internal/api/auditlog_test.go internal/api/v1.go
git commit -m "feat(api): add the team audit log endpoint"
```

---

## Task 14: The executable permission matrix, full wiring and docs

**Files:**

- Create: `internal/api/matrix_test.go`
- Modify: `internal/api/tenancy_test.go` (share one Redis container across the package)
- Modify: `cmd/api/main.go` (wire `Pool` and `Admin`)
- Modify: `README.md`

**Interfaces:**

- Consumes: every endpoint from Tasks 7–13.
- Produces: no new production interfaces. The matrix table in `matrix_test.go` becomes the enforced contract for later plans.

- [ ] **Step 1: Share one Redis container across the tenancy suite**

The matrix test builds a fixture per case-and-role pair. Starting a container each time is far too slow, so in `internal/api/tenancy_test.go` add a package-level cache and use it from `newTenancyFixture` in place of `testCache(t)`:

```go
var (
	tenancyCacheOnce   sync.Once
	tenancyCacheClient *cache.Client
	tenancyCacheErr    error
)

// tenancyCache starts one Redis container for the whole package. The tenancy
// suite builds many fixtures, and a container per fixture would dominate the
// suite's runtime.
func tenancyCache(t *testing.T) *cache.Client {
	t.Helper()

	tenancyCacheOnce.Do(func() {
		ctx := context.Background()
		container, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			tenancyCacheErr = err
			return
		}
		url, err := container.ConnectionString(ctx)
		if err != nil {
			tenancyCacheErr = err
			return
		}
		tenancyCacheClient, tenancyCacheErr = cache.New(url)
	})

	if tenancyCacheErr != nil {
		t.Skipf("Docker unavailable (%v) — cannot start a Redis container", tenancyCacheErr)
	}
	return tenancyCacheClient
}
```

Add the imports `"sync"` and `tcredis "github.com/testcontainers/testcontainers-go/modules/redis"`. The container is intentionally not terminated per test; it goes away when the test binary exits.

- [ ] **Step 2: Write the matrix and registry tests**

Create `internal/api/matrix_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// matrixCase is one row of the role permission matrix in the plan and spec.
// path is rendered against the fixture: {team} and {member} are substituted.
type matrixCase struct {
	operationID string
	method      string
	path        string
	body        any
	minRole     authz.Role
}

// teamScopedCases is the enforced contract. Every team-scoped operation must
// appear here, and TestEveryAuthenticatedOperationIsInTheMatrix fails the build
// if a new one is added without a row.
var teamScopedCases = []matrixCase{
	{"get-team", http.MethodGet, "/v1/teams/{team}", nil, authz.RoleViewer},
	{"update-team", http.MethodPatch, "/v1/teams/{team}",
		map[string]string{"name": "Umbenannt"}, authz.RoleAdmin},
	{"list-team-members", http.MethodGet, "/v1/teams/{team}/members", nil, authz.RoleViewer},
	{"add-team-member", http.MethodPost, "/v1/teams/{team}/members",
		map[string]string{"email": "matrix@verein.test", "role": "viewer"}, authz.RoleAdmin},
	{"update-team-member", http.MethodPatch, "/v1/teams/{team}/members/{member}",
		map[string]string{"role": "editor"}, authz.RoleAdmin},
	{"remove-team-member", http.MethodDelete, "/v1/teams/{team}/members/{member}",
		nil, authz.RoleAdmin},
	{"list-audit-log", http.MethodGet, "/v1/teams/{team}/audit-log", nil, authz.RoleAdmin},
}

// notTeamScoped names the authenticated operations that legitimately carry no
// scope embed. Adding to this list must be a deliberate edit.
var notTeamScoped = map[string]string{
	"get-me":      "session endpoint; scoped to the caller, not to a team",
	"create-team": "gated by the maintainer allowlist, not by a team role",
	"list-teams":  "returns only the caller's own memberships",
}

func renderPath(f *tenancyFixture, template string) string {
	path := template
	path = replaceAll(path, "{team}", f.teamID.String())
	path = replaceAll(path, "{member}", f.members[authz.RoleViewer].id.String())
	return path
}

func replaceAll(s, old, new string) string {
	for {
		next := replaceOnce(s, old, new)
		if next == s {
			return s
		}
		s = next
	}
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

// TestRolePermissionMatrix crosses every team-scoped operation with every role
// plus a non-member, and asserts the documented outcome. A fresh fixture per
// pair keeps mutating cases independent.
func TestRolePermissionMatrix(t *testing.T) {
	roles := []authz.Role{authz.RoleViewer, authz.RoleEditor, authz.RoleAdmin, authz.RoleOwner}

	for _, tc := range teamScopedCases {
		for _, role := range roles {
			t.Run(tc.operationID+"/"+role.String(), func(t *testing.T) {
				f := newTenancyFixture(t)
				rec := f.do(t, f.members[role], tc.method, renderPath(f, tc.path), tc.body)

				if role.AtLeast(tc.minRole) {
					require.Less(t, rec.Code, 400,
						"%s must be allowed for %s; body: %s", tc.operationID, role, rec.Body.String())
					return
				}
				require.Equal(t, http.StatusForbidden, rec.Code,
					"%s must be refused for %s", tc.operationID, role)
			})
		}

		t.Run(tc.operationID+"/non-member", func(t *testing.T) {
			f := newTenancyFixture(t)
			rec := f.do(t, f.stranger, tc.method, renderPath(f, tc.path), tc.body)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"%s must hide the team from a non-member", tc.operationID)
		})

		t.Run(tc.operationID+"/unauthenticated", func(t *testing.T) {
			f := newTenancyFixture(t)
			rec := f.do(t, testUser{}, tc.method, renderPath(f, tc.path), tc.body)

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s must require a bearer token", tc.operationID)
		})
	}
}

// TestEveryAuthenticatedOperationIsInTheMatrix is the structural guard. There
// is no RLS: an operation that reaches team data without a scope embed is a
// cross-tenant leak, so a new authenticated operation must either get a matrix
// row or be named in notTeamScoped.
func TestEveryAuthenticatedOperationIsInTheMatrix(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range teamScopedCases {
		covered[tc.operationID] = true
	}

	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	for path, item := range humaAPI.OpenAPI().Paths {
		for _, operation := range operationsOf(item) {
			if !declaresBearer(operation) {
				continue
			}
			if covered[operation.OperationID] {
				continue
			}
			if _, ok := notTeamScoped[operation.OperationID]; ok {
				continue
			}
			t.Fatalf("operation %q (%s) is authenticated but has no permission-matrix row; "+
				"add one to teamScopedCases, or name it in notTeamScoped with a reason",
				operation.OperationID, path)
		}
	}
}

func operationsOf(item *huma.PathItem) []*huma.Operation {
	all := []*huma.Operation{
		item.Get, item.Post, item.Put, item.Patch, item.Delete,
		item.Head, item.Options, item.Trace,
	}
	out := make([]*huma.Operation, 0, len(all))
	for _, operation := range all {
		if operation != nil {
			out = append(out, operation)
		}
	}
	return out
}

func declaresBearer(operation *huma.Operation) bool {
	for _, scheme := range operation.Security {
		if _, ok := scheme["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

// TestOpenAPIExcludesTheRedirectSurface re-asserts plan 1's boundary now that
// many more operations are registered.
func TestOpenAPIExcludesTheRedirectSurface(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	for path := range humaAPI.OpenAPI().Paths {
		require.NotContains(t, path, "{slug}",
			"the public redirect surface must never appear in the OpenAPI document")
	}
}
```

Use `strings.ReplaceAll` instead of the hand-rolled `replaceAll`/`replaceOnce` helpers if `strings` is already imported — they exist only to keep the example self-contained. Prefer `strings.ReplaceAll` and delete both helpers.

- [ ] **Step 3: Run the matrix**

Run: `go test ./internal/api/ -run 'Matrix|EveryAuthenticated|OpenAPIExcludes' -v` Expected: PASS. Each subtest builds a fixture; with the shared Redis container the whole matrix should finish in well under a minute.

If `add-team-member` fails for an allowed role because the fixture's invite quota is exhausted, note that each subtest gets a fresh team ID, so the per-team key differs — no shared quota. If it fails with 409, the case's email collided with a seeded member; change the case's email.

- [ ] **Step 4: Wire the new dependencies in main.go**

In `cmd/api/main.go`, extend the `api.Deps` literal:

```go
	deps := api.Deps{
		Config:   cfg,
		Queries:  queries,
		Pool:     pool,
		Cache:    redis,
		Recorder: recorder,
		Log:      log,
	}
```

and after the existing JWKS block, add:

```go
	// Invitations are optional at startup, like authentication: without a
	// service-role key the API runs, and only the invite branch of
	// POST /v1/teams/{team_id}/members refuses.
	if cfg.SupabaseServiceRoleKey != "" {
		admin, err := supabase.NewClient(cfg.SupabaseAuthURL, cfg.SupabaseServiceRoleKey)
		if err != nil {
			return fmt.Errorf("configure the supabase admin client: %w", err)
		}
		deps.Admin = admin
		log.Info("supabase invitations enabled")
	} else {
		log.Warn("SUPABASE_SERVICE_ROLE_KEY is unset — team invitations are disabled")
	}
```

Add the import `"github.com/mheob/kurze-url/apps/api/internal/supabase"` (and `"fmt"` if it is not already imported).

- [ ] **Step 5: Document the new variables in the README**

In `README.md`, under the "Running the API locally" section plan 1 added, append:

````markdown
### Teams and invitations

`POST /v1/teams` is restricted to the maintainer allowlist. To create the first team locally, put your own Supabase user ID in `MAINTAINER_USER_IDS`:

```bash
psql "$DATABASE_URL" -c "select id, email from auth.users;"
# then, in apps/api/.env
MAINTAINER_USER_IDS=<the id you just read>
```
````

Invitation emails need `SUPABASE_SERVICE_ROLE_KEY` (and `SUPABASE_AUTH_URL`, if the project's auth URL differs from `SUPABASE_JWT_ISSUER`). Without it the API still runs: adding an address that already has an account works, and an unknown address is refused with 503.

````

- [ ] **Step 6: Run the whole suite, vet and lint**

Run:
```bash
go vet ./... && go test ./... && golangci-lint run
````

Expected: all clean. Tests that need Postgres or Docker skip when unavailable; nothing may fail.

- [ ] **Step 7: Verify the OpenAPI document by hand**

Run: `go run ./cmd/api & sleep 2; curl -s localhost:8080/openapi.json | head -40; kill %1`

Check that `bearerAuth` is declared, that `PageTeam`, `PageMember` and `PageAuditEntry` appear as schema names, and that no path contains `{slug}`.

- [ ] **Step 8: Commit**

```bash
git add internal/api/matrix_test.go internal/api/tenancy_test.go cmd/api/main.go README.md
git commit -m "test(api): make the role permission matrix executable"
```

---

## Definition of done

- [ ] `cd apps/api && go vet ./... && go test ./... && golangci-lint run` is clean.
- [ ] `TestRolePermissionMatrix` passes for every team-scoped operation crossed with viewer, editor, admin, owner, a non-member and an unauthenticated caller.
- [ ] `TestEveryAuthenticatedOperationIsInTheMatrix` fails if a new authenticated operation is registered without a matrix row or a named exemption.
- [ ] A non-member gets 404 on every team-scoped route; an insufficient role gets 403; both are `application/problem+json`.
- [ ] A mutation whose audit insert fails commits nothing (`TestLogRollsBackWithItsTransaction`, `TestInTxRollsBackEverythingWhenTheCallbackFails`).
- [ ] The last owner of a team can be neither demoted nor removed, and the check holds the team's owner rows with `for update`.
- [ ] `POST /v1/teams` refuses every caller outside `MAINTAINER_USER_IDS`, and refuses all callers when it is unset.
- [ ] Inviting an unknown address calls the Admin API exactly once and audits `team_member.invited`; adding a known address calls it zero times and audits `team_member.added`.
- [ ] A failed invitation leaves no membership and no audit entry.
- [ ] `audit.Log` refuses an action outside the taxonomy and refuses metadata keyed `password`, `password_hash`, `hash`, `secret`, `token`, `ip` or `ip_address`.
- [ ] `/openapi.json` names the envelopes `PageTeam`, `PageMember` and `PageAuditEntry`, and contains no `{slug}` path.
- [ ] `.env.example` documents `MAINTAINER_USER_IDS`, `SUPABASE_AUTH_URL`, `SUPABASE_SERVICE_ROLE_KEY` and `RATE_LIMIT_INVITE_PER_HOUR`, all valueless or defaulted.
- [ ] The service-role key appears in no log line and no error message.

## Explicitly not in this plan

These belong to plan 3 (domains, links, safety) and must not be skipped there:

- **HTTPS-only URL scheme allowlist and SSRF protection with DNS-rebinding re-checks at fetch time** — they attach to link creation.
- **Async Google Safe Browsing scanning**, writing `link_scan_result` and flipping `link.state` to `flagged`.
- **`cache.InvalidateLink` on every link mutation**, or a 302's promise of an immediate destination change holds only after `LinkCacheTTL`.
- **`link_create` rate limiting** — configured since plan 1, still unconsumed.
- **The `?qr=1` marker** on the URL the QR generator encodes (plan 4), without which every scan records as `regular`.
- **Entity-scoped authorization** for `/v1/links/{link_id}` and `/v1/domains/{domain_id}`: the same role comparison, with `team_id` resolved from the entity first. Extend `internal/authz`; do not re-implement the check in a handler.
- **Plan 3's audit actions** — `domain.*`, `folder.*`, `tag.*`, `link.*` — must be added to `audit.knownActions`, or `audit.Log` will refuse them, by design.

## Open items this plan does not resolve

- **The invite rate-limit value.** Shipping at 20 per hour per team; revisit once real usage exists.
- **`public.profile`.** Still undecided; the member list exposes email addresses only, no display names.
- **"You were added to a team" notification** for the existing-account path. No notification system is decided.
- **Backups.** The Supabase free tier provides none; unchanged and still undesigned.
