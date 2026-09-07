-- A hostname may be claimed by several teams at once. Global uniqueness made
-- the first INSERT a lock: claim verein-xy.de and its actual owner can never
-- try. Uniqueness belongs on the outcome, not on the attempt — only one team
-- can hold a hostname once it is verified, and GetLinkableDomain already
-- refuses to put a link on anything else.
alter table domain drop constraint domain_hostname_key;

create unique index domain_hostname_verified_key
  on domain (hostname)
  where verification_status = 'verified';

-- The value the claiming team publishes as a TXT record under
-- _kurze-url-challenge.<hostname>. Not a secret — it is published in public
-- DNS — so it is stored and returned in the clear. Null for the shared
-- hostname, which is verified at boot and never proves anything.
alter table domain add column verification_token text;

-- Reserved, and deliberately never written: provisioning keeps the maintainer
-- in the loop, so this service never calls Vercel's Domain API and has no
-- reference to record. Kept so switching to self-service later is a code
-- change rather than a migration.
comment on column domain.vercel_domain_ref is
  'Unused under maintainer-in-the-loop provisioning; see the 2026-09-06 custom-domains design.';
