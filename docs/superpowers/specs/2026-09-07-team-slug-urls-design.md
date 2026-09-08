# Team Slugs in Frontend URLs — Design

**Status:** approved 2026-09-07 **Amends:** `CLAUDE.md` (the data-model summary gains `team.slug`; a new convention records that the frontend addresses a team by slug while the API keeps UUIDs), `docs/planning/05-database-schema.md` (the `team` table), `docs/planning/06-api-design.md` (`POST /v1/teams` gains a required `slug`, `GET /v1/me` and every team response carry it).

The eighth implementation spec. Seven plans have merged: the instance is live, a maintainer creates a team, a Verein claims a custom domain, and links redirect from `go.kurze-url.app`.

Every authenticated page is addressed by the team's UUID: `/teams/6f1c8f0e-8a1f-4a5e-9c2b-1d3e4f5a6b7c/links`. That URL is unreadable, unspeakable over the phone, and unusable in a Verein's own documentation. It is also the first thing a new member of a Verein sees after signing in.

## Goal

A team is addressed in the browser by a short, human-readable slug — `/teams/sv-gruenwald/links` — that the maintainer chooses when the team is created and that never changes afterwards.

## Scope

### In scope

- A `team.slug` column: unique, immutable, format-checked in the database.
- `POST /v1/teams` takes the slug; the create form suggests one from the Verein's name.
- `GET /v1/me` and every team response carry the slug, so the frontend can resolve a slug to a team id without an extra request.
- Every authenticated route path changes from `$teamId` to `$teamSlug`.
- The remembered-team cookie switches from the UUID to the slug.

### Out of scope, and where each lands

- **Slug changes.** The slug is immutable. `PATCH /v1/teams/{team_id}` keeps renaming the team's display name only, and renaming does not touch the slug — a slug that follows the name would break every URL a Verein has written down, and this app has no redirect layer for its own pages. If a slug ever genuinely has to change, that is a maintainer running one `update` and telling the Verein, not a feature.
- **Slug-addressed API paths.** `/v1/teams/{team_id}` and every team-scoped route keep taking the UUID. Accepting either form would mean a resolution step in front of `internal/authz`'s membership check — a second way into every tenancy-critical path, for no gain the frontend can feel. The frontend resolves the slug itself, from a membership list it already loads.
- **Redirects from the old UUID URLs.** They break. The only holders of such a bookmark are the maintainer and the e2e suite. A UUID-shaped-parameter fallback is code that would never be removed.
- **Per-user or per-team vanity URLs beyond the team slug** (`/sv-gruenwald/links` without the `/teams/` prefix) — that is a different URL scheme, and it collides with every future top-level route.
- **Showing the slug anywhere in the UI besides the address bar and the create form.** It is in the URL, which is the point; a second rendering needs copy in two languages and explains nothing.

## Global constraints

Inherited and not re-litigated here:

- No RLS. Every query filters by `team_id`; the check lives in Go.
- A non-member gets 404, never 403 — which is exactly what an unknown slug gets too.
- The redirect path is the hot path and this plan does not touch it. `GET /{slug}` resolves a _link_ slug against `(domain_id, slug)` and never reads `team`.
- No hardcoded user-facing string; English and German ship together.
- WCAG 2.1 AA, gated in CI at two levels.

## The slug format

`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, 3 to 40 characters. Lowercase only, digits allowed, single hyphens inside, none at either end. The regex is the whole rule: no separate lowercase check is needed, because an uppercase letter simply fails it.

German Vereinsnamen are the normal input, not the exception, so the suggestion the create form offers transliterates rather than dropping characters: `ä/ö/ü` become `ae/oe/ue`, `ß` becomes `ss`, `&` becomes `-und-`, and a trailing `e.V.` is removed. "Sportverein Grünwald e.V." suggests `sportverein-gruenwald`, which the maintainer is free to shorten to `sv-gruenwald` before submitting.

The suggestion lives in TypeScript, in the form. The server never derives a slug: it validates format, rejects reserved values, and reports a collision. That keeps exactly one transliteration implementation in the running system — Go has none — and makes the maintainer, not a heuristic, responsible for the value that ends up in every URL.

### Uniqueness is global, and that is accepted

Link slugs are unique per `(domain_id, slug)`. Team slugs are unique full stop: two Vereine called "SV Grünwald" cannot both hold `sv-gruenwald`, and the second one needs a different value. Teams are created by the maintainer, in a conversation with the Verein that already had to happen for the team to exist — so a collision is a sentence in that conversation, not a failure path the product has to design around.

The slug also puts the Verein's name in the URL, where an opaque UUID used to be. Anyone who is shown a screenshot or sent a link learns which Verein it belongs to. The page itself stays closed — a non-member gets 404 from `internal/authz`, unchanged — and a readable URL is the entire purpose of the change, so this is accepted without mitigation. No prefix, no random suffix by default.

### Reserved slugs

The frontend has a static route segment under `/teams/`: `/teams/new`, the create-team form. TanStack Router matches a static segment before a dynamic one, so a team whose slug was `new` would be permanently unreachable — its URL would render the create form instead.

Two things address it, and both are wanted:

1. **The create form moves out of the namespace**, to `/new-team`. That removes the collision that exists today rather than declaring it off-limits.
2. **A denylist in Go** — `new`, `create`, `settings`, `admin`, `api`, `login`, `logout`, `me`, `invite`, `teams` — refuses those slugs at creation time. This is the part that covers the _next_ static child route someone adds under `/teams/`. Without it, adding a route is silently also a decision to strip one existing team of its URL, and nobody making that change would notice.

The denylist lives beside the only handler that creates teams, in `apps/api/internal/api/teams.go`. It is not in `internal/slug`: that package owns the link-slug alphabet and generator, and team slugs share neither the alphabet nor the rule.

## Resolution happens in the frontend

`GET /v1/me` already returns every membership with the team's name and the caller's role, and `_authed.tsx`'s `beforeLoad` loads it once for the whole authenticated tree. Adding `slug` to that payload makes slug-to-id resolution a lookup in data the page has already fetched.

Each team route's `beforeLoad` does the resolution and puts the id into route context:

```ts
beforeLoad: ({ context, params }) => ({
  teamId: requireTeamId(context.me.memberships, params.teamSlug),
}),
```

`requireTeamId` replaces `assertMembership` and keeps its contract: an unknown slug — a typo, or a team the caller has since left — throws the router's `notFound()`, never a "forbidden". The two questions were always answered by one lookup; now the lookup returns the thing the answer is needed for.

Loaders and components then read `context.teamId` for API calls and `params.teamSlug` for navigation. The server functions in `server/links.ts` and `server/domains.ts` are untouched: they take a UUID, and they keep taking one, which also leaves the React Query keys (`['links', teamId, page]`) as they are.

### The remembered-team cookie holds the slug

`resolveCurrentTeam` reads the `team` cookie, validates it against the current membership list, and falls back to the first membership. It switches from comparing `team_id` to comparing `slug`, because `/`'s redirect needs a slug to build the URL, and an id would have to be translated right back. The slug is immutable, so it is exactly as stable a cookie value as the UUID was.

Cookies written before this change hold a UUID. They fail the membership validation that is already there and fall back to the first team — the same path a cookie for a team the person has left already takes. No migration, no failure.

## Errors on the create form

The API reports three distinct problems on the slug, and the form shows all three on the field itself:

| Problem                   | Status | Shape                                                 |
| ------------------------- | ------ | ----------------------------------------------------- |
| Malformed or wrong length | 422    | Huma's own schema validation, `location: "body.slug"` |
| Reserved                  | 422    | `huma.ErrorDetail{Location: "body.slug"}`             |
| Already taken             | 409    | `huma.ErrorDetail{Location: "body.slug"}`             |

The first two need no frontend work at all: `classifyApiError` already turns a 422 carrying `ErrorDetail`s into `{ kind: 'fields' }`, keyed by the last segment of `location`, and the form already renders `fields.name` that way.

The 409 does need a new `ApiFailure` kind — `slugTaken` — because a 409 without a recognised detail currently collapses into `unknown`, which renders as "Something went wrong" and tells the maintainer nothing about the one thing they can fix. Detection keys on the typed `location`, never on the message text, for the same reason `deleteDomain`'s blocking-link count does: a reworded message must not silently break the classification. `CLAUDE.md`'s existing note about a 409 carrying a value keyed by a path parameter is the same pattern one level over — this one keys on a body field, and carries no value beyond the slug that was refused.

## What the database enforces

```sql
alter table team
  alter column slug set not null,
  add constraint team_slug_key unique (slug),
  add constraint team_slug_format check (slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
  add constraint team_slug_length check (length(slug) between 3 and 40);
```

The format lives in the schema and not only in Go for the reason `tag_team_id_name_lower_idx`'s migration writes out: the migration itself is a writer that does not go through Go, and it must not be the one that introduces a malformed slug. The denylist stays in Go — Postgres has no business knowing the frontend's route table.

### The backfill

`slug` is `not null` with no default, so the existing rows need values before the constraint lands. One migration does all of it: add the column nullable, derive a slug from `name` in SQL (the same transliteration the form's suggestion performs, written as nested `replace` calls plus one `regexp_replace`), disambiguate collisions with a numeric suffix ordered by `created_at`, fall back to `team-<first 8 hex of the id>` for a name that normalises to fewer than three characters, then tighten the column.

Production holds only the maintainer's own teams, so the derived values get one manual read-through after the migration lands rather than an exhaustive SQL implementation of the transliteration table.

### No default, and 23 test inserts

Twenty-three places in the Go tests insert a team with raw SQL — `insert into team (name) values ('fixture')` — across `internal/api`, `internal/db` and `internal/audit`. A `not null` slug breaks every one of them, and because they run against one shared database with literal names like `'t'` and `'links'`, a literal slug would collide between concurrent runs. Each of those inserts gets a unique slug expression instead:

```sql
insert into team (name, slug)
values ('fixture', 'fixture-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 12))
returning id
```

The rejected alternative was a column default that generates a random slug when none is given, which would have left all 23 inserts untouched. It also permanently converts "forgot the slug" from an error into a silently invented value. Twenty-three mechanical edits happen once; a soft default stays for the life of the schema. Same reasoning as the tag index: an invariant that lives in the schema cannot be forgotten by a later code path, and a default is precisely a way of forgetting it.

## Consequences to expect

- **The Preview database needs the migration applied by hand** before this branch's e2e can pass. `CLAUDE.md` already records why (preview branches are off; the GitHub integration applies migrations on merge to `main`), and this branch is exactly the shape that trips it: every query against `team` selects `slug`, so without it the authenticated pages fail with SQLSTATE 42703 while both Vercel deployments stay green.
- **`packages/api-client` must be regenerated** with the TypeScript 6 devDependency in place; `CLAUDE.md`'s note on why TypeScript 7 crashes the generator applies unchanged.
- **Old UUID URLs 404.** Deliberate, per the scope note above.
