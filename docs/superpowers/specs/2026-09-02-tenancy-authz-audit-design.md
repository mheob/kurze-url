# Tenancy, Authorization and Audit — Design

**Status:** approved 2026-09-02 **Plan:** 2 of the implementation sequence (plan 1 = foundation and redirect path, shipped in PR #3) **Spec sources:** `docs/planning/05-database-schema.md`, `06-api-design.md`, and `CLAUDE.md`

## Goal

Build the tenancy and authorization core of the `/v1` API: the reusable authorization layer, the audit-log write path and its action taxonomy, the paginated list envelope, and the team and team-member endpoints. Every later endpoint — domains, folders, tags, links, QR, stats — depends on being able to answer "is the caller a member of this team, and is their role sufficient?". This plan builds that answer once, in one place, and proves it with an executable permission matrix.

## Scope

In scope:

- `Page[T]` response envelope and shared pagination parameters.
- The authorization layer: role type, role ordering, and scope input embeds enforced by Huma's `Resolve`.
- The audit-log writer, the plan-2 slice of the action taxonomy, and the transaction helper that keeps a mutation and its audit row atomic.
- `GET /v1/me`, extended with the caller's team memberships.
- Teams: `POST /v1/teams`, `GET /v1/teams`, `GET /v1/teams/{team_id}`, `PATCH /v1/teams/{team_id}`.
- Team members: `GET|POST /v1/teams/{team_id}/members`, `PATCH|DELETE /v1/teams/{team_id}/members/{user_id}`, including the real email invitation path through Supabase's Admin API.
- Audit log read: `GET /v1/teams/{team_id}/audit-log`.
- The signup gate: teams are created by maintainers only.

Out of scope, by dependency — these belong to plan 3 (domains, links, safety) and must not be skipped there:

- Domains, folders, tags, and link CRUD, along with the HTTPS-only URL scheme allowlist, SSRF protection with DNS-rebinding re-checks, async Safe Browsing scanning, `cache.InvalidateLink` calls on every link mutation, and `link_create` rate limiting.
- QR generation and the stats endpoint (plan 4), the OpenAPI export and the generated TypeScript client (plan 4).
- Cron surface, 90-day analytics retention, Sentry, Better Stack (plan 5).
- Team deletion, which `06-api-design.md` deliberately does not expose for MVP.

## Decisions

### The signup gate: maintainer-created teams only

`POST /v1/teams` requires the caller's user ID to appear in a `MAINTAINER_USER_IDS` allowlist, read from the environment as a comma-separated list of UUIDs and parsed at startup. Any other caller receives 403. A Verein asks the maintainer for an account; the maintainer creates the team and invites its first owner.

Rationale: this is one shared instance on free-tier infrastructure, and an unvetted stranger able to mint short links immediately is the classic URL-shortener abuse vector (spam, phishing, malware distribution) — it would burn the shared Redis and Safe Browsing budgets and the instance's domain reputation. The alternatives were open self-service, which accepts that exposure, and self-service plus an approval state, which needs a new `team.status` column, an approval endpoint, and a state check on every link write. The allowlist needs no schema change and no new endpoint, and self-service can be added later without breaking any existing behaviour.

An empty or unset `MAINTAINER_USER_IDS` means nobody can create a team. This is the safe default: the variable is deliberately not required at startup, so a misconfigured deployment refuses team creation rather than opening it to everyone. The maintainer sets their own user ID once the Supabase project exists.

This closes the "signup gate" open item in `CLAUDE.md`.

### The role permission matrix

Roles are the four already in the schema's check constraint, ordered `viewer < editor < admin < owner`:

| Action                               | viewer | editor | admin | owner |
| ------------------------------------ | ------ | ------ | ----- | ----- |
| Read team                            | yes    | yes    | yes   | yes   |
| Rename team                          | no     | no     | yes   | yes   |
| List members                         | yes    | yes    | yes   | yes   |
| Invite member                        | no     | no     | yes   | yes   |
| Change member role                   | no     | no     | yes   | yes   |
| Remove member                        | no     | no     | yes   | yes   |
| Read domains                         | yes    | yes    | yes   | yes   |
| Create or verify domain              | no     | no     | yes   | yes   |
| Delete domain                        | no     | no     | yes   | yes   |
| Write folders and tags               | no     | yes    | yes   | yes   |
| Read links                           | yes    | yes    | yes   | yes   |
| Write or delete any link in the team | no     | yes    | yes   | yes   |
| Set or remove a link password        | no     | yes    | yes   | yes   |
| Read stats                           | yes    | yes    | yes   | yes   |
| Read audit log                       | no     | no     | yes   | yes   |

Two qualifications on member management:

- An admin may grant or change roles only up to admin. Granting or revoking the owner role requires owner.
- An admin may not remove an owner.

Two invariants:

- A team always has at least one owner. The last owner can be neither demoted nor removed.
- Editors may edit and delete any link in their team, not only links they created. Small volunteer-run organisations hand work over constantly; a `created_by` restriction would mean a departing volunteer's links can only be fixed by an admin.

Domain, folder, tag, link and stats rows are in the table because the matrix is the contract for plan 3 and plan 4 as well. Plan 2 implements the rows it has endpoints for; the rest are the declared target.

The audit log is admin-and-above. It records administrative history — member removals and role changes — and a member demoted to viewer should not retain visibility into it.

### The authorization mechanism: scope input embeds

Authorization is expressed in the handler's input type, not in its body. `internal/authz` exports four embeddable structs:

```go
type ViewerScope struct{ TeamID uuid.UUID `path:"team_id"` }
type EditorScope struct{ TeamID uuid.UUID `path:"team_id"` }
type AdminScope  struct{ TeamID uuid.UUID `path:"team_id"` }
type OwnerScope  struct{ TeamID uuid.UUID `path:"team_id"` }
```

Each implements Huma's `Resolve(huma.Context) []error` **on a pointer receiver**. Resolve reads the caller's claims and a membership resolver from the request context, loads the `team_member` row for (caller, `team_id`), compares the role against the scope's minimum, and returns an error before the handler body runs. On success it stores the resolved membership in an unexported field of the scope itself, exposed to the handler through a `Member()` accessor — so the handler never repeats the query.

The membership travels on the struct rather than in the context because `Resolve(huma.Context) []error` returns only errors: it cannot replace the context the handler receives. It can mutate the input it is a part of, since handlers take a pointer to their input type.

The membership resolver reaches `Resolve` in the other direction, through the request context, placed there by the existing `/v1` auth middleware at wiring time. The alternative — a package-level `authz.Configure(*db.Queries)` global — would be untestable in parallel and would make two differently-configured test servers in one process impossible.

Because reflection cannot reliably set fields promoted through an unexported embedded struct, each scope declares its own `TeamID` field via an exported embedded `TeamPath` type rather than sharing one unexported base.

Rationale for putting the requirement in the type: `CLAUDE.md`'s golden rule 4 is that Postgres enforces nothing about tenancy, so a missing check is a data-leak bug. With an embed, the requirement is a visible field in the struct that Huma enforces automatically; the failure mode of forgetting it is a handler whose input has no `TeamID` to read, not a handler that silently serves another team's data. An explicit `authz.Require(...)` call as the first line of each handler body is more obvious to read but guarantees nothing — one forgotten line is a silent cross-tenant leak. Operation metadata (`Metadata: {"min_role": "admin"}`) centralises enforcement but moves the contract into untyped string maps, and it breaks down for plan 3's `/v1/links/{link_id}` routes, where the team must first be resolved from the entity.

A registry test walks every registered Huma operation that declares `bearerAuth` and fails if its input type embeds none of the four scopes. Operations that are legitimately not team-scoped (`GET /v1/me`, `POST /v1/teams`, `GET /v1/teams`) are named in an explicit allowlist inside that test, so adding one is a deliberate edit, not an omission.

Plan 3 extends this with entity-scoped variants that resolve `team_id` from the entity (`link.team_id` is denormalized precisely so this needs no join) and then apply the same role comparison.

### Status codes for authorization failures

- Caller is not a member of the team in the path: **404**. A team's existence is not disclosed to non-members, so team IDs cannot be enumerated.
- Caller is a member but the role is insufficient: **403**.
- Caller is a member, role sufficient, but the specific rule forbids it (an admin trying to grant owner, or demoting the last owner): **403**, with a distinct RFC 9457 `detail`.

All errors use Huma's default `application/problem+json`; no custom error model.

### Audit writes share the mutation's transaction

Every mutating handler runs its mutation and its `audit_log` insert inside one pgx transaction, through a hand-written helper:

```go
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error
```

It begins a transaction, calls `fn` with a `*Queries` bound to that transaction (`db.New(tx)`, since `pgx.Tx` satisfies sqlc's generated `DBTX`), commits on success and rolls back on error. An audited action either happened and is recorded, or neither.

Rationale: the audit log's entire value is being trustworthy. An async write, like the click recorder's, would lose history on a crash or a failed insert — acceptable for aggregate analytics, not for "who removed this member". Two sequential inserts on one connection without a transaction can leave a mutation committed with no audit row. The cost of a transaction is one extra insert per write, all on cold paths; `CLAUDE.md`'s hot-path rule concerns `GET /<slug>`, which this plan does not touch.

The helper lives in `internal/db/tx.go`. sqlc rewrites only its own outputs (`db.go`, `models.go`, `batch.go`, `*.sql.go`), so a hand-written file in that package is safe; the file carries a comment saying so.

### The audit action taxonomy

Actions are `entity.verb`, one action per mutating endpoint. `entity_type` values match the table names. Plan 2 defines:

| Action                     | entity_type   | entity_id       | metadata                  |
| -------------------------- | ------------- | --------------- | ------------------------- |
| `team.created`             | `team`        | team ID         | `{"name": …}`             |
| `team.renamed`             | `team`        | team ID         | `{"from": …, "to": …}`    |
| `team_member.invited`      | `team_member` | invited user ID | `{"email": …, "role": …}` |
| `team_member.added`        | `team_member` | added user ID   | `{"email": …, "role": …}` |
| `team_member.role_changed` | `team_member` | target user ID  | `{"from": …, "to": …}`    |
| `team_member.removed`      | `team_member` | target user ID  | `{"role": …}`             |

`team_member.invited` is the new-user path, where an email was sent. `team_member.added` is the existing-user path, where no email was sent. They are distinct actions because the observable side effect differs.

Reserved for plan 3, defined there alongside their endpoints: `domain.created`, `domain.verified`, `domain.deleted`, `folder.created`, `folder.renamed`, `folder.deleted`, `tag.created`, `tag.renamed`, `tag.deleted`, `link.created`, `link.updated`, `link.deleted`, `link.password_set`, `link.password_changed`, `link.password_removed`.

`metadata` carries only the fields that changed, and never a plaintext password, a password hash, or an IP address.

This closes the "`audit_log.action` value taxonomy" open item in `CLAUDE.md` for plan 2's endpoints, and fixes the shape for the rest.

### Identity data comes from `auth.users` by query; the Admin API only sends email

Email lookups (does this address already have an account?) and member-list enrichment (showing who a member is) are read-only sqlc queries against `auth.users`. The sqlc auth stub at `internal/db/schema/0000_auth_stub.sql` already exists for exactly this purpose and is never applied to a real database. It currently declares only `id` and gains a nullable `email text` column, matching Supabase's real table, where the column is nullable because phone-only accounts exist — so the generated Go type is `*string` and every read must handle nil.

Supabase's Admin API is called for one thing only: `POST /auth/v1/invite`, which creates the unconfirmed `auth.users` row and sends the invitation email. That cannot be done in SQL.

Rationale: routing member enrichment through the Admin API would make the member list either N+1 HTTP calls or a full user listing, on every page view, against a free-tier request budget. Mirroring identity into a `public.profile` table via a trigger is a new migration, a new trigger, and a sync path that can drift — and `CLAUDE.md` lists `public.profile` as an open item, not a decision. This plan does not resolve it: the member list exposes email addresses only, no display names.

## Components

```
internal/authz/roles.go        Role type, ordering, parsing, formatting
internal/authz/scope.go        ViewerScope/EditorScope/AdminScope/OwnerScope + Resolve;
                               membership resolver context plumbing
internal/audit/audit.go        Action constants + Log(ctx, *db.Queries, Entry)
internal/api/page.go           Page[T] envelope, PageParams embed, schema namer
internal/api/me.go             GET /v1/me
internal/api/teams.go          POST|GET /v1/teams, GET|PATCH /v1/teams/{team_id}
internal/api/members.go        GET|POST /v1/teams/{team_id}/members,
                               PATCH|DELETE /v1/teams/{team_id}/members/{user_id}
internal/api/auditlog.go       GET /v1/teams/{team_id}/audit-log
internal/supabase/admin.go     Admin API client: InviteUser(ctx, email, teamID, role)
internal/db/tx.go              InTx helper (hand-written; sqlc rewrites only its own files)
internal/db/queries/team.sql   team, team_member and auth.users reads and writes
internal/db/queries/audit.sql  audit insert and filtered paginated list
```

`internal/api` continues to own HTTP shape only: no SQL and no Redis commands inline. `authz` and `audit` are separate packages because every subsequent plan consumes them, and because their tests should not need an HTTP server.

`internal/api/v1.go` keeps Huma setup, the `bearerAuth` scheme, the auth middleware and operation registration; `GET /v1/me` moves out of it into `me.go`.

One refactor is required by the layering: the verified-claims context key currently lives unexported in `internal/api`, but `internal/authz` needs the caller's user ID and cannot import `internal/api` (which imports `authz`). The claims context helpers move down into `internal/auth` as `auth.WithClaims` and `auth.ClaimsFromContext`; `api.UserFromContext` stays as a one-line wrapper so existing call sites and tests are untouched.

## Data flow

An authenticated, team-scoped request:

1. chi routes by hostname to the `/v1` surface (unchanged from plan 1).
2. The auth middleware verifies the bearer JWT against the cached JWKS, puts the claims and the membership resolver into the request context.
3. Huma parses and validates the input, then calls `Resolve` on the embedded scope, which loads `team_member` and compares roles — 404 for a non-member, 403 for an insufficient role — and stores the resolved membership on the scope.
4. The handler body reads the membership through `input.Member()`, does its work, and for a mutation calls `db.InTx`, performing the mutation and the `audit.Log` insert in one transaction.
5. Huma serialises the typed response; list endpoints return `Page[T]`.

## Pagination

```go
type Page[T any] struct {
    Items      []T `json:"items"`
    Page       int `json:"page"`
    PerPage    int `json:"per_page"`
    TotalCount int `json:"total_count"`
}
```

Offset/limit via `?page=`(default 1) and `?per_page=`(default 25, maximum 100, enforced by Huma validation), shared through a `PageParams` embed. Every list endpoint in this plan returns `Page[T]`, including `GET /v1/teams` and `GET /v1/teams/{team_id}/members`, whose result sets are small — consistency matters more to a generated client than saving an envelope.

Huma's `DefaultSchemaNamer` already strips package paths and brackets from generic instantiations, so `Page[Member]` becomes `PageMember` in the OpenAPI document. No custom namer is needed, but a test locks the produced name: plan 4 generates the TypeScript client from that document, and a mangled generic name would propagate into the frontend's types.

## Endpoints

| Method and path | Minimum role | Behaviour |
| --- | --- | --- |
| `GET /v1/me` | authenticated | `user_id`, `email`, and `memberships` as `[{team_id, name, role}]` — drives the frontend's team switcher |
| `POST /v1/teams` | maintainer allowlist | Creates the team and the creator's `owner` membership, audits `team.created`; all in one transaction |
| `GET /v1/teams` | authenticated | The caller's teams as `Page[Team]` |
| `GET /v1/teams/{team_id}` | viewer | The team |
| `PATCH /v1/teams/{team_id}` | admin | Rename only; audits `team.renamed` |
| `GET /v1/teams/{team_id}/members` | viewer | `Page[Member]`, joined with `auth.users` for email |
| `POST /v1/teams/{team_id}/members` | admin | `{email, role}`; branches invite versus direct add; admin cannot grant owner; 409 if already a member |
| `PATCH /v1/teams/{team_id}/members/{user_id}` | admin | Role change; owner required to grant or revoke owner; last-owner invariant; audits `team_member.role_changed` |
| `DELETE /v1/teams/{team_id}/members/{user_id}` | admin | Admin cannot remove an owner; last-owner invariant; audits `team_member.removed` |
| `GET /v1/teams/{team_id}/audit-log` | admin | `Page[AuditEntry]`, filters `entity_type`, `action`, `actor_user_id`, `from`, `to` |

### Member addition

`POST /v1/teams/{team_id}/members` takes `{email, role}` and branches on whether the address already has an account:

- **Existing account** — look the user up in `auth.users`, insert the `team_member` row, audit `team_member.added`. No email is sent. The person sees the new team on their next login. A proper "you were added to a team" notification stays a known gap; no notification system is decided.
- **New address** — call Supabase's Admin API `POST /auth/v1/invite` with the service-role key, passing `{team_id, role}` as invite `data`. The call creates the unconfirmed `auth.users` row and sends the email. Insert the `team_member` row with the returned user ID and audit `team_member.invited`. The person is a member immediately; their account is simply unconfirmed until they follow the link.

The invite call is not part of the database transaction — it is an external side effect that cannot be rolled back. Order: send the invite first, then open the transaction for the membership row and its audit entry. A failed invite therefore leaves no membership, and a failed membership insert leaves an unconfirmed `auth.users` row with no team, which the next invite attempt for that address resolves through the existing-account branch.

This requires one new secret, `SUPABASE_SERVICE_ROLE_KEY`, documented valueless in `.env.example`. It is never logged.

The endpoint is rate-limited through the existing `cache.Allow` sliding window, keyed per team, because it spends real email quota (Resend's free tier is 3,000 per month across all Supabase auth email, not only invites).

### Concurrency and the last-owner invariant

Demotion and removal both check the owner count inside the transaction with `select … from team_member where team_id = $1 and role = 'owner' for update`, so two concurrent demotions cannot leave a team without an owner. Without the row lock, both transactions would read a count of two and both succeed.

## Error handling

RFC 9457 `application/problem+json`, Huma's default model. No custom error type.

| Condition                                                            | Status                 |
| -------------------------------------------------------------------- | ---------------------- |
| Missing or invalid bearer token                                      | 401                    |
| Caller is not a member of the team in the path                       | 404                    |
| Role insufficient for the operation                                  | 403                    |
| Rule violation (admin granting owner, last owner demoted or removed) | 403, distinct `detail` |
| Team, member or audit entry not found                                | 404                    |
| Address already a member of the team                                 | 409                    |
| Validation failure (bad role, malformed email, `per_page` over 100)  | 422                    |
| Invite rate limit exceeded                                           | 429                    |
| Supabase Admin API unreachable or failing                            | 502                    |
| An unknown address is invited but no service-role key is configured  | 503                    |

## Testing

Test-driven, following plan 1's established patterns.

- **`internal/authz`** — unit tests for role ordering and parsing; `Resolve` tested against a fake membership resolver, covering non-member (404), insufficient role (403), and success storing the membership on the scope.
- **`internal/audit`** — the writer tested inside a rolled-back transaction against local Supabase Postgres; asserts a rollback leaves no audit row.
- **`internal/db`** — query tests via the existing `testPool` helper, which skips when local Supabase is not running; covers member listing with email join, audit filtering and pagination, and the `for update` owner-count lock.
- **`internal/supabase`** — the Admin API client against an `httptest` server: success, an existing-address error, and a 5xx mapped to 502. No real network calls.
- **`internal/api`** — handler tests through `humatest`. The load-bearing one is a table-driven permission matrix: every endpoint crossed with every role, asserting the expected status, so the matrix in this document is executable rather than prose. Plus the registry test asserting every `bearerAuth` operation embeds a scope (with a named allowlist for the three that are not team-scoped), and a test asserting `/openapi.json` names the generic envelope schemas readably.

## Definition of done

- `cd apps/api && go vet ./... && go test ./... && golangci-lint run` is clean.
- The permission matrix test passes for every endpoint and every role.
- The registry test fails if a new `bearerAuth` operation omits a scope embed.
- A mutation whose audit insert fails commits neither.
- The last owner of a team can be neither demoted nor removed, including under two concurrent requests.
- `POST /v1/teams` refuses every caller outside `MAINTAINER_USER_IDS`, and refuses all callers when it is unset.
- Inviting a brand-new address sends one email and creates one membership; inviting an existing account creates a membership and sends none.
- No plaintext password, password hash or IP address appears in `audit_log.metadata`.
- `.env.example` documents `MAINTAINER_USER_IDS`, `SUPABASE_SERVICE_ROLE_KEY` and the invite rate limit, all valueless.

## Open items this design does not resolve

- **The invite rate-limit value.** The mechanism is decided and the endpoint is wired to `cache.Allow`; the proposed starting value is 20 invites per hour per team, to be confirmed. `CLAUDE.md`'s "concrete rate-limit numbers" open item stays open for link creation.
- **`public.profile`.** Still undecided. The member list exposes email addresses only; display names would need this table.
- **"You were added to a team" notification** for the existing-account path. No notification system is decided; the gap is unchanged from `06-api-design.md`.
- **Backups.** Unrelated to this plan but unchanged and still undesigned: the Supabase free tier provides none.
