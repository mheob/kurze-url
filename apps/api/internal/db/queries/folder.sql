-- Folder CRUD. There is no RLS: every query here except GetFolderScope filters
-- by team_id, because Postgres enforces nothing about tenancy.
--
-- parent_folder_id is never written. Folders are flat in this iteration and the
-- column is left for a later plan; no statement below names it, so the column
-- default applies and it stays NULL.

-- GetFolderScope is the one deliberate exception to the team_id rule. It is
-- what the FolderEditorScope resolver calls to *discover* which team a folder
-- belongs to, so it cannot filter by the answer. Everything the handler does
-- afterwards is filtered by the team_id this returns.

-- name: GetFolderScope :one
select id, team_id
from folder
where id = $1;

-- GetFolderInTeam validates a folder_id supplied in a link's request body. The
-- team_id comes from the authorization scope, never from the request, so a
-- folder belonging to another team simply returns no row.

-- name: GetFolderInTeam :one
select id, name
from folder
where team_id = $1 and id = $2;

-- name: CreateFolder :one
insert into folder (team_id, name)
values ($1, $2)
returning id, team_id, name, created_at;

-- Paginated. count(*) over () gives the total in the same scan, so the list and
-- its total_count cannot disagree the way two separate queries can. Ordered by
-- name because a folder list is something a human reads alphabetically.

-- name: ListFoldersForTeam :many
select id, team_id, name, created_at, count(*) over () as total_count
from folder
where team_id = $1
order by name
limit $2 offset $3;

-- CountFoldersForTeam serves two callers: the pagination total when the page
-- is past the end and the window function returned no rows, and the per-team
-- cap check before an insert.

-- name: CountFoldersForTeam :one
select count(*) from folder where team_id = $1;

-- name: UpdateFolder :one
update folder
set name = $3
where id = $1 and team_id = $2
returning id, team_id, name, created_at;

-- CountLinksInFolder is read before a delete, for the audit metadata. It is a
-- separate statement rather than a CTE beside the delete so that the number
-- reported is unambiguously the pre-delete count.

-- name: CountLinksInFolder :one
select count(*) from link where folder_id = $1 and team_id = $2;

-- DeleteFolder returns the id so the handler can tell "deleted" from "no such
-- folder in this team" without a second round trip. Links in the folder are
-- unfiled by the on delete set null foreign key, not by application code.

-- name: DeleteFolder :one
delete from folder
where id = $1 and team_id = $2
returning id;
