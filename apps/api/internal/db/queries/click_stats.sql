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

-- The daily series and, once summed in Go, the totals. One scan answers both
-- the all-clicks figures (the 'total' rows) and the human ones (the
-- bot_status = 'human' rows): every click writes both, so the two are exactly
-- comparable and no second query can disagree with the first.
--
-- The casts are not decoration. sum() returns numeric-or-null, and without an
-- explicit ::bigint sqlc generates *int64 for every one of these columns.
-- coalesce turns a day on which only bots clicked into a zero rather than a
-- null: the filtered sum has no rows to add up, and that is an answer, not a
-- missing value.

-- name: GetLinkClickSeries :many
select
  bucket_start,
  coalesce(sum(clicks) filter (where dimension_type = 'total'), 0)::bigint
    as clicks,
  coalesce(sum(unique_visitors) filter (where dimension_type = 'total'), 0)::bigint
    as unique_visitors,
  coalesce(sum(clicks) filter (
    where dimension_type = 'bot_status' and dimension_value = 'human'), 0)::bigint
    as human_clicks,
  coalesce(sum(unique_visitors) filter (
    where dimension_type = 'bot_status' and dimension_value = 'human'), 0)::bigint
    as human_unique_visitors
from link_click_stats
where link_id = sqlc.arg(link_id)
  and bucket_start between sqlc.arg(from_day) and sqlc.arg(to_day)
  and (dimension_type = 'total'
       or (dimension_type = 'bot_status' and dimension_value = 'human'))
group by bucket_start
order by bucket_start;

-- Every dimension's top values in one statement, with what they leave out.
-- The window functions run over the already-grouped rows of the first CTE, so
-- dimension_clicks is that dimension's full total while clicks is one value's
-- share of it; Go subtracts the returned rows from the former to get the
-- remainder. Ordering by clicks desc then dimension_value makes the cut
-- deterministic when two values tie — without the tie-break, which value the
-- cap drops could change between two identical requests.
--
-- 'total' is excluded because it is the series' source, not a breakdown: its
-- dimension_value is null by design, and including it would add a phantom
-- dimension that double-counts every click.

-- name: GetLinkClickBreakdowns :many
with per_value as (
  select
    dimension_type,
    dimension_value,
    sum(clicks)::bigint as clicks,
    sum(unique_visitors)::bigint as unique_visitors
  from link_click_stats
  where link_id = sqlc.arg(link_id)
    and bucket_start between sqlc.arg(from_day) and sqlc.arg(to_day)
    and dimension_type <> 'total'
  group by dimension_type, dimension_value
),
ranked as (
  select
    dimension_type,
    dimension_value,
    clicks,
    unique_visitors,
    row_number() over (
      partition by dimension_type
      order by clicks desc, dimension_value
    ) as value_rank,
    sum(clicks) over (partition by dimension_type)::bigint
      as dimension_clicks,
    sum(unique_visitors) over (partition by dimension_type)::bigint
      as dimension_unique_visitors,
    count(*) over (partition by dimension_type)::bigint
      as dimension_values
  from per_value
)
select dimension_type, dimension_value, clicks, unique_visitors,
       dimension_clicks, dimension_unique_visitors, dimension_values
from ranked
where value_rank <= sqlc.arg(top_values)::int
order by dimension_type, value_rank;
