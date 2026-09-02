-- Batched rollup upsert. The unique constraint is NULLS NOT DISTINCT, which
-- is what lets the dimension_type = 'total' row (dimension_value is null)
-- increment rather than duplicate.

-- name: UpsertClickStats :batchexec
insert into link_click_stats
  (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
values ($1, $2, $3, $4, $5, $6)
on conflict (link_id, bucket_start, dimension_type, dimension_value)
do update set
  clicks = link_click_stats.clicks + excluded.clicks,
  unique_visitors = link_click_stats.unique_visitors + excluded.unique_visitors;
