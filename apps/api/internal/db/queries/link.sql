-- Redirect-path lookup. Only verified domains resolve: an unverified domain
-- must not serve links, or a team could claim a hostname it does not own.
-- Note password_hash is projected as a boolean here so the hash never leaves
-- the database on the hot path.

-- name: GetLinkForRedirect :one
select
  l.id,
  l.team_id,
  l.destination_url,
  l.redirect_type,
  l.state,
  l.expires_at,
  l.analytics_enabled,
  (l.password_hash is not null)::boolean as has_password
from link l
join domain d on d.id = l.domain_id
where d.hostname = $1
  and l.slug = $2
  and d.verification_status = 'verified';

-- name: GetLinkForVerify :one
select
  l.id,
  l.team_id,
  l.destination_url,
  l.redirect_type,
  l.state,
  l.expires_at,
  l.analytics_enabled,
  l.password_hash
from link l
join domain d on d.id = l.domain_id
where d.hostname = $1
  and l.slug = $2
  and d.verification_status = 'verified';
