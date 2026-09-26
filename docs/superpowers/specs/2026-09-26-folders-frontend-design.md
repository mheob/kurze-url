# Folders in the Frontend — Design

Written 2026-09-26. This design brings folders to `apps/web`. That covers a page to manage them, a folder field on the link form, a folder column on the link list, and a folder filter on that list.

The API behind it came with the folders-and-tags plan (`docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`, PR #17). It is complete apart from two gaps this design closes. The link list cannot ask for links without a folder, and folder names are not unique.

This is the first of two slices. They were cut per entity, so that each one is useful on its own. Folders come first because a link has at most one of them, so a plain select is enough. Tags come second, in their own spec. They will reuse the patterns set here and add multi-select on top.

## Scope

**In:**

- A folders page at `/teams/$teamSlug/folders`: list, create, rename, delete.
- A folder field on the link form, for both create and edit.
- A folder column on the link list.
- A folder filter on the link list: all links, links without a folder, or one folder.
- Two API additions: an `unfiled` filter on the link list, and folder names that are unique per team, case-insensitively.

**Out, deliberately:**

- **Tags.** They are the second slice and get their own spec.
- **The link list's other filters** (`q`, `state`, `domain_id`, `sort`). They stay a backlog item. The `folder` search parameter below is shaped so they can join it in one filter row later.
- **Creating a folder from inside the link form.** That is a combobox with a "create new" entry. It pays off for tags, which people invent while typing. A folder is a decision people make on the folders page.
- **Link counts per folder.** The API has none. Counting on the client would take one list request per folder. The folder name links to its filtered list, and that list shows the count.
- **Role gating on the link pages.** The link list, form and detail page show create, edit and delete controls to every member, and a viewer only learns from the API's 403. That gap existed before this design and is tracked separately. The folders page gates by role from the start, as described below.

## The API additions

Both additions live in `apps/api`. Afterwards, `openapi.json` and `packages/api-client` are regenerated. The generator runs under TypeScript 6, as `CLAUDE.md` explains.

### 1. `unfiled` on `GET /v1/teams/{team_id}/links`

A new boolean query parameter: `unfiled=true` restricts the list to links whose `folder_id` is null.

**Why a separate parameter and not a reserved `folder_id` value.** The repository's convention is flat, explicitly typed query parameters per endpoint. `folder_id` is documented as "Restrict to one folder, as a UUID". A value like `none` would make one parameter mean two types. A boolean says what it means, and the generated TypeScript types it as a boolean.

**Combination.** `unfiled=true` together with a `folder_id` is a contradiction and returns **422**, with a plain message like the existing `must be a UUID` refusals in `listLinks`. `unfiled` combines with `q`, `state`, `domain_id`, `tag_id` and `sort` exactly as `folder_id` does.

**Implementation.** `ListLinksInput` gains `Unfiled bool \`query:"unfiled"\``. Both `ListLinksForTeam`and`CountLinksForTeam`in`apps/api/internal/db/queries/link_crud.sql`gain`and (not sqlc.arg('unfiled')::bool or l.folder_id is null)`, next to their existing `folder_id`predicate. The`team_id` predicate is untouched. Nothing here reaches the redirect path or Redis.

### 2. Folder names unique per team, case-insensitively

Today the `folder` table has no uniqueness on `name` at all (`supabase/migrations/20260902075125_initial_schema.sql`). Two folders called "Sommerfest" would be indistinguishable in a select and in the filter, and folders become visible exactly where that bites.

The rule becomes the one tags already follow (folders-and-tags design, "Tag names"). The name is stored as typed and compared by `lower(name)`. That keeps one rule in the user's head for both kinds of label.

- **Migration**, created with `supabase migration new folder_name_unique`:

  ```sql
  create unique index folder_team_id_name_lower_idx on folder (team_id, lower(name));
  ```

- **Handlers.** `createFolder` and `updateFolder` in `apps/api/internal/api/folders.go` map a unique violation to **409**. They do it the way `tags.go` already does, with `isUniqueViolation`, and answer "a folder with that name already exists". A rejected write writes no audit row, because the transaction rolls back as it does for every other refusal.

**Before merging.** The index fails to build if production already holds a duplicate, and then the GitHub integration's migration run fails on merge. Until this slice, nothing but the API could create folders, so this is unlikely. It still gets checked, by the maintainer, against production:

```sql
select team_id, lower(name), count(*) from folder group by 1, 2 having count(*) > 1;
```

An empty result means the migration is safe.

**Before e2e.** This branch adds a migration, so its e2e cannot pass until the migration has been applied to the Preview database by hand (`CLAUDE.md`, "A branch that adds a migration…"). The maintainer does that step. Nothing in this design touches a database directly.

## Authorization and visibility

The API enforces everything already. `GET` on folders needs `ViewerScope`, and `POST`, `PATCH` and `DELETE` need `EditorScope` or `FolderEditorScope`. A non-member gets 404.

The folders page reads the caller's role from `context.me.memberships`, the same list `requireTeamId` resolves the team from. It shows the create form and the rename and delete controls only to `editor`, `admin` and `owner`. A viewer sees the list and the links into filtered link lists, and nothing to click that the API would refuse. This is presentation only. The API remains the enforcement point.

## Data source

- **Folders.** Query key `['folders', teamId]`, with `GET /v1/teams/{team_id}/folders?per_page=100`. The folder cap is 100, so one page always holds every folder, and nothing paginates. The folders page, the link form (create and edit) and the link list all read this one query. Their loaders call `ensureQueryData` on it, so every select is filled at server render.
- **Links.** The existing links query gains the folder filter in its key and its request.

After any folder mutation, `['folders', teamId]` is invalidated, together with the team's link queries. Links do not change when a folder is renamed, but the link list shows folder names, so it has to re-read them. After a delete it also has to re-read the links, since their folder is gone.

## Server functions

A new file, `apps/web/src/server/folders.ts`, follows `server/domains.ts` and `server/members.ts`:

- `listFolders(teamId)`
- `createFolder(teamId, name)`
- `renameFolder(folderId, name)`
- `deleteFolder(folderId)`

`server/links.ts` changes in two places:

- The list call takes the folder filter as `{ kind: 'all' } | { kind: 'unfiled' } | { kind: 'folder', folderId }`. It maps that to no parameter, to `unfiled=true`, or to `folder_id` respectively.
- The create and update calls pass `folder_id`.

## Routes

### `/teams/$teamSlug/folders` (new, `teams.$teamSlug.folders.tsx`)

The route follows `teams.$teamSlug.domains.tsx`: `beforeLoad` resolves `teamId`, the loader goes through an exported `loadFolders`, and errors pass through `classifyApiError`.

- **Sidebar.** A new "Folders" / "Ordner" entry sits between "Links" and "Domains", with lucide's `Folder` icon. The route lives under `$teamSlug`, not under `/teams/`, so `reservedTeamSlugs` does not change.
- **List.** Folders are shown alphabetically, in the order the API returns them. Each name links to `/teams/$teamSlug/links?folder=<id>`. With no folders yet, the page reads "No folders yet". Editors additionally see a pointer to the create form.
- **Create** (editor and up). A form with one name field. The client trims the name and checks 1–60 characters before sending. The API decides:
  - 409 means "name already taken"
  - 422 at the cap means "at most 100 folders"
  - 422 on the name means "invalid name"

  All three are shown on the field, keyed by `body.name`.

- **Rename** (editor and up). A per-row inline form. Opening it moves focus into the field, and `Escape` cancels. Saving or cancelling returns focus to the row's "Rename" button. Errors are the same as for create.
- **Delete** (editor and up). Uses `ConfirmDelete`. The question reads: 'Delete folder "X"? The links in it are kept and will have no folder.' `DELETE` unfiles links rather than deleting them (folders-and-tags design), so the question says so.

### `/teams/$teamSlug/links` (changed, `teams.$teamSlug.links.index.tsx`)

**Search parameter.** `folder` is absent, `none`, or a UUID. `validateSearch` parses it next to `page`, and `loaderDeps` carries it. Any value other than `none` or a well-formed UUID is dropped, as `page` already drops non-numbers. Changing the filter resets `page` to 1, and the pagination links keep the filter. This follows the audit-log page's filter handling.

**Filter control.** A `NativeSelect` labelled "Folder" sits above the table. Its options are "All folders", "No folder", then the folders alphabetically.

- A change navigates straight away, the way `audit-filter-bar.tsx` does. Focus stays on the select, and the page does not change, so this is not a change of context under WCAG 3.2.2.

**Column.** A "Folder" column sits between destination and actions. It shows the folder's name as a link to that folder's filtered list. Links without a folder show "–", with the visually hidden text "No folder". The name comes from the folders query by `folder_id`, because the link payload carries only the id.

**Context and empty states.**

- An active filter shows "Folder: Sommerfest" or "No folder" under the heading.
- The "New link" button carries the active folder to the create page as `?folder=<id>`.
- The list has three empty states:
  - "No links in this folder."
  - "Every link is in a folder."
  - "This folder does not exist (any more)." with a link back to all links. This one applies when the UUID names no folder the team has. The filter is not silently discarded.

### `/teams/$teamSlug/links/new` and `/teams/$teamSlug/links/$linkId` (changed)

`LinkForm` gains a `folders` prop and a `folder_id` field. Both follow the existing `domains` prop and `domain_id` select. The value `''` means "No folder" and is the first option.

- **Create.** Defaults to "No folder". `links/new` accepts an optional `folder` search parameter, a UUID, and preselects that folder when the team has it. An unknown or foreign id is ignored, and the field stays on "No folder".
- **Edit.** `folder_id` is sent only when it changed. Choosing "No folder" sends `null`, which `UpdateLinkInputBody` admits for exactly this purpose, and unfiles the link.
- **No folders yet.** The select holds only "No folder". A hint underneath links to the folders page.
- **Folder deleted meanwhile.** The API answers 422 with `body.folder_id`. The form's existing `fieldErrors` shows "This folder no longer exists" on the field, and `['folders', teamId]` is refetched.

## Failure surfaces

| Where | Failure | Shown as |
| --- | --- | --- |
| folders page load | any API error | the page's `role="alert"` error, via `classifyApiError`, as on the domains page |
| create / rename | 409 | "A folder with this name already exists." on the name field |
| create | 422 at the cap | "A team can have at most 100 folders." on the name field |
| create / rename | 422 on the name | "Enter a name of 1 to 60 characters." on the name field |
| delete | any error | `role="alert"` next to the list, with the row left in place |
| link form | 422 `body.folder_id` | "This folder no longer exists." on the folder field |
| link list | folder UUID unknown | the "does not exist (any more)" empty state |

None of these reach Sentry. `apps/web` never reports what `classifyApiError` names, because the visitor already sees it (`CLAUDE.md`).

## i18n

- New keys under `folders.*` for the page: heading, intro, empty state, form, rename, delete question and errors.
- `nav.folders` for the sidebar entry.
- Link-list and link-form keys under `links.*`: column, filter label and options, context line, the three empty states, form field, hint and error.
- The route's document title.

Every key exists in both `en.json` and `de.json`, and `catalogues.test.ts` holds them to that. German copy uses "Ordner" throughout.

## Accessibility

- Every control has a visible label. The filter select is labelled, not only placeholder-captioned.
- Rename manages focus as described above.
- Errors are tied to their field through the form's existing field-error rendering, and page-level errors use `role="alert"`.
- The "–" cell carries visually hidden text, so a screen reader hears "No folder" rather than a dash.
- New stories run the Storybook a11y check, and the e2e spec runs axe on the folders page and on the filtered link list.

## Testing

**Go (`apps/api`):**

- `unfiled=true` returns only links without a folder, and its count matches.
- `unfiled=true` with a `folder_id` is 422.
- `unfiled=true` never returns another team's links.
- Creating a folder whose name matches an existing one, case-insensitively, is 409.
- Renaming onto another folder's name, case-insensitively, is 409. Renaming a folder to its own name in different case is allowed.

**Vitest + RTL (`apps/web`):**

- `server/folders.ts`: each call's request shape, and error passthrough, like `members.test.ts`.
- `validateSearch` for `folder`: absent, `none`, a UUID, and garbage. Also the mapping from the parsed value to the API parameters.
- `LinkForm`:
  - the default value
  - preselection from `?folder=`, including ignoring an unknown id
  - edit sends `null` when cleared and omits `folder_id` when unchanged
  - the 422 field error
- The link list: the column (name, link, "–" with its hidden text), the filter's options and navigation, the page reset, and the three empty states.
- The folders page: create, rename (including focus and `Escape`), delete confirmation, role gating (viewer against editor), and every row of the failure table.

**Storybook:** stories for the folder list and its forms, in the states empty, populated, viewer and error.

**e2e (`apps/web/e2e/folders.spec.ts`, Playwright + axe, against the preview):**

1. Create a folder.
2. Create a link in it through `?folder=`.
3. Filter the list to that folder, then to "No folder".
4. Rename the folder.
5. Delete it, and see the link without a folder.

`i18n.spec.ts` adds `/folders` to its crawl.

## Documentation

`CLAUDE.md` changes in two places:

- The data-model summary gains "folder names are unique per team, case-insensitively, like tag names".
- The API summary gains `unfiled` next to the link list's filters.

The folders-and-tags design is not edited. This spec records what changed after it.

## Files

**API**

- `supabase/migrations/<timestamp>_folder_name_unique.sql` (new)
- `apps/api/internal/db/queries/link_crud.sql`, and the regenerated `apps/api/internal/db/*.go`
- `apps/api/internal/api/links.go`, `links_test.go`, `links_isolation_test.go` (the cross-team case)
- `apps/api/internal/api/folders.go`, `folders_test.go`
- `apps/api/openapi.json`, `packages/api-client/src/generated/*` (regenerated)

**Web**

- `apps/web/src/server/folders.ts`, `folders.test.ts` (new)
- `apps/web/src/server/links.ts`, `links.test.ts`
- `apps/web/src/routes/_authed/teams.$teamSlug.folders.tsx` and its test (new)
- `apps/web/src/routes/_authed/teams.$teamSlug.links.index.tsx`, `teams.$teamSlug.links.new.tsx`, `teams.$teamSlug.links.$linkId.tsx` and their tests
- `apps/web/src/components/folder-list.tsx`, `folder-form.tsx`, their stories and tests (new)
- `apps/web/src/components/link-form.tsx`, `link-list.tsx`, their stories and tests
- `apps/web/src/components/app-sidebar.tsx`
- `apps/web/src/i18n/locales/en.json`, `de.json`
- `apps/web/src/routeTree.gen.ts`, regenerated by the Vite plugin because a route is added
- `apps/web/e2e/folders.spec.ts` (new), `apps/web/e2e/i18n.spec.ts`

**Docs**

- `CLAUDE.md`

## Open questions this design does not answer

None for this slice. The role-gating gap on the link pages is known and tracked on its own (see Scope). The tags slice will decide its own questions, including whether a tag is created from the link form.
