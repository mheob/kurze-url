-- Domain queries. A row with team_id IS NULL is the instance's shared
-- hostname: every team may create links on it. Every other row belongs to
-- exactly one team.

-- UpsertSharedDomain provisions the instance's shared hostname at boot. The
-- WHERE clause on the conflict branch is the safety catch: if the hostname is
-- already registered as some team's verified custom domain, no row is updated
-- and no row is returned, so the :one query fails with pgx.ErrNoRows rather
-- than silently seizing a hostname a team owns.

-- name: UpsertSharedDomain :one
insert into domain (team_id, hostname, verification_status, verified_at)
values (null, $1, 'verified', now())
on conflict (hostname) do update
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
