# URL Shortener

A multi-tenant URL shortener for German non-profit associations ("Vereine"), run as one shared, open-source instance.

> **Status:** early development. The API foundation and the redirect hot path (`GET /<slug>`, password-protected links, rate limiting, analytics rollups) exist; the rest of the design is documented in `docs/planning/`.

## What it does

- Short links on shared or custom domains, with folders and tags
- Optional password protection and QR codes for every link
- Privacy-friendly click analytics — daily aggregates only, no raw click log and no stored IP addresses
- Teams with roles, invitations, and an audit log
- English and German user interface

## Planned stack

| Layer    | Choice                                    |
| -------- | ----------------------------------------- |
| Backend  | Go (chi + Huma), sqlc for database access |
| Database | Supabase (Postgres), EU region            |
| Cache    | Upstash Redis                             |
| Frontend | TanStack Start (React) with shadcn/ui     |
| CLI      | Go, over the same HTTP API                |
| Hosting  | Vercel                                    |

## Repository layout

```
apps/
  api/            # Go backend
  web/            # TanStack Start frontend
  cli/            # Go CLI
packages/
  api-client/     # TypeScript client generated from the OpenAPI spec
supabase/         # Database migrations
docs/planning/    # Architecture and design documents
```

## Documentation

Start at [`docs/planning/00-index.md`](docs/planning/00-index.md) for a map of the design documents and the decision log. [`CLAUDE.md`](CLAUDE.md) holds the condensed conventions that apply across the whole repository.

## License

MIT

## Running the stack locally

Prerequisites: Go 1.27+, Node 24+, Docker, the [Supabase CLI](https://supabase.com/docs/guides/cli), and [sqlc](https://sqlc.dev).

```bash
# 1. Configure the two env files
cp apps/api/.env.example apps/api/.env        # then set VISITOR_SALT
cp apps/web/.env.example apps/web/.env.local  # then set the Supabase values below

# 2. Generate the local JWT signing key (see "Why a signing key" below)
printf '[]' > supabase/signing_keys.json
supabase gen signing-key --algorithm ES256 --append

# 3. Start everything
pnpm dev
```

`pnpm dev` runs `scripts/dev.ts`: it checks the prerequisites, starts Supabase and a Redis container if they are not already up, waits for both to answer, starts the Go API with `apps/api/.env` exported into its environment, waits for `/health`, and then starts the web app. Ctrl-C stops the API and the web app; Supabase and Redis stay up so the next run starts in seconds and the local database survives. `pnpm dev:stop` shuts those two down, and `pnpm db:reset` re-applies the migrations and the seed — which drops everything else in the local database, so it is deliberately not part of `pnpm dev`.

The API reads `os.Getenv` and has no dotenv dependency of its own. `pnpm dev` is what exports the file for it; running the binary by hand needs `cd apps/api && set -a && . ./.env && set +a && go run ./cmd/api`.

### Pointing both halves at the local Supabase

`supabase status` prints the values. In `apps/web/.env.local`:

```
SUPABASE_URL=http://127.0.0.1:54321
SUPABASE_PUBLISHABLE_KEY=<the PUBLISHABLE_KEY from `supabase status`>
```

And in `apps/api/.env`:

```
SUPABASE_JWKS_URL=http://127.0.0.1:54321/auth/v1/.well-known/jwks.json
SUPABASE_JWT_ISSUER=http://127.0.0.1:54321/auth/v1
```

Both halves must name the same Supabase instance. They do not have to — the web app signs a visitor in against whatever `SUPABASE_URL` says while the API verifies tokens against `SUPABASE_JWKS_URL` — and when they disagree, login succeeds and every `/v1` call answers 401. Worse, `team_member.user_id` is a foreign key into `auth.users`, so a user id minted by a hosted project cannot be written into a local database at all: creating a team fails on the constraint. `pnpm dev` refuses to start when the two origins differ.

### Why a signing key

The API verifies ES256 through JWKS and rejects the legacy HS256 shared secret. A local stack signs HS256 until `signing_keys_path` is set in `supabase/config.toml` (it is, in this repository) **and** the file it names exists — so without the key, sign-in works and every authenticated request is refused.

Generate it with the two commands above, never with a plain redirect: `supabase gen signing-key … > supabase/signing_keys.json` truncates the file before the CLI reads it, and the CLI reads the configured path before it generates anything, so that form always fails. The file holds private key material and is gitignored.

### Signing in

The seed creates `dev@example.test` and gives it the `dev-verein` team. Sign in with that address at http://localhost:3000/login; the magic link arrives in the local mail catcher at http://127.0.0.1:54324. No other address works — `signInWithOtp` runs with `shouldCreateUser: false`, so an unknown address never becomes an account.

The seed also creates a verified `short.test` domain with a `hello` link, so:

```bash
curl -i -H 'Host: short.test' http://localhost:8080/hello   # 302 to example.org
curl -i http://localhost:8080/v1/health                     # 200, API surface
```

Regenerate the database layer after changing a migration or a query:

```bash
cd apps/api && sqlc generate
```

### Teams and invitations

`POST /v1/teams` is restricted to the maintainer allowlist. Locally that is the seeded user, whose id is a fixed literal in `supabase/seed.sql` — in `apps/api/.env`:

```
MAINTAINER_USER_IDS=00000000-0000-0000-0000-0000000000a1
```

Only needed to create _further_ teams; the seed already gives that user `dev-verein`. For any other user, read the id out of the database the stack is actually using:

```bash
psql "$DATABASE_URL" -c "select id, email from auth.users;"
```

Invitation emails need `SUPABASE_SERVICE_ROLE_KEY` (and `SUPABASE_AUTH_URL`, if the project's auth URL differs from `SUPABASE_JWT_ISSUER`). Without it the API still runs: adding an address that already has an account works, and an unknown address is refused with 503.
