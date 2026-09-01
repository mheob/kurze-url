# URL Shortener — Architecture Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code.

## Project context

**Correction, 2026-09-01**: this is an open-source project for non-profit associations ("Vereine"), unrelated to AWESOME!. Earlier notes in this doc referencing "AWESOME!" should be read as "the maintainer(s) operating the shared instance" — left in place below since the underlying technical decisions don't change, only the ownership framing.

- **License**: MIT (decided 2026-09-01; permissive, AGPL ruled out — no plan to protect against a competing hosted fork, so no SaaS-loophole clause needed).
- **Hosting model, reconfirmed 2026-09-01**: one shared multi-tenant instance operated by the maintainer(s) for participating Vereine — not each Verein self-hosting its own copy. This means the Vercel-native (no Docker/portability layer) decision from earlier still holds; open-sourcing the code is about transparency and contribution, not about making self-hosting turnkey for every Verein. Worth revisiting if that scope ever changes.
- **Tenant model**: each "team" in this doc = one participating Verein.

## System overview

Components:

- **API/backend**: Go, API-first design. Handles link CRUD, redirects, analytics ingestion, auth.
- **Frontend**: TanStack Start (React), consumes the API.
- **CLI**: Go, thin client over the same API (`short link create`, `short link list`, `short link stats`, `short qr`).
- **Database**: Supabase (Postgres), managed/SaaS, not self-hosted.
- **Cache**: Redis (Upstash), managed/SaaS, in MVP scope, fronting the redirect path.
- **Compute/hosting**: **Vercel, decided 2026-09-01.** Built directly against Vercel's native APIs (Go serverless functions), no Docker, no portability abstraction. Cloudflare was considered and dropped (see `02-external-services-and-hosting.md` for rationale).

## Core data flow: redirect

1. Request hits `<domain>/<slug>`.
2. Backend checks Redis for the `slug -> destination URL` mapping.
   - Cache hit: redirect immediately, record the click asynchronously afterward.
   - Cache miss: look up in Postgres, populate the cache, then redirect.
   - **Exception — password-protected links** (decided 2026-09-01, see `05-database-schema.md`): if the link has a password set, this step branches — instead of redirecting immediately, serve an interstitial page collecting the password, verify it against the stored Argon2id hash, and only redirect on success. The password-check endpoint carries its own tighter rate limit (see `04-backend-architecture.md`), separate from general API rate limiting, since a weak password would otherwise be crackable quickly.
3. Click recording must be async / non-blocking relative to the redirect response — analytics should never add latency to the user-facing hop.
4. Analytics events are aggregated (see below) rather than kept as an ever-growing raw event log.
5. **Redirect status code is configurable per link** (decided 2026-09-01) — see "Redirect status code" below.

Redirect performance is a first-class architectural concern, not an add-on. It's the one code path nearly every other design decision should be checked against — this is doubly true now that hosting is on Vercel, since serverless cold starts make the Redis cache layer load-bearing, not optional.

## Redirect status code (per-link, configurable)

Decided 2026-09-01: yes, this is straightforward to support. Add a `redirect_type` field per link (`301` or `302`, default `302`) and have the redirect handler set the response status accordingly.

Trade-offs to surface to users when they choose (worth putting directly in the create/edit UI, not just docs):

- **302 (default recommendation)**: every click always hits the backend, guaranteeing fresh destination lookups, accurate analytics, and instant effect when a destination URL is changed. No caching surprises.
- **301**: signals "permanent" to browsers and intermediary caches/CDNs, which may cache the redirect client-side — a returning visitor's browser can skip the backend entirely on repeat clicks. That means undercounted analytics and stale destinations if the target is changed later, until the client-side cache expires (which is often "indefinitely" in practice). 301 does carry more SEO weight/link-equity signal for the destination page, which is the main reason someone would choose it over 302 despite the downsides.
- Vercel Functions themselves don't cache responses by default (no server-side caching surprise) — the caching risk with 301 is purely client/browser and intermediary-CDN side, not something Vercel adds on top.

Recommendation: default every new link to 302, let users opt into 301 per link if they specifically need the SEO signal, and show a short inline warning when they do (something like "301 redirects may be cached by browsers, meaning some clicks and destination changes won't be tracked immediately").

## Data model implications

- Custom/branded domains per team (= per Verein) are a must-have (decided 2026-08-14). Slugs must be domain-scoped: the uniqueness constraint is on `(domain, slug)`, not `slug` alone.
- Domain provisioning (decided 2026-09-01): self-service via Vercel's Domain API — each domain record needs a verification status (e.g. `pending`, `verified`, `failed`) tracked against Vercel's own verification state, plus the team it belongs to. See `02-external-services-and-hosting.md` for the API call sequence.
- Destination URL changes must not invalidate existing QR codes or shared short links — QR codes and printed links always encode the short URL, never the destination, so updating the destination is a pure data update with no regeneration needed.
- Expiration is currently date-based only. Click-count-based expiration is a known future gap, not yet designed.
- Link state needs to support at least: active, disabled (manual), expired (date or future click-count), flagged (malware/abuse).
- Each link needs a `redirect_type` field (301/302, default 302) per the decision above.
- **Password protection is in MVP scope** (decided 2026-09-01, moved out of "deferred" — see `05-database-schema.md` for the schema and security implementation notes: Argon2id hashing, per-link rate limiting on the password-check endpoint, and explicit exclusion from audit log values).

## Security-by-design (MVP, not deferred)

Decided 2026-08-14 that these ship with the very first publicly reachable version, not later:

- URL scheme allowlist (`https://` only); explicit block list for `javascript:`, `data:`, `file:`, etc.
- SSRF protections: block server-side fetches to localhost/private IP ranges; account for DNS rebinding by re-resolving and re-checking the IP at fetch time, not only at validation time.
- Rate limiting on link creation and redirect endpoints.
- Malware/phishing scanning of destination URLs: async, not blocking link creation — a link goes live immediately and gets flagged/quarantined if scanning later returns positive. Provider: **Google Safe Browsing API**, licensing accepted 2026-09-01 (non-profit/community use, no separate formal legal review) — see `02-external-services-and-hosting.md` for the full reasoning and the Web Risk API fallback if this is ever challenged.

## Analytics & privacy architecture

- No full IP addresses stored, ever.
- Unique-visitor counting: daily-rotating salted hash of IP + User-Agent, not reversible (Plausible/Fathom-style approach).
- GeoIP resolved locally, no third-party geolocation API call per request.
- Retention: 90-day automatic deletion, confirmed.
- Storage strategy: aggregate into rollups (hourly/daily counts by dimension — browser, OS, device, country, referrer, bot/human, QR vs. regular) rather than storing one row per click indefinitely. This is needed both to respect the 90-day retention policy cleanly and to stay within Supabase's 500 MB free-tier database cap.
- No cookies, no fingerprinting beyond the hash above. Analytics collection must be possible to disable per link or per account.

## CLI authentication

Preference stated 2026-09-01: OAuth is preferred over a plain API key, contingent on implementation effort.

Finding: **Supabase now ships an OAuth 2.1 Server** ("Sign in with Your App"), supporting the **Authorization Code + PKCE** grant and refresh tokens. It does **not** support the Device Authorization Grant (RFC 8628) — confirmed against Supabase's own docs and an open, unresolved feature request for it.

Practical implication: the modern, low-effort path is Authorization Code + PKCE with a **local loopback redirect** — the CLI starts a short-lived local HTTP listener, opens the user's browser to Supabase's OAuth authorization endpoint, and receives the token on the loopback callback once the user approves. This is the same pattern used by the Vercel CLI, GitHub CLI (newer versions), AWS CLI v2 SSO, and Doppler CLI. Because Supabase now hosts the OAuth server piece, this is meaningfully lower effort than it would have been a year ago — no custom authorization-server build required.

Caveat: this pattern needs a browser available on the same machine as the CLI. If a fully headless flow (e.g. pasting a code shown in the terminal into a browser on a *different* device) is ever required, that's the classic Device Authorization Grant use case, which Supabase doesn't support — would need a custom-built device-code flow on top of the Go backend (a well-trodden pattern, but real extra work, not just configuration).

**Decided 2026-09-01**: build CLI auth as Authorization Code + PKCE via Supabase's OAuth 2.1 Server with a loopback redirect, as the primary and only method for v1. A plain API key is kept as a documented fallback design, but only gets built if/when a real CI or headless use case shows up — not built speculatively now.

## Open questions

- None. `02-external-services-and-hosting.md` is fully decided except for the alert notification channel (email vs. webhook), which doesn't block architecture work.
