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
