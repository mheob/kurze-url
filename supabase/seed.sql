-- Local development seed. Never applied to the hosted project.
-- Gives the redirect path something to resolve without going through the API.

-- The empty strings are not decoration. GoTrue scans confirmation_token,
-- recovery_token, email_change_token_new and email_change into Go `string`
-- fields, so a NULL in any of them fails the row with "converting NULL to
-- string is unsupported" and every sign-in attempt answers 500. The login form
-- reports success regardless, because its message is deliberately the same
-- whether or not an account exists — so the only symptom is a link that never
-- arrives.
--
-- The auth.identities row is required too: GoTrue resolves an address to a user
-- through that table, and a user without one cannot be found at all.
insert into auth.users (id, instance_id, aud, role, email, encrypted_password,
                        email_confirmed_at, created_at, updated_at,
                        confirmation_token, recovery_token,
                        email_change_token_new, email_change,
                        raw_app_meta_data, raw_user_meta_data)
values ('00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-000000000000',
        'authenticated', 'authenticated', 'dev@example.test',
        '', now(), now(), now(),
        '', '', '', '',
        '{"provider":"email","providers":["email"]}'::jsonb, '{}'::jsonb)
on conflict (id) do nothing;

insert into auth.identities (user_id, provider_id, provider, identity_data,
                             last_sign_in_at, created_at, updated_at)
values ('00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000a1', 'email',
        jsonb_build_object('sub', '00000000-0000-0000-0000-0000000000a1',
                           'email', 'dev@example.test',
                           'email_verified', true, 'phone_verified', false),
        now(), now(), now())
on conflict (provider, provider_id) do nothing;

insert into team (id, name, slug)
values ('00000000-0000-0000-0000-0000000000b1', 'Dev Verein', 'dev-verein')
on conflict (id) do nothing;

insert into team_member (team_id, user_id, role)
values ('00000000-0000-0000-0000-0000000000b1',
        '00000000-0000-0000-0000-0000000000a1', 'owner')
on conflict do nothing;

insert into domain (id, team_id, hostname, verification_status, verified_at)
values ('00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'short.test', 'verified', now())
on conflict (id) do nothing;

insert into link (id, domain_id, team_id, slug, destination_url, created_by)
values ('00000000-0000-0000-0000-0000000000d1',
        '00000000-0000-0000-0000-0000000000c1',
        '00000000-0000-0000-0000-0000000000b1',
        'hello', 'https://example.org/hello',
        '00000000-0000-0000-0000-0000000000a1')
on conflict (domain_id, slug) do nothing;
