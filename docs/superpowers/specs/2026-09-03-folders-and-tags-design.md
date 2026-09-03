# Folders and Tags — Design

**Status:** approved 2026-09-03 **Supersedes nothing. Amends:** `docs/planning/05-database-schema.md` (the `folder.parent_folder_id` note "nullable, allows nesting"), `docs/planning/06-api-design.md` (silent on how tags attach to a link).

This is the fourth implementation spec for the URL shortener. It builds on three merged plans:

- **Plan 1** — foundation and the redirect path: the schema, the Redis-fronted `GET /{slug}` hot path, negative caching, unique-visitor hashing, the password interstitial, rate limiting.
- **Plan 2** — tenancy, authorization and audit: teams, members, invitations, the four role scopes resolved by Huma before a handler body runs, and one audit row per mutation inside the mutation's own transaction.
- **Plan 3** — links and the shared domain: `domain.team_id` made nullable for the shared hostname, the boot-time upsert, the entity-scope pattern in `internal/authz`, slug generation and normalization, destination validation, and link CRUD with redirect-cache invalidation.

Plan 3 shipped link CRUD without any way to organize the result. The schema has carried `folder`, `tag`, `link_tag` and `link.folder_id` since the initial migration, and nothing reads or writes them. A team with forty links has forty rows and no structure over them. This spec closes that gap, and in doing so finishes the link representation that plan 3 deliberately left partial.

---

## Goal

A team can group its links into folders and label them with tags, and can find links by either. Every such operation is confined to the caller's team, and none of it costs the redirect path anything.

---

## Scope

### In scope

- Folder CRUD, team-scoped: create, list, rename, delete.
- Tag CRUD, team-scoped: create, list, rename, delete.
- `folder_id` and `tag_ids` on link create and update.
- `folder_id` and `tag_id` filters on the link list.
- `folder_id` and the embedded tag set on every link representation.
- One migration making tag names unique case-insensitively per team.
- Per-team and per-link count caps.
- Six new audit actions.

### Out of scope, and where each lands

- **Folder nesting.** `parent_folder_id` stays in the schema and stays unwritten. See "Folders are flat" below.
- **Custom domains and their verification.** The next plan; they need a Vercel API token and a DNS-verification surface that has nothing in common with this work.
- **Password endpoints, QR, stats, scanning.** Later plans, unchanged by this one.
- **Bulk operations** — moving many links into a folder in one request. Deferred with the rest of the bulk surface in `CLAUDE.md`.

---

## Global constraints

These bind every task. They are the project's rules, restated here so the plan's executors do not have to reconstruct them from `CLAUDE.md`.

- The tenant is called `team` in every identifier. "Verein" appears only in user-facing German copy.
- `GET /<slug>` is the hot path. Nothing this spec adds may cost it a single extra Redis command or database round trip.
- There is no RLS. Every query that touches team data filters by `team_id` in Go. A query without that filter is a data-leak bug.
- Never store a full IP address.
- Errors are Huma's RFC 9457 `application/problem+json`. No custom error model.
- Pagination is offset/limit with the existing `api.Page[T]` envelope, `per_page` capped at 100.
- Filtering uses flat, explicitly typed query parameters per endpoint.
- Operations that need authentication declare `Security: []map[string][]string{{"bearerAuth": {}}}`.
- Migrations are owned by the Supabase CLI (`supabase migration new`, `supabase db push`).
- Database access is sqlc-generated from raw SQL in `apps/api/internal/db/queries/`. No ORM.
- Go tests run against real Postgres and Redis, as plans 2 and 3 do. Mocks are for external HTTP only.

---

## Folders are flat

### The decision

Folders have exactly one level. Every folder belongs to a team and to nothing else. `parent_folder_id` remains in the schema, remains nullable, and is never written by the API.

### Why

`docs/planning/05-database-schema.md` annotates the column "nullable, allows nesting", which is a statement about the schema's capacity, not a requirement anyone wrote down. Nothing in the feature list asks for a tree. A Verein with a few dozen links does not need one, and tags already provide the cross-cutting grouping that nesting is usually reached for.

Shipping the tree would add four obligations, each of which has to be built, reviewed and tested:

- **Cycle prevention.** No Postgres constraint can express "this parent chain does not return to me". It would have to be a Go check on every parent change, walking the chain — correct only if no code path ever skips it.
- **A depth cap**, for the same reason, plus the recursion it implies in every read.
- **A recursion decision on `?folder_id=`** — whether filtering by a folder includes its descendants. Both answers surprise somebody.
- **Delete semantics with children** — reparent to the grandparent, orphan to the root, or refuse.

None of these earn their cost at this stage. Keeping the column costs nothing and means nesting arrives later as an API change rather than a migration.

### What enforces it

The column being unused is a property that decays silently, so it is asserted rather than assumed: a test creates folders through the API and fails if any row comes back with a non-NULL `parent_folder_id`. The insert statement does not name the column at all, so the default applies.

### The rejected alternative

Dropping the column outright is the honest schema, and it was rejected only because it costs a migration now and a second one if nesting ever ships. The column is inert either way; the comment on it carries the intent.

---

## Tag names

### The decision

Tag names are unique per team, case-insensitively. The name is stored exactly as the user typed it. A migration replaces the existing constraint:

```sql
drop constraint tag_team_id_name_key;
create unique index tag_team_id_name_lower_idx on tag (team_id, lower(name));
```

Creating "Sommerfest" when "sommerfest" exists is a 409.

### Why case-insensitive

German capitalizes every noun, so a team's tags are overwhelmingly capitalized words that a hurried user will sometimes type lowercase. "Sommerfest" and "sommerfest" as two distinct tags is a filter list that quietly rots. Slugs are already case-insensitive for the same class of reason, and matching the two keeps one rule in the user's head instead of two.

### Why the stored value keeps its case

Lowercasing on write is the cheaper implementation — the existing constraint would suffice and no migration would be needed — and it is wrong for this language. "sommerfest" rendered in a German UI reads as a typo. The display value and the uniqueness key are different things, so they are stored and computed differently.

### Why the database enforces it rather than Go

There is no RLS on this project, which means Postgres already enforces nothing about tenancy and every such guarantee lives in Go. That is a large enough surface without adding to it. A `strings.ToLower` before an insert is one line, and one line that a later code path forgets to copy; a unique index cannot be forgotten. Where a constraint can hold an invariant, it holds it.

The handler still lowercases for its own comparisons, but correctness does not depend on that.

---

## Referencing a folder or a tag from a link

### The problem

`POST /v1/teams/{team_id}/links` and `PATCH /v1/links/{link_id}` accept `folder_id` and `tag_ids` in the request body. Those are user-supplied UUIDs naming rows in tables that other teams also populate. A link must never end up filed in another team's folder or labelled with another team's tag, and the request must never reveal whether such a row exists.

This is precisely the failure mode the executable permission matrix cannot see. `apps/api/internal/api/matrix_test.go` observes HTTP status per operation and role; a handler with a correct scope embed whose SQL forgets its tenancy filter passes it cleanly.

### The decision

Every reference resolves through a query that filters by the link's team, and the handler diffs what it asked for against what came back:

```sql
-- name: ListTagsByIDs :many
select id, name from tag where team_id = $1 and id = any($2::uuid[]);
```

```sql
-- name: GetFolderInTeam :one
select id, name from folder where team_id = $1 and id = $2;
```

The `team_id` comes from the authorization scope, never from the request body. An id that does not come back — because no such row exists, or because it belongs to another team — produces **422** naming that id.

### Why 422 and not 404

Plan 3 established that a non-member addressing `/v1/links/{id}` gets 404, never 403, so that link ids cannot be probed. That rule is about the _addressed_ resource. Here the addressed resource is a link the caller demonstrably may edit; it exists, and saying otherwise would be a lie that also breaks the client's retry logic. The bad value is a field in the body, which is what 422 is for.

Nothing leaks, because the two cases are indistinguishable in the response: "no such folder" and "another team's folder" produce byte-identical problem details. An attacker learns only that an id they guessed is not theirs, which they knew.

### Why the filter lives in the SQL

It could equally be a Go-side comparison after an unfiltered fetch, and that reads about the same at the call site. It is written into the statement because a reviewer looking at `tag.sql` sees the tenancy rule without holding the handler in their head, and because an unfiltered `where id = any($1)` sitting in the query file is a loaded gun for the next person who reuses it.

---

## Entity-scoped authorization

`/v1/folders/{folder_id}` and `/v1/tags/{tag_id}` carry an entity id and no team id, so they authorize the way `/v1/links/{link_id}` does: a scope struct resolves the entity to discover its team, then reuses the membership check.

Two new scopes in `internal/authz`, copied from `LinkEditorScope`:

| Scope               | Used by                                   |
| ------------------- | ----------------------------------------- |
| `FolderEditorScope` | `PATCH`, `DELETE /v1/folders/{folder_id}` |
| `TagEditorScope`    | `PATCH`, `DELETE /v1/tags/{tag_id}`       |

There is deliberately **no** `FolderViewerScope` or `TagViewerScope`. Plan 3 needed a viewer scope for `GET /v1/links/{link_id}`, but folders and tags have no read-one endpoint: reads go through the team-scoped list endpoints, which use the existing team viewer scope. Building the entity viewer scopes anyway — for symmetry, or against a read-one endpoint nobody has asked for — would be two resolvers with no caller and two more things a reviewer has to check.

The 404-not-403 rule carries over exactly: a folder that does not exist and a folder belonging to another team return byte-identical 404s, so folder ids cannot be probed. A member of the owning team whose role is too low gets 403 — that caller already knows the folder exists.

Read the existing `LinkViewerScope` before writing these rather than reinventing the rule; the asymmetry between 404 and 403 is easy to get subtly wrong.

**A debt paid here.** `resolveMembership` currently documents rather than enforces its precondition that callers check claims first. Plan 3 left it that way because both callers complied. This spec adds the third, fourth, fifth and sixth callers, which is where a documented precondition becomes a real risk — so it becomes enforced.

---

## Endpoints

All under `/v1`, all Bearer-authenticated, all declaring their scope.

### `POST /v1/teams/{team_id}/folders`

Editor. Body `{name}`. 201 with the folder. 422 when the team is at its folder cap.

### `GET /v1/teams/{team_id}/folders`

Viewer. `Page[Folder]`, ordered by name. No filters — a capped list of at most 100 folders does not need them.

### `PATCH /v1/folders/{folder_id}`

Editor, via `FolderEditorScope`. Body `{name}`. 200 with the folder.

### `DELETE /v1/folders/{folder_id}`

Editor, via `FolderEditorScope`. 204.

Links in the folder become unfiled: `link.folder_id` falls to NULL, which the existing `on delete set null` foreign key already does without application help. Nothing is destroyed and the operation is reversible by refiling, which is why a non-empty folder is not refused and no `?force=true` flag exists. The audit metadata records how many links were unfiled.

### `POST /v1/teams/{team_id}/tags`

Editor. Body `{name}`. 201. 409 when the name collides case-insensitively, 422 at the cap.

### `GET /v1/teams/{team_id}/tags`

Viewer. `Page[Tag]`, ordered by name.

### `PATCH /v1/tags/{tag_id}`

Editor, via `TagEditorScope`. Body `{name}`. 200. 409 on a case-insensitive collision with another tag in the same team.

### `DELETE /v1/tags/{tag_id}`

Editor, via `TagEditorScope`. 204. `link_tag` rows cascade; the links themselves are untouched.

### Changes to plan 3's link endpoints

`POST /v1/teams/{team_id}/links` and `PATCH /v1/links/{link_id}` gain `folder_id` (nullable) and `tag_ids` (array) in the body. `GET /v1/teams/{team_id}/links` gains `folder_id` and `tag_id` query parameters, each restricting to one value, combinable with the existing `q`, `state`, `domain_id` and `sort`.

---

### Name validation

Folder and tag names share one rule, applied on create and on rename: Unicode-trimmed of leading and trailing whitespace, non-empty after trimming, at most 60 characters. Rejection is a 422.

Sixty is a label, not a sentence — it fits a filter chip and a table column without truncation, and it holds the longest realistic German compound a Verein will reach for. The cap exists mainly because `name` is `text` in Postgres, so without it a single row can carry a megabyte; the count caps above bound how many rows a team creates, and this bounds how large one gets.

Names are otherwise unrestricted: umlauts, emoji and spaces are all fine. The stored value is exactly what was typed, per the case decision above.

---

## Representation

```jsonc
// Folder
{ "id": "…", "team_id": "…", "name": "Sommerfest 2026", "created_at": "…" }

// Tag
{ "id": "…", "team_id": "…", "name": "Presse" }
```

`Folder` deliberately does not expose `parent_folder_id`. The API does not write it, so publishing it would advertise a capability that does not exist.

The `Link` representation gains two fields:

```jsonc
{
	// … everything plan 3 already returns …
	"folder_id": "…", // nullable
	"tags": [{ "id": "…", "name": "Presse" }],
}
```

Tags embed their names rather than being a bare id array. A link table is the primary place tags are read, and ids alone would force every client to fetch the team's whole tag list and join it in the browser — a second round trip and a cache-coherence problem, to save a few bytes per row.

---

## Tag replacement semantics

`tag_ids` is a whole-set replacement, not a delta:

- Omitted from a PATCH: the link's tags are untouched.
- `[]`: every tag is detached.
- `[a, c]` on a link tagged `[a, b]`: `b` is detached, `c` attached.

Inside the link write's existing transaction: `delete from link_tag where link_id = $1`, then insert the validated set. The delete-then-insert is unconditional rather than a computed diff — the set is at most ten rows, and a diff would be more code for no measurable gain.

This mirrors plan 3's decision that a PATCH is one request and the audit log should say so. Two people editing the same link's tags concurrently is last-write-wins, which is the exposure every other PATCH field on this endpoint already has; it is not worth a separate concurrency mechanism for one field.

The rejected alternative was a subresource pair, `PUT`/`DELETE /v1/links/{link_id}/tags/{tag_id}`, which is idempotent per tag and immune to lost updates. It was rejected because three tag changes become three requests, the client has to diff old against new to know which calls to make, and the precedent it would follow — the password subresource — earned its split with a distinct audit action and its own rate limit. Tags have neither.

---

## Listing links with their tags

The link list returns each link's tags, which invites an N+1. It is avoided with one additional query per page, not a join:

```sql
-- name: ListTagsForLinks :many
select lt.link_id, t.id, t.name
from link_tag lt join tag t on t.id = lt.tag_id
where lt.link_id = any($1::uuid[]) and t.team_id = $2;
```

The handler runs the existing list query, collects the page's link ids, runs this once, and stitches the results in Go.

A `left join` onto the main list query is wrong rather than merely slower: it multiplies rows before `LIMIT` applies, so a page of 20 links would silently return fewer. A lateral `array_agg` or `json_agg` would work in one round trip, at the cost of a sqlc type that has to be unpacked by hand. For a page capped at 100 rows, one extra round trip on a warm connection is not worth that complexity — and the second query carries its own visible `team_id` filter, which the aggregate would bury inside a subquery.

---

## Count caps

| Limit            | Value |
| ---------------- | ----- |
| Folders per team | 100   |
| Tags per team    | 200   |
| Tags per link    | 10    |

Exceeding one is a 422 whose detail states the limit. The per-team caps are checked with one `count(*)` against an indexed `team_id` immediately before the insert, inside the same transaction; the per-link cap is a length check on the request array.

These are not rate limits — the write rate limit from plan 1 still applies on top. They exist because the Supabase free tier is 500 MB **and has no backups at all** (`CLAUDE.md`, open items), which makes unbounded row growth the one failure mode with no recovery path. A rate limit makes bulk creation slow; a cap makes it impossible.

The numbers are generous for a Verein by roughly an order of magnitude and live in one constants block, so raising them is a one-line change.

The `count(*)`-then-insert is a check-then-act, so two concurrent creates can both pass the check and leave a team one row over its cap. That is accepted: the cap protects a 500 MB budget, not an invariant, and being one row over does not matter. Closing the race would need serializable isolation or an advisory lock on every create, which is real cost for no benefit.

---

## The redirect path is untouched

`link.Cached` (`apps/api/internal/link/cached.go`) carries the id, team, destination, redirect type, state, expiry, password flag and analytics flag. It carries nothing about folders or tags, and this spec does not add anything.

Therefore **no folder or tag write invalidates the redirect cache**, and the hot path's Redis command count is unchanged by this plan. This is stated explicitly because the opposite is the intuitive assumption: plan 3 established that link writes invalidate the cache, and someone extending link writes here will reasonably reach for the same call. Adding a defensive invalidation would cost a Redis command per organizational edit and buy nothing.

If a future change puts a folder or tag into the cached payload, that changes and the rule moves with it.

---

## Audit

Six new actions beside the existing block in `internal/audit/audit.go`:

```
folder.created   folder.updated   folder.deleted
tag.created      tag.updated      tag.deleted
```

Folder and tag changes made _through a link write_ do not get their own action. They are part of the `link.updated` row the write already produces, with the affected fields listed in `metadata.changed` — governed by the comment already in `audit.go`: one row per PATCH, not one per changed field, because a single PATCH is one request and splitting it would misrepresent one request as several.

`folder.deleted` metadata records the number of links unfiled. No metadata carries a name that could be a secret; the existing denylist in `audit.go` applies unchanged.

This writes down part of the `audit_log.action` taxonomy that `CLAUDE.md` flags as an open item. It does not close that item — domains, passwords, QR and stats actions remain unwritten.

---

## Testing

Following plans 2 and 3: real Postgres and Redis, no mocks except for external HTTP, and no skips.

- **Per-resource cross-team isolation**, mirroring `links_isolation_test.go`: a team's folders and tags are invisible and unmodifiable from another team, and the assertion is on persisted state, not only on the response status.
- **Reference validation**: a link create and a link update each rejected with 422 for a folder and for a tag belonging to another team, with the response proven identical to the nonexistent-id case.
- **Case-fold uniqueness**: "Sommerfest" then "SOMMERFEST" is a 409; the stored name keeps its original case.
- **Flatness**: no folder created through the API has a non-NULL `parent_folder_id`.
- **Caps**: each of the three enforced, each reporting its limit.
- **Delete semantics**: deleting a folder unfiles its links and leaves them otherwise untouched; deleting a tag removes its `link_tag` rows and leaves the links untouched.
- **The permission matrix** picks up the eight new operations automatically, or fails the build.

**Falsification by mutation, not deletion**, as in plan 3 — each property is shown to fail when the behaviour is mutated and to pass when it is restored. Deleting the code instead proves only that the compiler noticed a dangling reference. The properties to falsify: each new query's `team_id` filter, the 422 reference check, the tag-replacement delete, the case-fold index, and each cap.

---

## Open questions

None blocking. Two things this spec deliberately leaves to a later plan:

- **Bulk refiling** — moving many links into a folder in one request. The obvious next ask once folders exist, and out of scope with the rest of the bulk surface.
- **Whether tags need a colour or description.** Every tag UI eventually grows one. Adding the column later is a trivial migration; guessing now is a schema change made without a requirement.
