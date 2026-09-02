-- Not a migration. sqlc reads this so the foreign keys to Supabase's
-- auth.users resolve during code generation. Supabase owns the real table;
-- this file is never applied to any database.
create schema if not exists auth;

create table auth.users (
  id uuid primary key
);
