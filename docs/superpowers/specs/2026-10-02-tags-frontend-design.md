# Tags in the Frontend — Design

Written 2026-10-02. This design brings tags to `apps/web`. It covers a page to manage them, a tag picker on the link form that can also create tags, a tag column on the link list, and a tag filter on that list.

This is the second slice of folders and tags. The first, folders, shipped in #97 (`docs/superpowers/specs/2026-09-26-folders-frontend-design.md`). This slice reuses that slice's patterns:

- role gating;
- status-based error mapping;
- row-scoped errors with focus management;
- tolerant prefetching;
- filters in the URL.

It also turns the folder-only management components into shared ones, so tags do not duplicate them.

The tags API came with the folders-and-tags plan (`docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`):

- tag names are unique per team, case-insensitively, and a collision is a 409;
- a team has at most 200 tags and a link at most 10;
- `tag_ids` replaces a link's whole tag set;
- links embed `tags: [{id, name}]`;
- `tag_id` filters the link list and combines with `folder_id` and `unfiled`.

## Scope

**In:**

- A tags page at `/teams/$teamSlug/tags`: list, create, rename, delete.
- A tag picker on the link form, for both create and edit. It is a combobox with chips that can also create a tag inline.
- A tag column on the link list.
- A tag filter on the link list: all tags or one tag, combinable with the folder filter.
- Shared management components, extracted from the folder ones, which the folders page then uses.
- One API correction: the unknown-tag 422 gets a location.

**Out, deliberately:**

- **Filtering by several tags at once.** `tag_id` takes one value. Several tags would need an API change and a multi-select filter, which is more than a Verein with a few dozen links needs.
- **A "No tags" filter.** It would need a new API parameter, and nobody has asked for it.
- **Link counts per tag**, for the same reason as for folders: the API has none, and each name links to its filtered list.
- **Role gating on the link pages.** It is tracked separately, as for folders. Creating a tag inside the picker, however, is gated here, because this slice adds it.

## The API correction

`resolveTagRefs` (`apps/api/internal/api/links.go`) answers an unknown or foreign id in `tag_ids` with a bare 422 message. That is the same gap the folders slice closed for `folder_id`: without a location, `classifyApiError` returns `unknown`, and the form cannot show the error on the field.

- The 422 gets `huma.ErrorDetail{Location: "body.tag_ids", Message: <message>, Value: <id>}`.
- The message stays byte-identical for a missing tag and a foreign tag, so ids still cannot be probed.
- A Go test asserts the location.
- `openapi.json` and `packages/api-client` are regenerated.

No migration: tag names have been unique per team, case-insensitively, since 2026-09-03.

## Data source

**Tags query.** Key `['tags', teamId]`. `GET /v1/teams/{team_id}/tags` returns tags ordered by name, at most 100 per page, and a team may have 200. `listTagsFor` therefore:

1. fetches page 1 with `per_page=100`;
2. fetches page 2 when `total_count` is above 100;
3. returns the concatenation.

That is at most two requests. The tags page, the link form and the link list all read this one query.

**Links query.** The filter grows from `FolderFilter` into `LinkFilter = { folder: FolderFilter; tagId?: string }`, both in the query key and in the request (`tag_id`).

**Tolerant loading.** The link pages prefetch the tags with a `prefetchTags` that never rejects, mirroring `prefetchFolders`, and read them with a non-suspense `useQuery`. The tags page loads them strictly.

**Invalidation.** After any tag write, `['tags', teamId]` and the team's link queries are invalidated. That includes a tag created from the picker.

## Shared management components

The folders slice built the management UI for folders only. Tags need the same UI, so it becomes shared. This is a refactor, and the folders page behaves exactly as before.

| Today (folder-only) | Becomes (shared) |
| --- | --- |
| `components/folder-form.tsx` `FolderForm` | `components/name-form.tsx` `NameForm` |
| `components/folder-list.tsx` `FolderList`, `FolderRowError` | `components/name-list.tsx` `NameList`, `NameRowError` |
| `FoldersPageBody` in the folders route | `components/name-management-body.tsx` `NameManagementBody` |
| the folders route's create/rename/delete wiring | a `useNameMutations` hook |
| `normalizeFolderName`, `folderFailureOf` in `lib/folders.ts` | `normalizeName`, `nameFailureOf` in `lib/names.ts` |

- **`NameForm`.** Label and submit text stay props. The two strings both kinds share move to a `names.*` namespace: "Enter a name of 1 to 60 characters." and "Cancel".
- **`NameList`.**
  - Items are `{ id, name }`.
  - Entity copy arrives in one `copy` prop: the rename label, delete label, delete question, empty text and editor hint.
  - `searchKey: 'folder' | 'tag'` decides which filter each name's link sets on `/teams/$teamSlug/links`.
  - Unchanged from today: row errors keep their `action: 'rename' | 'delete'`, `onDismissError` keeps clearing them, and focus management stays as it is.
- **`NameManagementBody`** renders the heading, intro, the editor-only create form and the list.
- **`useNameMutations`** takes the entity's server functions, its query key, its cap (100 folders, 200 tags) and its copy. It owns:
  - mapping failures through `nameFailureOf`;
  - the redirect to `/login` on 401;
  - invalidating the entity's query and the team's link queries;
  - moving focus after a create and after a delete.
- **`lib/folders.ts`** keeps only what is folder-specific: the filter helpers and the deleted-folder remap.

The proof that the refactor changed nothing is the folders slice's own tests, stories and e2e spec. They pass unchanged, except where they import the renamed modules.

## Routes

### `/teams/$teamSlug/tags` (new, `teams.$teamSlug.tags.tsx`)

- **Structure.** The route follows the folders route: a strict `loadTags` loader, an errors boundary, and `NameManagementBody` with tag copy. It lives under `$teamSlug`, so `reservedTeamSlugs` does not change.
- **Sidebar.** A "Tags" entry goes directly under "Folders", with lucide's `Tag` icon.
- **Copy.** The delete question reads: 'Delete tag "X"? The links keep everything else; they only lose this tag.' The cap message reads "A team can have at most 200 tags." In German: "Ein Verein kann höchstens 200 Tags haben."
- **Links.** Each tag name links to `/teams/$teamSlug/links?tag=<id>`.
- **Permissions.** Role gating and failure mapping are those of the folders page.

### The tag picker on the link form (`links.new`, `links.$linkId`)

**Component.** `shadcn add combobox` adds the generated combobox, built on Base UI 1.8's `Combobox` with `multiple` and chips. It sits under `components/ui/` and is never hand-edited. A wrapper, `components/tag-picker.tsx`, owns the behavior below.

The plan's first step checks that the `base-sera` registry ships a combobox with chips. If it does not, the wrapper composes `@base-ui/react/combobox` directly. Either way, the generated-file rule holds.

**Behavior.**

- **Field.** The field is labelled "Tags". Typing filters the team's tags case-insensitively, and already chosen tags are not offered again.
- **Chips.** Chosen tags show as chips. Each chip's remove button is named 'Remove tag "X"'. Backspace in the empty input removes the last chip.
- **Cap of 10.** At 10 tags the picker offers no more options and shows "At most 10 tags per link". Chips stay removable.
- **Create inline.** This is offered to editors and above only, with the role taken from `context.me.memberships`.
  - When nothing matches the input case-insensitively, the last option is 'Create tag "xyz"'. The name is checked with `normalizeName` first.
  - **Success:** the new tag is added as a chip, and `['tags', teamId]` is invalidated.
  - **409** (someone created the same name meanwhile, in any case): the tags are refetched, and the existing tag with that name is chosen.
  - **422** with the team at 200 tags: the cap message shows on the field.
  - A tag created this way stays even if the link is never saved. It is an ordinary tag with no links.
- **Focus.** It stays in the input after choosing, removing or creating.

**Values.**

- **Create.**
  - `tag_ids` is sent only when at least one tag is chosen.
  - `links/new?tag=<id>` preselects that tag when the team has it, and ignores an unknown id. "New link" on a tag-filtered list carries it, as it carries `?folder=`.
- **Edit.**
  - `tag_ids` is sent only when the set changed, compared without order.
  - Removing every chip sends `[]`.
  - This mirrors the folder field's protection against overwriting by accident.
- **Chip names.** They come from the loaded tags and, on edit, also from the link's own `tags` payload. Chips are therefore right even when the tags query failed.

**Tag deleted meanwhile.**

- The API answers 422 with `body.tag_ids`.
- The field shows "A chosen tag no longer exists. Remove it and save again.", and the tags are refetched.
- Once the tags have loaded, a chip whose id is not among them is marked "(deleted)".
- The chip is not removed automatically. This is the same rule as for a deleted folder, applied to a set.
- The remap lives in one shared, tested function next to `remapFolderGoneFailure`.

### `/teams/$teamSlug/links` (changed)

**Search parameter.**

- `tag` is a UUID or absent; anything else is dropped. It combines with `folder` and `page`.
- Changing either filter resets `page` to 1 and keeps the other filter: `folderChangeSearch` becomes a general `filterChangeSearch`.
- Pagination keeps both filters.

**Filter.** A `NativeSelect` labelled "Tag" sits next to the folder filter, with "All tags" followed by the tags alphabetically (up to 200 options). A change navigates immediately, as the folder filter does.

**Column.** A "Tags" column sits between "Folder" and "Actions".

- It shows the link's tags as chips, with names taken from `link.tags`.
- A chip links to `?tag=<id>`, keeping the current folder filter, with page 1.
- A link without tags shows "–" with the visually hidden text "No tags".

**Context.**

- An active tag filter adds "Tag: Presse" under the heading.
- "New link" carries both `?folder=` and `?tag=`.

**Empty states.**

- Tag filter only: "No links with this tag."
- Folder and tag filter: "No links in this folder with this tag."
- The tag id in the URL names no tag the team has, and the tags have loaded: "This tag does not exist (any more)." with a link back to all links.
- If both the folder and the tag are unknown, the folder message wins.

## Failure surfaces

| Where | Failure | Shown as |
| --- | --- | --- |
| tags page load | any API error | the page's `role="alert"` error, via `classifyApiError` |
| tags page create / rename | 409 | "A tag with this name already exists." on the name field |
| tags page create | 422, team at 200 tags | the cap message on the name field |
| tags page create / rename | other 422 | the shared invalid-name message on the name field |
| tags page delete | any error | `role="alert"` on the row, which stays |
| picker create | 409 | refetch, then the existing tag is chosen |
| picker create | 422, team at 200 tags | the cap message on the Tags field |
| link save | 422 `body.tag_ids` | the deleted-tag message on the Tags field; affected chips are marked |
| link list | unknown tag id, tags loaded | the "does not exist (any more)" empty state |

None of these reach Sentry (`CLAUDE.md`: `apps/web` never reports what `classifyApiError` names).

## i18n

New keys, all in both `en.json` and `de.json`. German copy says "Tag" and, where the subject is the tenant, "Verein".

- `names.*`: the shared invalid-name message and cancel.
- `tags.*`: page, form, rename, delete, errors, cap.
- `nav.tags`.
- `links.*`: picker label, placeholder, chip remove label, create option, cap hint, deleted marker and the deleted-tag error; column, filter, context line and empty states.

`catalogues.test.ts` keeps both catalogues in step. `e2e/i18n.spec.ts` adds `/tags` to its crawl, creating one tag first so the populated copy renders.

## Accessibility

- **Combobox.** Base UI supplies the ARIA roles and keyboard model.
- **Picker.** Each chip's remove button has its own name. The cap hint and field errors are tied to the input via `aria-describedby`. Focus stays in the input as described above.
- **Storybook.** The picker has stories for empty, with chosen tags, full (10), with the create option, with a deleted chip and with an error, all under the a11y check.
- **e2e.** axe runs on the tags page, on the link form with the picker open, and on the tag-filtered list.

## Testing

**Go (`apps/api`):** the unknown-tag 422 carries `location: "body.tag_ids"`, and the missing and foreign cases stay indistinguishable.

**Refactor:** the folders slice's unit tests, route tests, stories and `e2e/folders.spec.ts` pass after the extraction. Tests of moved code move with it.

**Vitest + RTL (`apps/web`):**

- `listTagsFor` fetches page 2 when `total_count` is above 100 and returns the concatenation.
- `tag-picker`:
  - filtering, choosing, removing by click and by Backspace;
  - the cap of 10;
  - inline creation with the 409 recovery and the cap message;
  - deleted-chip marking;
  - no create option for a viewer;
  - focus staying in the input.
- Create route: `tag_ids` only when non-empty, and `?tag=` preselection, including ignoring an unknown id.
- Edit route: `tag_ids` only when the set changed, compared without order, and `[]` when cleared.
- The tag-gone remap function.
- Link list:
  - the tag column, with chip links keeping the folder filter;
  - the tag filter and its combination with the folder filter;
  - `filterChangeSearch` resetting the page;
  - pagination keeping both filters;
  - the empty states, including the unknown-tag state appearing only once the tags are known.
- Tags page: the shared management tests, run against tag copy.

**e2e (`apps/web/e2e/tags.spec.ts`, against the preview):**

1. Create a tag on the tags page.
2. Create a link, choosing that tag and creating a second one inside the picker.
3. Filter the list by tag, and by tag and folder.
4. Rename the tag.
5. Delete it, and see that the link no longer shows it.

## Documentation

`CLAUDE.md` gains an entry only if this slice produces something non-obvious. The likeliest candidate is how the combobox was obtained if the `base-sera` registry did not ship one with chips. The folders-and-tags design and the folders frontend design are not edited.

## Files

**API**

- `apps/api/internal/api/links.go`, `links_test.go`
- `apps/api/openapi.json`, `packages/api-client/src/generated/*` (regenerated)

**Web: shared extraction**

- `apps/web/src/components/name-form.tsx`, `name-list.tsx`, `name-management-body.tsx`, with their stories and tests (new; replacing `folder-form.tsx` and `folder-list.tsx`, whose stories and tests move)
- `apps/web/src/lib/names.ts`, `names.test.ts` (new); `apps/web/src/lib/folders.ts` (slimmed)
- `apps/web/src/hooks/use-name-mutations.ts` and its test (new; `hooks/use-mobile.ts` is the only generated file in that directory, and `generated.config.ts` lists it by path, so a new hook beside it is linted and formatted normally)
- `apps/web/src/routes/_authed/teams.$teamSlug.folders.tsx` and its tests (switched to the shared parts)

**Web: tags**

- `apps/web/src/server/tags.ts`, `tags.test.ts` (new)
- `apps/web/src/lib/tags.ts`, `tags.test.ts` (new: search parsing, tag-gone remap)
- `apps/web/src/components/ui/combobox.tsx` (generated, via `shadcn add combobox`)
- `apps/web/src/components/tag-picker.tsx`, its stories and tests (new)
- `apps/web/src/routes/_authed/teams.$teamSlug.tags.tsx` and its tests (new)
- `apps/web/src/routes/_authed/teams.$teamSlug.links.index.tsx`, `teams.$teamSlug.links.new.tsx`, `teams.$teamSlug.links.$linkId.tsx` and their tests
- `apps/web/src/components/link-form.tsx`, `link-list.tsx`, their stories and tests
- `apps/web/src/server/links.ts` (`LinkFilter`)
- `apps/web/src/components/app-sidebar.tsx`
- `apps/web/src/i18n/locales/en.json`, `de.json`
- `apps/web/src/routeTree.gen.ts` (regenerated by the Vite plugin, because a route is added)
- `apps/web/e2e/tags.spec.ts` (new), `apps/web/e2e/i18n.spec.ts`

## Open questions this design does not answer

None for this slice. The role-gating gap on the link pages stays tracked on its own.
