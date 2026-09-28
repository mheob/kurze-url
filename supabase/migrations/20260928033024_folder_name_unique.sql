-- Folder names are unique per team, case-insensitively, the same rule tag
-- names follow (tag_team_id_name_lower_idx): the name is stored as typed and
-- compared folded, so "Sommerfest" and "sommerfest" cannot both exist in one
-- team's folder select. See docs/superpowers/specs/2026-09-26-folders-frontend-design.md.
create unique index folder_team_id_name_lower_idx on folder (team_id, lower(name));
