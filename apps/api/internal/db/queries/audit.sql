-- Audit log. Writes always share the transaction of the mutation they record;
-- see db.InTx.

-- name: InsertAuditLog :exec
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
