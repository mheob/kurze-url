-- Safe Browsing scanning (docs/superpowers/specs/2026-10-03-safe-browsing-design.md).
--
-- link.state stays the only switch the redirect path reads. These two columns
-- record when a link was last checked and which destination that check
-- judged. A verdict never applies to a URL it did not see, so a link whose
-- scan_destination differs from destination_url is due again, whatever
-- scan_checked_at says.
alter table link
  add column scan_checked_at timestamptz,
  add column scan_destination text;

-- A row is written only when a link's verdict changes, from active to flagged
-- or back, so the table stays small and still holds the whole history. The
-- first check that finds a link clean writes nothing. 'error' stays in
-- verdict's check constraint and stays unused: a failed check is logged, not
-- stored. The table also stays outside the retention job, as
-- docs/superpowers/specs/2026-09-12-analytics-retention-design.md decided.
--
-- Nothing has written this table before, so it should be empty everywhere.
-- destination_url is still added with a default that is dropped at once, so
-- the migration also succeeds on a table that is not: a row older than this
-- column reads '' for a destination nobody recorded, and every row written
-- from now on names its own. threat_types keeps its default, the empty list a
-- clean verdict carries.
alter table link_scan_result
  add column destination_url text not null default '',
  add column threat_types text[] not null default '{}';

alter table link_scan_result
  alter column destination_url drop default;
