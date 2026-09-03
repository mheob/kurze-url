-- Tag CRUD and the link_tag join. There is no RLS: every query here except
-- GetTagScope filters by team_id, because Postgres enforces nothing about
-- tenancy.

-- GetTagScope is the one deliberate exception to the team_id rule. It is what
-- the TagEditorScope resolver calls to *discover* which team a tag belongs to,
-- so it cannot filter by the answer.

-- name: GetTagScope :one
select id, team_id
from tag
where id = $1;

-- ListTagsByIDs validates the tag_ids supplied in a link's request body. The
-- team_id comes from the authorization scope, never from the request, so a tag
-- belonging to another team simply does not come back — and the handler names
-- the missing id in a 422. The message is identical for a nonexistent id and
-- another team's id, so nothing leaks.

-- name: ListTagsByIDs :many
select id, name
from tag
where team_id = $1 and id = any(@ids::uuid[]);

-- name: CreateTag :one
insert into tag (team_id, name)
values ($1, $2)
returning id, team_id, name;

-- name: ListTagsForTeam :many
select id, team_id, name, count(*) over () as total_count
from tag
where team_id = $1
order by name
limit $2 offset $3;

-- name: CountTagsForTeam :one
select count(*) from tag where team_id = $1;

-- name: UpdateTag :one
update tag
set name = $3
where id = $1 and team_id = $2
returning id, team_id, name;

-- DeleteTag returns the name as well as the id: the audit row records which
-- tag was deleted, and returning it here costs nothing over a second lookup.

-- name: DeleteTag :one
delete from tag
where id = $1 and team_id = $2
returning id, name;

-- ListTagsForLinks is the second query the link list runs, once per page. A
-- left join onto the list query itself would be wrong rather than merely
-- slower: it multiplies rows before LIMIT applies, so a page of 20 links would
-- silently return fewer. The tag.team_id filter is redundant given the link
-- ids are already the caller's, and it stays because a query file is read on
-- its own.

-- name: ListTagsForLinks :many
select lt.link_id, t.id, t.name
from link_tag lt
join tag t on t.id = lt.tag_id
where lt.link_id = any(@link_ids::uuid[]) and t.team_id = $1
order by t.name;

-- DeleteLinkTags and InsertLinkTags implement whole-set replacement inside the
-- link write's own transaction. The delete is unconditional rather than a
-- computed diff: the set is at most ten rows, and a diff would be more code
-- for no measurable gain.

-- name: DeleteLinkTags :exec
delete from link_tag where link_id = $1;

-- name: InsertLinkTags :exec
insert into link_tag (link_id, tag_id)
select $1, unnest(@tag_ids::uuid[]);
