# URL Shortener — Backend/Go Architecture Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code.

Project context: open-source URL shortener for non-profit associations ("Vereine"), Go backend, hosted on Vercel (see `01-architecture.md`, `02-external-services-and-hosting.md`).

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Deployment model | **Vercel Go Framework Preset**, single persistent server | Not the fragmented `/api/*.go`-per-endpoint pattern — see "Deployment model" below |
| HTTP router | **chi** | Lightweight, idiomatic, explicitly shown in Vercel's own Go runtime docs alongside stdlib `net/http` |
| API framework | **Huma**, code-first (decided 2026-09-01) | Sits on top of chi; generates OpenAPI 3.1 from Go types — see "API framework" below |
| DB access | **sqlc** | Type-safe Go generated from raw SQL queries; no ORM — see "Database access" below |
| Migrations | **Supabase CLI** (`supabase migration new/up`, `db push`) | No separate Go migration tool — see "Migrations" below |
| Rate limiting | **Custom, Redis-backed** (Upstash) | No official Upstash Go SDK exists for this — see "Rate limiting" below |
| Frontend API client | **Generated from the Huma-produced OpenAPI spec** | via `@hey-api/openapi-ts` — single source of truth shared with the frontend, avoids hand-written/drifting types |

## Deployment model

Vercel's Go runtime supports two patterns: (1) many small files under `/api/`, each exporting one `http.HandlerFunc` and becoming its own Vercel Function, or (2) a **Go Framework Preset** — a normal, single, persistent `net/http` server (entrypoint `main.go`, `cmd/api/main.go`, or `cmd/server/main.go`, listening on the `PORT` env var), explicitly documented as working with `net/http` and frameworks like chi or gin.

**Decided**: use the Go Framework Preset with a single `cmd/api/main.go` entrypoint, not the fragmented `/api/*.go` pattern. For an API with many resources (links, domains, Vereine/teams, analytics, auth callbacks, QR previews), one file per endpoint would fragment routing, middleware (auth, rate limiting, logging), and shared DB/Redis connection setup across dozens of files — a real router inside one server is a much better fit, and it's the pattern Vercel's own docs point to for a real Go API rather than a couple of standalone functions.

## API framework: Huma (code-first)

Decided 2026-09-01, resolving the code-first-vs-spec-first question raised alongside this doc: **code-first with Huma**, not hand-written OpenAPI + oapi-codegen.

Huma is a Go framework built specifically around OpenAPI 3 / JSON Schema: Go types and handlers are annotated, and the OpenAPI 3.1 spec is generated automatically — it structurally cannot drift out of sync with the implementation, since the spec _is_ the code's own declared shape. It explicitly supports "bring your own router," including chi. The generated spec still functions as a real contract: `@hey-api/openapi-ts` (or `oapi-codegen` in client-generation mode) can turn it into a typed TypeScript client for the TanStack Start frontend, so the "one spec, generated clients" benefit of spec-first is kept without the overhead of hand-authoring and maintaining a YAML contract separately from the implementation.

Trade-off accepted: this is less strict than true contract-first design (the spec can't be finalized and reviewed _before_ any implementation exists, the way spec-first would allow) — acceptable here since the team is small and the same person is driving both API and frontend early on.

## Database access: sqlc

Decided: **sqlc**, not GORM or a hand-rolled `database/sql`/`sqlx` layer.

Reasoning specific to this project's setup: schema migrations are already owned by the Supabase CLI (see below), so an ORM's migration-management features (GORM's main differentiator) provide no value and would just be a second, conflicting way to think about schema. sqlc instead takes plain SQL queries (written against the Supabase-managed schema) and generates fully type-safe Go functions from them at build time — no runtime reflection, no risk of typo'd field names surfacing as runtime errors instead of compile errors, and full access to Postgres-specific features (JSONB, arrays) that this project already needs (e.g. UTM parameter handling, configurable query-parameter rules from the original feature list).

## Migrations: Supabase CLI only

Decided: use Supabase's own migration workflow (`supabase migration new`, `supabase migration up`, `supabase db push`) as the single source of truth for schema changes — no additional Go-specific migration tool (golang-migrate, Atlas) layered on top.

Reasoning: Supabase migrations are plain versioned SQL files, tracked in a `supabase_migrations.schema_migrations` table. Adding golang-migrate or Atlas on top would mean two systems that could each believe they own the current schema state — pure risk with no benefit, since the project is already committed to Supabase specifically (not portable to arbitrary Postgres). sqlc reads the resulting schema/queries to generate Go code, so the two tools compose cleanly: Supabase CLI owns "what the schema is," sqlc owns "how Go code talks to it."

## Rate limiting: custom, Redis-backed

Decided: rate limiting (already required for MVP, see `01-architecture.md`) will be implemented directly against Upstash Redis using a standard Go Redis client, not a pre-built rate-limiting package.

Note: Upstash ships official rate-limiting SDKs for JavaScript/TypeScript (`@upstash/ratelimit`) and Python, but **not Go** — so unlike the frontend/edge world, there's no official drop-in library here. The implementation itself is a well-known, small pattern (sliding window or token bucket via `INCR` + `EXPIRE`, or a small Lua script for atomicity), so this isn't a blocker, just a "build it, don't expect to `go get` it" expectation to set correctly upfront.

## Project layout

Suggested starting structure (to refine once real code exists, not a rigid final answer):

```
cmd/api/main.go        # entrypoint Vercel's Go Framework Preset detects
internal/
  api/                  # Huma route registration, request/response types
  db/                   # sqlc-generated code + hand-written queries (.sql)
  redis/                # cache + rate-limit helpers
  auth/                 # Supabase OAuth 2.1 / PKCE verification, session handling
  scanning/             # Google Safe Browsing integration (async)
  qr/                   # piglig/go-qr wrapper
supabase/migrations/    # Supabase CLI-owned SQL migrations
```

`internal/` keeps application code non-importable from outside the module — appropriate here since this is an application, not a library other projects would import (the CLI talks to the deployed API over HTTP, not by importing Go packages).

## Not yet decided / to revisit

- Concrete DB schema (tables, fields, indexes) — next planning topic, will use sqlc's query-generation model directly.
- Concrete API endpoints/resources — next planning topic after the schema, using Huma's operation model.
- Repo structure across backend, frontend, and CLI (monorepo vs. separate repos) — planned as a later topic; affects how `internal/` boundaries and the generated TS client actually get shared.
