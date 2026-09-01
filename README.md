# URL Shortener

A multi-tenant URL shortener for German non-profit associations ("Vereine"), run as one shared, open-source instance.

> **Status:** planning. The design is documented, but no application code exists yet.

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
