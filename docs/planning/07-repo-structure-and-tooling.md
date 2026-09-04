# URL Shortener — Repo Structure & Tooling Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code.

Project context: monorepo housing the Go API (`04-backend-architecture.md`), the TanStack Start frontend (`03-frontend.md`), and the Go CLI (`01-architecture.md`), all consuming/producing the schema in `05-database-schema.md` and the endpoints in `06-api-design.md`. This is the last of the four planning topics selected earlier (API Design → Database Schema → Backend Architecture → Repo Structure & Tooling) — resolving it closes out the initial planning pass.

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Repo shape | **Single monorepo** | Not separate repos per app — see "Repo layout" below |
| JS/TS workspace tooling | **pnpm workspaces** | Standard for the TanStack ecosystem, efficient with a generated-client package shared between apps |
| Go module structure | **Separate `go.mod` per Go app** (`apps/api`, `apps/cli`), no `go.work` yet | CLI is a thin HTTP client — no shared Go code to justify a workspace yet; add `go.work` if that changes |
| Vercel deployment | **Two Vercel projects from one repo** (`apps/api`, `apps/web`), via Root Directory | Officially supported monorepo pattern — see "Deployment" below |
| Preview environments | **Vercel preview deployments + Related Projects**, **not** Supabase Branching | Branching is usage-billed, not covered by the project's free-tier/Spend Cap commitment — see "Database migrations in CI/CD" below |
| DB migrations in CD | **`supabase db push` via GitHub Actions on merge to `main`** | Not per-PR preview branches — see below |
| CI | **Path-filtered GitHub Actions per app** | Resolves the CI-wiring item left open in `03-frontend.md` — see "CI" below |
| CLI releases | **goreleaser, triggered on git tag, published to GitHub Releases** | SemVer tags; Homebrew/Scoop packaging deferred |
| Commit convention | **Conventional Commits**, checked but not hard-blocking for MVP | Cheap to adopt now, enables automated changelogs later |

## Repo layout

```
apps/
  api/            # Go backend (chi + Huma) — Vercel project "api", Root Directory = apps/api
  web/            # TanStack Start frontend — Vercel project "web", Root Directory = apps/web
  cli/            # Go CLI — not a Vercel project, released via goreleaser
packages/
  api-client/     # TypeScript client generated from apps/api's OpenAPI spec (@hey-api/openapi-ts), pnpm workspace package
supabase/         # Supabase CLI-owned migrations (top-level, not nested under apps/api — see note below)
.github/
  workflows/      # ci-api.yml, ci-web.yml, ci-cli.yml, db-migrate.yml, release-cli.yml, e2e.yml, secret-scan.yml
pnpm-workspace.yaml
```

`supabase/` sits at the repo root rather than under `apps/api/` even though `sqlc` (which reads the resulting schema) lives in `apps/api` — migrations are conceptually project-wide infrastructure, not backend application code, and keeping them top-level avoids implying the frontend or CLI can't ever need schema awareness (e.g. the generated TS client's types ultimately trace back to this schema too, even though it gets there via the OpenAPI spec, not by reading SQL directly).

`apps/cli` is deliberately kept as a separate Go module from `apps/api` rather than joined via a `go.work` workspace: the CLI is "a thin client over the same API" (per `01-architecture.md`) making plain HTTP calls, not importing `apps/api`'s internal packages — there's currently nothing to share. Revisit if the CLI ever needs to import shared Go types (e.g. a hand-written or `oapi-codegen`-generated Go API client, symmetric with the TS client) — a `go.work` at the repo root would then link the two modules without restructuring anything.

## Deployment: two Vercel projects, one repo

Vercel's monorepo support is exactly the "one Git repo, N Vercel projects, each with its own Root Directory" pattern already implied by having both a Go backend and a TanStack Start frontend on Vercel (per `01-architecture.md`, `02-external-services-and-hosting.md`). Concretely: create two Vercel projects against the same GitHub repo, set **Root Directory** to `apps/api` on one (Go Framework Preset applies) and `apps/web` on the other (TanStack Start is a first-class supported framework on Vercel). Every push triggers a deployment attempt for both projects independently, each with its own production URL, preview URLs, and environment variables.

**Build skipping differs per app**, worth knowing upfront rather than discovering later:

- `apps/web` qualifies for Vercel's automatic "skip unaffected builds" — it's inside the pnpm workspace, so Vercel can detect via the lockfile/workspace graph whether a given commit actually touched `apps/web` or its `packages/api-client` dependency, and skips the build otherwise, for free (no configuration, no wasted build-queue slot).
- `apps/api` is Go, not part of the pnpm/npm/yarn/Bun workspace graph Vercel's automatic skipping understands — it needs an explicit **Ignored Build Step** script instead (e.g. `git diff --quiet HEAD^ HEAD -- apps/api supabase` — exit 0 to skip, non-zero to build), configured in that project's settings.

**Related Projects**, used to solve a specific cross-app preview problem: without it, a PR that changes both the frontend and the API would deploy two independent preview URLs with no way for the frontend preview to know the API preview's URL short of hardcoding or manual lookup. `apps/web/vercel.json` lists `apps/api`'s Vercel project ID as a related project; Vercel then exposes the matching preview (or production) API URL as an environment variable on every `apps/web` deployment automatically, via the `@vercel/related-projects` package:

```ts
// apps/web, resolving the API base URL per-deployment
import { withRelatedProject } from '@vercel/related-projects';

const apiBaseUrl = withRelatedProject({
	projectName: 'kurze-url-api',
	defaultHost: process.env.API_HOST, // fallback for local dev / non-Vercel builds
});
```

This removes an entire category of "preview frontend is silently talking to production API" or "talking to a stale API preview" bugs, for a small one-time setup cost.

## Database migrations in CI/CD

Decided against **Supabase Branching** (automatic per-PR preview database branches, official GitHub integration) despite it being the "obvious" pairing with Vercel's per-PR preview deployments: branching compute is **usage-billed per hour** (~$0.01344/hour on the default Micro size) and explicitly **not covered by the account's Spend Cap** — incompatible with this project's consistent free-tier-first, reactive-scaling stance (the same reasoning that already ruled out Cloudflare Containers' Workers-Paid requirement and keeps Web Risk API as only a fallback in `02-external-services-and-hosting.md`). A single small-scale non-profit tool doesn't have PR volume high enough to make isolated per-PR databases worth a real recurring cost.

Decided instead: migrations apply to the **one** free-tier Supabase project directly, on merge to `main` — not per-PR. GitHub Actions workflow using the official `supabase/setup-cli` action:

```yaml
# .github/workflows/db-migrate.yml (on push to main, paths: supabase/**)
- uses: supabase/setup-cli@v2
  with: { version: latest }
- run: supabase link --project-ref $SUPABASE_PROJECT_ID
- run: supabase db push
```

Requires three GitHub Actions secrets: `SUPABASE_ACCESS_TOKEN`, `SUPABASE_DB_PASSWORD`, `SUPABASE_PROJECT_ID`. Gated behind the `ci-api.yml` checks (see below) passing first via a required-status-check branch protection rule on `main`, so a broken migration can't land alongside broken application code. This is a real trade-off versus Branching's isolation — a bad migration on `main` hits the one shared project directly — mitigated by CI validating migrations before merge (see "CI" below) and by Supabase migrations already being reversible SQL files under version control if a rollback is ever needed.

## CI

Path-filtered GitHub Actions workflows — each app gets its own workflow, triggered only by changes to its own directory (plus shared dependencies), so a frontend-only PR doesn't wait on Go tests and vice versa. This also resolves the "exact CI wiring" item explicitly left open in `03-frontend.md`.

- **`ci-api.yml`** (triggers: `apps/api/**`, `supabase/**`) — `go vet`, `golangci-lint`, `go test ./...`, and a migration sanity check (`supabase db diff` / equivalent dry-run against a local Postgres via the Supabase CLI's local dev stack, catching malformed migrations before they ever reach `db-migrate.yml`).
- **`ci-web.yml`** (triggers: `apps/web/**`, `packages/**`) — typecheck, lint, the Vitest unit + MSW-backed integration suite from `03-frontend.md`, and a Storybook build (surfaces the a11y addon's findings at build time, not just when someone happens to open Storybook locally).
- **`ci-cli.yml`** (triggers: `apps/cli/**`) — `go vet`, `go test ./...`.
- **`e2e.yml`** — **not on every PR**, resolving the "E2E is slower, may make sense to run less frequently" note from `03-frontend.md`: runs the Playwright + axe-core suite from `03-frontend.md` against the PR's actual Vercel preview URLs (frontend preview, correctly wired to the matching API preview via Related Projects above) once both preview deployments succeed, triggered via a `workflow_run`/deployment-status hook — plus a scheduled nightly run against `main`'s production-equivalent state as a backstop. Avoids paying Playwright's runtime cost on every single push while still catching integration regressions before merge and on a predictable cadence otherwise.
- **`secret-scan.yml`** — gitleaks (or equivalent) on every PR, repo-wide, not scoped to one app — the cheap backstop against a service-role key, Redis token, or Safe Browsing/Resend API key ending up in a commit, extending the project's existing security-by-design posture (`01-architecture.md`) into the tooling layer itself.

## Secrets management

No secrets committed to the repo; `.env.example` (no real values) per app, `.gitignore` covering `.env*`; `secret-scan.yml` above as the automated backstop.

- **Vercel project environment variables** (scoped Production / Preview / Development), split by project:
  - `apps/api`: Supabase connection string + service-role key, Supabase JWKS URL (`06-api-design.md`), Upstash Redis URL/token, Google Safe Browsing API key, a Vercel API token (used by the backend itself to call Vercel's Domain API on a user's behalf, per `02-external-services-and-hosting.md`).
  - `apps/web`: Supabase URL + anon key (for `supabase-js` on the client, per `03-frontend.md`); the API base URL is resolved automatically per-deployment via Related Projects above rather than being a manually-set secret.
- **GitHub Actions secrets** (a separate store from Vercel's): `SUPABASE_ACCESS_TOKEN` / `SUPABASE_DB_PASSWORD` / `SUPABASE_PROJECT_ID` for `db-migrate.yml`; `GITHUB_TOKEN` (automatic) for `release-cli.yml`.
- **Supabase dashboard setting**, not a repo/CI secret at all but still credential-like: the Resend API key, entered directly as the custom SMTP password per `02-external-services-and-hosting.md` — never touches the Go backend or CI.

## CLI release process

Decided: **SemVer git tags** (`vX.Y.Z`) trigger **goreleaser** via a `release-cli.yml` GitHub Actions workflow, cross-compiling `apps/cli` for the common OS/arch combinations (Linux/macOS/Windows, amd64/arm64) and publishing the binaries plus checksums to a GitHub Release. Distribution for v1: a simple install script (`curl ... | sh`, fetching the latest GitHub Release for the caller's platform) — a Homebrew tap or Scoop bucket is explicitly **deferred**, not needed until there's real external adoption to justify maintaining a second packaging surface.

This versioning applies only to the CLI, which is a distributed binary people install and hold onto a version of. `apps/api` and `apps/web` are **not** tag-released the same way — they deploy continuously via Vercel on every merge to `main`, the normal model for a hosted service with no separate "install a version" step for end users.

**Conventional Commits** adopted as the commit-message convention repo-wide, checked in CI but not configured as a hard merge-blocker for MVP — cheap to start now (unlocks automated changelog generation for CLI releases later, e.g. via `goreleaser`'s own changelog support) without adding friction for a small, informal early contributor base. Worth tightening (blocking, `release-please`-style automated version bumps) if/when the project gets real external contributors.

## Task runner: plain pnpm workspace, not Turborepo

Considered 2026-09-04 and declined for now, with a specific trigger to revisit.

Turborepo earns its keep on a dependency graph of packages with slow tasks worth caching. This repo has neither yet. `packages/api-client` is the only TypeScript package; `apps/web` is still empty. `apps/api` and `apps/cli` are separate Go modules outside the pnpm workspace entirely, so the slowest thing in the repo — a Go test suite around 40 seconds — is beyond a JS task runner's reach and is already path-filtered in `ci-api.yml`.

The root scripts are single global passes rather than per-package ones: `oxlint`, `oxfmt` and `tsc --noEmit` each cover the whole repository in one invocation. Measured on the tree as it stands, `pnpm lint` takes 1.15s and `pnpm typecheck` 0.44s. Adopting Turbo means decomposing those into per-package scripts for it to orchestrate — slower rather than faster at this size, and `tsc` would need project references to split at all. Against a 0.44s task, cache hashing and daemon startup are a net loss.

There is also no build step to cache. `api-client` ships TypeScript source, and Vercel builds `apps/web` and `apps/api` as two separate projects with their own Root Directory, so deploy orchestration already belongs to Vercel.

**Revisit when all three hold:** `apps/web` exists with a real build (a Vite/TanStack Start build is the first genuinely slow JS task); there are three or more TypeScript packages with dependencies between them, so `dependsOn: ["^build"]` expresses something real; and something slow runs repeatedly, such as Storybook or a Playwright suite. Vercel offers first-party remote caching for Turbo, which makes adopting it later cheap — the retrofit is a `turbo.json` plus moving scripts into packages, much less than maintaining orchestration config for a single package now.

## Not yet decided / to revisit

- `CONTRIBUTING.md` and issue/PR templates — open-source hygiene worth having, but not blocking any technical decision; reasonable to add once the codebase itself exists rather than now.
- Whether to tighten Conventional Commits enforcement (hard-blocking, automated version bumps) — deferred per above, revisit if the contributor base grows.
- Concrete `golangci-lint` ruleset and any frontend-specific ESLint/Prettier config specifics — implementation detail for when `Claude Code` scaffolds the actual repo, not a planning-level decision.
