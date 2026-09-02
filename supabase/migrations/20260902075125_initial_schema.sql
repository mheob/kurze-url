-- Initial schema for the multi-tenant URL shortener.
-- Tenant unit is a "team" (= one participating Verein). No RLS: authorization
-- is enforced entirely in the Go API, which connects with a service role.

create extension if not exists pg_trgm;

-- Tenancy -------------------------------------------------------------------

create table team (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  created_at  timestamptz not null default now()
);

create table team_member (
  team_id     uuid not null references team (id) on delete cascade,
  user_id     uuid not null references auth.users (id) on delete cascade,
  role        text not null check (role in ('owner', 'admin', 'editor', 'viewer')),
  created_at  timestamptz not null default now(),
  primary key (team_id, user_id)
);

create index team_member_user_id_idx on team_member (user_id);

-- Domains -------------------------------------------------------------------

create table domain (
  id                   uuid primary key default gen_random_uuid(),
  team_id              uuid not null references team (id) on delete cascade,
  hostname             text not null unique,
  verification_status  text not null default 'pending'
                         check (verification_status in ('pending', 'verified', 'failed')),
  vercel_domain_ref    text,
  created_at           timestamptz not null default now(),
  verified_at          timestamptz
);

create index domain_team_id_idx on domain (team_id);

-- Organization --------------------------------------------------------------

create table folder (
  id                uuid primary key default gen_random_uuid(),
  team_id           uuid not null references team (id) on delete cascade,
  parent_folder_id  uuid references folder (id) on delete set null,
  name              text not null,
  created_at        timestamptz not null default now()
);

create index folder_team_id_idx on folder (team_id);

create table tag (
  id       uuid primary key default gen_random_uuid(),
  team_id  uuid not null references team (id) on delete cascade,
  name     text not null,
  unique (team_id, name)
);

-- Links ---------------------------------------------------------------------

create table link (
  id                   uuid primary key default gen_random_uuid(),
  domain_id            uuid not null references domain (id) on delete cascade,
  -- Denormalized from domain.team_id so every authorization check avoids a
  -- join. Assumes a link's domain is never reassigned to another team.
  team_id              uuid not null references team (id) on delete cascade,
  slug                 text not null,
  destination_url      text not null,
  redirect_type        smallint not null default 302 check (redirect_type in (301, 302)),
  state                text not null default 'active'
                         check (state in ('active', 'disabled', 'expired', 'flagged')),
  folder_id            uuid references folder (id) on delete set null,
  expires_at           timestamptz,
  -- Argon2id, PHC-encoded. Null means the link is unprotected.
  password_hash        text,
  -- When false the redirect path records no click at all for this link.
  analytics_enabled    boolean not null default true,
  created_by           uuid not null references auth.users (id),
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  qr_size              int,
  qr_error_correction  text check (qr_error_correction in ('low', 'medium', 'quartile', 'high')),
  qr_margin            int,
  qr_logo_url          text,
  qr_fg_color          text,
  qr_bg_color          text,
  unique (domain_id, slug)
);

create index link_team_id_idx on link (team_id);
create index link_created_by_idx on link (created_by);
create index link_state_idx on link (state);
create index link_slug_trgm_idx on link using gin (slug gin_trgm_ops);
create index link_destination_url_trgm_idx on link using gin (destination_url gin_trgm_ops);

create table link_tag (
  link_id  uuid not null references link (id) on delete cascade,
  tag_id   uuid not null references tag (id) on delete cascade,
  primary key (link_id, tag_id)
);

create table link_scan_result (
  id            uuid primary key default gen_random_uuid(),
  link_id       uuid not null references link (id) on delete cascade,
  provider      text not null default 'google_safe_browsing',
  verdict       text not null check (verdict in ('clean', 'flagged', 'error')),
  scanned_at    timestamptz not null default now(),
  raw_response  jsonb
);

create index link_scan_result_link_id_scanned_at_idx
  on link_scan_result (link_id, scanned_at desc);

-- Analytics (aggregated only — no raw click table exists, and none should be
-- added). One row per link per day per distinct value seen for a dimension.

create table link_click_stats (
  id               bigint generated always as identity primary key,
  link_id          uuid not null references link (id) on delete cascade,
  bucket_start     date not null,
  dimension_type   text not null check (dimension_type in (
                     'total', 'browser', 'os', 'device', 'country',
                     'referrer', 'utm_source', 'bot_status', 'qr_vs_regular')),
  -- Null exactly when dimension_type = 'total'. NULLS NOT DISTINCT is what
  -- makes the upsert increment that row instead of inserting a duplicate.
  dimension_value  text,
  clicks           bigint not null default 0,
  unique_visitors  bigint not null default 0,
  unique nulls not distinct (link_id, bucket_start, dimension_type, dimension_value)
);

create index link_click_stats_link_id_bucket_start_idx
  on link_click_stats (link_id, bucket_start desc);

-- Audit ---------------------------------------------------------------------

create table audit_log (
  id             bigint generated always as identity primary key,
  team_id        uuid references team (id) on delete set null,
  actor_user_id  uuid references auth.users (id) on delete set null,
  action         text not null,
  entity_type    text not null,
  entity_id      uuid,
  -- Never carries a plaintext password or a password hash.
  metadata       jsonb,
  created_at     timestamptz not null default now()
);

create index audit_log_team_id_created_at_idx on audit_log (team_id, created_at desc);
create index audit_log_entity_idx on audit_log (entity_type, entity_id);
