# URL Shortener — API Design Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code (concrete Huma operation definitions).

Project context: Go backend, **Huma** (code-first, generates OpenAPI 3.1) on **chi**, hosted via Vercel's Go Framework Preset (see `04-backend-architecture.md`), against the schema in `05-database-schema.md`. Consumed by the TanStack Start frontend (via a TypeScript client generated from Huma's OpenAPI spec) and the Go CLI (thin client calling the same REST endpoints directly).

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Versioning | **`/v1` URL path prefix** | See "Versioning" below |
| Error format | **Huma's default** — RFC 9457 `application/problem+json` | No custom error model; Huma's built-in behavior is kept as-is |
| Auth scheme | **Bearer JWT (Supabase-issued), verified via JWKS** | Asymmetric (ES256) verification, no shared secret in the backend — see "Authentication" below |
| Pagination | **Offset/limit (`page`, `per_page`), typed response envelope** | Not cursor-based — see "Pagination" below |
| Filtering | **Flat, typed query parameters** per list endpoint | Not a generic `filter=field:op:value` scheme — see "Filtering" below |
| Public redirect/password surface | **Separate from `/v1`, hostname-routed, excluded from the OpenAPI spec** | See "Public redirect surface" below |
| QR delivery | **Raw image bytes** (`image/png` / `image/svg+xml`) via `GET`, not base64-in-JSON | Matches "server calls a preview endpoint" from `03-frontend.md` |
| Team invitations | **Email invite via Supabase `inviteUserByEmail` + Resend as custom SMTP** (decided 2026-09-01) | New external service — see "Team invitations" below, also update `02-external-services-and-hosting.md` |

## Versioning

Decided: a leading `/v1` path segment (e.g. `/v1/links`), not header-based or query-parameter versioning.

Reasoning: with a single frontend and a single generated TypeScript client (no external third-party API consumers to accommodate with content-negotiation-style versioning), URL path versioning is the pragmatic choice — it's visible in logs, in the generated client's method paths, and in debugging tools, at the cost of a marginally longer URL. Header-based versioning is more "correct" for a public, multi-consumer API, which this isn't. The public redirect/password surface (see below) is intentionally **not** versioned — it's not a machine contract, it's browser-facing HTML/redirects.

## Authentication & authorization

Both the frontend and the CLI already authenticate against **Supabase's OAuth 2.1 Server** (Authorization Code + PKCE, see `01-architecture.md`) and hold a Supabase-issued access token (JWT). The Go API's job is purely to **verify** that token, not to issue or manage it.

Decided: verify via Supabase's **JWKS endpoint** (`https://<project>.supabase.co/auth/v1/.well-known/jwks.json`), using **asymmetric (ES256) signing keys** rather than the legacy HS256 shared-secret approach. This is Supabase's current recommended setup: the backend fetches and caches the public keys, verifies the JWT signature locally (no network call to Supabase per request), and checks issuer/audience/expiry — matching Huma's own documented JWT middleware pattern (fetch + cache JWKS, extract the bearer token, validate, reject on failure). No shared secret needs to be distributed to or rotated across the Go backend, which is a meaningful simplification over the legacy approach.

Huma-level implementation:

- Declare a `bearerAuth` security scheme (`Type: "http"`, `Scheme: "bearer"`, `BearerFormat: "JWT"`) — not the full OAuth2 flow object, since the actual authorization-code/PKCE dance happens directly against Supabase's own OAuth server, not through this API.
- A global middleware checks `ctx.Operation().Security` and enforces the bearer check only on operations that declare `Security: []map[string][]string{{"bearerAuth": {}}}`. Operations that omit `Security` (health checks, if any are ever exposed under `/v1`) stay open.
- **Authorization** (as already decided in `05-database-schema.md`) stays entirely in the Go backend, not in Postgres RLS: every handler resolves the caller's `team_member.role` for the `team_id` in the request path/body and rejects with `403` if the role doesn't permit the action (e.g. only `owner`/`admin` can manage members or delete a domain).

## Pagination

Decided: **offset/limit** (`?page=1&per_page=25`), with a typed response envelope — not cursor/keyset pagination, and not pagination-via-headers (`Link`, `X-Total-Count`).

Reasoning: the consuming UI is an admin-style data table (TanStack Table, page-numbered, "search and filter by URL, alias, creator, time period" per the original feature list) at a scale (per-Verein link counts) where offset drift from concurrent inserts is a non-issue — this isn't a high-write public feed where keyset pagination's stability advantage would matter. Headers are the more "RESTful" option but are invisible to a strongly-typed generated TypeScript client — Huma's core value is typed response _bodies_, so pagination metadata belongs in the body:

```go
type Page[T any] struct {
    Items      []T `json:"items"`
    Page       int `json:"page"`
    PerPage    int `json:"per_page"`
    TotalCount int `json:"total_count"`
}
```

Every list endpoint (`GET /v1/teams/{team_id}/links`, `.../audit-log`, etc.) returns `Page[T]` for its resource type. `per_page` capped server-side (e.g. max 100) to bound query cost.

## Filtering

Decided: **flat, explicitly typed query parameters** per endpoint (e.g. `?search=&tag_id=&folder_id=&state=&created_by=&domain_id=&from=&to=`) rather than a generic `filters=field:op:value` scheme.

Reasoning: Huma supports a generic filter-parameter pattern (repeated `filters=field:op:value` query params via `explode`, with a `Resolve()` method doing manual validation of field names/operators/value formats) for cases where the filterable field set is open-ended. That's not this project — the filterable dimensions per resource are a small, known, closed set straight from the original feature list (URL, alias, creator, time period for links; entity_type/action/date for the audit log). Flat typed params get full compile-time typing and automatic Huma validation for free, with no hand-rolled parsing/validation layer — simpler and safer for a closed field set, at the cost of being less extensible if the filter set grows arbitrarily later (an acceptable trade-off here).

## Public redirect surface (not part of `/v1`)

The redirect and password-verification routes are **not** versioned JSON API endpoints — they're browser-facing (HTML/redirect responses), served on whatever hostname the request arrives on (the shared default domain or a team's verified custom domain), and deliberately **excluded from the Huma-registered OpenAPI operations** since they're not a typed contract for the frontend/CLI to consume. Since Huma explicitly supports "bring your own router," these are implemented as plain `chi` handlers registered on the same mux, alongside (but outside) Huma's operation registry — one Go binary, one Vercel deployment, hostname-based routing distinguishes "this is a short-link domain" (redirect/password routes) from "this is the API's own domain" (`/v1/*`).

Routes:

- `GET /{slug}` — the redirect handler described in `01-architecture.md` (Redis cache check → Postgres fallback → redirect, or the password-protected branch below).
- `GET /{slug}/verify` — password interstitial page (server-rendered HTML, not a TanStack Start route — kept deliberately lightweight/framework-free since this sits on the redirect hot path where cold-start latency already matters, per `01-architecture.md`).
- `POST /{slug}/verify` — password submission; verifies against `link.password_hash` (Argon2id); on success, redirects; on failure, re-renders the form with an error. Carries its own tighter rate limit, per `05-database-schema.md`.

## Team invitations

Decided 2026-09-01: real email invitations from day one, not deferred to "add existing users only."

Finding: Supabase's Admin API (`auth.admin.inviteUserByEmail`) creates the `auth.users` row synchronously (unconfirmed) and sends the invite email itself — no separate `team_invitation` table is needed, since a valid `user_id` exists immediately and the `team_member` row (with `team_id`/`role` passed through as `data` metadata on the invite call) can be inserted right away, before the invitee has even clicked the link. The person simply appears as a member whose account happens to be unconfirmed until they follow the email.

Caveat that changes the plan: Supabase's **built-in** email sending is capped at **2 emails/hour** — unusable for real invite volume even for a single small Verein onboarding a handful of people at once. Fixing this requires **custom SMTP** on the Supabase project; **Resend** is the pick (free tier: 3,000 emails/month, simplest setup of the compared providers — API key as the SMTP password, no separate domain/IAM ceremony like SES). This becomes a new external service: `02-external-services-and-hosting.md` needs a short addition. Note this is purely a Supabase project setting (SMTP configured in the Supabase dashboard) — the Go backend itself doesn't send email, it only calls Supabase's Admin API (already using the service-role key it needs anyway) to trigger the invite, so no new secret is needed in `04-backend-architecture.md`. Since Supabase routes _all_ auth emails (signup confirmation, magic link, password reset, invites) through whatever SMTP is configured, this one addition also fixes the 2/hour ceiling for every other auth email, not just invites.

Edge case flagged, not fully resolved here: inviting an email address that **already** has a Supabase account (e.g. a person being added to a second Verein) needs different handling than a brand-new invite — `inviteUserByEmail` is for creating new users. Recommendation to carry into implementation: detect this case (lookup by email first, or handle the error `inviteUserByEmail` returns for an existing address) and fall back to inserting the `team_member` row directly with no email sent — the person simply sees the new team the next time they log in. A proper "you were added to a team" notification for that path is out of scope for MVP (no notification system decided yet) and stays a known gap, not a blocker.

Endpoint: `POST /v1/teams/{team_id}/members` accepts `{email, role}`; internally branches on whether the email is new (invite flow) or existing (direct-add flow) as described above.

## Endpoints

Grouped by resource. All under `/v1` and Bearer-authenticated unless noted otherwise (the public redirect surface above is the only unauthenticated exception).

**Session**

- `GET /v1/me` — current user's profile plus their team memberships and roles (drives the team switcher in the frontend).

**Teams**

- `POST /v1/teams` — create a team; creator becomes `owner`.
- `GET /v1/teams` — teams the caller belongs to.
- `GET /v1/teams/{team_id}`
- `PATCH /v1/teams/{team_id}` — e.g. rename.
- Team deletion: **not exposed for MVP** — a rare, destructive operation with no clear requirement for it yet; revisit if/when a real need shows up.

**Team members**

- `GET /v1/teams/{team_id}/members`
- `POST /v1/teams/{team_id}/members` — invite/add, per "Team invitations" above.
- `PATCH /v1/teams/{team_id}/members/{user_id}` — change role.
- `DELETE /v1/teams/{team_id}/members/{user_id}` — remove.

**Domains**

- `POST /v1/teams/{team_id}/domains`
- `GET /v1/teams/{team_id}/domains`
- `GET /v1/domains/{domain_id}`
- `POST /v1/domains/{domain_id}/verify` — trigger/check verification status against Vercel's Domain API (see `02-external-services-and-hosting.md`).
- `DELETE /v1/domains/{domain_id}`

**Folders**

- `POST /v1/teams/{team_id}/folders`, `GET /v1/teams/{team_id}/folders`
- `PATCH /v1/folders/{folder_id}`, `DELETE /v1/folders/{folder_id}`

**Tags**

- `POST /v1/teams/{team_id}/tags`, `GET /v1/teams/{team_id}/tags`
- `PATCH /v1/tags/{tag_id}`, `DELETE /v1/tags/{tag_id}`

  A tag attaches to a link through `tag_ids` on `POST`/`PATCH /v1/links` rather than through a subresource of its own — there is no `PUT`/`DELETE /v1/links/{link_id}/tags/{tag_id}`. `tag_ids` is a whole-set replacement, not a delta: an omitted array leaves the link's tags untouched, `[]` detaches every tag, and any other array replaces the set exactly as given. The rejected subresource pair would have been idempotent per tag, but three tag changes become three requests instead of one, and the client has to diff old against new to know which calls to make; the precedent it would follow — the password subresource splitting off from the general `PATCH` — earned that split with its own audit action and its own rate limit, and tags have neither. The full reasoning is in `docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`.

**Links**

- `POST /v1/teams/{team_id}/links` — create.
- `GET /v1/teams/{team_id}/links` — list; filter/sort/paginate per "Pagination"/"Filtering" above.
- `GET /v1/links/{link_id}` — includes the latest `link_scan_result` verdict as a nested field (no separate scan-status endpoint — folding it into the main resource is simpler than a second round-trip).

  As of 2026-09-03 this ships without the nested `link_scan_result` verdict: scanning does not exist yet, so there is nothing to nest. The field arrives with Safe Browsing scanning.

- `PATCH /v1/links/{link_id}` — general edit (destination, `redirect_type`, `state`, folder, tags, `expires_at`, QR defaults). Deliberately **excludes** `password_hash` — see next.
- `DELETE /v1/links/{link_id}`
- `PUT /v1/links/{link_id}/password` — set or change the password (body: new plaintext password; server hashes with Argon2id per `05-database-schema.md`). Kept as its own endpoint, separate from the general `PATCH`, so it maps to its own distinct, auditable `audit_log` action (`link.password_set`/`link.password_changed`) and so the tighter rate limit on password-related mutations (per `05-database-schema.md`) can be scoped to exactly this route.
- `DELETE /v1/links/{link_id}/password` — remove password protection.
- `GET /v1/links/{link_id}/qr` — QR image; query params for size/ECC level/margin/logo/colors (override the link's stored defaults); returns `image/png` or `image/svg+xml` directly based on `?format=`.
- `GET /v1/links/{link_id}/stats` — analytics rollups; query params `from`, `to`, `dimension` (daily granularity only for MVP, per `05-database-schema.md`).

**Audit log**

- `GET /v1/teams/{team_id}/audit-log` — paginated; filter by `entity_type`, `action`, `actor_user_id`, `from`/`to`.

**CLI note**: every CLI command (`short link create`, `short link list`, `short link stats`, `short qr`) maps directly onto one of the endpoints above — no CLI-specific endpoints exist or are needed, confirming the "CLI is a thin client over the same API" principle from `01-architecture.md` holds with zero exceptions.

## Not yet decided / to revisit

- The exact `audit_log.action` value taxonomy (carried over from `05-database-schema.md`) — falls out directly from this endpoint list now that it exists (one action per mutating endpoint).
- Notification mechanism for "you were added to a team" when the direct-add path (existing user, no invite email) is taken — flagged above, no system decided yet.
- Repo structure & tooling (next planning topic) — will settle where the generated TypeScript client actually lives relative to the frontend package.
