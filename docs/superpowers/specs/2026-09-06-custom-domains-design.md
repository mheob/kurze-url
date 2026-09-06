# Custom Domains — Design

**Status:** approved 2026-09-06 **Amends:** `docs/planning/02-external-services-and-hosting.md` (custom-domain provisioning is no longer self-service via Vercel's Domain API), `CLAUDE.md` (the `domain.hostname` uniqueness rule).

The seventh implementation spec. Six plans have merged and the instance is live: a maintainer signs in, creates a team, creates a link, and `https://go.kurze-url.app/<slug>` redirects.

Every team is on that one hostname. `domain` has carried `team_id`, `verification_status` and `vercel_domain_ref` since the first migration, and `GetLinkableDomain` already refuses to put a link on an unverified domain — the schema was shaped for this feature and then left unused. No endpoint creates a `domain` row; the only one that exists is the shared hostname, upserted at boot.

## Goal

A Verein can put its short links on its own hostname — `links.verein.de` instead of `go.kurze-url.app` — and the app tells them exactly what to do, and tells them the truth about whether it works yet.

## Scope

### In scope

- The five domain endpoints from `06-api-design.md`.
- Ownership verification by DNS TXT token, plus a reachability check.
- A domains screen: claim a hostname, see the DNS records, trigger verification, read why it failed.
- A domain picker on the link form. Without it a verified domain is unusable.
- Navigation in the authenticated shell, so a second team page is reachable at all.

### Out of scope, and where each lands

- **Apex domains** (`verein.de` rather than `links.verein.de`) — rejected at claim time. An apex cannot be a CNAME, so pointing one here means A records and taking the Verein's own website offline. If a Verein ever genuinely wants this, it is a decision with them, not a feature.
- **Automated provisioning through Vercel's Domain API** — see below. The schema keeps `vercel_domain_ref` for it.
- **Notifying the maintainer that a claim is waiting** — the Verein asks, the same out-of-band path by which they asked for a team.
- **Moving existing links between domains** — `UpdateLinkInput` already omits `domain_id` on purpose, and its own comment gives the reason: moving a link changes its short URL, silently breaking every printed copy, and across teams it would break the `link.team_id` denormalization. Nothing here reopens that.
- **Expiring stale claims** — needs a cron surface that does not exist. The partial unique index means a stale claim blocks nobody.

## Global constraints

Inherited and not re-litigated here:

- No RLS. Every query filters by `team_id`; the check lives in Go.
- A non-member gets 404, never 403.
- The redirect path is the hot path and this plan does not touch it.
- No hardcoded user-facing string; English and German ship together.
- WCAG 2.1 AA, gated in CI at two levels.

## Provisioning keeps the maintainer in the loop

### The decision

The Verein claims a hostname in the app. The maintainer adds it to the `kurze-url-api` Vercel project by hand. The backend never calls Vercel.

### Why not the Domain API

`02-external-services-and-hosting.md` sketched the self-service flow: `projectsAddProjectDomain`, then `projectsVerifyProjectDomain`. It would remove the manual step. It also requires a Vercel API token, held as a backend secret, with write access to the project that hosts the instance itself — a service that can rewrite its own hosting configuration at runtime. And whether the Hobby plan permits programmatic domain management at all is unverified.

Team creation is already maintainer-mediated, deliberately. A Verein that wants a custom domain has already had a conversation with the maintainer to exist at all. One more step in that same conversation costs little; a self-mutating deployment token costs more.

### What it costs

The maintainer is on the critical path for every custom domain. If they are away, verification cannot succeed — the reachability half will keep failing. That is a real limitation and the UI must not hide it: the pending reason says the hostname does not reach us yet, which is exactly true.

### The rejected middle option

Skipping Vercel entirely — the Verein points DNS at us, we verify by TXT, done — does not work. Vercel issues no TLS certificate for a hostname that is not registered on the project, so every request fails at the handshake, before the Go router is consulted. The registration is not optional; only who performs it is.

## Verification proves two different things

### The decision

`verification_status` stays a three-value column. A domain becomes `verified` when **both** hold:

1. `_kurze-url-challenge.<hostname>` has a TXT record whose value equals the row's `verification_token`.
2. `https://<hostname>/health` reaches this API.

One stored state, two conditions. The verify endpoint reports which condition failed; that reason is returned, never stored.

### Why a token rather than "does it point at us"

Checking only that the hostname resolves to us is cheaper for the Verein — one record instead of two — and it is weaker in a specific way. Suppose Verein A claims `example.org`, which Verein B actually owns. B later points it here for their own reasons. Under the reachability-only rule, A's claim verifies on B's DNS change. Under a per-claim token, A and B hold different tokens, so only whoever controls the zone can satisfy their own claim.

The token also decouples the order of operations. A Verein can prove ownership before touching the records that carry their live traffic, rather than having to cut over first and prove afterwards.

### Why reachability is checked at all

Because `verified` is what `GetLinkableDomain` gates on, and a link created on a domain that does not resolve is a link that 404s for everyone who clicks it. If `verified` meant only "ownership proven", the window between the token check and the maintainer's Vercel step would be a window in which the app invites a Verein to create links that cannot work — and the app would have no honest way to say so. This project has now spent three separate incidents on systems that reported success while serving nothing. `verified` means "works".

The reachability probe is a readiness check, not a security boundary. A third party could stand up a server that answers `{"status":"ok"}` on a hostname they control — and would gain nothing, because the token already proved ownership and the links still would not be served by us. It is stated plainly here so nobody later mistakes it for authentication.

## A claim is not a reservation

`domain.hostname` is globally unique today, so the first team to insert a row locks the hostname against everyone, verified or not. That is a denial by squatting: claim `verein-xy.de`, and its actual owner can never try.

The migration replaces it with a partial unique index over `hostname` where `verification_status = 'verified'`. Several teams may hold a claim on the same hostname; each gets its own token; the first to satisfy both conditions wins. On that transition, competing claims for the same hostname move to `failed` rather than being deleted — the losing team sees that their claim was lost instead of watching a row disappear. A `failed` row blocks nothing, because the index only covers `verified`.

Unverified rows can never serve links: `GetLinkableDomain` already filters on `verification_status = 'verified'`, and that query is unchanged by this plan.

## Deleting a domain that still has links is refused

`link.domain_id` is `on delete cascade`, so a `DELETE` on a domain currently destroys every link on it, every row in `link_tag`, and every `link_click_stats` rollup. There is no raw click table by design, so those rollups cannot be recomputed from anything — they are the only copy.

`DELETE /v1/domains/{domain_id}` therefore answers 409 while any link references the domain, and the response carries the count. The team deletes the links first. This is the only rule under which an accidental delete cannot destroy data, and given what is at stake "impossible" is worth more than "warned".

The cost is real: a team abandoning a domain with two hundred links deletes them one at a time. Bulk deletion and moving links between domains are both out of scope; if this becomes a genuine complaint, moving links is the better answer than a force flag.

## The verify endpoint is this service's first outbound fetch

`internal/destination` opens with:

> The DNS-rebinding re-check belongs wherever the service itself fetches a URL, which nothing in the link endpoints does.

This endpoint is where that becomes false. The hostname is chosen by the caller and the service connects to it, which is the definition of an SSRF sink. Four requirements, none optional:

**The address is checked at connect time, not after resolution.** A `net.Dialer.Control` hook validates the actual destination address after the resolver has run and before the socket connects. Checking the resolved address and then dialing the hostname again re-opens exactly the rebinding hole the package comment names: first lookup public, second lookup internal. `destination.isPublic` is unexported today and has to become reachable — it is the same predicate, and duplicating it would let the two copies drift.

**Redirects are refused.** `CheckRedirect` returns an error. Otherwise a 302 to `169.254.169.254` walks straight past the address check.

**Timeouts are hard and the response body is capped.** A few seconds in total, a few kilobytes read. The endpoint waits on a network nobody here controls.

**Nothing from the probed server is echoed back.** The response carries a status and a reason code, never the probe's body, headers or timing. An endpoint that returns what it fetched is a reading primitive for everything reachable from the Vercel network.

## Hostname rules at claim time

Before a row is written the hostname is normalised — lowercased, IDN converted to punycode — and rejected if it carries a scheme, a path, a port or credentials; if it is an IP literal or a single label; if it exceeds the DNS length limits; if it is an apex domain; or if it falls under this instance's own names (`API_HOSTNAME`, `SHARED_DOMAIN_HOSTNAME`, `*.vercel.app`).

The last rule is not load-bearing for security — nobody outside the maintainer can place a TXT record under `kurze-url.app`, so such a claim could never verify. It is there because failing at claim time with a clear message is honest, and failing later at a check the caller could never have passed is not.

## Roles: admin, not editor

Reading domains requires `RoleViewer`. Claiming, verifying and deleting require `RoleAdmin`.

Links, folders and tags are content, and `RoleEditor` creates them. A domain is not content — it is the namespace the team's content lives in, and losing it takes every link along. That belongs with member management, not with link creation.

Authorization goes through a fourth instance of the entity-scope pattern, `DomainAdminScope`, alongside `LinkViewerScope`, `FolderEditorScope` and `TagEditorScope`. Read one before writing it; the 404-not-403 rule is the part that must be copied exactly.

## The endpoints

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| `POST` | `/v1/teams/{team_id}/domains` | admin | Body: `hostname`. Returns the row plus the DNS records to set. |
| `GET` | `/v1/teams/{team_id}/domains` | viewer | Paginated `Page[Domain]`, as everywhere else. |
| `GET` | `/v1/domains/{domain_id}` | viewer | Includes the records again, so the screen can show them any time. |
| `POST` | `/v1/domains/{domain_id}/verify` | admin | Runs both checks, updates the status, returns the status and, on failure, the reason. |
| `DELETE` | `/v1/domains/{domain_id}` | admin | 409 with the link count while links exist. |

The domain representation carries `hostname`, `verification_status`, `verified_at`, and the two records the Verein must create: the TXT name and value, and the CNAME target. The failure reason is a fixed enum — `token_missing`, `token_mismatch`, `unreachable` — so the frontend can translate it rather than display a server-authored sentence.

Every write records an `audit_log` action: `domain.claimed`, `domain.verified`, `domain.deleted`.

## The link form needs a domain picker

`toRequestBody` in the create-link route sends no `domain_id`, so `resolveLinkDomain` falls through to the shared domain for every link. The API has accepted an explicit domain since plan 3; the form simply never asked.

A verified custom domain with no way to put a link on it is not a feature. The picker lists the team's verified domains plus the shared hostname, defaults to the shared one, and is hidden entirely when the team has no verified domain — a select with one option is furniture.

Separately, the authenticated shell has no navigation: the team switcher links to `/teams/$teamId/links` and the only other control is sign-out. A second team page is unreachable without adding one. Two entries in the existing `<header>`, links and domains.

## The redirect path does not change

Not one line of `GET /{slug}` is touched. `GetLinkableDomain` already filters on `verified`, so a link on an unverified domain cannot exist to be resolved, and no new query joins the hot path. Nothing in this plan adds a Redis command to a redirect.

One cache interaction is worth stating so it is not rediscovered: a probe against `links.verein.de/abc` before any link exists caches the not-found sentinel under that hostname's key. It stays correct, because creating a link already invalidates its own key — the rule `CLAUDE.md` states for exactly this reason.

## Rate limits

Verification costs a DNS lookup and a TLS connection to a third party, so it cannot be free to trigger. The existing hand-rolled Redis sliding window is reused, limited per domain and per team. Claiming is limited per team as well; the concrete numbers join the other undecided limits in `CLAUDE.md`'s open items rather than being invented here.

The Redis cost is negligible against the ~16.7K/day ceiling — these are administrative actions, not redirects.

## Testing

**Tenancy is tested per query, against sqlc, in `internal/db/tenancy_test.go`.** This is the lesson plan 4 paid for: six of its properties were false passes, because the entity-scope layer intercepts before the query runs and a missing `team_id` filter is therefore invisible at the HTTP layer. The permission matrix in `matrix_test.go` checks status per operation and role and explicitly cannot see this. This plan is almost entirely new queries, so every one gets a direct test.

**`internal/domainverify` is tested with an injected resolver and HTTP client.** The cases are the reason the package exists: token absent, token wrong, several TXT values with one correct, hostname resolving to a private address, rebinding where the first lookup is public and the second is not, a redirect to an internal host, and a timeout. Without these it is an `http.Get` with extra steps.

**Scope tests** for `DomainAdminScope` against fakes, mirroring link, folder and tag.

**End to end**, against a preview: claim a domain, assert the records are displayed, trigger verification, and expect `token_missing`. The success path cannot be exercised — it needs real DNS under our control — but the failure path proves the whole chain from form to DNS lookup is connected, which is the part that breaks.

## Migration

One migration, three changes to `domain`:

- Drop the unique constraint on `hostname`; add a unique index on `hostname` where `verification_status = 'verified'`.
- Add `verification_token text`, null for the shared row. The value is public — it is published in DNS — so it is stored in the clear and returned by the API.
- Keep `vercel_domain_ref`, with a comment recording that it stays null under maintainer-in-the-loop provisioning and what it is reserved for.

The existing shared-domain row is unaffected: it has `team_id IS NULL` and `verification_status = 'verified'`, and `UpsertSharedDomain`'s conflict guard still works against the new index.

## Open questions

- **The CNAME target.** Vercel assigns per-project DNS targets (`e4c1ef3e26bd6d34.vercel-dns-016.com` for this one) alongside the generic `cname.vercel-dns.com`. Which one a Verein should be told to use is unconfirmed. The value is configuration (`DOMAIN_DNS_TARGET`) rather than a constant, and the right value gets settled the first time a real domain is set up.
- **Rate-limit numbers** for claiming and verifying, in line with the other undecided limits.
- **Whether `_kurze-url-challenge` is the right record name.** It is a convention, not a standard; it only has to be stable once a Verein has been told to create it.
