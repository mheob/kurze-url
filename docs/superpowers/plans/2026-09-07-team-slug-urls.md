# Team Slugs in Frontend URLs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team is addressed in the browser by a short, immutable, human-readable slug — `/teams/sv-gruenwald/links` instead of `/teams/6f1c8f0e-…/links`.

**Architecture:** `team` gains a unique, format-checked `slug`. The maintainer chooses it when creating the team; the create form suggests one transliterated from the Verein's name. `GET /v1/me` carries the slug for every membership, so each team route's `beforeLoad` resolves `params.teamSlug` to a team id out of data the authenticated tree has already loaded, and puts that id into route context. Every API path keeps taking the UUID — nothing about tenancy resolution moves into the API.

**Tech Stack:** Go (chi + Huma + sqlc + pgx), Postgres via Supabase, TanStack Start + Router/Query/Form, Vitest + RTL, Playwright + axe-core.

**Spec:** `docs/superpowers/specs/2026-09-07-team-slug-urls-design.md`

## Global Constraints

Copied from the spec and from `CLAUDE.md`; every task's requirements include these.

- **There is no RLS.** Every query filters by `team_id`. This plan adds no new query path, but any query it edits keeps its filter exactly as it is.
- **A non-member gets 404, never 403** — and so does an unknown or mistyped slug. `requireTeamId` throws the router's `notFound()`.
- **The tenant is called `team`** in every identifier. "Verein" appears only in German user-facing copy.
- **The redirect path is untouched.** No task edits `GET /{slug}`, and no new query runs on it. `link.slug` and `team.slug` are different namespaces with different alphabets; nothing in this plan changes link slugs.
- **The slug format is `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, length 3–40.** Exactly this regex, in the database check, in the Huma `pattern` tag, and in the frontend validator.
- **The slug is immutable.** No endpoint updates it; `PATCH /v1/teams/{team_id}` keeps renaming the display name only.
- **No hardcoded user-facing string.** Every new string lands in both `apps/web/src/i18n/locales/en.json` and `de.json`; `catalogues.test.ts` proves the two stay in step.
- **WCAG 2.1 AA**, gated in CI by the Storybook a11y addon and by axe-core in the e2e suite. The new slug input needs a `<label>`, and its error needs `aria-describedby` + `aria-invalid`, the same as the name field beside it.
- **Version control goes through GitButler.** Every commit step uses `but commit`, never `git commit`. Conventional Commits, **max 50 characters including type and scope**. No co-author or generator footer.
- **Lint and format are oxlint and oxfmt**, never ESLint or Prettier. Run `pnpm format` before every commit that touches JS/TS.
- **`packages/api-client` regenerates through `pnpm generate:api`** from the repo root, which needs the pinned TypeScript 6 devDependency — see `CLAUDE.md` on why TypeScript 7 crashes the generator.
- **Go tests need the local Supabase Postgres** (`supabase start`); they skip with a message when it is absent, so a green `go test ./...` on a machine with no database proves nothing.
- **This branch cannot pass e2e until its migration is applied to the Preview database by hand.** See `CLAUDE.md`; every query against `team` selects `slug`, so without it the authenticated pages fail with SQLSTATE 42703 while both Vercel deployments stay green.

---

## File Structure

**API and database**

| File | Responsibility |
| --- | --- |
| `supabase/migrations/<ts>_team_slug.sql` | Add `slug`, backfill from `name`, tighten to `not null` + unique + format/length checks |
| `apps/api/internal/db/queries/team.sql` | Five queries select or accept `slug` |
| `apps/api/internal/api/teams.go` | `Team.Slug`, `CreateTeamInput.Body.Slug`, reserved-slug denylist, 409 on collision |
| `apps/api/internal/api/me.go` | `TeamMembership.Slug` |
| 12 Go test files | 23 raw `insert into team (name)` statements gain a unique slug expression |

**Web**

| File | Responsibility |
| --- | --- |
| `apps/web/src/lib/team-slug.ts` | `suggestTeamSlug(name)` — the only transliteration in the running system |
| `apps/web/src/lib/api-errors.ts` | New `slugTaken` failure kind, keyed on `location: "body.slug"` |
| `apps/web/src/lib/current-team.ts` | The `team` cookie holds a slug |
| `apps/web/src/routes/_authed.tsx` | `Membership.slug`; `requireTeamId` replaces `assertMembership` |
| `apps/web/src/routes/_authed/teams.$teamSlug.*` | Four routes renamed; `beforeLoad` resolves the id into route context |
| `apps/web/src/routes/_authed/new-team.tsx` | Moved out of `/teams/`; gains the slug field |
| `apps/web/src/routes/index.tsx` | Redirect target is a slug |
| `apps/web/src/components/{authed-shell,team-switcher,link-list}.tsx` | Navigation by slug |
| `apps/web/src/server/teams.ts` | `createTeamFor` takes name and slug |
| `apps/web/e2e/fixtures/auth.ts` | Fixture team gets a slug; new `teamSlug` fixture |

---

### Task 1: The migration, and the 23 test inserts it breaks

**Files:**

- Create: `supabase/migrations/<timestamp>_team_slug.sql`
- Modify: `apps/api/internal/api/{testhelper,teams,tenancy,auditlog,bootstrap}_test.go`, `apps/api/internal/db/{schema,queries,tenancy,link_crud,shared_domain}_test.go`, `apps/api/internal/audit/audit_test.go` — every `insert into team (name)`
- Test: `apps/api/internal/db/schema_test.go` (new constraint test)

**Interfaces:**

- Consumes: nothing.
- Produces: `team.slug text not null`, constraints `team_slug_key` (unique), `team_slug_format`, `team_slug_length`. Every existing row has a slug derived from its name.

- [ ] **Step 1: Create the migration file**

```bash
supabase migration new team_slug
```

- [ ] **Step 2: Write the migration**

Paste this into the generated file (the timestamped one under `supabase/migrations/`):

```sql
-- Teams are addressed by slug in the frontend's URLs: /teams/sv-gruenwald/links
-- rather than /teams/6f1c8f0e-…/links. The API keeps taking the UUID
-- everywhere; the slug exists so that a URL can be read out loud, put in a
-- Verein's own documentation, and recognised by the person who receives it.
--
-- The format check lives here and not only in Go for the same reason
-- tag_team_id_name_lower_idx does: the backfill below is itself a writer that
-- does not go through Go, and it must not be the writer that introduces a
-- malformed slug. The reserved-slug denylist stays in Go — Postgres has no
-- business knowing the frontend's route table.
alter table team add column slug text;

-- Backfill. Nested replace() calls do the German transliteration a translate()
-- cannot (it maps single characters, and ä has to become two). The suffix
-- disambiguates two Vereine that normalise to the same value, oldest first, so
-- the result is stable if this ever runs twice against the same data. A name
-- that normalises to fewer than three characters falls back to the row's own
-- id, which is always long enough and always unique.
with normalized as (
  select
    id,
    created_at,
    trim(both '-' from left(
      regexp_replace(
        replace(replace(replace(replace(replace(
          regexp_replace(lower(name), '\s+e\.?\s*v\.?\s*$', ''),
          'ä', 'ae'), 'ö', 'oe'), 'ü', 'ue'), 'ß', 'ss'), '&', '-und-'),
        '[^a-z0-9]+', '-', 'g'),
      34)) as base
  from team
),
padded as (
  select
    id,
    created_at,
    case
      when length(base) >= 3 then base
      else 'team-' || substr(replace(id::text, '-', ''), 1, 8)
    end as base
  from normalized
),
numbered as (
  select
    id,
    base,
    row_number() over (partition by base order by created_at, id) as n
  from padded
)
update team t
set slug = case when numbered.n = 1 then numbered.base else numbered.base || '-' || numbered.n end
from numbered
where numbered.id = t.id;

alter table team
  alter column slug set not null,
  add constraint team_slug_key unique (slug),
  add constraint team_slug_format check (slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
  add constraint team_slug_length check (length(slug) between 3 and 40);

comment on column team.slug is
  'Immutable, globally unique, human-readable identifier used in the frontend''s URLs. The API addresses teams by id.';
```

- [ ] **Step 3: Write the failing constraint test**

Append to `apps/api/internal/db/schema_test.go`:

```go
func TestTeamSlugRejectsMalformedAndDuplicateValues(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	insert := `insert into team (name, slug) values ('t', $1)`

	_, err = tx.Exec(ctx, insert, "sv-gruenwald")
	require.NoError(t, err, "a well-formed slug is accepted")

	_, err = tx.Exec(ctx, insert, "sv-gruenwald")
	require.Error(t, err, "slugs are globally unique, not unique per anything")

	for _, malformed := range []string{"SV-Gruenwald", "-leading", "trailing-", "sv_gruenwald", "ab"} {
		_, err = tx.Exec(ctx, `savepoint s`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, insert, malformed)
		require.Error(t, err, "the schema must reject %q, not only Go", malformed)
		_, err = tx.Exec(ctx, `rollback to savepoint s`)
		require.NoError(t, err)
	}
}
```

- [ ] **Step 4: Run it against a database without the migration to see it fail**

Run: `cd apps/api && go test ./internal/db/ -run TestTeamSlugRejects -v` Expected: FAIL with `column "slug" of relation "team" does not exist` (SQLSTATE 42703).

- [ ] **Step 5: Apply the migration locally**

Run: `supabase db reset` Expected: every migration replays, the new one included, with no error.

- [ ] **Step 6: Run the constraint test again**

Run: `cd apps/api && go test ./internal/db/ -run TestTeamSlugRejects -v` Expected: PASS.

- [ ] **Step 7: See the rest of the suite break**

Run: `cd apps/api && go test ./... 2>&1 | head -40` Expected: many failures reading `null value in column "slug" of relation "team" violates not-null constraint`.

- [ ] **Step 8: Fix all 23 raw inserts**

Every `insert into team (name) values (…)` in a `_test.go` file becomes an insert that also names a unique slug. Find them:

```bash
grep -rn "insert into team (name)" apps/api --include='*.go' | grep _test
```

For a literal name:

```go
`insert into team (name, slug)
 values ('fixture', 'fixture-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 12))
 returning id`
```

For a parameterised name (`$1`), keep the parameter and generate the slug in SQL:

```go
`insert into team (name, slug)
 values ($1, 'verein-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 12))
 returning id`
```

The random suffix is not decoration: these tests share one database and run concurrently, so a literal slug would collide between two runs of the same test file. Twelve characters of hex is 48 bits, and the rows are deleted at the end of each test.

- [ ] **Step 9: Run the whole Go suite**

Run: `cd apps/api && go test ./...` Expected: PASS, all packages.

- [ ] **Step 10: Read the backfilled values**

Run:

```bash
psql "postgres://postgres:postgres@127.0.0.1:54322/postgres" -c "select name, slug from team order by created_at"
```

Expected: each slug is lowercase, hyphen-separated, derived from the name, with umlauts transliterated. This is the manual check the spec calls for; do the same read against Production after merge.

- [ ] **Step 11: Commit**

```bash
but commit -m "feat(db): add an immutable team slug"
```

---

### Task 2: The slug through the API

**Files:**

- Modify: `apps/api/internal/db/queries/team.sql`, `apps/api/internal/api/teams.go`, `apps/api/internal/api/me.go`
- Test: `apps/api/internal/api/teams_test.go`

**Interfaces:**

- Consumes: `team.slug` from Task 1.
- Produces: `api.Team{ID, Name, Slug, CreatedAt, Role}`; `api.TeamMembership{TeamID, Name, Slug, Role}`; `POST /v1/teams` requires `{"name": string, "slug": string}` and answers 422 for a malformed or reserved slug, 409 for a taken one, both carrying `ErrorDetail{Location: "body.slug"}`.

- [ ] **Step 1: Write the failing API tests**

Append to `apps/api/internal/api/teams_test.go`:

```go
func TestCreateTeamStoresTheSlugAndReportsItBack(t *testing.T) {
	f := newTenancyFixture(t)
	slug := "verein-" + uuid.NewString()[:8]

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein", "slug": slug})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	created := decode[api.Team](t, rec)
	require.Equal(t, slug, created.Slug)

	t.Cleanup(func() {
		_, _ = f.pool.Exec(t.Context(), `delete from team where id = $1`, created.ID)
	})

	var stored string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select slug from team where id = $1`, created.ID).Scan(&stored))
	require.Equal(t, slug, stored)
}

func TestCreateTeamRejectsAMalformedSlug(t *testing.T) {
	f := newTenancyFixture(t)

	for _, malformed := range []string{"SV-Gruenwald", "sv_gruenwald", "-leading", "ab"} {
		rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams",
			map[string]string{"name": "Neuer Verein", "slug": malformed})

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"%q must be refused by the request schema", malformed)
	}
}

func TestCreateTeamRefusesAReservedSlug(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein", "slug": "new"})

	// 422, and located on the field, so the create form can render the reason
	// next to the input rather than as a page-level "something went wrong".
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "body.slug")
}

func TestCreateTeamReportsATakenSlugAsAConflict(t *testing.T) {
	f := newTenancyFixture(t)
	slug := "verein-" + uuid.NewString()[:8]
	body := map[string]string{"name": "Neuer Verein", "slug": slug}

	first := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams", body)
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())
	created := decode[api.Team](t, first)
	t.Cleanup(func() {
		_, _ = f.pool.Exec(t.Context(), `delete from team where id = $1`, created.ID)
	})

	second := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams", body)

	require.Equal(t, http.StatusConflict, second.Code)
	// The frontend keys on the typed location, never on the message text, so
	// this assertion is what a reworded message must not break.
	require.Contains(t, second.Body.String(), "body.slug")
}

func TestMeCarriesTheTeamSlug(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/me", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Memberships []api.TeamMembership `json:"memberships"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.Memberships)
	require.NotEmpty(t, body.Memberships[0].Slug,
		"the frontend resolves a URL slug out of this payload; an empty slug makes every team page unreachable")
}
```

Add `"encoding/json"` to that file's imports if it is not already there.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestCreateTeam|TestMeCarries' -v` Expected: FAIL — `api.Team` has no field `Slug`, and the create tests get 422 for an unknown body field or 201 without a slug.

- [ ] **Step 3: Extend the queries**

In `apps/api/internal/db/queries/team.sql`:

```sql
-- name: CreateTeam :one
insert into team (name, slug) values ($1, $2)
returning id, name, slug, created_at;

-- name: GetTeam :one
select id, name, slug, created_at from team where id = $1;

-- name: RenameTeam :one
update team set name = $2 where id = $1
returning id, name, slug, created_at;
```

`ListTeamsForUser` selects `t.slug` alongside `t.name`; `ListMembershipsForUser` selects `t.slug as team_slug`. Both keep their `where` clauses and ordering untouched.

The slug is deliberately absent from every `update`: it is immutable, and `RenameTeam` returns it only so the response can echo the row it just wrote.

- [ ] **Step 4: Regenerate sqlc**

Run: `cd apps/api && sqlc generate` Expected: `internal/db/team.sql.go` now has `CreateTeamParams{Name, Slug}`, and `Team`/`ListTeamsForUserRow` carry `Slug`; `ListMembershipsForUserRow` carries `TeamSlug`.

- [ ] **Step 5: Extend the API types and handlers**

In `apps/api/internal/api/me.go`:

```go
type TeamMembership struct {
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
	Slug   string    `json:"slug"`
	Role   string    `json:"role"`
}
```

and in the loop that builds it, `Slug: row.TeamSlug`.

In `apps/api/internal/api/teams.go`, `Team` gains `Slug string \`json:"slug"\`` and every construction of it (`createTeam`, `listTeams`, `getTeam`, `updateTeam`) fills it from the row. `CreateTeamInput` gains the field:

```go
type CreateTeamInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"200" doc:"The Verein's display name."`
		Slug string `json:"slug" minLength:"3" maxLength:"40" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" doc:"Immutable identifier used in the app's URLs, e.g. \"sv-gruenwald\"."`
	}
}
```

The denylist and its check, beside the handler:

```go
// reservedTeamSlugs may not be taken, because the frontend has static route
// segments that would shadow them. TanStack Router matches a static segment
// before a dynamic one, so a team holding one of these would have its pages
// permanently answered by another screen. The create form itself has already
// moved out of /teams/, so this list is the guard for the *next* static child
// route someone adds there — without it, adding a route is silently also a
// decision to strip an existing team of its URL.
var reservedTeamSlugs = map[string]struct{}{
	"new": {}, "create": {}, "settings": {}, "admin": {}, "api": {},
	"login": {}, "logout": {}, "me": {}, "invite": {}, "teams": {},
}
```

At the top of `createTeam`, after the maintainer check:

```go
if _, reserved := reservedTeamSlugs[in.Body.Slug]; reserved {
	return nil, huma.Error422UnprocessableEntity("that slug is reserved",
		&huma.ErrorDetail{
			Location: "body.slug",
			Message:  "this slug is reserved; choose another",
			Value:    in.Body.Slug,
		})
}
```

`q.CreateTeam` now takes params:

```go
team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: in.Body.Name, Slug: in.Body.Slug})
```

and the audit entry's metadata carries both values: `map[string]any{"name": team.Name, "slug": team.Slug}`.

The error handling after `db.InTx` becomes a switch, the same shape `createTag` uses:

```go
switch {
case isUniqueViolation(err):
	return nil, huma.Error409Conflict("a team with that slug already exists",
		&huma.ErrorDetail{
			Location: "body.slug",
			Message:  "this slug is already taken",
			Value:    in.Body.Slug,
		})
case err != nil:
	d.Log.Error("create team", "error", err)
	return nil, huma.Error500InternalServerError("could not create the team")
}
```

- [ ] **Step 6: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -run 'TestCreateTeam|TestMeCarries' -v` Expected: PASS.

- [ ] **Step 7: Run the whole Go suite and the linter**

Run: `cd apps/api && go vet ./... && golangci-lint run && go test ./...` Expected: PASS.

- [ ] **Step 8: Commit**

```bash
but commit -m "feat(api): expose and require the team slug"
```

---

### Task 3: The generated client and the two pure helpers

**Files:**

- Create: `apps/web/src/lib/team-slug.ts`, `apps/web/src/lib/team-slug.test.ts`
- Modify: `apps/api/openapi.json` and `packages/api-client/src/generated/**` (both generated), `apps/web/src/lib/api-errors.ts`
- Test: `apps/web/src/lib/api-errors.test.ts`

**Interfaces:**

- Consumes: Task 2's API surface.
- Produces: `suggestTeamSlug(name: string): string`; `ApiFailure` gains `{ kind: 'slugTaken' }`. Generated `Team` and `TeamMembership` types carry `slug`.

- [ ] **Step 1: Regenerate the spec and the client**

Run: `pnpm generate:api` Expected: `apps/api/openapi.json` and `packages/api-client/src/generated/**` change; `Team.slug`, `TeamMembership.slug` and `CreateTeamInputBody.slug` appear. If the generator crashes, check that `packages/api-client`'s TypeScript 6 devDependency is installed — see `CLAUDE.md`.

- [ ] **Step 2: Write the failing test for the suggestion**

Create `apps/web/src/lib/team-slug.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { suggestTeamSlug } from './team-slug';

describe('suggestTeamSlug', () => {
	it('transliterates German umlauts rather than dropping them', () => {
		expect(suggestTeamSlug('Sportverein Grünwald')).toBe('sportverein-gruenwald');
		expect(suggestTeamSlug('Schützenverein Höchstädt')).toBe('schuetzenverein-hoechstaedt');
		expect(suggestTeamSlug('Fußballclub')).toBe('fussballclub');
	});

	it('drops a trailing legal form', () => {
		expect(suggestTeamSlug('Sportverein Grünwald e.V.')).toBe('sportverein-gruenwald');
		expect(suggestTeamSlug('Turnverein 1899 e. V.')).toBe('turnverein-1899');
	});

	it('spells out an ampersand, because German reads it as a word', () => {
		expect(suggestTeamSlug('Sport & Spiel')).toBe('sport-und-spiel');
	});

	it('collapses punctuation and whitespace into single hyphens', () => {
		expect(suggestTeamSlug('  TSV   Ober-/Unterdorf  ')).toBe('tsv-ober-unterdorf');
	});

	/**
	 * The truncation must not leave the trailing hyphen a cut through a word
	 * produces: the format the API enforces forbids one, so a suggestion that
	 * ends in `-` would be refused by the server the moment it is submitted
	 * unchanged.
	 */
	it('truncates to the 40-character limit without a trailing hyphen', () => {
		const suggestion = suggestTeamSlug('Verein zur Foerderung des ' + 'langen Namens im Dorfe');
		expect(suggestion.length).toBeLessThanOrEqual(40);
		expect(suggestion).not.toMatch(/-$/);
	});

	it('returns an empty string when nothing usable is left', () => {
		expect(suggestTeamSlug('!!!')).toBe('');
	});
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd apps/web && pnpm test -- team-slug` Expected: FAIL — cannot resolve `./team-slug`.

- [ ] **Step 4: Implement the suggestion**

Create `apps/web/src/lib/team-slug.ts`:

```ts
/**
 * Suggests a URL slug for a Verein's name. This is the only transliteration in
 * the running system: the Go API validates the format, rejects reserved values
 * and reports collisions, but never derives a slug — the maintainer submits the
 * value that ends up in every URL, and this only saves them the typing.
 *
 * German names are the normal input. `ä/ö/ü/ß` become `ae/oe/ue/ss` rather than
 * being dropped, because "grnwald" is not a name anybody recognises, and `&`
 * becomes `und` because that is how the name is read out loud. A trailing legal
 * form (`e.V.`, `e. V.`) carries no information in a URL and goes.
 *
 * The result can be empty — a name of nothing but punctuation has no slug — so
 * the form treats it as a suggestion, not a value it may submit unchecked.
 */
const TRANSLITERATIONS: readonly (readonly [RegExp, string])[] = [
	[/ä/g, 'ae'],
	[/ö/g, 'oe'],
	[/ü/g, 'ue'],
	[/ß/g, 'ss'],
	[/&/g, '-und-'],
];

export const TEAM_SLUG_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
export const TEAM_SLUG_MIN_LENGTH = 3;
export const TEAM_SLUG_MAX_LENGTH = 40;

export function suggestTeamSlug(name: string): string {
	let slug = name.toLowerCase().replace(/\s+e\.?\s*v\.?\s*$/, '');

	for (const [pattern, replacement] of TRANSLITERATIONS) {
		slug = slug.replace(pattern, replacement);
	}

	return slug
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, TEAM_SLUG_MAX_LENGTH)
		.replace(/-+$/, '');
}
```

- [ ] **Step 5: Run the test**

Run: `cd apps/web && pnpm test -- team-slug` Expected: PASS.

- [ ] **Step 6: Write the failing test for the new failure kind**

Append to `apps/web/src/lib/api-errors.test.ts`:

```ts
describe('a slug conflict', () => {
	it('is its own kind, not the generic unknown failure', () => {
		expect(
			classifyApiError({
				status: 409,
				detail: 'a team with that slug already exists',
				errors: [{ location: 'body.slug', message: 'this slug is already taken' }],
			}),
		).toEqual({ kind: 'slugTaken' });
	});

	/**
	 * Keyed on the typed location, never on the prose: a 409 that carries no
	 * recognised detail — the domain verify endpoint's "another team already
	 * verified this hostname" — must keep collapsing into `unknown`, or every
	 * such call site would start rendering a message about slugs.
	 */
	it('does not swallow a conflict that carries no field detail', () => {
		expect(classifyApiError({ status: 409, detail: 'already verified elsewhere' })).toEqual({
			kind: 'unknown',
		});
	});
});
```

- [ ] **Step 7: Run it to verify it fails**

Run: `cd apps/web && pnpm test -- api-errors` Expected: FAIL — the first case returns `{ kind: 'unknown' }`.

- [ ] **Step 8: Add the kind**

In `apps/web/src/lib/api-errors.ts`, extend the union with `| { kind: 'slugTaken' }`, add the predicate beside `blockingLinkCountOf`:

```ts
/**
 * `createTeam` answers a taken slug with 409 and a typed detail on the field,
 * the same convention `deleteDomain`'s blocking-link count uses one level over
 * (`path.domain_id` there, a body field here). Reading `location` rather than
 * matching the message means a reworded message cannot silently turn this back
 * into a generic failure — and there is deliberately no text fallback, since
 * that is exactly how such drift goes unnoticed.
 */
function isSlugConflict(error: unknown): boolean {
	return problemDetailsOf(error).some((detail) => detail.location === 'body.slug');
}
```

and inside `classifyApiError`'s `status === 409` branch, after the count check:

```ts
if (isSlugConflict(error)) return { kind: 'slugTaken' };
```

- [ ] **Step 9: Run the frontend unit suite**

Run: `cd apps/web && pnpm test` Expected: PASS.

- [ ] **Step 10: Format, lint, typecheck, commit**

```bash
pnpm format && pnpm lint && pnpm typecheck
but commit -m "feat(web): add slug helpers and codegen"
```

---

### Task 4: The URL cutover

Every authenticated route path changes at once. A URL scheme cannot half-change: the routes, the guard that resolves them, the cookie that remembers one, and every `<Link>` that builds one are a single unit.

**Files:**

- Rename: `apps/web/src/routes/_authed/teams.$teamId.links.index.tsx` → `teams.$teamSlug.links.index.tsx`, plus `.links.new.tsx`, `.links.$linkId.tsx`, `.domains.tsx`, and the four test files that sit beside them (`teams.$teamSlug.links.index.test.ts`, `teams.$teamSlug.links.index.error.test.tsx`, `teams.$teamSlug.links.new.test.ts`, `teams.$teamSlug.links.$linkId.test.ts`)
- Modify: `apps/web/src/routes/_authed.tsx`, `_authed.test.ts`, `routes/index.tsx`, `routes/index.test.ts`, `lib/current-team.ts`, `lib/current-team.test.ts`, `components/authed-shell.tsx`, `authed-shell.test.tsx`, `components/team-switcher.tsx`, `team-switcher.test.tsx`, `components/link-list.tsx`, `link-list.test.tsx`, `link-list.stories.tsx`, `routeTree.gen.ts` (generated)

**Interfaces:**

- Consumes: `Membership.slug` from the regenerated client (Task 3).
- Produces: `requireTeamId(memberships: Membership[], teamSlug: string): string`; route context `{ teamId: string }` on every team route; route paths `/_authed/teams/$teamSlug/...`; `resolveCurrentTeam` returns a slug.

- [ ] **Step 1: Write the failing guard test**

Replace the `assertMembership` block in `apps/web/src/routes/_authed.test.ts` (keep the `thrown` helper and its docstring):

```ts
const memberships = [{ team_id: 'a', name: 'Verein A', role: 'owner', slug: 'verein-a' }];

describe('requireTeamId', () => {
	it('resolves a slug you belong to to that team id', () => {
		expect(requireTeamId(memberships, 'verein-a')).toBe('a');
	});

	/**
	 * 404, never 403 — unchanged from `assertMembership`, which this replaces.
	 * `internal/authz` answers a non-member with 404 so the API never confirms
	 * that a team exists; a frontend rendering "forbidden" here would leak
	 * exactly what the API withholds. An unknown slug is the same answer as a
	 * team you have left: `isNotFound` is what tells that apart from a generic
	 * thrown error, and from nothing thrown at all.
	 */
	it('throws a not-found, not a generic error, for a slug you do not belong to', () => {
		expect(isNotFound(thrown(() => requireTeamId(memberships, 'verein-b')))).toBe(true);
	});
});
```

Update the import to `requireTeamId`.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/web && pnpm test -- _authed` Expected: FAIL — `requireTeamId` is not exported.

- [ ] **Step 3: Replace the guard**

In `apps/web/src/routes/_authed.tsx`: add `slug: string;` to `Membership`, and replace `assertMembership` with

```ts
/**
 * 404, never 403: `internal/authz` in the Go API already answers a non-member
 * with 404, never 403, so the API itself never confirms that a team exists at
 * all. A frontend that rendered "forbidden" here would leak exactly the
 * information the API withholds — so this throws the router's own `notFound()`,
 * and the test file asserts on that distinction with `isNotFound` rather than a
 * bare `.toThrow()`.
 *
 * It returns the team id rather than only asserting, because the URL now
 * carries the slug while every API call still takes the UUID. Both questions —
 * "may this caller be here" and "which team is this" — are answered by one
 * lookup in the membership list `_authed`'s `beforeLoad` has already fetched,
 * so nothing here costs a request.
 */
export function requireTeamId(memberships: Membership[], teamSlug: string): string {
	const membership = memberships.find((entry) => entry.slug === teamSlug);
	if (!membership) throw notFound();
	return membership.team_id;
}
```

In `fetchMe`, the `memberships` normalisation stays as it is — the generated type now includes `slug`, so nothing to add.

`AuthedLayout` reads the slug from the URL instead of the id:

```ts
const { teamSlug } = useParams({ strict: false });
```

and passes `currentTeamSlug={teamSlug ?? me.memberships[0]?.slug}`.

- [ ] **Step 4: Run the guard test**

Run: `cd apps/web && pnpm test -- _authed` Expected: PASS.

- [ ] **Step 5: Move the cookie to the slug**

In `apps/web/src/lib/current-team.ts`: `resolveCurrentTeam` compares `membership.slug === remembered` and falls back to `memberships[0]?.slug`; `teamCookie(teamSlug: string)`. Extend the docstring on `resolveCurrentTeam` with why the slug is the stored value:

```
 * The stored value is the team's slug, not its id: `/`'s redirect builds a URL
 * from it, and an id would have to be translated straight back. The slug is
 * immutable, so it is exactly as stable a cookie value as the UUID was — and a
 * cookie written before the slug existed simply fails the membership check
 * below and falls back, the same path a cookie for a team you have left takes.
```

Update `apps/web/src/lib/current-team.test.ts`: its membership fixtures gain `slug`, and every expectation switches from an id to the matching slug.

- [ ] **Step 6: Rename the four route files**

```bash
cd apps/web/src/routes/_authed
for f in teams.\$teamId.*; do but mv "$f" "${f/teamId/teamSlug}" 2>/dev/null || mv "$f" "${f/teamId/teamSlug}"; done
```

- [ ] **Step 7: Convert each renamed route**

In all four, the route id gains the new parameter name and `beforeLoad` resolves the id into context. `teams.$teamSlug.links.index.tsx`:

```ts
export const Route = createFileRoute('/_authed/teams/$teamSlug/links/')({
	// … the existing `validateSearch` and its comment, unchanged …
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	loaderDeps: ({ search }) => ({ page: search.page }),
	loader: ({ context, deps }) => loadLinks(context.queryClient, context.teamId, deps.page),
	// … component, errorComponent, unchanged …
});
```

`beforeLoad`'s return value merges into the route context, so `loader` and the component read `context.teamId` — the same mechanism by which `_authed`'s `beforeLoad` publishes `me`. In each component:

```ts
const { teamSlug } = Route.useParams();
const { teamId } = Route.useRouteContext();
```

`teamId` feeds `linksQueryOptions`, `domainsQueryOptions`, `createLinkFn`, `claimDomainFn`, the `['links', teamId]` invalidations and every other API call — none of those signatures change. `teamSlug` feeds `router.navigate` and `<Link params>`. `teams.$teamSlug.links.$linkId.tsx` uses both: `Route.useParams()` there yields `{ linkId, teamSlug }`.

- [ ] **Step 8: Update the four route test files**

Each one builds a fake loader context or params object. Where they pass a team id as a _param_, they now pass a slug as a param and a team id in context; the `loadLinks`/`loadDomains`/`loadVerifiedDomains` helpers keep taking a plain `teamId: string`, so their existing direct-call tests need only their fixture values renamed, not restructured.

- [ ] **Step 9: Update the navigation components**

- `components/authed-shell.tsx`: the prop becomes `currentTeamSlug`, both `<Link>`s become `to="/teams/$teamSlug/links"` / `to="/teams/$teamSlug/domains"` with `params={{ teamSlug: currentTeamSlug }}`, and both guards read `currentTeamSlug && memberships.length > 0`. Its docstring's mention of "resolving the current team id from the URL" becomes the slug.
- `components/team-switcher.tsx`: prop `currentTeamSlug`, `aria-current` compares `membership.slug`, `params={{ teamSlug: membership.slug }}`, `remember(membership.slug)`.
- `components/link-list.tsx`: prop `teamSlug`, all five `<Link>`s switch to `$teamSlug`. This component only ever used the value for navigation.
- `components/link-list.stories.tsx`: `teamId: 'team-a'` becomes `teamSlug: 'team-a'` in all three story args.
- The three matching test files: fixtures gain `slug`, asserted hrefs become slug-shaped.

- [ ] **Step 10: Update `/`'s redirect**

In `apps/web/src/routes/index.tsx`: rename `getCurrentTeamId` to `getCurrentTeamSlug`, `HomeOutcome`'s redirect arm to `{ kind: 'redirect'; teamSlug: string }`, `resolveHomeOutcome(me, teamSlug)`, and the throw to

```ts
throw redirect({ params: { teamSlug: outcome.teamSlug }, to: '/teams/$teamSlug/links' });
```

`routes/index.test.ts`: the three `resolveHomeOutcome` redirect expectations become `{ kind: 'redirect', teamSlug: 'verein-a' }` and so on, and the `memberships` fixture gains `slug`.

- [ ] **Step 11: Regenerate the route tree**

Run: `cd apps/web && pnpm generate-routes` Expected: `routeTree.gen.ts` lists `/_authed/teams/$teamSlug/...` and no `$teamId` route remains.

- [ ] **Step 12: Run the full frontend gate**

Run: `pnpm format && pnpm lint && pnpm typecheck && cd apps/web && pnpm test && pnpm test:storybook` Expected: PASS. A leftover `$teamId` anywhere fails `typecheck`, because the generated route tree no longer has such a route.

- [ ] **Step 13: Commit**

```bash
but commit -m "feat(web): address teams by slug in URLs"
```

---

### Task 5: The create form, and moving it out of `/teams/`

**Files:**

- Rename: `apps/web/src/routes/_authed/teams.new.tsx` → `apps/web/src/routes/_authed/new-team.tsx`, `teams.new.test.ts` → `new-team.test.ts`
- Modify: `apps/web/src/server/teams.ts`, `apps/web/src/components/authed-shell.tsx`, `authed-shell.test.tsx`, `apps/web/src/routes/index.tsx`, `apps/web/src/i18n/locales/en.json`, `de.json`
- Test: `apps/web/src/routes/_authed/new-team.test.ts`

**Interfaces:**

- Consumes: `suggestTeamSlug` and the `slugTaken` failure kind (Task 3); `POST /v1/teams`'s required `slug` (Task 2).
- Produces: route `/new-team`; `createTeamFor(request, name, slug)`; `createTeamFn({ data: { name, slug } })`.

- [ ] **Step 1: Add the copy**

`apps/web/src/i18n/locales/en.json`, under `teams`:

```json
"slug": "URL name",
"slugHint": "Appears in this team's web address, e.g. sv-gruenwald. It cannot be changed later.",
"slugRequired": "A URL name is required.",
"slugInvalid": "Use 3 to 40 lowercase letters, digits and hyphens, starting and ending with a letter or digit.",
"slugTaken": "That URL name is already taken. Please choose another."
```

`de.json`, same keys:

```json
"slug": "URL-Name",
"slugHint": "Erscheint in der Web-Adresse dieses Teams, z. B. sv-gruenwald. Er kann später nicht geändert werden.",
"slugRequired": "Ein URL-Name ist erforderlich.",
"slugInvalid": "3 bis 40 Kleinbuchstaben, Ziffern und Bindestriche, am Anfang und Ende ein Buchstabe oder eine Ziffer.",
"slugTaken": "Dieser URL-Name ist schon vergeben. Bitte wähle einen anderen."
```

- [ ] **Step 2: Move the route**

```bash
cd apps/web/src/routes/_authed
mv teams.new.tsx new-team.tsx && mv teams.new.test.ts new-team.test.ts
```

Change the route id to `createFileRoute('/_authed/new-team')`, and update `new-team.test.ts`'s import path to `'./new-team'`. `assertMaintainer` and its docstring are unchanged — the guard did not depend on the path.

Then the two links to it: `components/authed-shell.tsx`'s `<Link to="/teams/new">` and `routes/index.tsx`'s maintainer button both become `to="/new-team"`; `authed-shell.test.tsx`'s `path: '/teams/new'` and the comment two lines below it follow.

Add the reason to the route's docstring:

```
 * At `/new-team`, not `/teams/new`: a static segment under `/teams/` is matched
 * before the dynamic `$teamSlug`, so a team whose slug were `new` would have
 * this form rendered at its own URL forever. The Go API also refuses a
 * reserved slug (`reservedTeamSlugs` in `internal/api/teams.go`), which is what
 * covers the *next* static child route someone adds under `/teams/`.
```

- [ ] **Step 3: Write the failing test for the slug field's validation**

Append to `apps/web/src/routes/_authed/new-team.test.ts`:

```ts
import { validateSlugField } from './new-team';

describe('validateSlugField', () => {
	it('accepts a well-formed slug', () => {
		expect(validateSlugField('sv-gruenwald', translate)).toBeUndefined();
	});

	it('reports an empty value as required, not as malformed', () => {
		expect(validateSlugField('  ', translate)).toBe('teams.slugRequired');
	});

	/**
	 * The same regex and bounds the Go API enforces via its Huma `pattern` tag
	 * and the database enforces via `team_slug_format`. Validating here is not
	 * a substitute for either — it saves a round trip that would otherwise
	 * answer 422 for a value the maintainer can see is wrong.
	 */
	it('reports a malformed or out-of-range value', () => {
		for (const malformed of ['SV-Gruenwald', 'sv_gruenwald', '-leading', 'trailing-', 'ab']) {
			expect(validateSlugField(malformed, translate)).toBe('teams.slugInvalid');
		}
	});
});
```

with a translate stub beside the existing tests:

```ts
const translate = (key: string): string => key;
```

- [ ] **Step 4: Run it to verify it fails**

Run: `cd apps/web && pnpm test -- new-team` Expected: FAIL — `validateSlugField` is not exported.

- [ ] **Step 5: Implement the field and the validator**

In `apps/web/src/routes/_authed/new-team.tsx`, above the component:

```ts
/**
 * Exported and translate-injected so it can be unit-tested without rendering
 * the form — the same shape `assertMaintainer` above uses for the same reason.
 */
export function validateSlugField(value: string, t: (key: string) => string): string | undefined {
	const slug = value.trim();
	if (slug === '') return t('teams.slugRequired');
	if (
		slug.length < TEAM_SLUG_MIN_LENGTH ||
		slug.length > TEAM_SLUG_MAX_LENGTH ||
		!TEAM_SLUG_PATTERN.test(slug)
	) {
		return t('teams.slugInvalid');
	}
	return undefined;
}
```

The form gains the field, defaulting to a suggestion that follows the name until the maintainer edits the slug themselves:

```ts
const form = useForm({
	defaultValues: { name: '', slug: '' },
	onSubmit: ({ value }) => {
		mutation.mutate({ name: value.name, slug: value.slug.trim() });
	},
});
```

In the name field's `onChange`, keep the slug in step while it is still untouched — a maintainer who wants `sv-gruenwald` instead of `sportverein-gruenwald` types it and the suggestion stops following:

```tsx
onChange={(event) => {
	field.handleChange(event.target.value);
	if (!form.getFieldMeta('slug')?.isTouched) {
		form.setFieldValue('slug', suggestTeamSlug(event.target.value));
	}
}}
```

The slug field itself mirrors the name field's markup — `<label htmlFor="slug">`, `aria-describedby`, `aria-invalid`, `role="alert"` on the message — and additionally renders the hint, wired into the same `aria-describedby` so a screen reader hears it before the input is wrong:

```tsx
<form.Field name="slug" validators={{ onChange: ({ value }) => validateSlugField(value, t) }}>
	{(field) => {
		const hintId = 'slug-hint';
		const errorId = 'slug-error';
		const errorMessage =
			fieldError ?? (field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

		return (
			<div>
				<label htmlFor="slug">{t('teams.slug')}</label>
				<input
					aria-describedby={errorMessage ? `${hintId} ${errorId}` : hintId}
					aria-invalid={errorMessage ? true : undefined}
					id="slug"
					name={field.name}
					onBlur={field.handleBlur}
					onChange={(event) => field.handleChange(event.target.value)}
					required
					value={field.state.value}
				/>
				<p id={hintId}>{t('teams.slugHint')}</p>
				{errorMessage ? (
					<p id={errorId} role="alert">
						{errorMessage}
					</p>
				) : null}
			</div>
		);
	}}
</form.Field>
```

`fieldError` above it now reads the slug's server-side field error where this field is concerned — the existing line becomes two:

```ts
const nameFieldError = failure?.kind === 'fields' ? failure.fields.name : undefined;
const slugFieldError =
	failure?.kind === 'slugTaken'
		? t('teams.slugTaken')
		: failure?.kind === 'fields'
			? failure.fields.slug
			: undefined;
```

and `formMessage` excludes the new kind, so a taken slug renders once, on the field:

```ts
const formMessage =
	failure && failure.kind !== 'fields' && failure.kind !== 'slugTaken'
		? t(`errors.${failure.kind}`)
		: null;
```

`onSuccess` navigates by slug:

```ts
await router.navigate({ params: { teamSlug: team.slug }, to: '/teams/$teamSlug/links' });
```

- [ ] **Step 6: Widen the server function**

In `apps/web/src/server/teams.ts`:

```ts
export const createTeamFor = createServerOnlyFn(
	async (request: Request, name: string, slug: string): Promise<Team> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createTeam({
			body: { name, slug },
			client: authedApiClient(accessToken),
			throwOnError: true,
		});
		return data;
	},
);

export const createTeamFn = createServerFn({ method: 'POST' })
	.validator((data: { name: string; slug: string }) => data)
	.handler(async ({ data }) => createTeamFor(getRequest(), data.name, data.slug));
```

The existing comment about `throwOnError` stays.

- [ ] **Step 7: Run the tests**

Run: `cd apps/web && pnpm test -- new-team` Expected: PASS.

- [ ] **Step 8: Full frontend gate**

Run: `pnpm format && pnpm lint && pnpm typecheck && cd apps/web && pnpm test && pnpm test:storybook` Expected: PASS, `catalogues.test.ts` included — it fails if `en.json` and `de.json` disagree on the five new keys.

- [ ] **Step 9: Commit**

```bash
but commit -m "feat(web): choose a team slug when creating"
```

---

### Task 6: The e2e suite

**Files:**

- Modify: `apps/web/e2e/fixtures/auth.ts`, `apps/web/e2e/links.spec.ts`, `apps/web/e2e/domains.spec.ts`, `apps/web/e2e/i18n.spec.ts`

**Interfaces:**

- Consumes: everything above.
- Produces: fixture `team: { id, name, slug }`, plus the derived `teamSlug` fixture beside `teamId` and `teamName`.

- [ ] **Step 1: Give the fixture team a slug**

In `apps/web/e2e/fixtures/auth.ts`, beside the existing `teamName`:

```ts
// Twelve hex characters, not the full UUID: the slug format caps at 40
// characters and forbids a trailing hyphen, and these rows are deleted at the
// end of each test, so 48 bits of entropy is far more than the collision
// window needs.
const teamSlug = `e2e-${randomUUID().replaceAll('-', '').slice(0, 12)}`;
const teamResult = await db.query<{ id: string }>(
	'insert into team (name, slug) values ($1, $2) returning id',
	[teamName, teamSlug],
);
```

`use({ id: teamId, name: teamName, slug: teamSlug })`, the `Team` interface gains `slug: string`, and a `teamSlug` fixture joins `teamId`/`teamName`:

```ts
teamSlug: async ({ team }, use): Promise<void> => {
	await use(team.slug);
},
```

The docstring above those derived fixtures already explains why they are thin derivatives of `team`; extend its list to mention that the specs build URLs from `teamSlug` while `teamId` remains for direct database assertions.

- [ ] **Step 2: Switch the specs to slug-shaped URLs**

`links.spec.ts`: `createLink(page, teamSlug, …)` and `page.goto(\`/teams/${teamSlug}/links/new\`)`; the four tests destructure `{ page, teamSlug }`. The comment in the signed-out test that refers to "the `teamId`fixture's session cookies" names`teamSlug` instead.

`domains.spec.ts`: `claimDomain(page, teamSlug)` and `page.goto(\`/teams/${teamSlug}/domains\`)`; three tests destructure `{ page, teamSlug }`.

`i18n.spec.ts`: the authenticated crawl's paths (`/teams/${teamSlug}/links/new`, `/teams/${teamSlug}/domains`, and the `` `/teams/${teamSlug}/${suffix}` `` builder) and its fixture destructuring.

- [ ] **Step 3: Apply the migration to the Preview database**

Required before the suite can pass, and not optional: the Preview project's schema is whatever `main` has, and every team query now selects `slug`. Run this branch's migration against the Preview database by hand — see `CLAUDE.md`, which records the day four specs failed on a missing column while both deployments were green. This leaves Preview ahead of `main` until the branch lands.

- [ ] **Step 4: Run the e2e suite against the preview**

Run: `cd apps/web && pnpm exec playwright test` Expected: PASS, axe-core checks included.

- [ ] **Step 5: Commit**

```bash
but commit -m "test(e2e): drive the app by team slug"
```

---

### Task 7: The documentation

**Files:**

- Modify: `CLAUDE.md`, `docs/planning/05-database-schema.md`, `docs/planning/06-api-design.md`

**Interfaces:**

- Consumes: the shipped behaviour of Tasks 1–6.
- Produces: no code.

- [ ] **Step 1: Update `CLAUDE.md`**

Under **Data model (summary)**, add to the `team` bullets:

```
- `team.slug` is globally unique, immutable, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, 3–40 characters. The **frontend** addresses a team by it (`/teams/sv-gruenwald/links`); the **API** addresses teams by UUID everywhere and never accepts a slug in a path.
```

Under **Non-obvious constraints**, add:

```
- **A team slug is not a route-safe name by itself.** TanStack Router matches a static segment before a dynamic one, so any static child route under `/teams/` permanently shadows the team whose slug matches it. The create form therefore lives at `/new-team`, outside the namespace, and `reservedTeamSlugs` in `apps/api/internal/api/teams.go` refuses the values a future static route would claim. Adding a static child route under `/teams/` means adding its segment to that list in the same change.
- **Slug-to-team-id resolution happens in the frontend**, in each team route's `beforeLoad`, out of `GET /v1/me`'s membership list — `requireTeamId` in `apps/web/src/routes/_authed.tsx`. It costs no request, and it keeps a second resolution path out of every tenancy-critical query. Route context carries `teamId`; `params` carries `teamSlug`. The `team` cookie holds the slug.
```

- [ ] **Step 2: Update the planning docs**

`docs/planning/05-database-schema.md`: the `team` table in the schema listing gains `slug text not null unique` with the format and length checks, and a short paragraph records the backfill and the "no column default" decision (an invariant in the schema cannot be forgotten by a later writer; a default is a way of forgetting it).

`docs/planning/06-api-design.md`: `POST /v1/teams` takes `{name, slug}` and answers 422 for malformed or reserved and 409 for taken, both located on `body.slug`; `GET /v1/me` and every team response carry `slug`; and a sentence stating that no endpoint accepts a slug in place of a team id, with the reason from the spec.

- [ ] **Step 3: Commit**

```bash
but commit -m "docs: record the team slug decisions"
```

---

## Self-Review

**Spec coverage.** Slug column, format and length → Task 1. Backfill and the no-default decision → Task 1. `POST /v1/teams` taking the slug, the denylist, 422/409 shapes → Task 2. Slug on `/v1/me` and team responses → Task 2. Suggestion with German transliteration → Task 3. `slugTaken` classification → Task 3. Route paths, `requireTeamId`, route context, cookie, navigation, `/`'s redirect → Task 4. Create form and the move to `/new-team` → Task 5. e2e → Task 6. Documentation amendments named in the spec's header → Task 7. Immutability needs no task: no endpoint is added that would change a slug, and Task 2's `RenameTeam` deliberately does not touch it.

**Type consistency.** `requireTeamId(memberships, teamSlug): string` is used identically in Task 4's four routes. `suggestTeamSlug`, `TEAM_SLUG_PATTERN`, `TEAM_SLUG_MIN_LENGTH`, `TEAM_SLUG_MAX_LENGTH` are defined in Task 3 and consumed in Task 5. `createTeamFor(request, name, slug)` and `createTeamFn({ data: { name, slug } })` match between Task 5's form and its server function. Go: `db.CreateTeamParams{Name, Slug}` and `row.TeamSlug` match Task 2's queries; `api.Team.Slug` and `api.TeamMembership.Slug` match the tests that assert on them.

**Open risk, flagged rather than assumed.** Production's backfilled slugs get one manual read-through (Task 1, Step 10, repeated against Production after merge). If a real Verein's name transliterates to something the maintainer dislikes, the fix is one `update team set slug = …` before anybody has the URL — not a plan change.
