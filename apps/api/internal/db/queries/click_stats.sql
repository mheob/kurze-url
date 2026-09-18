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

-- Retention. docs/planning/01-architecture.md promises 90-day automatic
-- deletion of click analytics; this statement is what keeps that promise.
--
-- :execrows rather than :exec because the row count is the only evidence the
-- job did anything. For the first eighty days after it ships nothing is old
-- enough to delete, so "0 rows" is the correct answer — and a job that has
-- silently stopped running produces exactly the same silence.
--
-- The cutoff is a parameter, never a literal. The stats endpoint serves
-- bucket_start >= today-89, computed from api.RetentionDays; a number written
-- here as well would be a second definition of one boundary. The two drifting
-- apart fails silently in both directions: rows the promise says are gone stay
-- readable, or statistics vanish from inside a window the API still offers.
--
-- Retention is instance-wide, not per-team. A nightly cron has no team in
-- scope, and link_click_stats has no team_id column to filter by. A future
-- contributor might add a join to link and scope this to a team, thinking it
-- follows golden rule 4. But retention is a time-based promise: the app keeps
-- 90 days of all analytics, regardless of team. Scoping the delete would make
-- that promise depend on call timing instead. The guarantee must be
-- instance-wide.

-- name: DeleteExpiredClickStats :execrows
delete from link_click_stats
where bucket_start < sqlc.arg(oldest_kept)::date;

-- The window a link has data for, which is deliberately not the window the
-- caller asked about: the stats endpoint reports it so a page looking at an
-- empty week can tell "nothing was ever clicked" from "wrong week".
--
-- Filtered by the retention floor, and that filter is load-bearing rather than
-- tidy. The retention job runs nightly while the endpoint's floor moves at
-- midnight, so rows the endpoint would refuse to serve can survive for the
-- better part of a day. Reported, they name a window that comes back empty.
-- Applying the floor to the result instead of inside the query has a worse
-- failure: when every row is below the floor, raising the range's start to the
-- floor leaves it later than the range's own end.
--
-- `having count(*) > 0` is what makes the two non-null columns honest. Without
-- it a link with no rows returns one row of nulls, which sqlc cannot model
-- here: it propagates bucket_start's NOT NULL through the aggregate rather
-- than allowing that an aggregate over zero rows is null. With it, that link
-- returns no row at all, :one answers pgx.ErrNoRows, and "this link has no
-- statistics" travels as the absence the caller already handles for
-- GetLinkForAPI rather than as a zero time.Time that would format as a real
-- date.

-- name: GetLinkRecordedRange :one
select min(bucket_start)::date as first_day, max(bucket_start)::date as last_day
from link_click_stats
where link_id = sqlc.arg(link_id)
  and bucket_start >= sqlc.arg(floor_day)
having count(*) > 0;
