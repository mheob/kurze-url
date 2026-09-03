-- Tag names are unique per team case-insensitively. German capitalizes every
-- noun, so a team's tags are capitalized words a hurried user will sometimes
-- type lowercase; "Sommerfest" and "sommerfest" as two tags is a filter list
-- that quietly rots. The name is still STORED exactly as typed, because
-- "sommerfest" rendered in a German UI reads as a typo — the display value and
-- the uniqueness key are different things.
--
-- The index holds the invariant rather than a strings.ToLower in Go: there is
-- no RLS on this project, so Go already carries every tenancy guarantee, and a
-- normalization call is one line that a later code path can forget to copy. A
-- unique index cannot be forgotten.
alter table tag drop constraint tag_team_id_name_key;

create unique index tag_team_id_name_lower_idx on tag (team_id, lower(name));
