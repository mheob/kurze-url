-- Tenancy queries. There is no RLS: every one of these filters by team_id (or
-- by the caller's user_id), because Postgres enforces nothing about tenancy.

-- name: GetTeamMembership :one
select team_id, user_id, role
from team_member
where team_id = $1 and user_id = $2;

-- name: CreateTeam :one
insert into team (name) values ($1)
returning id, name, created_at;

-- name: GetTeam :one
select id, name, created_at from team where id = $1;

-- name: RenameTeam :one
update team set name = $2 where id = $1
returning id, name, created_at;

-- Paginated. count(*) over () gives the total in the same scan, so the list
-- and its total_count cannot disagree the way two separate queries can.

-- name: ListTeamsForUser :many
select t.id, t.name, t.created_at, tm.role, count(*) over () as total_count
from team t
join team_member tm on tm.team_id = t.id
where tm.user_id = $1
order by t.name, t.id
limit $2 offset $3;

-- count(*) over () above is only readable off a returned row, so a page past
-- the end has nothing to read it from. This plain count is the fallback for
-- that case only; the paginated query above remains the source of truth for
-- every in-range page.

-- name: CountTeamsForUser :one
select count(*) from team_member tm where tm.user_id = $1;

-- Unpaginated on purpose: this drives the frontend's team switcher, and a
-- person belongs to a handful of Vereine.

-- name: ListMembershipsForUser :many
select tm.team_id, t.name as team_name, tm.role
from team_member tm
join team t on t.id = tm.team_id
where tm.user_id = $1
order by t.name, t.id;

-- Returns created_at because POST /v1/teams/{team_id}/members echoes the
-- created member back, and a fabricated timestamp would be a lie.

-- name: InsertTeamMember :one
insert into team_member (team_id, user_id, role) values ($1, $2, $3)
returning created_at;

-- name: ListTeamMembers :many
select tm.user_id, u.email, tm.role, tm.created_at, count(*) over () as total_count
from team_member tm
join auth.users u on u.id = tm.user_id
where tm.team_id = $1
order by u.email, tm.user_id
limit $2 offset $3;

-- Fallback for a page past the end; see CountTeamsForUser.

-- name: CountTeamMembers :one
select count(*) from team_member tm where tm.team_id = $1;

-- name: UpdateTeamMemberRole :exec
update team_member set role = $3 where team_id = $1 and user_id = $2;

-- name: DeleteTeamMember :exec
delete from team_member where team_id = $1 and user_id = $2;

-- Locks the team's owner rows for the rest of the transaction. Without this,
-- two concurrent demotions both read "there are two owners" and both succeed,
-- leaving the team ownerless.

-- name: LockTeamOwners :many
select user_id from team_member
where team_id = $1 and role = 'owner'
for update;

-- name: GetUserIDByEmail :one
select id from auth.users where lower(email) = lower($1) limit 1;
