-- Link CRUD. There is no RLS: every query here except GetLinkScope filters by
-- team_id, because Postgres enforces nothing about tenancy. password_hash is
-- never selected — only whether one exists.

-- GetLinkScope is the one deliberate exception to the team_id rule. It is what
-- the LinkViewerScope/LinkEditorScope resolver calls to *discover* which team a
-- link belongs to, so it cannot filter by the answer. Everything the handler
-- does afterwards is filtered by the team_id this returns.

-- name: GetLinkScope :one
select l.id, l.team_id, l.domain_id, d.hostname, l.slug
from link l
join domain d on d.id = l.domain_id
where l.id = $1;

-- name: CreateLink :one
with inserted as (
  insert into link (domain_id, team_id, slug, destination_url, redirect_type,
                    expires_at, analytics_enabled, created_by, folder_id)
  values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
  returning *
)
select i.id, i.domain_id, i.team_id, d.hostname, i.slug, i.destination_url,
       i.redirect_type, i.state, i.expires_at,
       (i.password_hash is not null)::boolean as has_password,
       i.analytics_enabled, i.folder_id, i.created_by, i.created_at, i.updated_at
from inserted i
join domain d on d.id = i.domain_id;

-- name: GetLinkForAPI :one
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.folder_id, l.created_by, l.created_at, l.updated_at
from link l
join domain d on d.id = l.domain_id
where l.id = $1 and l.team_id = $2;

-- Paginated. count(*) over () gives the total in the same scan, so the list
-- and its total_count cannot disagree the way two separate queries can. The
-- two CASE branches in ORDER BY implement a direction switch: whichever branch
-- is not selected evaluates to NULL for every row and therefore orders nothing.
-- l.id is the tie-break, so paging is stable when two links share a timestamp.

-- name: ListLinksForTeam :many
select l.id, l.domain_id, l.team_id, d.hostname, l.slug, l.destination_url,
       l.redirect_type, l.state, l.expires_at,
       (l.password_hash is not null)::boolean as has_password,
       l.analytics_enabled, l.folder_id, l.created_by, l.created_at, l.updated_at,
       count(*) over () as total_count
from link l
join domain d on d.id = l.domain_id
where l.team_id = sqlc.arg('team_id')
  and (sqlc.narg('state')::text is null or l.state = sqlc.narg('state')::text)
  and (sqlc.narg('domain_id')::uuid is null or l.domain_id = sqlc.narg('domain_id')::uuid)
  and (sqlc.narg('q')::text is null
       or l.slug ilike '%' || sqlc.narg('q')::text || '%'
       or l.destination_url ilike '%' || sqlc.narg('q')::text || '%')
order by
  case when sqlc.arg('sort_asc')::boolean then l.created_at end asc,
  case when not sqlc.arg('sort_asc')::boolean then l.created_at end desc,
  l.id
limit sqlc.arg('limit') offset sqlc.arg('offset');

-- CountLinksForTeam repeats the filters because the page-past-the-end fallback
-- needs a total when the windowed count returned no rows to carry it.

-- name: CountLinksForTeam :one
select count(*)
from link l
where l.team_id = sqlc.arg('team_id')
  and (sqlc.narg('state')::text is null or l.state = sqlc.narg('state')::text)
  and (sqlc.narg('domain_id')::uuid is null or l.domain_id = sqlc.narg('domain_id')::uuid)
  and (sqlc.narg('q')::text is null
       or l.slug ilike '%' || sqlc.narg('q')::text || '%'
       or l.destination_url ilike '%' || sqlc.narg('q')::text || '%');

-- UpdateLink writes every mutable column. The handler reads the row first,
-- inside the same transaction, merges the request onto it, and passes the
-- merged result here — the same read-then-write shape RenameTeam uses, and the
-- reason the audit entry can list exactly which fields changed.

-- name: UpdateLink :one
with updated as (
  update link set
    slug = $3,
    destination_url = $4,
    redirect_type = $5,
    state = $6,
    expires_at = $7,
    analytics_enabled = $8,
    updated_at = now()
  where link.id = $1 and link.team_id = $2
  returning *
)
select u.id, u.domain_id, u.team_id, d.hostname, u.slug, u.destination_url,
       u.redirect_type, u.state, u.expires_at,
       (u.password_hash is not null)::boolean as has_password,
       u.analytics_enabled, u.folder_id, u.created_by, u.created_at, u.updated_at
from updated u
join domain d on d.id = u.domain_id;

-- name: DeleteLink :execrows
delete from link where id = $1 and team_id = $2;
