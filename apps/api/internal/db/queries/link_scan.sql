-- Safe Browsing scanning. Two of these queries are instance-wide on purpose
-- and say so where they are: the sweep's due list and its count act for the
-- instance, like the retention job's deletes, and scoping them to a team would
-- make "every link is checked daily" depend on who happened to call. Every
-- other query here filters by team_id although the scanner belongs to no team,
-- because it always knows the link's team: the due list hands it over, and so
-- does every handler that starts a check.

-- ListDueLinksForScan is one sweep's batch, the oldest debt first: links whose
-- current destination has never been checked, then flagged links (due on
-- every sweep, so a false positive Google corrects is lifted even if nobody
-- visits the link), then the longest-unchecked. created_at and id make the
-- order total.
--
-- No team_id, and that is correct here; see the note at the top.
--
-- The due predicate below is repeated verbatim in CountDueLinksForScan, because
-- sqlc has no shared fragments. Change one and change the other:
-- TestCountDueLinksForScanAgreesWithTheListOverTheSameLinks fails when they
-- drift.
--
-- No index, deliberately. EXPLAIN (2026-10-03, Postgres 17, 20,000 rows
-- analyzed) shows Limit over Sort over Seq Scan on link, with the whole
-- predicate as the scan's Filter. Forcing the planner off sequential scans
-- shows what the existing link_state_idx can do: a Bitmap Index Scan serving
-- `state in ('active', 'flagged')` alone, which matches nearly every link, and
-- everything else stays a Filter, on purpose. `scan_destination is distinct
-- from destination_url` compares two columns of one row, so it cannot be an
-- index key, though a partial index could carry it as its predicate. That
-- would serve only the never-checked half of the OR: the daily half needs an
-- index on scan_checked_at, the column every check writes, and an indexed
-- column written on every check rules out heap-only (HOT) updates of every
-- link once a day. At this instance's size (thousands of rows, 48 sweeps a
-- day) the EXPLAIN's sequential scan is cheap.
-- Re-run the EXPLAIN in docs/superpowers/plans/2026-10-03-safe-browsing.md
-- (Task 1, Step 9) if link ever reaches six figures.

-- name: ListDueLinksForScan :many
select l.id, l.team_id, l.destination_url
from link l
where l.state in ('active', 'flagged')
  and (l.expires_at is null or l.expires_at > sqlc.arg('now')::timestamptz)
  and (l.scan_checked_at is null
       or l.scan_destination is distinct from l.destination_url
       or l.scan_checked_at < sqlc.arg('now')::timestamptz - interval '24 hours'
       or l.state = 'flagged')
order by
  (l.scan_checked_at is null or l.scan_destination is distinct from l.destination_url) desc,
  (l.state = 'flagged') desc,
  l.scan_checked_at asc nulls first,
  l.created_at,
  l.id
limit sqlc.arg('batch_limit');

-- CountDueLinksForScan repeats ListDueLinksForScan's predicate for the sweep's
-- `remaining`. The two must stay identical: sqlc has no shared fragments, so
-- the copy is the price, and
-- TestCountDueLinksForScanAgreesWithTheListOverTheSameLinks is what holds it.
-- Flagged links are due on every sweep, so this never reaches zero while any
-- link is blocked. Instance-wide, like the list.

-- name: CountDueLinksForScan :one
select count(*)
from link l
where l.state in ('active', 'flagged')
  and (l.expires_at is null or l.expires_at > sqlc.arg('now')::timestamptz)
  and (l.scan_checked_at is null
       or l.scan_destination is distinct from l.destination_url
       or l.scan_checked_at < sqlc.arg('now')::timestamptz - interval '24 hours'
       or l.state = 'flagged');

-- SetLocalLockTimeout bounds how long the rest of the current transaction
-- waits for a lock. set_config with is_local true is SET LOCAL, written as a
-- function call because SET takes no bind parameters. applyVerdict runs it
-- before GetLinkForScan, so a row lock held by an instance Vercel froze
-- mid-verdict fails that one link within seconds instead of stalling the sweep
-- behind it until its budget ends.

-- name: SetLocalLockTimeout :exec
select set_config('lock_timeout', sqlc.arg('timeout')::text, true);

-- GetLinkForScan reads the one link a verdict is about to be applied to,
-- locked, inside applyVerdict's transaction. The lock is what makes "is this
-- still the URL that was checked?" and the write that follows one decision: a
-- PATCH changing the destination either waits for it, or this waits for the
-- PATCH and then sees the new URL and discards the verdict. hostname and slug
-- come along for the redirect-cache key. FOR UPDATE OF l rather than FOR
-- UPDATE: the domain row needs no lock, and locking it would serialize every
-- verdict on the shared domain behind every other.

-- name: GetLinkForScan :one
select l.id, l.team_id, l.destination_url, l.state, l.slug, d.hostname
from link l
join domain d on d.id = l.domain_id
where l.id = sqlc.arg('id') and l.team_id = sqlc.arg('team_id')
for update of l;

-- RecordLinkScan writes a completed check and the state its verdict leaves the
-- link in. updated_at is deliberately not touched: it is the dashboard's "last
-- changed by somebody", and a daily check of every link would make it mean
-- "last scanned" instead.

-- name: RecordLinkScan :exec
update link set
  state = sqlc.arg('state'),
  scan_checked_at = sqlc.arg('checked_at')::timestamptz,
  scan_destination = sqlc.arg('checked_destination')::text
where id = sqlc.arg('id') and team_id = sqlc.arg('team_id');

-- ClearLinkScanDestination forgets which destination the last check judged.
-- updateLink calls it when a new destination lifts a flag: otherwise changing
-- the destination back before the new one's check lands would leave the link
-- active on the URL Google flagged, with a recent check on record for exactly
-- that URL, and the sweep would not look at it for a day. With
-- scan_destination null the link is due at once. scan_checked_at is left as
-- it is: it still says when that check ran.

-- name: ClearLinkScanDestination :exec
update link set scan_destination = null
where id = sqlc.arg('id') and team_id = sqlc.arg('team_id');

-- InsertLinkScanResult records a verdict change. link_scan_result has no
-- team_id column, so the row is selected from the link and the team filter
-- sits on that select: a link that is not the given team's gets no row, and
-- the statement still succeeds, exactly as RecordLinkScan's update does. So it
-- reports how many rows it wrote. Its one caller has just read and locked the
-- link under that team in the same transaction, so a miss is not an outcome it
-- expects, and it fails on one rather than commit a flag with no record of
-- why. A nil threat_types is stored as an empty array: a clean verdict has
-- none, and the column is not null.

-- name: InsertLinkScanResult :execrows
insert into link_scan_result (link_id, verdict, destination_url, threat_types, scanned_at)
select l.id, sqlc.arg('verdict')::text, sqlc.arg('destination_url')::text,
       coalesce(sqlc.arg('threat_types')::text[], '{}'), sqlc.arg('scanned_at')::timestamptz
from link l
where l.id = sqlc.arg('link_id') and l.team_id = sqlc.arg('team_id');

-- GetLatestLinkScanResult backs the nested scan verdict on GET
-- /v1/links/{link_id}. Filtered by team through the link: the result table
-- carries no team_id of its own, and the rule that every read filters by
-- tenant has no exception for a table that happens not to.

-- name: GetLatestLinkScanResult :one
select r.verdict, r.threat_types, r.destination_url, r.scanned_at
from link_scan_result r
join link l on l.id = r.link_id
where r.link_id = sqlc.arg('link_id') and l.team_id = sqlc.arg('team_id')
order by r.scanned_at desc, r.id desc
limit 1;
