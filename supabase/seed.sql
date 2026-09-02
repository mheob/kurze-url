-- Local development seed. Never applied to the hosted project.
-- Gives the redirect path something to resolve without going through the API.

insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
                        email_confirmed_at, created_at, updated_at)
values ('00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-000000000000',
        'authenticated', 'authenticated', 'dev@example.test',
        '', now(), now(), now())
on conflict (id) do nothing;

insert into team (id, name)
values ('00000000-0000-0000-0000-0000000000b1', 'Dev Verein')
on conflict (id) do nothing;

insert into team_member (team_id, user_id, role)
values ('00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-0000000000a1', 'owner')
on conflict do nothing;

insert into domain (id, team_id, hostname, verification_status, verified_at)
values ('00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'short.test', 'verified', now())
on conflict (hostname) do nothing;

insert into link (id, domain_id, team_id, slug, destination_url, created_by)
values ('00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'hello', 'https://example.org/hello',
        '00000000-0000-0000-0000-0000000000a1')
on conflict (domain_id, slug) do nothing;
