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

The seed creates a verified `short.test` domain with a `hello` link, so:

```bash
curl -i -H 'Host: short.test' http://localhost:8080/hello   # 302 to example.org
curl -i http://localhost:8080/v1/health                     # 200, API surface
```

Regenerate the database layer after changing a migration or a query:

```bash
cd apps/api && sqlc generate
```
