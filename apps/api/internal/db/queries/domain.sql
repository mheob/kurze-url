-- Domain queries. A row with team_id IS NULL is the instance's shared
-- hostname: every team may create links on it. Every other row belongs to
-- exactly one team.

-- UpsertSharedDomain provisions the instance's shared hostname at boot. The
-- conflict target repeats domain_hostname_verified_key's own predicate
-- (verification_status = 'verified') because Postgres will only match a
-- partial unique index when the ON CONFLICT clause names that same
-- predicate. One consequence falls out of that: a *pending* claim on this
-- hostname by some team no longer collides at boot, so this insert lands
-- beside it as a second, verified row — and that team's claim can now never
-- verify, since only one verified row per hostname is allowed. That is the
-- "a claim is not a reservation" rule this migration introduces, working as
-- intended, not a gap in this query. It also narrows ErrHostnameClaimed (see
-- bootstrap.go) to what it always meant: a hostname a team has *verified*,
-- not merely asked for. The WHERE clause on the DO UPDATE branch is the
-- remaining safety catch: if the hostname is already some team's *verified*
-- custom domain, no row is updated and no row is returned, so the :one query
-- fails with pgx.ErrNoRows rather than silently seizing a hostname a team
-- owns.

-- name: UpsertSharedDomain :one
insert into domain (team_id, hostname, verification_status, verified_at)
values (null, $1, 'verified', now())
on conflict (hostname) where verification_status = 'verified' do update
  set verification_status = 'verified',
      verified_at = coalesce(domain.verified_at, now())
  where domain.team_id is null
returning id, hostname;

-- GetLinkableDomain answers "may this team put a link on this domain?".
-- Both halves matter: an unverified domain must not serve links, or a team
-- could claim a hostname it does not own, and a domain belonging to another
-- team is not this team's to use.

-- name: GetLinkableDomain :one
select id, hostname
from domain
where id = $1
  and verification_status = 'verified'
  and (team_id is null or team_id = sqlc.arg(team_id)::uuid);

-- CreateDomainClaim records a team's claim on a hostname. It is a claim, not
-- a reservation: several teams may hold one on the same hostname, and the
-- partial unique index decides the winner at verification time.

-- The team_id param is cast explicitly, same as GetDomainForTeam and its
-- siblings below: domain.team_id is nullable at the column level (the shared
-- hostname has none), and without the cast sqlc infers a nullable *uuid.UUID
-- parameter here too — but a claim always has a real, non-null owning team,
-- so the cast keeps the generated Go type honest about that.

-- name: CreateDomainClaim :one
insert into domain (team_id, hostname, verification_token)
values (sqlc.arg(team_id)::uuid, sqlc.arg(hostname), sqlc.arg(verification_token))
returning *;

-- ListDomainsForTeam casts team_id for the same reason CreateDomainClaim does
-- just above: the column is nullable, this query is never called with a null
-- team.

-- name: ListDomainsForTeam :many
select *, count(*) over () as total_count
from domain
where team_id = sqlc.arg(team_id)::uuid
order by hostname
limit sqlc.arg('limit') offset sqlc.arg('offset');

-- name: GetDomainForTeam :one
select *
from domain
where id = $1 and team_id = sqlc.arg(team_id)::uuid;

-- GetDomainScope discovers the owning team from a domain ID alone, so it
-- cannot filter by the answer — the same exception GetTagScope is. team_id is
-- nullable because the shared hostname has none; the resolver treats that null
-- as "not found", because nobody administers the shared domain through this
-- API.

-- name: GetDomainScope :one
select id, team_id
from domain
where id = $1;

-- MarkDomainVerified is the transition. It fails with a unique violation when
-- another team already holds this hostname as verified, which is exactly the
-- race the partial index exists to lose safely.

-- name: MarkDomainVerified :one
update domain
set verification_status = 'verified',
    verified_at = now()
where id = $1 and team_id = sqlc.arg(team_id)::uuid
returning *;

-- FailCompetingClaims settles the other claims on a hostname once one wins.
-- They are marked rather than deleted so the losing team sees an answer
-- instead of a vanished row. A failed row blocks nothing: the unique index
-- covers verified rows only.

-- name: FailCompetingClaims :exec
update domain
set verification_status = 'failed'
where hostname = $1
  and id <> sqlc.arg(keep_id)::uuid
  and verification_status <> 'verified';

-- CountLinksForDomain answers "would deleting this domain destroy anything?".
-- link.team_id is denormalized precisely so this needs no join, and it is
-- filtered here even though the scope already authorized the caller.

-- name: CountLinksForDomain :one
select count(*)
from link
where domain_id = sqlc.arg(domain_id)::uuid
  and team_id = sqlc.arg(team_id)::uuid;

-- name: DeleteDomain :execrows
delete from domain
where id = $1 and team_id = sqlc.arg(team_id)::uuid;
