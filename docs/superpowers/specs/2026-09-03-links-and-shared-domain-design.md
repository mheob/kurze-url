# Links and the Shared Domain — Design

**Status:** approved 2026-09-03 **Supersedes nothing. Amends:** `docs/planning/05-database-schema.md` (the `link.team_id` denormalization note), `docs/planning/06-api-design.md` (the audit action name `link.destination_changed`).

This is the third implementation spec for the URL shortener. It builds on two merged plans:

- **Plan 1** — foundation and the redirect path: the schema, the Redis-fronted `GET /{slug}` hot path, negative caching, unique-visitor hashing, the password interstitial, rate limiting.
- **Plan 2** — tenancy, authorization and audit: teams, members, invitations, the four role scopes resolved by Huma before a handler body runs, and one audit row per mutation inside the mutation's own transaction.

What is missing after those two plans is the product itself. No link can be created, because there is no endpoint to create one and no domain row for a link to belong to. This spec closes that gap.

---

## Goal

A team can create, find, change and delete short links on a hostname the instance owns, and every such link resolves through the existing redirect path with no change to that path's cost.

---

## Scope

### In scope

- A migration making `domain.team_id` nullable, so one shared hostname can serve every team.
- Boot-time provisioning of the shared domain row from configuration.
- Entity-scoped authorization: the pattern for routes like `/v1/links/{link_id}` that carry no `team_id` in the path.
- Slug generation, normalization, and a reserved-slug list.
- Destination URL validation.
- Five link endpoints: create, list, read, update, delete.
- Redirect-cache invalidation on every write path.
- Three audit actions.

### Out of scope, and where each lands

| Deferred to | What |
| --- | --- |
| Plan 4 | Folders, tags, and the link list filters that depend on them |
| Plan 5 | Custom domains: the domain endpoints, Vercel's Domain API, DNS verification |
| Plan 6 | `PUT`/`DELETE /v1/links/{id}/password`, Safe Browsing scanning, QR codes, `GET /v1/links/{id}/stats` |

One consequence is worth stating plainly rather than discovering during review: `GET /v1/links/{link_id}` ships **without** the nested latest-scan-verdict field that `06-api-design.md` specifies for it. Scanning does not exist until plan 6, so there is nothing to nest. The field is added there.

---

## Global constraints

These bind every task. They are the project's rules, restated here so the plan's executors do not have to reconstruct them from `CLAUDE.md`.

- The tenant is called `team` in every identifier. "Verein" appears only in user-facing German copy.
- `GET /<slug>` is the hot path. Nothing this spec adds may cost it a single extra Redis command or database round trip.
- There is no RLS. Every query that touches team data filters by `team_id` in Go. A query without that filter is a data-leak bug.
- Never store a full IP address.
- Errors are Huma's RFC 9457 `application/problem+json`. No custom error model.
- Pagination is offset/limit with the existing `api.Page[T]` envelope, `per_page` capped at 100.
- Filtering uses flat, explicitly typed query parameters per endpoint.
- Operations that need authentication declare `Security: []map[string][]string{{"bearerAuth": {}}}`.
- Migrations are owned by the Supabase CLI (`supabase migration new`, `supabase db push`).
- Database access is sqlc-generated from raw SQL in `apps/api/internal/db/queries/`. No ORM.
- Go tests run against real Postgres and Redis, as plan 2's do. Mocks are for external HTTP only.

---

## The shared domain

### The problem

`domain.hostname` is globally unique — correctly, since DNS is global — and `domain.team_id` is `NOT NULL`. Docs 02 and 06 both assume a "shared default domain" that every team's links live on until that team brings its own hostname. Under the current schema that row cannot exist: it would have to belong to exactly one team, and then no other team could use it.

### The decision

Make `domain.team_id` nullable. A row with `team_id IS NULL` is shared, and any team may create links on it.

```sql
alter table domain alter column team_id drop not null;
```

`link.team_id` remains the sole authorization key and is unaffected. The denormalization note in `05-database-schema.md` is amended: `link.team_id` equals `domain.team_id` for custom domains, and is simply the creating team for links on a shared domain. The invariant that matters — that `link.team_id` never changes and never needs a join to check — is untouched.

Two alternatives were considered and rejected. An `is_shared` flag keeping `team_id NOT NULL` needs a maintainer-owned team to exist before any link can be created, which is a bootstrap step the instance does not have. Making `link.domain_id` nullable instead would change the redirect hot-path query, weaken the `(domain_id, slug)` unique constraint with NULLs, and spread the special case across every link query.

### Provisioning

A new configuration value, `SHARED_DOMAIN_HOSTNAME`, defaults to `localhost` — matching the existing default for `API_HOSTNAME`, so a local checkout works with no environment file. At startup the API upserts that hostname as a domain row with `team_id NULL`, `verification_status = 'verified'` and `verified_at = now()`.

The upsert is idempotent and runs on every boot. It is logged at info level and is **not** audited: there is no actor and no team, and `audit_log` rows without either are noise.

The hostname belongs in configuration rather than in a migration because it differs per environment — `localhost` locally, the real short domain in production, and a Vercel preview hostname in between. A seed migration would be wrong in two of those three.

### Which domains a link may use

A link's `domain_id` is optional at create time. Omitted, it resolves to the shared domain.

Supplied, the domain must satisfy both:

1. `verification_status = 'verified'`, and
2. `team_id IS NULL` (shared) **or** `team_id` equals the caller's team.

A `domain_id` that fails either check — or that does not exist at all — answers 422 with one identical message. Distinguishing "unverified" from "belongs to another team" from "no such domain" would confirm the existence of a hostname the caller does not own.

This rule is written once here and needs no revision when plan 5 introduces real custom domains.

---

## Entity-scoped authorization

### The problem

Plan 2's four scopes (`ViewerScope`, `EditorScope`, `AdminScope`, `OwnerScope`) all embed `TeamPath`, which binds a `team_id` path parameter. The documented link surface has no such parameter: `GET|PATCH|DELETE /v1/links/{link_id}`. The same shape recurs for domains, folders and tags in later plans, so this is a pattern to establish once, not a one-off.

### The decision

Per-entity scope structs that mirror plan 2 exactly. For this plan, two:

```go
// LinkPath carries the link ID every link-scoped operation takes in its path.
type LinkPath struct {
	LinkID uuid.UUID `path:"link_id" doc:"The link this request operates on."`
}

type LinkViewerScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

type LinkEditorScope struct {
	LinkPath
	member Membership
	link   ResolvedLink
}

func (s *LinkEditorScope) Resolve(ctx huma.Context) []error {
	return resolveLinkScope(ctx, s.LinkID, RoleEditor, &s.member, &s.link)
}

func (s *LinkEditorScope) Member() Membership  { return s.member }
func (s *LinkEditorScope) Link() ResolvedLink  { return s.link }
```

`ResolvedLink` carries what the resolver already had to read, so handlers do not read it again:

```go
type ResolvedLink struct {
	ID       uuid.UUID
	TeamID   uuid.UUID
	DomainID uuid.UUID
	Hostname string
	Slug     string
}
```

`resolveLinkScope` does, in order:

1. Read the caller's claims; 401 if absent.
2. Re-check the raw `link_id` path value parses as a UUID, answering 422 if not. This mirrors the guard plan 2 needed in `resolveScope`: Huma runs every resolver even when its own parameter binding already failed, and picks the last error's status, so without this check a malformed ID would be reported as a 404.
3. Load the link through a `LinkResolver` interface taken from the request context; a link that does not exist answers **404**.
4. Delegate to plan 2's existing membership resolution and role check against `link.TeamID`. A link belonging to a team the caller is not a member of answers **404**, not 403 — the same non-disclosure rule teams already use, so link IDs cannot be probed for existence.

The `LinkResolver` is installed into the request context by the same `/v1` middleware that already installs the membership resolver.

### Defence in depth

The scope authorizes, but every link query still carries its own tenancy filter:

```sql
where id = $1 and team_id = $2
```

with `$2` taken from `in.Member().TeamID`. This is deliberate redundancy. The permission matrix test observes HTTP status per operation and role; it cannot see a handler that has the correct scope but whose SQL forgot its filter. The filter in the SQL is the thing a reviewer can actually look at, and this plan is almost entirely new queries.

### Role mapping

| Operation                        | Scope                                       |
| -------------------------------- | ------------------------------------------- |
| `POST /v1/teams/{team_id}/links` | `EditorScope` (team-scoped, already exists) |
| `GET /v1/teams/{team_id}/links`  | `ViewerScope` (team-scoped, already exists) |
| `GET /v1/links/{link_id}`        | `LinkViewerScope`                           |
| `PATCH /v1/links/{link_id}`      | `LinkEditorScope`                           |
| `DELETE /v1/links/{link_id}`     | `LinkEditorScope`                           |

`EditorScope` and `OwnerScope` had no endpoint after plan 2. Link creation is `EditorScope`'s first real use.

---

## Slugs

### Normalization

Slugs are case-insensitive. They are stored lowercase, and the redirect path lowercases the incoming slug before building its cache key. `/Abc` and `/abc` are the same link.

This matters because of how these links actually travel: printed in a Verein's newsletter, on a poster, on a flyer, read aloud at a meeting. Case-sensitive slugs mean a mistyped capital is a dead link, and on a shared hostname they would also let one team register a visual lookalike of another team's slug.

The redirect path's added cost is one `strings.ToLower` on a short string — no extra Redis command, no extra query.

### Generation

Generated slugs are 8 characters drawn from a 32-symbol alphabet with visually ambiguous characters removed:

```
23456789abcdefghijkmnpqrstuvwxyz
```

`0`, `1`, `l` and `o` are absent, and no uppercase appears at all. The space is 32^8, about 1.1e12 combinations, drawn from `crypto/rand`.

### Custom aliases

After lowercasing, a custom alias must match:

```
^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$
```

Three to sixty-four characters, starting and ending alphanumeric, with hyphens and underscores allowed inside.

### Reserved slugs

Reserved on **every** domain, not only the shared one, because a single chi mux serves them all and a custom domain reaches the same routes:

```
health, verify, api, admin, login, static, assets,
robots.txt, favicon.ico, sitemap.xml, apple-touch-icon.png,
.well-known, _next
```

`health` is the load-bearing one: `/health` is registered on the root router today and would shadow a link with that slug. The rest are conventional browser and platform paths that would otherwise produce confusing behaviour.

A reserved alias answers 422, not 409 — it is a malformed request, not a conflict with another resource.

### Collisions

Uniqueness is `(domain_id, slug)`, as it already is in the schema.

- **Generated slug:** retry the insert up to five times on a `23505` unique violation, then answer 500. At 1.1e12 combinations, reaching five collisions means something is wrong that a sixth attempt will not fix.
- **Custom alias:** answer **409 Conflict** immediately. No retry — the caller asked for a specific slug.

On the shared hostname that 409 tells the caller some other team holds the slug. This is inherent to a single shared namespace and cannot be designed away while the namespace is shared; it discloses only that a slug is taken, never by whom.

---

## Destination URL validation

Applied identically at create and at update.

- **Scheme:** `https://` only, enforced by allowlist. `http:`, `javascript:`, `data:`, `file:` and everything else are rejected because they are not on the list, not because they are on a blocklist — a blocklist is a promise to enumerate every dangerous scheme forever.
- **Literal addresses:** reject a host that is a literal loopback, private, link-local, unique-local or multicast IP. A short link is a way to get someone else's browser to make a request; pointing one at a visitor's own router is not a use case this instance needs to support.
- **Self-reference:** reject any destination whose hostname equals `SHARED_DOMAIN_HOSTNAME` or `API_HOSTNAME`. That is a redirect loop. The check reads configuration, not the `domain` table: a per-request query to catch a rare mistake is not worth the round trip, and plan 5 can widen it when custom domains exist.
- **Length:** 2048 characters.

No DNS resolution happens at create time. It would cost a network round trip on every create, fail closed whenever a resolver is slow, and prove nothing — the record can change between creation and the first click. Doc 01's DNS-rebinding requirement is about **server-side fetches**, and nothing in this plan fetches a destination. The re-check belongs where the fetch is: the QR logo fetch in plan 6, and link health monitoring if it is ever built.

The read-side guard already in `writeRedirect` stays exactly as it is. It is defence in depth against a bad row reaching the browser, and this spec's validation does not replace it.

---

## Endpoints

All under `/v1`, all Bearer-authenticated.

### `POST /v1/teams/{team_id}/links`

Scope: `EditorScope`. Rate-limited per user through the existing `LinkCreateRateLimitPerMin`, using `cache.Allow` with the key `rl:link-create:<user_id>`. The caller is authenticated here, so the user ID is the correct subject and no IP is involved.

Body: `destination_url` (required), `slug` (optional custom alias), `domain_id` (optional), `redirect_type` (301 or 302, default 302), `expires_at` (optional, must be in the future), `analytics_enabled` (default true).

`expires_at` in the past is rejected with 422. Creating an already-dead link is never intentional.

Returns 201 with the link representation.

### `GET /v1/teams/{team_id}/links`

Scope: `ViewerScope`. Returns `api.Page[Link]`.

| Parameter | Meaning |
| --- | --- |
| `q` | Substring match across `slug` and `destination_url`, using the `pg_trgm` GIN indexes the schema already has |
| `state` | `active`, `disabled`, `expired` or `flagged` |
| `domain_id` | Restrict to one domain |
| `page`, `per_page` | Standard, `per_page` capped at 100 |
| `sort` | `created_at` or `-created_at`; `-created_at` is the default |

Folder and tag filters arrive with plan 4.

### `GET /v1/links/{link_id}`

Scope: `LinkViewerScope`.

### `PATCH /v1/links/{link_id}`

Scope: `LinkEditorScope`. Accepts `destination_url`, `slug`, `redirect_type`, `state`, `expires_at`, `analytics_enabled`.

It does **not** accept:

- `password` — its own endpoint, with its own audit action and its own tighter rate limit, in plan 6.
- `domain_id` — moving a link between domains changes its short URL, silently breaking every printed copy of it, and across teams it would break the `link.team_id` denormalization. Rejected as an unknown field.

`state` accepts only `active` and `disabled`. `expired` follows from `expires_at`, and `flagged` is set by scanning; neither is a user's to write.

### `DELETE /v1/links/{link_id}`

Scope: `LinkEditorScope`. Hard delete; returns 204. `link_tag`, `link_scan_result` and `link_click_stats` cascade, which is what the schema already declares.

### Representation

```json
{
	"id": "uuid",
	"team_id": "uuid",
	"domain_id": "uuid",
	"hostname": "kurze.url",
	"slug": "k7fp2mqd",
	"short_url": "https://kurze.url/k7fp2mqd",
	"destination_url": "https://example.org/...",
	"redirect_type": 302,
	"state": "active",
	"expires_at": null,
	"has_password": false,
	"analytics_enabled": true,
	"created_by": "uuid",
	"created_at": "2026-09-03T10:00:00Z",
	"updated_at": "2026-09-03T10:00:00Z"
}
```

`password_hash` never appears in any response, in any form. `has_password` is the projection, exactly as the redirect query already does it.

`short_url` is composed from the domain's hostname and a scheme taken from configuration, so a local checkout on `localhost` produces an `http://` URL and production produces `https://`. It is not stored.

---

## Redirect-cache invalidation

Every write path invalidates the Redis key `l:<hostname>:<slug>` after the transaction commits, through the existing `cache.InvalidateLink`.

| Path                   | Keys to delete               |
| ---------------------- | ---------------------------- |
| Create                 | The new link's key           |
| Update, slug unchanged | The link's key               |
| Update, slug changed   | Both the old and the new key |
| Delete                 | The link's key               |

**Create must invalidate too.** Negative caching means a probe of an unused slug stores the not-found sentinel under exactly the key the new link will use, with `NotFoundCacheTTL` on it. Skip this and a freshly created link 404s for up to a minute for no visible reason. This is the single easiest step in the plan to omit and the hardest to notice, so it gets its own falsification test.

Invalidation is best-effort. A failure is logged at error level and reported to Sentry; the request still succeeds. The database is the source of truth, `LinkCacheTTL` is one hour, so a failed invalidation is bounded staleness rather than corruption. Failing the request would be strictly worse: the write has already committed, so a retry cannot repair anything and only confuses the caller about whether their change landed.

---

## Audit

Three actions, added to the closed taxonomy in `internal/audit`:

| Action         | Entity |
| -------------- | ------ |
| `link.created` | `link` |
| `link.updated` | `link` |
| `link.deleted` | `link` |

Each is written with `audit.Log` inside the same `db.InTx` as its mutation, using the transaction's `*db.Queries` — never the pool-backed one.

`link.updated` carries `metadata.changed`, the list of field names the request actually changed, plus old and new values for `destination_url`, `slug`, `state`, `redirect_type` and `expires_at`.

This supersedes the example action name `link.destination_changed` in `05-database-schema.md`. One PATCH can change several fields atomically; splitting that into several audit rows would misrepresent one request as several, and choosing one row per request keeps the log a faithful record of what was asked. The detail survives in `metadata.changed`, and filtering by action stays indexed.

The metadata denylist already blocks anything password-shaped, so it needs no change — but the `link.updated` metadata builder must be written so it cannot pick up a password field if one is ever added to the PATCH body.

---

## Testing

The bar plan 2 set holds: real Postgres and Redis containers, no mocking of our own data layer, and every security-relevant property falsified before it is trusted.

**Permission matrix.** Rows for all five new operations, covering each role and a non-member. `TestEveryOperationIsAccountedFor` already fails the build for any registered operation that is neither matrix-covered nor explicitly public, so the new endpoints must be added to it.

**Cross-team isolation.** For each of the three link-scoped routes, a test where the caller is an owner of their own team and the link belongs to another team, asserting 404 and not 403.

**Falsification.** Three properties are shown to fail before they are shown to pass:

1. The `team_id` filter on each link query — remove it and the cross-team test must fail.
2. Create-path cache invalidation — pre-seed the not-found sentinel for the slug, create the link, and assert the redirect resolves. Remove the invalidation and this must fail.
3. Reserved-slug rejection — `health` must be refused; remove the list and it must be accepted.

**Hot-path regression.** A test asserting the redirect path's Redis command count is unchanged from plan 1: two commands on a cache hit, four to five on a miss. Slug lowercasing must not have added a round trip.

**Slug generation.** Alphabet membership, length, and absence of the excluded characters, over enough samples to be meaningful.

**Validation.** A table test over destination URLs: each rejected scheme, each private range, a self-referencing hostname, an over-length URL, and the accepted case.

---

## Open questions

None blocking. Two things are deliberately left as they are:

- The 409 on a taken custom alias discloses that a slug exists on the shared hostname. Accepted, as described under "Collisions".
- `GET /v1/links/{link_id}` ships without `scan_result`. It is added in plan 6, along with the scanning that would populate it.
