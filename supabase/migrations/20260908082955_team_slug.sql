-- Teams are addressed by slug in the frontend's URLs: /teams/sv-gruenwald/links
-- rather than /teams/6f1c8f0e-…/links. The API keeps taking the UUID
-- everywhere; the slug exists so that a URL can be read out loud, put in a
-- Verein's own documentation, and recognised by the person who receives it.
--
-- The format check lives here and not only in Go for the same reason
-- tag_team_id_name_lower_idx does: the backfill below is itself a writer that
-- does not go through Go, and it must not be the writer that introduces a
-- malformed slug. The reserved-slug denylist stays in Go — Postgres has no
-- business knowing the frontend's route table.
alter table team add column slug text;

-- Backfill. Nested replace() calls do the German transliteration a translate()
-- cannot (it maps single characters, and ä has to become two). The suffix
-- disambiguates two Vereine that normalise to the same value, oldest first, so
-- the result is stable if this ever runs twice against the same data. A name
-- that normalises to fewer than three characters falls back to the row's own
-- id, which is always long enough and always unique.
with normalized as (
  select
    id,
    created_at,
    trim(both '-' from left(
      regexp_replace(
        replace(replace(replace(replace(replace(
          regexp_replace(lower(name), '\s+e\.?\s*v\.?\s*$', ''),
          'ä', 'ae'), 'ö', 'oe'), 'ü', 'ue'), 'ß', 'ss'), '&', '-und-'),
        '[^a-z0-9]+', '-', 'g'),
      34)) as base
  from team
),
padded as (
  select
    id,
    created_at,
    case
      when length(base) >= 3 then base
      else 'team-' || substr(replace(id::text, '-', ''), 1, 8)
    end as base
  from normalized
),
numbered as (
  select
    id,
    base,
    row_number() over (partition by base order by created_at, id) as n
  from padded
)
update team t
set slug = case when numbered.n = 1 then numbered.base else numbered.base || '-' || numbered.n end
from numbered
where numbered.id = t.id;

alter table team
  alter column slug set not null,
  add constraint team_slug_key unique (slug),
  add constraint team_slug_format check (slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$'),
  add constraint team_slug_length check (length(slug) between 3 and 40);

comment on column team.slug is
  'Immutable, globally unique, human-readable identifier used in the frontend''s URLs. The API addresses teams by id.';
