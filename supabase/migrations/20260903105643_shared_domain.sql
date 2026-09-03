-- A globally unique hostname cannot belong to one team while serving every
-- team. team_id IS NULL therefore means "shared": any team may create links
-- on this domain. link.team_id remains the sole authorization key, so no
-- authorization path changes.
alter table domain alter column team_id drop not null;
