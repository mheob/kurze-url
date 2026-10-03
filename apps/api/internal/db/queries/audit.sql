-- Audit log. Writes always share the transaction of the mutation they record;
-- see db.InTx.

-- name: InsertAuditLog :exec
-- No cast on metadata: sqlc's default pgx/v5 mapping generates jsonb as
-- []byte, and Postgres has no cast from text to jsonb, so an explicit
-- ::text or ::jsonb cast here would break the insert rather than help it.
-- Leave this parameter uncast so Postgres keeps inferring it as jsonb from
-- the insert context, and pgx.QueryExecModeCacheDescribe (cmd/api/main.go)
-- keeps a real, server-described OID for it even through Supavisor's
-- transaction pooler, so a plain []byte argument encodes correctly.
insert into audit_log (team_id, actor_user_id, action, entity_type, entity_id, metadata)
values ($1, $2, $3, $4, $5, $6);

-- name: ListAuditLog :many
-- The explicit ::uuid cast on team_id keeps the required (non-nullable)
-- caller-provided parameter typed as uuid.UUID rather than *uuid.UUID, even
-- though the audit_log.team_id column itself is nullable (see the migration:
-- "on delete set null"). Without the cast sqlc infers the param's
-- nullability from the column and generates a pointer.
select id, team_id, actor_user_id, action, entity_type, entity_id, metadata, created_at,
       count(*) over () as total_count
from audit_log
where team_id = sqlc.arg('team_id')::uuid
  and (sqlc.narg('entity_type')::text is null or entity_type = sqlc.narg('entity_type')::text)
  and (sqlc.narg('action')::text is null or action = sqlc.narg('action')::text)
  and (sqlc.narg('actor_user_id')::uuid is null or actor_user_id = sqlc.narg('actor_user_id')::uuid)
  and (sqlc.narg('from')::timestamptz is null or created_at >= sqlc.narg('from')::timestamptz)
  and (sqlc.narg('to')::timestamptz is null or created_at <= sqlc.narg('to')::timestamptz)
order by created_at desc, id desc
limit sqlc.arg('result_limit') offset sqlc.arg('result_offset');

-- Fallback for a page past the end; see CountTeamsForUser in team.sql. Mirrors
-- ListAuditLog's filters exactly so the recovered total matches the same set
-- of rows the paginated query would have counted.

-- name: CountAuditLog :one
select count(*)
from audit_log
where team_id = sqlc.arg('team_id')::uuid
  and (sqlc.narg('entity_type')::text is null or entity_type = sqlc.narg('entity_type')::text)
  and (sqlc.narg('action')::text is null or action = sqlc.narg('action')::text)
  and (sqlc.narg('actor_user_id')::uuid is null or actor_user_id = sqlc.narg('actor_user_id')::uuid)
  and (sqlc.narg('from')::timestamptz is null or created_at >= sqlc.narg('from')::timestamptz)
  and (sqlc.narg('to')::timestamptz is null or created_at <= sqlc.narg('to')::timestamptz);

-- Retention. Audit entries are kept for api.AuditRetentionYears (two years)
-- and POST /internal/retention deletes everything older, after the click
-- rollup's own delete.
--
-- :execrows for the reason DeleteExpiredClickStats gives: the row count is the
-- only evidence the job did anything, and for the first two years nothing is
-- old enough to delete, so "0 rows" is the correct answer.
--
-- The floor is a parameter, never a literal. GET /v1/teams/{team_id}/audit-log
-- serves created_at >= the same instant, computed by api.auditRetentionFloor;
-- an interval written here as well would be a second definition of one
-- boundary, and the two drifting apart would either show entries the policy
-- says are gone or hide entries the endpoint still offers.
--
-- No team_id, deliberately — the one documented exception to golden rule 4,
-- for the same reason as DeleteExpiredClickStats: a nightly job acts for the
-- instance, not for a caller, and the retention period is the same for every
-- Verein. Scoping it to a team would make the promise depend on who called.

-- name: DeleteExpiredAuditLog :execrows
delete from audit_log
where created_at < sqlc.arg(oldest_kept)::timestamptz;
