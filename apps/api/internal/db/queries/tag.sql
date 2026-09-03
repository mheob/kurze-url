-- Tag CRUD and the link_tag join. There is no RLS: most queries here filter by
-- team_id because Postgres enforces nothing about tenancy. Three queries do not:
-- GetTagScope deliberately discovers the team (so it cannot filter by the answer),
-- and DeleteLinkTags and InsertLinkTags cannot filter by team_id because the
-- link_tag table has no team_id column — their safety rests entirely on the caller.

-- GetTagScope discovers the owning team from a tag ID alone. It is what the
-- TagEditorScope resolver calls to learn which team a tag belongs to, so it
-- cannot filter by the answer and is the only query that takes only an id.

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

-- DeleteLinkTags filters only by link_id because the link_tag table has no
-- team_id column. Its safety is contingent on the caller: the link_id must
-- already be the resolved link from a LinkEditorScope (which checked the
-- caller's membership in the link's owning team) and this query runs inside
-- the handler's transaction. If an invalid link_id arrives, the statement
-- silently deletes nothing — the caller must validate the link exists first.

-- name: DeleteLinkTags :exec
delete from link_tag where link_id = $1;

-- InsertLinkTags also cannot filter by team_id: the link_tag table stores only
-- link_id and tag_id. Like DeleteLinkTags, it enforces nothing itself and relies
-- on the caller. The link_id must be the resolved link (checked by LinkEditorScope),
-- and every tag_id must have already been validated through ListTagsByIDs, which
-- filters by the requesting team. If an invalid tag_id arrives, the foreign key
-- constraint fails; if a tag from a different team arrives, the handler should
-- not have passed it, but ListTagsByIDs ensures it never will.

-- name: InsertLinkTags :exec
insert into link_tag (link_id, tag_id)
select $1, unnest(@tag_ids::uuid[]);
