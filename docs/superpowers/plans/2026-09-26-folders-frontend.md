# Folders Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Folders reach `apps/web`. The slice adds a folders page, a folder field on the link form, and a folder column and folder filter on the link list. It also adds the API changes the frontend needs.

**Architecture:**

- **API.** `unfiled` becomes a list filter. Folder names become unique per team, case-insensitively. Two fixes make the link payload honest about folders.
- **Web.**
  - Server functions under `apps/web/src/server/folders.ts`.
  - Pure helpers under `apps/web/src/lib/folders.ts`.
  - One new route: `/teams/$teamSlug/folders`.
  - Changes to the link list, create and edit routes.
  - All folder reads share one query, `['folders', teamId]`.

**Tech Stack:** Go (Huma, sqlc, pgx), Supabase migrations, TanStack Start/Router/Query/Form, shadcn on Base UI, react-i18next, Vitest + RTL + MSW, Storybook, Playwright + axe.

**Spec:** `docs/superpowers/specs/2026-09-26-folders-frontend-design.md`. Read it before any task. This plan argues from it.

## Rulings on the spec

Reading the code while writing this plan turned up four things the spec gets wrong or leaves out. Each is decided here.

1. **`Link.folder_id` must become `omitempty`** (Task 1). `apps/api/internal/api/links.go` declares `FolderID *uuid.UUID \`json:"folder_id"\``. `uuid.UUID`is not a scalar to Huma, so the schema publishes a required`folder_id: string`while the wire sends`null`for an unfiled link. That is exactly the trap`CLAUDE.md`describes under "Huma's automatic nullability covers only scalars".`omitempty`makes the generated type`folder_id?: string`, and the frontend code below depends on it.
2. **The 422 for a foreign or missing folder must carry `Location: "body.folder_id"`** (Task 1). Today `resolveFolderRef` answers with a bare message. `classifyApiError` would then return `unknown`, not the field error the spec's failure table promises.
3. **The API's 409 and 422 on folder writes carry no location.** The frontend tells them apart by status, through `folderFailureOf` in Task 4:
   - a 409 on create or rename means the name is taken
   - a 422 on create means the cap is reached when the team already has 100 folders, and an invalid name otherwise
   - a 422 on rename always means an invalid name

   `loadAuditLogPage` reading `statusOf(error)` is the precedent.

4. **There is no per-route document title.** `pageTitle` is one string for the whole app. The spec's "the route's document title" i18n item is dropped.

5. **The e2e does not need the migration on Preview.** The spec says it does, citing the `CLAUDE.md` trap. That trap bites when branch code reads a column the Preview schema lacks. The unique index adds no column, so the branch passes e2e either way. Apply the migration to Preview anyway, after merge, so Preview's schema matches `main`.

The cross-team `unfiled` test goes into `links_test.go`, next to `TestListLinksNeverShowsAnotherTeamsLinks`, not into `links_isolation_test.go`.

## Global Constraints

- **Naming.** The tenant is `team` in every identifier. "Verein" appears only in German copy.
- **No hardcoded user-facing strings.** Every key exists in both `apps/web/src/i18n/locales/en.json` and `de.json`. German copy says "Ordner".
- **Accessibility.** WCAG 2.1 AA. Every control has a visible label. The Storybook a11y check and axe in e2e must pass.
- **JSDoc.** Any function that gets a `/** … */` block needs `@param` and `@returns` tags, including dotted tags for destructured props (`CLAUDE.md`, Conventions). An undocumented function may stay undocumented.
- **Lint and format.** `pnpm lint`, `pnpm typecheck` and `pnpm format` pass. oxlint and oxfmt only, never ESLint or Prettier.
- **Generated files.**
  - `apps/web/src/components/ui/*` is never hand-edited.
  - `routeTree.gen.ts` is regenerated only by `pnpm --filter @kurze-url/web run build` or the dev server, never by `tsr`.
  - `packages/api-client` is regenerated only by `pnpm run generate:api`, which runs the TypeScript 6 generator.
- **Tenancy.** Every query keeps its `team_id` predicate. No folder write invalidates the redirect cache.
- **Migrations.** Created only by `supabase migration new`. Never add a `db push` workflow.
- **Sentry.** Nothing `classifyApiError` names is reported.
- **Commits.**
  - All git writes go through GitButler (`but`), on the lane `feat/folders-frontend`.
  - The subject follows Conventional Commits and is at most 50 characters including type and scope. No co-author or generator footer.
  - Run `pnpm format` before every commit. Never skip hooks.
- **Commands run from the repository root** `/Users/ab/dev/customer/itsb/kurze-url`. Go tests need a local Supabase (`supabase start`) and `TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres`, the value `ci-api.yml` uses.

## Review Focus

1. **A folder named in the URL that this team does not have** (deleted, foreign, or a stale bookmark). The list shows the "does not exist (any more)" state. The create form ignores the id and does not preselect it. _Tests: Task 6 and Task 7._
2. **Editing a link whose folder is missing from the loaded folder list** (a delete or refetch race). Saving without touching the folder field must not unfile the link, so `folder_id` is omitted when unchanged. _Test: Task 6, `toUpdateBody`._
3. **Renaming a folder to its own name in different case** ("sommerfest" to "Sommerfest") succeeds. The unique index compares the row against itself. _Test: Task 2._
4. **A name of spaces only, or longer than 60 characters.** It is rejected on the client before any request, with the same message the API's 422 maps to. _Test: Task 4 and Task 5._
5. **The filter, the page, and pagination together.** Changing the folder resets `page` to 1. "Next page" keeps the folder. _Test: Task 7._

---

### Task 1: API — `unfiled` filter and an honest folder field on links

**Files:**

- Modify: `apps/api/internal/db/queries/link_crud.sql` (`ListLinksForTeam`, `CountLinksForTeam`)
- Regenerate: `apps/api/internal/db/*.go` (`sqlc generate`)
- Modify: `apps/api/internal/api/links.go` (the `Link` struct, `ListLinksInput`, `listLinks`, `resolveFolderRef`)
- Test: `apps/api/internal/api/links_test.go`

**Interfaces:**

- Produces: `GET /v1/teams/{team_id}/links?unfiled=true`
- Produces: `Link.folder_id` is omitted when null
- Produces: the unknown-folder 422 carries `errors[0].location == "body.folder_id"`

- [ ] **Step 1: Write the failing tests** (append to `links_test.go`)

```go
func TestListLinksFiltersToUnfiledLinks(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	f.createLinkInFolder(t, "https://example.org/in", folder.ID)
	loose := f.createLink(t, "ohne-ordner", "https://example.org/out")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?unfiled=true&per_page=100", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[linkPage](t, rec)
	var ids []uuid.UUID
	for _, item := range page.Items {
		require.Nil(t, item.FolderID, "an unfiled filter must return no filed link")
		ids = append(ids, item.ID)
	}
	require.Contains(t, ids, loose.ID)
	require.Equal(t, len(page.Items), page.TotalCount, "the count must respect the filter")
}

func TestListLinksRejectsUnfiledTogetherWithAFolder(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?unfiled=true&folder_id="+folder.ID.String(), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListLinksUnfiledNeverShowsAnotherTeamsLinks(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	other.createLink(t, "geheim-lose", "https://example.org/geheim")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?unfiled=true&per_page=100", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, item := range decode[linkPage](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
		require.NotEqual(t, "geheim-lose", item.Slug)
	}
}

func TestLinkOmitsFolderIDWhenUnfiled(t *testing.T) {
	f := newTenancyFixture(t)
	loose := f.createLink(t, "ohne-feld", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+loose.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	_, present := raw["folder_id"]
	require.False(t, present, "a null folder must be absent, as the schema says")
}

func TestCreateLinkNamesTheFolderFieldForAnUnknownFolder(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/y", "folder_id": uuid.NewString()})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var problem struct {
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	require.NotEmpty(t, problem.Errors)
	require.Equal(t, "body.folder_id", problem.Errors[0].Location)
}
```

If `createLink` in `links_test.go` returns something other than a `linkBody` with an `ID` field, adapt the two `.ID` reads to its actual return value. Add `encoding/json` to the file's imports if it is missing.

- [ ] **Step 2: Run them and confirm they fail**

Run:

```bash
supabase start
cd apps/api && TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres go test ./internal/api/ -run 'TestListLinksFiltersToUnfiledLinks|TestListLinksRejectsUnfiledTogetherWithAFolder|TestListLinksUnfiledNeverShowsAnotherTeamsLinks|TestLinkOmitsFolderIDWhenUnfiled|TestCreateLinkNamesTheFolderFieldForAnUnknownFolder' -count=1
```

Expected: FAIL. `unfiled` is ignored, so filed links come back and the 422 test gets 200. `folder_id` is present as null. `location` is empty.

- [ ] **Step 3: Implement.** In `link_crud.sql`, add this line to both `ListLinksForTeam` and `CountLinksForTeam`, directly after each query's `folder_id` predicate:

```sql
  and (not sqlc.arg('unfiled')::boolean or l.folder_id is null)
```

Then run `cd apps/api && sqlc generate`. This adds `Unfiled bool` to `ListLinksForTeamParams` and `CountLinksForTeamParams`.

In `links.go`:

```go
// In type Link:
	FolderID         *uuid.UUID `json:"folder_id,omitempty"`

// In type ListLinksInput, after TagID:
	Unfiled bool `query:"unfiled" doc:"Only links without a folder. Cannot be combined with folder_id."`

// In listLinks, before the FolderID block:
	if in.Unfiled && in.FolderID != "" {
		return nil, huma.Error422UnprocessableEntity("unfiled cannot be combined with folder_id")
	}
	params.Unfiled, countParams.Unfiled = in.Unfiled, in.Unfiled

// In resolveFolderRef, replace the ErrNoRows return:
	if errors.Is(err, pgx.ErrNoRows) {
		message := fmt.Sprintf("no folder %s in this team", folderID)
		return nil, huma.Error422UnprocessableEntity(message, &huma.ErrorDetail{
			Location: "body.folder_id", Message: message, Value: folderID.String(),
		})
	}
```

Add a line to the `omitempty` field's comment block: "`omitempty`, not a bare tag: `uuid.UUID` is not a scalar to Huma, so without it the schema promised a required string while the wire sent null (`CLAUDE.md`, Huma nullability)."

- [ ] **Step 4: Run the Step 2 command again and confirm everything passes.** Then run the whole package: `cd apps/api && TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres go test ./... -count=1`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(api): filter the link list to unfiled links"
```

---

### Task 2: API — folder names unique per team, case-insensitively

**Files:**

- Create: `supabase/migrations/<timestamp>_folder_name_unique.sql`, via `supabase migration new folder_name_unique`
- Modify: `apps/api/internal/api/folders.go` (the error switches in `createFolder` and `updateFolder`)
- Test: `apps/api/internal/api/folders_test.go`

**Interfaces:**

- Produces: `POST /v1/teams/{team_id}/folders` and `PATCH /v1/folders/{folder_id}` answer 409 on a case-insensitive name collision within the team

- [ ] **Step 1: Write the failing tests** (append to `folders_test.go`)

```go
func TestCreateFolderRejectsADuplicateNameIgnoringCase(t *testing.T) {
	f := newTenancyFixture(t)
	f.createFolder(t, "Sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders", map[string]any{"name": "SOMMERFEST"})

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestUpdateFolderRejectsAnotherFoldersNameIgnoringCase(t *testing.T) {
	f := newTenancyFixture(t)
	f.createFolder(t, "Sommerfest")
	other := f.createFolder(t, "Newsletter")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/folders/"+other.ID.String(), map[string]any{"name": "sommerfest"})

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestUpdateFolderAllowsRecasingItsOwnName(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/folders/"+folder.ID.String(), map[string]any{"name": "Sommerfest"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestFolderNamesMayRepeatAcrossTeams(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	f.createFolder(t, "Sommerfest")

	other.createFolder(t, "Sommerfest") // createFolder itself requires 201
}
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd apps/api && TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:54322/postgres go test ./internal/api/ -run 'TestCreateFolderRejectsADuplicateNameIgnoringCase|TestUpdateFolderRejectsAnotherFoldersNameIgnoringCase|TestUpdateFolderAllowsRecasingItsOwnName|TestFolderNamesMayRepeatAcrossTeams' -count=1`

Expected: the two 409 tests FAIL with 201 or 200. The other two pass already, which is fine: they pin behavior the index must not break.

- [ ] **Step 3: Implement.** Run `supabase migration new folder_name_unique` and write this into the created file:

```sql
-- Folder names are unique per team, case-insensitively, the same rule tag
-- names follow (tag_team_id_name_lower_idx): the name is stored as typed and
-- compared folded, so "Sommerfest" and "sommerfest" cannot both exist in one
-- team's folder select. See docs/superpowers/specs/2026-09-26-folders-frontend-design.md.
create unique index folder_team_id_name_lower_idx on folder (team_id, lower(name));
```

Apply it locally with `supabase migration up`. This is local only; production and Preview are the maintainer's steps. Then, in both `createFolder` and `updateFolder`, add this case to the error `switch`, before `case err != nil:`:

```go
	case isUniqueViolation(err):
		// The index folds case, so this fires for "SOMMERFEST" against an
		// existing "Sommerfest" as well as for an exact repeat.
		return nil, huma.Error409Conflict("a folder with that name already exists")
```

Update the Huma operation registrations for create and update folder in `registerFolders`, if they list their error statuses, so they include `http.StatusConflict`.

- [ ] **Step 4: Run the Step 2 command again (PASS), then the whole package: `go test ./... -count=1` (PASS).**

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(api): make folder names unique per team"
```

---

### Task 3: Regenerate the contract and update `CLAUDE.md`

**Files:**

- Regenerate: `apps/api/openapi.json`, `packages/api-client/src/generated/*`
- Modify: `CLAUDE.md`

**Interfaces:**

- Produces: `ListLinksData['query']['unfiled']?: boolean`
- Produces: `Link['folder_id']?: string`

- [ ] **Step 1: Regenerate.** Run `pnpm run generate:api`.
- [ ] **Step 2: Verify the generated types.**

Run: `grep -n "unfiled" packages/api-client/src/generated/types.gen.ts && awk '/^export type Link = /,/^};/' packages/api-client/src/generated/types.gen.ts | grep folder_id`

Expected: `unfiled?: boolean;` and `folder_id?: string;`.

- [ ] **Step 3: Update `CLAUDE.md`.**
  - In "Data model (summary)", add the bullet: `- Folder names are unique per team, case-insensitively (\`folder_team_id_name_lower_idx\`), the same rule tag names follow.`
  - In "API surface (summary)", change `links (\`POST|GET /teams/{id}/links\`, …)`so the list's filters read`(\`q\`, \`state\`, \`domain_id\`, \`folder_id\`, \`unfiled\`, \`tag_id\`, \`sort\`)`.
- [ ] **Step 4: Typecheck.** Run `pnpm typecheck`. Expected: PASS. Nothing reads `folder_id` yet.
- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "chore: regenerate client, document folders"
```

---

### Task 4: Folder helpers and server functions

**Files:**

- Create: `apps/web/src/lib/folders.ts`, `apps/web/src/lib/folders.test.ts`
- Create: `apps/web/src/server/folders.ts`, `apps/web/src/server/folders.test.ts`
- Modify: `apps/web/src/server/links.ts` (`listLinksFor`, `listLinksFn`, `linksQueryOptions`), `apps/web/src/server/links.test.ts`
- Modify: `apps/web/src/lib/team-roles.ts` (add `canEdit`)

**Interfaces:**

- Produces (`lib/folders.ts`):
  - `type FolderFilter = { kind: 'all' } | { kind: 'unfiled' } | { kind: 'folder'; folderId: string }`
  - `UNFILED_SEARCH_VALUE = 'none'`
  - `parseFolderSearch(value: unknown): string | undefined`, which returns `'none'`, a lowercased UUID, or `undefined`
  - `parseFolderIdSearch(value: unknown): string | undefined`, which accepts a UUID only
  - `folderFilterOf(folder: string | undefined): FolderFilter`
  - `folderQueryOf(filter: FolderFilter): { folder_id?: string; unfiled?: boolean }`
  - `FOLDERS_PER_TEAM = 100`
  - `normalizeFolderName(raw: string): string | undefined`
  - `type FolderFailure = 'nameTaken' | 'capReached' | 'nameInvalid' | 'notFound' | 'rateLimited' | 'unauthenticated' | 'unknown'`
  - `folderFailureOf(error: unknown, atCap: boolean): FolderFailure`
- Produces (`lib/team-roles.ts`): `canEdit(role: string | undefined): boolean`
- Produces (`server/folders.ts`):
  - `listFoldersFor` / `listFoldersFn`, and `foldersQueryOptions(teamId)` with key `['folders', teamId]`
  - `createFolderFor(request, teamId, name)` / `createFolderFn({ data: { teamId, name } })`
  - `renameFolderFor(request, folderId, name)` / `renameFolderFn({ data: { folderId, name } })`
  - `deleteFolderFor(request, folderId)` / `deleteFolderFn({ data: { folderId } })`
- Produces (`server/links.ts`): `linksQueryOptions(teamId, page, filter: FolderFilter)` with key `['links', teamId, page, filter]`. `listLinksFn({ data: { teamId, page, filter } })`.

- [ ] **Step 1: Write the failing tests for `lib/folders.ts`**

```ts
import { describe, expect, it } from 'vitest';
import {
	folderFailureOf,
	folderFilterOf,
	folderQueryOf,
	normalizeFolderName,
	parseFolderIdSearch,
	parseFolderSearch,
} from './folders';

const ID = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';

describe('parseFolderSearch', () => {
	it('keeps "none" and a UUID, lowercasing the UUID', () => {
		expect(parseFolderSearch('none')).toBe('none');
		expect(parseFolderSearch(ID.toUpperCase())).toBe(ID);
	});
	it('drops anything else', () => {
		for (const value of [undefined, '', 'sommerfest', 42, `${ID}x`])
			expect(parseFolderSearch(value)).toBeUndefined();
	});
	it('parseFolderIdSearch refuses "none"', () => {
		expect(parseFolderIdSearch('none')).toBeUndefined();
		expect(parseFolderIdSearch(ID)).toBe(ID);
	});
});

describe('folderFilterOf and folderQueryOf', () => {
	it('maps the search value to the API parameters', () => {
		expect(folderQueryOf(folderFilterOf(undefined))).toEqual({});
		expect(folderQueryOf(folderFilterOf('none'))).toEqual({ unfiled: true });
		expect(folderQueryOf(folderFilterOf(ID))).toEqual({ folder_id: ID });
	});
});

describe('normalizeFolderName', () => {
	it('trims and accepts 1 to 60 characters', () => {
		expect(normalizeFolderName('  Sommerfest  ')).toBe('Sommerfest');
		expect(normalizeFolderName('ä'.repeat(60))).toBe('ä'.repeat(60));
	});
	it('refuses blank and over-long names', () => {
		expect(normalizeFolderName('   ')).toBeUndefined();
		expect(normalizeFolderName('a'.repeat(61))).toBeUndefined();
	});
});

describe('folderFailureOf', () => {
	it('reads 409 as a taken name and 422 by whether the team is at its cap', () => {
		expect(folderFailureOf({ status: 409 }, false)).toBe('nameTaken');
		expect(folderFailureOf({ status: 422 }, true)).toBe('capReached');
		expect(folderFailureOf({ status: 422 }, false)).toBe('nameInvalid');
	});
	it('falls back to classifyApiError for everything else', () => {
		expect(folderFailureOf({ status: 401 }, false)).toBe('unauthenticated');
		expect(folderFailureOf({ status: 404 }, false)).toBe('notFound');
		expect(folderFailureOf({ status: 429 }, false)).toBe('rateLimited');
		expect(folderFailureOf({ status: 500 }, false)).toBe('unknown');
	});
});
```

Check `statusOf`'s input shape in `lib/api-errors.ts` before running. If it needs the generated client's error wrapper rather than `{ status }`, build the fixtures the way `api-errors.test.ts` builds them.

- [ ] **Step 2: Run them and confirm they fail.** Run `pnpm --filter @kurze-url/web test -- src/lib/folders.test.ts`. Expected: FAIL, module not found.

- [ ] **Step 3: Implement `lib/folders.ts`**

```ts
import { classifyApiError, statusOf } from './api-errors';

/** Which links the link list shows, by folder. */
export type FolderFilter =
	| { readonly kind: 'all' }
	| { readonly kind: 'unfiled' }
	| { readonly folderId: string; readonly kind: 'folder' };

/** The `folder` search parameter's value for "links without a folder". */
export const UNFILED_SEARCH_VALUE = 'none';

/** Mirrors maxFoldersPerTeam in apps/api/internal/api/limits.go. */
export const FOLDERS_PER_TEAM = 100;

/** Mirrors the API's shared folder and tag name rule. */
const FOLDER_NAME_MAX_LENGTH = 60;

const HTTP_CONFLICT = 409;
const HTTP_UNPROCESSABLE_CONTENT = 422;

const UUID_PATTERN = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/iu;

/**
 * Parses the link list's `folder` search parameter. Anything but `none` or a
 * well-formed UUID is dropped, the way `page` drops a non-number.
 *
 * @param value - The raw search parameter.
 * @returns `none`, the lowercased UUID, or `undefined`.
 */
export function parseFolderSearch(value: unknown): string | undefined {
	if (value === UNFILED_SEARCH_VALUE) return value;
	return parseFolderIdSearch(value);
}

/**
 * Parses a search parameter that may only name one folder, as `links/new`'s
 * preselection does.
 *
 * @param value - The raw search parameter.
 * @returns The lowercased UUID, or `undefined`.
 */
export function parseFolderIdSearch(value: unknown): string | undefined {
	return typeof value === 'string' && UUID_PATTERN.test(value) ? value.toLowerCase() : undefined;
}

/**
 * @param folder - A value `parseFolderSearch` returned.
 * @returns The filter it names.
 */
export function folderFilterOf(folder: string | undefined): FolderFilter {
	if (folder === undefined) return { kind: 'all' };
	if (folder === UNFILED_SEARCH_VALUE) return { kind: 'unfiled' };
	return { folderId: folder, kind: 'folder' };
}

/**
 * @param filter - The filter to express.
 * @returns The `GET /v1/teams/{team_id}/links` query parameters for it.
 */
export function folderQueryOf(filter: FolderFilter): {
	readonly folder_id?: string;
	readonly unfiled?: boolean;
} {
	switch (filter.kind) {
		case 'all': {
			return {};
		}
		case 'unfiled': {
			return { unfiled: true };
		}
		case 'folder': {
			return { folder_id: filter.folderId };
		}
	}
}

/**
 * The client-side half of the API's name rule: trimmed, then 1 to 60
 * characters, counted by code point as Go counts runes.
 *
 * @param raw - What the user typed.
 * @returns The name to send, or `undefined` when the API would refuse it.
 */
export function normalizeFolderName(raw: string): string | undefined {
	const name = raw.trim();
	const length = [...name].length;
	return length > 0 && length <= FOLDER_NAME_MAX_LENGTH ? name : undefined;
}

/** Every way a folder write can fail, as the folders page words it. */
export type FolderFailure =
	| 'capReached'
	| 'nameInvalid'
	| 'nameTaken'
	| 'notFound'
	| 'rateLimited'
	| 'unauthenticated'
	| 'unknown';

/**
 * The folder endpoints send 409 and 422 without a `location`, so this reads
 * the status the way `loadAuditLogPage` does, and only a create can hit the
 * cap.
 *
 * @param error - Whatever the failed folder call threw.
 * @param atCap - Whether the team already had FOLDERS_PER_TEAM folders; always false for a rename.
 * @returns The failure to show.
 */
export function folderFailureOf(error: unknown, atCap: boolean): FolderFailure {
	const status = statusOf(error);
	if (status === HTTP_CONFLICT) return 'nameTaken';
	if (status === HTTP_UNPROCESSABLE_CONTENT) return atCap ? 'capReached' : 'nameInvalid';

	const { kind } = classifyApiError(error);
	if (kind === 'unauthenticated' || kind === 'notFound' || kind === 'rateLimited') return kind;
	return 'unknown';
}
```

Add this to `lib/team-roles.ts`, and export it next to `canManageMember`:

```ts
/**
 * Whether a role may create, rename and delete folders (and, later, tags),
 * matching the API's EditorScope.
 *
 * @param role - The caller's role on the team, from `GET /v1/me`.
 * @returns True for editor, admin and owner.
 */
function canEdit(role: string | undefined): boolean {
	return (
		role !== undefined &&
		isTeamRole(role) &&
		TEAM_ROLES.indexOf(role) >= TEAM_ROLES.indexOf('editor')
	);
}
```

Add one test to `team-roles.test.ts`: `canEdit` is false for `'viewer'` and `undefined`, and true for `'editor'`, `'admin'` and `'owner'`.

- [ ] **Step 4: Write the failing server-function tests.** For `server/folders.test.ts`, copy the harness from `server/domains.test.ts` verbatim: the file-level oxlint comment, `FakeSupabaseClient`, `FakeResponse`, `mocks`, both `vi.mock` calls, `withSession`, and its `afterEach`. Change only the dynamic import to `const { createFolderFor, deleteFolderFor, listFoldersFor, renameFolderFor } = await import('./folders');`. Then add:

```ts
describe('folder server functions', () => {
	it('lists every folder in one page of 100', async () => {
		withSession('token-a');
		let seen: URL | undefined;
		server.use(
			http.get('*/v1/teams/team-a/folders', ({ request }) => {
				seen = new URL(request.url);
				return HttpResponse.json({
					items: [
						{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
					],
					page: 1,
					per_page: 100,
					total_count: 1,
				});
			}),
		);

		const page = await listFoldersFor(new Request('http://localhost/'), 'team-a');

		expect(seen?.searchParams.get('per_page')).toBe('100');
		expect(page.items?.[0]?.name).toBe('Sommerfest');
	});

	it('creates, renames and deletes with the name in the body', async () => {
		withSession('token-a');
		const bodies: unknown[] = [];
		server.use(
			http.post('*/v1/teams/team-a/folders', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json(
					{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
					{ status: 201 },
				);
			}),
			http.patch('*/v1/folders/f1', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json({
					created_at: '2026-09-26T00:00:00Z',
					id: 'f1',
					name: 'Newsletter',
					team_id: 'team-a',
				});
			}),
			http.delete('*/v1/folders/f1', () => new HttpResponse(null, { status: 204 })),
		);

		await createFolderFor(new Request('http://localhost/'), 'team-a', 'Sommerfest');
		await renameFolderFor(new Request('http://localhost/'), 'f1', 'Newsletter');
		await deleteFolderFor(new Request('http://localhost/'), 'f1');

		expect(bodies).toEqual([{ name: 'Sommerfest' }, { name: 'Newsletter' }]);
	});

	it('rejects on an API error instead of resolving empty', async () => {
		withSession('token-a');
		server.use(
			http.post('*/v1/teams/team-a/folders', () =>
				HttpResponse.json({ status: 409, title: 'Conflict' }, { status: 409 }),
			),
		);

		await expect(
			createFolderFor(new Request('http://localhost/'), 'team-a', 'Sommerfest'),
		).rejects.toBeDefined();
	});
});
```

In `server/links.test.ts`, add one test next to the existing `listLinksFor` test: `listLinksFor(request, 'team-a', 1, { kind: 'unfiled' })` sends `unfiled=true` and no `folder_id`. `{ kind: 'folder', folderId: 'f1' }` sends `folder_id=f1`. `{ kind: 'all' }` sends neither. Update the existing `listLinksFor` calls in that file to pass `{ kind: 'all' }`.

- [ ] **Step 5: Implement `server/folders.ts`.** Follow `server/domains.ts` exactly, including its file-level `/* oxlint-disable typescript/prefer-readonly-parameter-types -- … */` comment, the `createServerOnlyFn` `*For` plus `createServerFn` `*Fn` pairs, `throwOnError: true`, and the `explicit-function-return-type` disable on `foldersQueryOptions`:

```ts
import {
	createFolder,
	deleteFolder,
	listFolders,
	updateFolder,
	type Folder,
	type PageFolder,
} from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';
import { FOLDERS_PER_TEAM } from '../lib/folders';
import { authedApiClient, flushSessionCookies, requireSession } from './session';

export const listFoldersFor = createServerOnlyFn(
	async (request: Request, teamId: string): Promise<PageFolder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);
		// One page always holds every folder: the cap equals the maximum page size.
		const { data } = await listFolders({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			query: { per_page: FOLDERS_PER_TEAM },
			throwOnError: true,
		});
		return data;
	},
);

export const listFoldersFn = createServerFn({ method: 'GET' })
	.validator((data: { readonly teamId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly teamId: string } }) =>
		listFoldersFor(getRequest(), data.teamId),
	);

// oxlint-disable-next-line typescript/explicit-function-return-type, typescript/explicit-module-boundary-types -- same reason as `domainsQueryOptions`.
export const foldersQueryOptions = (teamId: string) =>
	queryOptions({
		queryFn: async () => listFoldersFn({ data: { teamId } }),
		queryKey: ['folders', teamId] as const,
	});

export const createFolderFor = createServerOnlyFn(
	async (request: Request, teamId: string, name: string): Promise<Folder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);
		const { data } = await createFolder({
			body: { name },
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			throwOnError: true,
		});
		return data;
	},
);

export const createFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly name: string; readonly teamId: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly name: string; readonly teamId: string } }) =>
			createFolderFor(getRequest(), data.teamId, data.name),
	);

export const renameFolderFor = createServerOnlyFn(
	async (request: Request, folderId: string, name: string): Promise<Folder> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);
		const { data } = await updateFolder({
			body: { name },
			client: authedApiClient(accessToken),
			path: { folder_id: folderId },
			throwOnError: true,
		});
		return data;
	},
);

export const renameFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly folderId: string; readonly name: string }) => data)
	.handler(
		async ({ data }: { readonly data: { readonly folderId: string; readonly name: string } }) =>
			renameFolderFor(getRequest(), data.folderId, data.name),
	);

export const deleteFolderFor = createServerOnlyFn(
	async (request: Request, folderId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);
		await deleteFolder({
			client: authedApiClient(accessToken),
			path: { folder_id: folderId },
			throwOnError: true,
		});
	},
);

export const deleteFolderFn = createServerFn({ method: 'POST' })
	.validator((data: { readonly folderId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly folderId: string } }) =>
		deleteFolderFor(getRequest(), data.folderId),
	);
```

If the generated body type for `createFolder`/`updateFolder` is named differently or needs the `Writable` variant, as `CreateLinkInputBodyWritable` does, use what `types.gen.ts` exports.

In `server/links.ts`:

- `listLinksFor` takes a fourth parameter, `filter: FolderFilter`, and sends `query: { page, per_page: 20, ...folderQueryOf(filter) }`.
- `listLinksFn`'s validator and handler carry `filter` next to `page`.
- `linksQueryOptions` becomes `(teamId: string, page: number, filter: FolderFilter)`, with `queryKey: ['links', teamId, page, filter] as const`. Update its JSDoc `@param`/`@returns` to name the new key.

- [ ] **Step 6: Run the tests and confirm they pass.** Run `pnpm --filter @kurze-url/web test -- src/lib src/server`, then `pnpm typecheck`. Typecheck fails in `teams.$teamSlug.links.index.tsx`, whose `linksQueryOptions(teamId, page)` call is now missing an argument. Pass `{ kind: 'all' }` there for now, so this task stays green. Task 7 replaces it.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(web): add folder helpers and server functions"
```

---

### Task 5: The folders page

**Files:**

- Create: `apps/web/src/components/folder-form.tsx`, `folder-list.tsx`, `folder-list.stories.tsx`, `folder-list.test.tsx`
- Create: `apps/web/src/routes/_authed/teams.$teamSlug.folders.tsx`, `teams.$teamSlug.folders.test.ts`
- Modify: `apps/web/src/components/app-sidebar.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `de.json`
- Regenerate: `apps/web/src/routeTree.gen.ts`, via `pnpm --filter @kurze-url/web run build`

**Interfaces:**

- Consumes (Task 4): `foldersQueryOptions`, `createFolderFn`, `renameFolderFn`, `deleteFolderFn`, `normalizeFolderName`, `folderFailureOf`, `FOLDERS_PER_TEAM`, `canEdit`
- Produces:
  - `FolderForm` props: `{ label: string; submitLabel: string; initialName?: string; error?: string; onSubmit: (name: string) => void; onCancel?: () => void; autoFocus?: boolean }`
  - `FolderList` props: `{ folders: readonly Folder[]; teamSlug: string; canEdit: boolean; onRename: (folderId: string, name: string) => Promise<boolean>; onDelete: (folderId: string) => void; rowError: { folderId: string; message: string } | null }`

- [ ] **Step 1: Add the i18n keys.** `en.json`:

```json
"nav": { "folders": "Folders" },
"folders": {
	"heading": "Folders",
	"intro": "Group your links. A link is in at most one folder.",
	"empty": "No folders yet.",
	"emptyEditorHint": "Create the first one above.",
	"name": "Folder name",
	"create": "Create folder",
	"rename": "Rename",
	"renameLabel": "Rename folder {{name}}",
	"save": "Save",
	"cancel": "Cancel",
	"deleteLabel": "Delete folder {{name}}",
	"deleteQuestion": "Delete folder \"{{name}}\"? The links in it are kept and will have no folder.",
	"nameTaken": "A folder with this name already exists.",
	"capReached": "A team can have at most 100 folders.",
	"nameInvalid": "Enter a name of 1 to 60 characters.",
	"notFound": "This folder no longer exists."
}
```

`de.json`, same keys:

```json
"nav": { "folders": "Ordner" },
"folders": {
	"heading": "Ordner",
	"intro": "Ordne deine Links. Ein Link liegt in höchstens einem Ordner.",
	"empty": "Noch keine Ordner.",
	"emptyEditorHint": "Lege oben den ersten an.",
	"name": "Ordnername",
	"create": "Ordner anlegen",
	"rename": "Umbenennen",
	"renameLabel": "Ordner {{name}} umbenennen",
	"save": "Speichern",
	"cancel": "Abbrechen",
	"deleteLabel": "Ordner {{name}} löschen",
	"deleteQuestion": "Ordner „{{name}}“ löschen? Die Links darin bleiben erhalten und sind danach ohne Ordner.",
	"nameTaken": "Einen Ordner mit diesem Namen gibt es schon.",
	"capReached": "Ein Team kann höchstens 100 Ordner haben.",
	"nameInvalid": "Gib einen Namen mit 1 bis 60 Zeichen ein.",
	"notFound": "Diesen Ordner gibt es nicht mehr."
}
```

Merge `nav.folders` into the existing `nav` objects. Do not replace them. The copy uses "du", like the rest of `de.json`. If `de.json` addresses the user with "Sie", adapt the copy to that.

- [ ] **Step 2: Write the failing component tests** (`folder-list.test.tsx`, RTL with `userEvent`; render with the i18n provider the other component tests use, e.g. `member-list.test.tsx`)

```tsx
const folders = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Newsletter', team_id: 'team-a' },
	{ created_at: '2026-09-26T00:00:00Z', id: 'f2', name: 'Sommerfest', team_id: 'team-a' },
];

it('shows a viewer the folders and no controls', () => {
	renderList({ canEdit: false });
	expect(screen.getByRole('link', { name: 'Sommerfest' })).toHaveAttribute(
		'href',
		expect.stringContaining('folder=f2'),
	);
	expect(screen.queryByRole('button', { name: /rename/iu })).toBeNull();
	expect(screen.queryByRole('button', { name: /delete/iu })).toBeNull();
});

it('renames inline, moving focus into the field and back to the button', async () => {
	const onRename = vi.fn(async () => true);
	renderList({ canEdit: true, onRename });
	await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
	const field = screen.getByRole('textbox', { name: 'Folder name' });
	expect(field).toHaveFocus();
	await userEvent.clear(field);
	await userEvent.type(field, 'Sommerfest 2027{Enter}');
	expect(onRename).toHaveBeenCalledWith('f2', 'Sommerfest 2027');
	expect(await screen.findByRole('button', { name: 'Rename folder Sommerfest' })).toHaveFocus();
});

it('cancels a rename on Escape without calling onRename', async () => {
	const onRename = vi.fn(async () => true);
	renderList({ canEdit: true, onRename });
	await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
	await userEvent.keyboard('{Escape}');
	expect(onRename).not.toHaveBeenCalled();
	expect(screen.getByRole('button', { name: 'Rename folder Sommerfest' })).toHaveFocus();
});

it('keeps the rename open when it fails, showing the row error', async () => {
	renderList({
		canEdit: true,
		onRename: vi.fn(async () => false),
		rowError: { folderId: 'f2', message: 'A folder with this name already exists.' },
	});
	await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
	await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), 'x{Enter}');
	expect(screen.getByText('A folder with this name already exists.')).toBeVisible();
	expect(screen.getByRole('textbox', { name: 'Folder name' })).toBeVisible();
});

it('refuses a blank name on the client', async () => {
	const onSubmit = vi.fn();
	render(<FolderForm label="Folder name" onSubmit={onSubmit} submitLabel="Create folder" />);
	await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), '   {Enter}');
	expect(onSubmit).not.toHaveBeenCalled();
	expect(screen.getByText('Enter a name of 1 to 60 characters.')).toBeVisible();
});

it('deletes only after confirming', async () => {
	const onDelete = vi.fn();
	renderList({ canEdit: true, onDelete });
	await userEvent.click(screen.getByRole('button', { name: 'Delete folder Sommerfest' }));
	expect(onDelete).not.toHaveBeenCalled();
	await userEvent.click(screen.getByRole('button', { name: /yes, delete it/iu }));
	expect(onDelete).toHaveBeenCalledWith('f2');
});
```

`renderList(overrides)` is a local helper. It renders `<FolderList canEdit={false} folders={folders} onDelete={vi.fn()} onRename={vi.fn(async () => true)} rowError={null} teamSlug="verein" {...overrides} />` inside whatever router or i18n wrapper `member-list.test.tsx` uses, because the names render as `Link`s.

- [ ] **Step 3: Run them and confirm they fail.** Run `pnpm --filter @kurze-url/web test -- src/components/folder-list.test.tsx`. Expected: FAIL, module not found.

- [ ] **Step 4: Implement `folder-form.tsx`**

```tsx
import { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { normalizeFolderName } from '../lib/folders';
import { Button } from './ui/button';
import { Field, FieldError, FieldLabel } from './ui/field';
import { Input } from './ui/input';

interface FolderFormProps {
	readonly autoFocus?: boolean;
	readonly error?: string;
	readonly initialName?: string;
	readonly label: string;
	readonly onCancel?: () => void;
	readonly onSubmit: (name: string) => void;
	readonly submitLabel: string;
}

/**
 * One name field, used both to create a folder and, inline, to rename one.
 * The client applies the API's name rule first, so a blank or over-long name
 * never costs a request, and it shows the same words the API's 422 maps to.
 *
 * @param props - The form's props.
 * @param props.autoFocus - Focus the field on mount, for the inline rename.
 * @param props.error - A server-side failure to show on the field.
 * @param props.initialName - The current name, when renaming.
 * @param props.label - The field's visible label.
 * @param props.onCancel - Present for the inline rename; Escape and the cancel button call it.
 * @param props.onSubmit - Called with the normalized name.
 * @param props.submitLabel - The submit button's text.
 * @returns The form.
 */
export function FolderForm({
	autoFocus = false,
	error,
	initialName = '',
	label,
	onCancel,
	onSubmit,
	submitLabel,
}: FolderFormProps): React.JSX.Element {
	const { t } = useTranslation();
	const id = useId();
	const errorId = useId();
	const input = useRef<HTMLInputElement>(null);
	const [value, setValue] = useState(initialName);
	const [localError, setLocalError] = useState<string | undefined>(undefined);
	const message = localError ?? error;

	useEffect(() => {
		if (autoFocus) input.current?.focus();
	}, [autoFocus]);

	return (
		<form
			noValidate
			onKeyDown={(event) => {
				if (event.key === 'Escape' && onCancel) onCancel();
			}}
			onSubmit={(event) => {
				event.preventDefault();
				const name = normalizeFolderName(value);
				if (name === undefined) {
					setLocalError(t('folders.nameInvalid'));
					return;
				}
				setLocalError(undefined);
				onSubmit(name);
			}}
		>
			<Field data-invalid={message !== undefined}>
				<FieldLabel htmlFor={id}>{label}</FieldLabel>
				<Input
					aria-describedby={message === undefined ? undefined : errorId}
					aria-invalid={message === undefined ? undefined : true}
					id={id}
					onChange={(event) => {
						setValue(event.target.value);
					}}
					ref={input}
					value={value}
				/>
				{message === undefined ? null : <FieldError id={errorId}>{message}</FieldError>}
			</Field>
			<Button type="submit">{submitLabel}</Button>
			{onCancel ? (
				<Button onClick={onCancel} type="button" variant="ghost">
					{t('folders.cancel')}
				</Button>
			) : null}
		</form>
	);
}
```

Lint may demand the readonly event-parameter annotations that `link-form.tsx` uses, such as `Readonly<{ target: Readonly<{ value: string }> }>`. Copy that style exactly as `link-form.tsx` writes it.

- [ ] **Step 5: Implement `folder-list.tsx`**

```tsx
import type { Folder } from '@kurze-url/api-client';
import { Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDelete } from './confirm-delete';
import { FolderForm } from './folder-form';
import { Button } from './ui/button';

interface FolderListProps {
	readonly canEdit: boolean;
	readonly folders: readonly Folder[];
	readonly onDelete: (folderId: string) => void;
	readonly onRename: (folderId: string, name: string) => Promise<boolean>;
	readonly rowError: Readonly<{ folderId: string; message: string }> | null;
	readonly teamSlug: string;
}

interface FolderRowProps extends Omit<FolderListProps, 'folders'> {
	readonly folder: Folder;
}

function FolderRow({
	canEdit,
	folder,
	onDelete,
	onRename,
	rowError,
	teamSlug,
}: FolderRowProps): React.JSX.Element {
	const { t } = useTranslation();
	const [editing, setEditing] = useState(false);
	const [restoreFocus, setRestoreFocus] = useState(false);
	const renameButton = useRef<HTMLButtonElement>(null);
	const error = rowError?.folderId === folder.id ? rowError.message : undefined;

	// Focus returns to the row's own Rename button once the inline form closes,
	// so a keyboard user is not dropped at the top of the page.
	useEffect(() => {
		if (!editing && restoreFocus) {
			renameButton.current?.focus();
			setRestoreFocus(false);
		}
	}, [editing, restoreFocus]);

	const close = (): void => {
		setEditing(false);
		setRestoreFocus(true);
	};

	return (
		<li>
			{editing ? (
				<FolderForm
					autoFocus
					error={error}
					initialName={folder.name}
					label={t('folders.name')}
					onCancel={close}
					onSubmit={(name) => {
						void onRename(folder.id, name).then((saved) => {
							if (saved) close();
						});
					}}
					submitLabel={t('folders.save')}
				/>
			) : (
				<>
					<Link params={{ teamSlug }} search={{ folder: folder.id }} to="/teams/$teamSlug/links">
						{folder.name}
					</Link>
					{canEdit ? (
						<>
							<Button
								aria-label={t('folders.renameLabel', { name: folder.name })}
								onClick={() => {
									setEditing(true);
								}}
								ref={renameButton}
								variant="ghost"
							>
								{t('folders.rename')}
							</Button>
							<ConfirmDelete
								label={t('folders.deleteLabel', { name: folder.name })}
								onConfirm={() => {
									onDelete(folder.id);
								}}
								question={t('folders.deleteQuestion', { name: folder.name })}
							/>
						</>
					) : null}
					{error === undefined ? null : <p role="alert">{error}</p>}
				</>
			)}
		</li>
	);
}

/**
 * The team's folders, alphabetically as the API returns them, each linking to
 * its filtered link list. Editors and up also rename and delete; a viewer gets
 * no control the API would refuse.
 *
 * @param props - The list's props.
 * @param props.canEdit - Whether the caller may rename and delete.
 * @param props.folders - The team's folders.
 * @param props.onDelete - Deletes the folder with the given id.
 * @param props.onRename - Renames a folder; resolves true on success, which closes the inline form.
 * @param props.rowError - The last failure and the row it happened on.
 * @param props.teamSlug - The team's slug, for the links into the filtered list.
 * @returns The list.
 */
export function FolderList({
	canEdit,
	folders,
	onDelete,
	onRename,
	rowError,
	teamSlug,
}: FolderListProps): React.JSX.Element {
	const { t } = useTranslation();

	if (folders.length === 0) {
		return (
			<p>
				{t('folders.empty')} {canEdit ? t('folders.emptyEditorHint') : null}
			</p>
		);
	}

	return (
		<ul>
			{folders.map((folder) => (
				<FolderRow
					canEdit={canEdit}
					folder={folder}
					key={folder.id}
					onDelete={onDelete}
					onRename={onRename}
					rowError={rowError}
					teamSlug={teamSlug}
				/>
			))}
		</ul>
	);
}
```

`ConfirmDelete`'s trigger text is its `label`, so the accessible name "Delete folder Sommerfest" comes from that. Check `confirm-delete.tsx` before relying on it. If the trigger renders a different accessible name, adjust the test's query rather than editing `ConfirmDelete`.

`search={{ folder: folder.id }}` only typechecks once Task 7 declares `folder` in the link list's `validateSearch`. Until then, add `folder?: string` to that route's `validateSearch` return type as a stub: parse it with `parseFolderSearch` and ignore it in the loader. Task 7 wires it through.

- [ ] **Step 6: Implement the route, `teams.$teamSlug.folders.tsx`.** It follows `teams.$teamSlug.domains.tsx`:
  - `beforeLoad` resolves `teamId` with `requireTeamId(context.me.memberships, params.teamSlug)` and also returns `role: context.me.memberships.find((m) => m.slug === params.teamSlug)?.role`.
  - The loader calls an exported `loadFolders(queryClient, teamId)`: `ensureQueryData(foldersQueryOptions(teamId))`, with the same try/catch as `loadLinks`, which turns `unauthenticated` into `throw redirect({ to: '/login' })`.
  - `errorComponent` mirrors `LinksError`.

The component:

```tsx
function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { role, teamId } = Route.useRouteContext();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const { data } = useSuspenseQuery(foldersQueryOptions(teamId));
	const folders = data.items ?? [];
	const editor = canEdit(role);
	const [createError, setCreateError] = useState<string | undefined>(undefined);
	const [createKey, setCreateKey] = useState(0);
	const [rowError, setRowError] = useState<{ folderId: string; message: string } | null>(null);

	const refresh = async (): Promise<void> => {
		await queryClient.invalidateQueries({ queryKey: ['folders', teamId] });
		await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	};
	const messageFor = (failure: FolderFailure): string | undefined => {
		if (failure === 'unauthenticated') {
			void router.navigate({ to: '/login' });
			return undefined;
		}
		return failure === 'notFound' || failure === 'rateLimited' || failure === 'unknown'
			? t(failure === 'notFound' ? 'folders.notFound' : `errors.${failure}`)
			: t(`folders.${failure}`);
	};

	const create = useMutation({
		mutationFn: async (name: string) => createFolderFn({ data: { name, teamId } }),
		onError: (error: unknown) => {
			setCreateError(messageFor(folderFailureOf(error, folders.length >= FOLDERS_PER_TEAM)));
		},
		onSuccess: async () => {
			setCreateError(undefined);
			setCreateKey((key) => key + 1); // remounts the form, clearing the field
			await refresh();
		},
	});

	const onRename = async (folderId: string, name: string): Promise<boolean> => {
		try {
			await renameFolderFn({ data: { folderId, name } });
			setRowError(null);
			await refresh();
			return true;
		} catch (error) {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { folderId, message });
			return false;
		}
	};

	const remove = useMutation({
		mutationFn: async (folderId: string) => deleteFolderFn({ data: { folderId } }),
		onError: (error: unknown, folderId: string) => {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { folderId, message });
		},
		onSuccess: async () => {
			setRowError(null);
			await refresh();
		},
	});

	return (
		<>
			<h1>{t('folders.heading')}</h1>
			<p>{t('folders.intro')}</p>
			{editor ? (
				<FolderForm
					error={createError}
					key={createKey}
					label={t('folders.name')}
					onSubmit={(name) => {
						create.mutate(name);
					}}
					submitLabel={t('folders.create')}
				/>
			) : null}
			<FolderList
				canEdit={editor}
				folders={folders}
				onDelete={(folderId) => {
					remove.mutate(folderId);
				}}
				onRename={onRename}
				rowError={rowError}
				teamSlug={teamSlug}
			/>
		</>
	);
}
```

Flatten `messageFor`'s nested ternary into an `if` chain if oxlint's `no-nested-ternary` flags it. It maps `nameTaken`, `capReached` and `nameInvalid` to `folders.*`, `notFound` to `folders.notFound`, and `rateLimited` and `unknown` to `errors.*`.

Add a route test, `teams.$teamSlug.folders.test.ts`, following `teams.$teamSlug.links.index.test.ts`:

- `loadFolders` returns the page from a fake `ensureQueryData`.
- It throws a redirect to `/login` for a 401-shaped error.
- It rethrows any other error unchanged.

- [ ] **Step 7: Sidebar.** In `app-sidebar.tsx`:
  - Import `FolderIcon` from `lucide-react`.
  - Insert a `SidebarMenuItem` between the links and domains items. Copy the domains item exactly, with `to="/teams/$teamSlug/folders"`, `<FolderIcon aria-hidden />` and `t('nav.folders')`.
  - Its `oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop` comment reads `-- same reason as the \`links\` button above.`

- [ ] **Step 8: Stories.** Create `folder-list.stories.tsx` with the same `Meta`/`StoryObj` setup as `domain-list.stories.tsx`, and args `{ canEdit, folders, onDelete: fn(), onRename: fn(async () => true), rowError: null, teamSlug: 'verein' }`. Stories:
  - `Empty` (`folders: []`, `canEdit: true`)
  - `Editor` (two folders, `canEdit: true`)
  - `Viewer` (two folders, `canEdit: false`)
  - `RowError` (`rowError: { folderId: 'f2', message: 'A folder with this name already exists.' }`)

- [ ] **Step 9: Regenerate the route tree and run everything.**

```bash
pnpm --filter @kurze-url/web run build
pnpm --filter @kurze-url/web test
pnpm --filter @kurze-url/web run test:storybook
pnpm lint && pnpm typecheck
```

Expected: all PASS. `routeTree.gen.ts` gains the folders route and keeps its trailing `declare module '@tanstack/react-start'` block. Check the diff's deletions for that block before committing.

- [ ] **Step 10: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(web): add the folders page"
```

---

### Task 6: Folder field on the link form

**Files:**

- Modify: `apps/web/src/components/link-form.tsx`, `link-form.test.tsx`, `link-form.stories.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.new.tsx` and its test
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx` and its test
- Modify: `en.json`, `de.json`

**Interfaces:**

- Consumes: `foldersQueryOptions`, `parseFolderIdSearch`
- Produces:
  - `LinkFormValues.folder_id: string`, where `''` means "No folder"
  - `LinkForm` props `folders?: readonly Readonly<{ id: string; name: string }>[]` and `folderHint?: React.ReactNode`
  - `toRequestBody(values)` sends `folder_id`
  - `toUpdateBody(values, initialFolderId: string)` is exported and sends `folder_id` only when changed

- [ ] **Step 1: i18n keys.** Under `links` in `en.json`:
  - `"folder": "Folder"`
  - `"folderNone": "No folder"`
  - `"folderNoneYet": "No folders yet. Create them on the Folders page."`
  - `"folderGone": "This folder no longer exists."`

  `de.json`:
  - `"folder": "Ordner"`
  - `"folderNone": "Kein Ordner"`
  - `"folderNoneYet": "Noch keine Ordner. Lege sie auf der Seite „Ordner“ an."`
  - `"folderGone": "Diesen Ordner gibt es nicht mehr."`

- [ ] **Step 2: Write the failing tests.**

In `link-form.test.tsx`:

```tsx
it('offers "No folder" first, then the folders, and hands back the chosen id', async () => {
	const onSubmit = vi.fn();
	renderForm({ folders: [{ id: 'f1', name: 'Sommerfest' }], onSubmit });
	const select = screen.getByRole('combobox', { name: 'Folder' });
	expect(
		within(select)
			.getAllByRole('option')
			.map((o) => o.textContent),
	).toEqual(['No folder', 'Sommerfest']);
	await userEvent.selectOptions(select, 'f1');
	await submitWithDestination();
	expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ folder_id: 'f1' }));
});

it('shows the hint when the team has no folders', () => {
	renderForm({ folderHint: 'No folders yet. Create them on the Folders page.', folders: [] });
	expect(screen.getByText('No folders yet. Create them on the Folders page.')).toBeVisible();
});

it('shows a folder_id field error on the folder field', () => {
	renderForm({
		fieldErrors: { folder_id: 'This folder no longer exists.' },
		folders: [{ id: 'f1', name: 'Sommerfest' }],
	});
	expect(screen.getByRole('combobox', { name: 'Folder' })).toHaveAccessibleDescription(
		'This folder no longer exists.',
	);
});
```

`renderForm` and `submitWithDestination` stand for this file's existing render and submit steps. Reuse them if they exist. Otherwise inline the file's existing way of rendering `LinkForm` and filling `destination_url`.

In `teams.$teamSlug.links.$linkId.test.tsx` (create it if the route's tests live elsewhere):

```ts
const base = {
	analytics_enabled: true,
	destination_url: 'https://example.org',
	domain_id: 'd1',
	expires_at: '',
	folder_id: 'f1',
	redirect_type: 302,
	slug: 'x',
};

it('omits folder_id when unchanged, so a folder missing from the list is never unfiled by accident', () => {
	expect(toUpdateBody(base, 'f1')).not.toHaveProperty('folder_id');
});
it('sends null to unfile and the id to refile', () => {
	expect(toUpdateBody({ ...base, folder_id: '' }, 'f1')).toMatchObject({ folder_id: null });
	expect(toUpdateBody({ ...base, folder_id: 'f2' }, 'f1')).toMatchObject({ folder_id: 'f2' });
});
```

In the `links.new` route test:

```ts
it('sends the chosen folder and omits an empty one', () => {
	expect(toRequestBody({ ...values, folder_id: 'f1' })).toMatchObject({ folder_id: 'f1' });
	expect(toRequestBody({ ...values, folder_id: '' }).folder_id).toBeUndefined();
});
it('preselects only a folder the team has', () => {
	expect(initialFolderId('f1', [{ id: 'f1', name: 'A' }])).toBe('f1');
	expect(initialFolderId('ffff', [{ id: 'f1', name: 'A' }])).toBe('');
	expect(initialFolderId(undefined, [{ id: 'f1', name: 'A' }])).toBe('');
});
```

- [ ] **Step 3: Run them and confirm they fail.** Run `pnpm --filter @kurze-url/web test -- link-form links.new links.\$linkId`. Expected: FAIL.

- [ ] **Step 4: Implement `LinkForm`.**
  - Add `folder_id: ''` to `defaultValues` and `'folder_id'` to `KNOWN_FIELD_NAMES`.
  - Add `readonly folder_id: string;` to `LinkFormValues`.
  - Add the props `folders` and `folderHint`, with `@param props.folders` and `@param props.folderHint` in the docblock.
  - Add `const folderErrorId = useId();`.
  - After the domain field, render the block below whenever `folders !== undefined`:

```tsx
<form.Field name="folder_id">
	{(field) => {
		const errorMessage = fieldErrors?.folder_id;
		return (
			<Field data-invalid={errorMessage !== undefined}>
				<FieldLabel htmlFor={field.name}>{t('links.folder')}</FieldLabel>
				<NativeSelect
					aria-describedby={errorMessage !== undefined ? folderErrorId : undefined}
					aria-invalid={errorMessage !== undefined ? true : undefined}
					id={field.name}
					name={field.name}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						field.handleChange(event.target.value);
					}}
					value={field.state.value}
				>
					<NativeSelectOption value="">{t('links.folderNone')}</NativeSelectOption>
					{folders.map((folder) => (
						<NativeSelectOption key={folder.id} value={folder.id}>
							{folder.name}
						</NativeSelectOption>
					))}
				</NativeSelect>
				{folders.length === 0 && folderHint !== undefined ? (
					<FieldDescription>{folderHint}</FieldDescription>
				) : null}
				{errorMessage === undefined ? null : (
					<FieldError id={folderErrorId}>{errorMessage}</FieldError>
				)}
			</Field>
		);
	}}
</form.Field>
```

- [ ] **Step 5: Implement the create route (`links.new.tsx`).**
  - Add `validateSearch: (search: { folder?: unknown } & SearchSchemaInput): { folder?: string } => ({ folder: parseFolderIdSearch(search.folder) })`, declared before `loader`, for the reason `links.index.tsx` gives.
  - The loader becomes `async ({ context }) => { const [domains, folders] = await Promise.all([loadVerifiedDomains(context.queryClient, context.teamId), context.queryClient.ensureQueryData(foldersQueryOptions(context.teamId))]); return { domains, folders: folders.items ?? [] }; }`. Widen `QueryClientSlice`'s `ensureQueryData` type if it does not accept the folders options.
  - Export `initialFolderId(requested: string | undefined, folders: readonly { id: string }[]): string`, which returns `requested` when the team has that folder and `''` otherwise.
  - `toRequestBody` gains `folder_id: values.folder_id === '' ? undefined : values.folder_id`.
  - Render `<LinkForm domains={domains} fieldErrors={fieldErrors} folderHint={<Link params={{ teamSlug }} to="/teams/$teamSlug/folders">{t('links.folderNoneYet')}</Link>} folders={folders} initial={{ folder_id: initialFolderId(search.folder, folders) }} … />`.
  - In `onError`, when `classified.kind === 'fields'` and `classified.fields.folder_id !== undefined`, replace that entry with `t('links.folderGone')`, then call `void queryClient.invalidateQueries({ queryKey: ['folders', teamId] })`.

- [ ] **Step 6: Implement the edit route (`links.$linkId.tsx`).**
  - The loader becomes `async ({ context, params }) => { const [link] = await Promise.all([loadLink(getLinkFn, params.linkId), context.queryClient.ensureQueryData(foldersQueryOptions(context.teamId))]); return link; }`.
  - In the component, read `const { data: folderPage } = useSuspenseQuery(foldersQueryOptions(teamId));`.
  - `toFormValues` gains `folder_id: link.folder_id ?? ''`.
  - Export `toUpdateBody(values, initialFolderId)` and add `...(values.folder_id === initialFolderId ? {} : { folder_id: values.folder_id === '' ? null : values.folder_id })`. Add a line to its docblock: omitting an unchanged folder means a folder missing from the loaded list is never unfiled by saving other fields.
  - The mutation calls `toUpdateBody(values, link.folder_id ?? '')`.
  - Pass `folders={folderPage.items ?? []}` and the same `folderHint` to `<LinkForm>`.
  - Apply the same `folder_id` → `t('links.folderGone')` replacement and folder refetch in the update `onError`.

- [ ] **Step 7: Stories.** Add a `WithFolders` story, with two folders, and a `NoFoldersYet` story, with `folders: []` and a plain-text `folderHint`, to `link-form.stories.tsx`.

- [ ] **Step 8: Run the tests and confirm they pass.** Run `pnpm --filter @kurze-url/web test && pnpm --filter @kurze-url/web run test:storybook && pnpm lint && pnpm typecheck`. Expected: PASS.

- [ ] **Step 9: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(web): choose a folder in the link form"
```

---

### Task 7: Folder column and filter on the link list

**Files:**

- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.index.tsx`, `teams.$teamSlug.links.index.test.ts`
- Modify: `apps/web/src/components/link-list.tsx`, `link-list.test.tsx`, `link-list.stories.tsx`
- Modify: `en.json`, `de.json`

**Interfaces:**

- Consumes: `parseFolderSearch`, `folderFilterOf`, `UNFILED_SEARCH_VALUE`, `foldersQueryOptions`, `linksQueryOptions(teamId, page, filter)`
- Produces: `LinkList` props `{ data; page; teamSlug; folders: readonly Folder[]; folder: string | undefined; onFolderChange: (folder: string | undefined) => void }`

- [ ] **Step 1: i18n keys.** Under `links` in `en.json`:
  - `"columnFolder": "Folder"`
  - `"folderFilter": "Folder"`
  - `"folderAll": "All folders"`
  - `"folderNone": "No folder"` (it already exists from Task 6; reuse it)
  - `"inFolder": "Folder: {{name}}"`
  - `"emptyInFolder": "No links in this folder."`
  - `"emptyUnfiled": "Every link is in a folder."`
  - `"folderMissing": "This folder does not exist (any more)."`
  - `"showAllLinks": "Show all links"`

  `de.json`:
  - `"columnFolder": "Ordner"`
  - `"folderFilter": "Ordner"`
  - `"folderAll": "Alle Ordner"`
  - `"inFolder": "Ordner: {{name}}"`
  - `"emptyInFolder": "Keine Links in diesem Ordner."`
  - `"emptyUnfiled": "Alle Links sind einem Ordner zugeordnet."`
  - `"folderMissing": "Diesen Ordner gibt es nicht (mehr)."`
  - `"showAllLinks": "Alle Links anzeigen"`

- [ ] **Step 2: Write the failing tests.**

In `link-list.test.tsx`:

```tsx
const folders = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
];

it('shows the folder column: a link for a filed link, "–" with hidden text for an unfiled one', () => {
	renderList({
		data: pageOf([linkWith({ folder_id: 'f1', id: 'l1' }), linkWith({ id: 'l2' })]),
		folders,
	});
	expect(screen.getByRole('columnheader', { name: 'Folder' })).toBeVisible();
	expect(screen.getByRole('link', { name: 'Sommerfest' })).toHaveAttribute(
		'href',
		expect.stringContaining('folder=f1'),
	);
	expect(screen.getByText('No folder', { selector: '.sr-only' })).toBeInTheDocument();
});

it('offers All folders, No folder, then the folders, and reports a change', async () => {
	const onFolderChange = vi.fn();
	renderList({ folders, onFolderChange });
	const select = screen.getByRole('combobox', { name: 'Folder' });
	expect(
		within(select)
			.getAllByRole('option')
			.map((o) => o.textContent),
	).toEqual(['All folders', 'No folder', 'Sommerfest']);
	await userEvent.selectOptions(select, 'none');
	expect(onFolderChange).toHaveBeenCalledWith('none');
	await userEvent.selectOptions(select, '');
	expect(onFolderChange).toHaveBeenLastCalledWith(undefined);
});

it('keeps the filter and heading visible over each empty state', () => {
	const { rerender } = renderList({ data: pageOf([]), folder: 'f1', folders });
	expect(screen.getByText('No links in this folder.')).toBeVisible();
	expect(screen.getByRole('combobox', { name: 'Folder' })).toBeVisible();
	rerender(listElement({ data: pageOf([]), folder: 'none', folders }));
	expect(screen.getByText('Every link is in a folder.')).toBeVisible();
	rerender(
		listElement({ data: pageOf([]), folder: '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a', folders }),
	);
	expect(screen.getByText('This folder does not exist (any more).')).toBeVisible();
	expect(screen.getByRole('link', { name: 'Show all links' })).toBeVisible();
});

it('keeps the folder in the pagination links and in "New link"', () => {
	renderList({
		data: { ...pageOf([linkWith({ id: 'l1' })]), total_count: 45 },
		folder: 'f1',
		folders,
	});
	expect(screen.getByRole('link', { name: /next/iu })).toHaveAttribute(
		'href',
		expect.stringContaining('folder=f1'),
	);
	expect(screen.getByRole('link', { name: /new link|create/iu })).toHaveAttribute(
		'href',
		expect.stringContaining('folder=f1'),
	);
});
```

`renderList`, `listElement`, `pageOf` and `linkWith` are small local builders. They go around this file's existing way of rendering `LinkList` and its existing `PageLink` fixtures. Keep the existing tests, and add the new props `folders={[]}`, `folder={undefined}` and `onFolderChange={vi.fn()}` to them.

In `teams.$teamSlug.links.index.test.ts`, add:

- `validateSearch({ folder: 'none', page: '2' })` returns `{ folder: 'none', page: 2 }`.
- `validateSearch({ folder: 'garbage' })` returns `{ folder: undefined, page: 1 }`.
- `loadLinks(fake, 'team-a', 1, { kind: 'unfiled' })` asks `ensureQueryData` for the key `['links', 'team-a', 1, { kind: 'unfiled' }]`.
- `folderChangeSearch('f1')` returns `{ folder: 'f1', page: 1 }`, and `folderChangeSearch(undefined)` returns `{ folder: undefined, page: 1 }`. This pins Review Focus 5: a new filter always starts at page 1.

Call `validateSearch` through `Route.options.validateSearch`, the way that file already reaches route options, if it does. Otherwise extract the function as an exported `parseLinksSearch` and test that.

- [ ] **Step 3: Run them and confirm they fail.** Run `pnpm --filter @kurze-url/web test -- link-list links.index`. Expected: FAIL.

- [ ] **Step 4: Implement the route.**

```ts
validateSearch: (search: { folder?: unknown; page?: number | string } & SearchSchemaInput): { folder?: string; page: number } => {
	const page = Number(search.page ?? 1);
	return { folder: parseFolderSearch(search.folder), page: Number.isFinite(page) && page > 0 ? page : 1 };
},
loaderDeps: ({ search }) => ({ folder: search.folder, page: search.page }),
loader: async ({ context, deps }) => {
	await Promise.all([
		loadLinks(context.queryClient, context.teamId, deps.page, folderFilterOf(deps.folder)),
		context.queryClient.ensureQueryData(foldersQueryOptions(context.teamId)),
	]);
},
```

`loadLinks` gains the `filter: FolderFilter` parameter and passes it to `linksQueryOptions`. Update its docblock with `@param filter`. Also export:

```ts
/**
 * A new folder filter always starts at page 1, as the audit log's filters do.
 *
 * @param folder - The newly chosen `folder` search value, or undefined for all folders.
 * @returns The search parameters to navigate to.
 */
export function folderChangeSearch(folder: string | undefined): {
	readonly folder?: string;
	readonly page: number;
} {
	return { folder, page: 1 };
}
```

The component:

```tsx
function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { teamId } = Route.useRouteContext();
	const { folder, page } = Route.useSearch();
	const navigate = Route.useNavigate();
	const { data } = useSuspenseQuery(linksQueryOptions(teamId, page, folderFilterOf(folder)));
	const { data: folderPage } = useSuspenseQuery(foldersQueryOptions(teamId));

	return (
		<LinkList
			data={data}
			folder={folder}
			folders={folderPage.items ?? []}
			onFolderChange={(next) => {
				void navigate({ search: folderChangeSearch(next) });
			}}
			page={page}
			teamSlug={teamSlug}
		/>
	);
}
```

- [ ] **Step 5: Implement `LinkList`.** Restructure it so the heading, the "New link" link, the filter and the context line always render, and the empty state sits under them:

```tsx
const selected = folders.find((candidate) => candidate.id === folder);
const missing = folder !== undefined && folder !== UNFILED_SEARCH_VALUE && selected === undefined;
const folderNames = new Map(folders.map((candidate) => [candidate.id, candidate.name]));
const newLinkSearch = selected === undefined ? {} : { folder: selected.id };
const emptyText =
	folder === undefined
		? t('links.empty')
		: folder === UNFILED_SEARCH_VALUE
			? t('links.emptyUnfiled')
			: t('links.emptyInFolder');
```

The rendered pieces, in order:

1. The `<h1>{t('links.heading')}</h1>`.
2. When `selected` is set, `<p>{t('links.inFolder', { name: selected.name })}</p>`. When `folder === UNFILED_SEARCH_VALUE`, `<p>{t('links.folderNone')}</p>`.
3. `<Link params={{ teamSlug }} search={newLinkSearch} to="/teams/$teamSlug/links/new">{t('links.create')}</Link>`.
4. A labelled filter: `<Field><FieldLabel htmlFor={filterId}>{t('links.folderFilter')}</FieldLabel><NativeSelect id={filterId} onChange={(event) => { onFolderChange(event.target.value === '' ? undefined : event.target.value); }} value={folder ?? ''}>`. Its options are `''` → `links.folderAll`, `'none'` → `links.folderNone`, then one per folder.
5. When `missing`, `<p>{t('links.folderMissing')} <Link params={{ teamSlug }} search={{ page: 1 }} to="/teams/$teamSlug/links">{t('links.showAllLinks')}</Link></p>` instead of the table.
6. Otherwise, when `items.length === 0`, the existing `<Empty>` block with `emptyText`. It keeps its "create" link only in the unfiltered case.
7. Otherwise, the table with a new `<TableHead>{t('links.columnFolder')}</TableHead>` between destination and actions. The cell is `link.folder_id === undefined ? (<><span aria-hidden>–</span><span className="sr-only">{t('links.folderNone')}</span></>) : (<Link params={{ teamSlug }} search={{ folder: link.folder_id, page: 1 }} to="/teams/$teamSlug/links">{folderNames.get(link.folder_id) ?? link.folder_id}</Link>)`.
8. Pagination links pass `search={{ folder, page: page ± 1 }}`.

Flatten the nested ternary in `emptyText` into an `if` chain if `no-nested-ternary` fires. Update `LinkList`'s docblock with the three new `@param props.*` tags.

- [ ] **Step 6: Stories.** In `link-list.stories.tsx`:
  - Add `folders`, `folder: undefined` and `onFolderChange: fn()` to the meta args.
  - Add the stories `WithFolderColumn`, `FilteredToFolder`, `UnfiledEmpty` and `MissingFolder`.

- [ ] **Step 7: Run the tests and confirm they pass.** Run `pnpm --filter @kurze-url/web test && pnpm --filter @kurze-url/web run test:storybook && pnpm lint && pnpm typecheck`. Expected: PASS.

- [ ] **Step 8: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "feat(web): filter the link list by folder"
```

---

### Task 8: e2e — folders end to end

**Files:**

- Create: `apps/web/e2e/folders.spec.ts`
- Modify: `apps/web/e2e/i18n.spec.ts`

**Interfaces:**

- Consumes: `test` from `./fixtures/auth` (its `page` and `teamSlug` fixtures), `waitForHydration` from `./fixtures/hydration`, `AxeBuilder`

- [ ] **Step 1: Write the spec**

```ts
import { AxeBuilder } from '@axe-core/playwright';
import { expect } from '@playwright/test';
import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

test('files a link into a folder, filters by it, renames and deletes it', async ({
	page,
	teamSlug,
}) => {
	const name = `Sommerfest ${Date.now()}`;

	await page.goto(`/teams/${teamSlug}/folders`);
	const field = page.getByLabel('Folder name');
	await waitForHydration(field);
	await field.fill(name);
	await page.getByRole('button', { name: 'Create folder' }).click();
	const folderLink = page.getByRole('link', { name });
	await expect(folderLink).toBeVisible();
	expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);

	await folderLink.click();
	await expect(page.getByText(`Folder: ${name}`)).toBeVisible();
	await page
		.getByRole('link', { name: /create/iu })
		.first()
		.click();
	const destination = page.getByLabel(/destination/iu);
	await waitForHydration(destination);
	await expect(page.getByRole('combobox', { name: 'Folder' })).toHaveValue(/.+/u);
	await destination.fill(`https://example.org/folders-${Date.now()}`);
	await page.getByRole('button', { name: /create|save/iu }).click();

	await page.goto(`/teams/${teamSlug}/links`);
	const filter = page.getByRole('combobox', { name: 'Folder' });
	await waitForHydration(filter);
	await filter.selectOption({ label: name });
	await expect(page.getByRole('cell').getByRole('link', { name })).toBeVisible();
	expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);

	await page.goto(`/teams/${teamSlug}/folders`);
	await waitForHydration(page.getByLabel('Folder name'));
	await page.getByRole('button', { name: `Rename folder ${name}` }).click();
	const renameField = page.getByRole('listitem').getByLabel('Folder name');
	await renameField.fill(`${name} 2`);
	await renameField.press('Enter');
	await expect(page.getByRole('link', { name: `${name} 2` })).toBeVisible();

	await page.getByRole('button', { name: `Delete folder ${name} 2` }).click();
	await page.getByRole('button', { name: /yes, delete it/iu }).click();
	await expect(page.getByRole('link', { name: `${name} 2` })).toHaveCount(0);

	await page.goto(`/teams/${teamSlug}/links?folder=none`);
	await expect(page.getByRole('cell', { name: 'No folder' }).first()).toBeAttached();
});
```

Match the labels and button names to the real ones in `links.spec.ts` and `fixtures/create-link.ts` for the destination field and the create button. If `create-link.ts` exports a helper that fills and submits the link form, use it after the `?folder=` navigation instead of the inline fill.

- [ ] **Step 2: Add `'folders'` to the i18n crawl.** In `i18n.spec.ts`, add `'folders'` to the route-suffix list after `'links/new'`. If the crawl's per-suffix branches (from line 354 on) need the page populated to show all its copy, add a `folders` branch that creates one folder through the UI before crawling, the way the `domains` branch claims a domain.

- [ ] **Step 3: Run the e2e.** Run it locally against a production build (memory: "Local e2e needs a production build"), or let it run against the PR's preview in CI. This spec does not depend on the Task 2 index, because no query reads a new column. See ruling 5.

- [ ] **Step 4: Commit**

```bash
pnpm format
but commit -b feat/folders-frontend -m "test(web): cover folders end to end"
```

---

## Before merging (maintainer)

1. Before merging, run against **production**: `select team_id, lower(name), count(*) from folder group by 1, 2 having count(*) > 1;`. It must return no rows, or the migration fails on merge.
2. After merging, apply `supabase/migrations/<timestamp>_folder_name_unique.sql` to the **Preview** database, so its schema matches `main`. Ruling 5 explains why this is not a precondition for e2e.
