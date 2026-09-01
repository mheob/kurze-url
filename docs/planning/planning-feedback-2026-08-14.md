# Planning Feedback — URL Shortener (2026-08-14)

Feedback on the initial planning doc (`planningurlshortener.md`). Verified stack facts are current as of Aug 2026.

## Stack facts (verified)

- **Supabase free tier**: projects pause after 1 week of inactivity; capped at 2 active projects; 500 MB database; 50k MAU; 5 GB egress + 5 GB cached egress; unlimited API requests.
- **Upstash Redis free tier**: 256 MB storage, 500K commands/month (~16.7k/day), 10 GB bandwidth/month, up to 10 free databases (unclaimed DBs deleted after 3 days).
- **TanStack Start**: reached stable v1.0 GA in March 2026 (was RC as of late 2025) — safe production pick now. No RSC support yet.
- **Go hosting on Vercel**: officially supported via Vercel's Go serverless function runtime.
- **Go hosting on Cloudflare**: Cloudflare Containers reached GA in April 2026 — supports standard Go HTTP servers as persistent services via Docker Hub images, with active-CPU pricing. **Workers Paid plan only, no free tier.** (The older WASM/TinyGo-on-Workers route still exists but has stdlib/threading limitations — Containers is the better fit for a normal Go server.)

## Decisions made (2026-08-14)

1. Redis is in MVP scope.
2. Security basics (scheme allowlist, block `javascript:`/`data:`/`file:`, SSRF protection, block localhost/private IPs, rate limiting) are in MVP scope, not deferred.
3. Malware/phishing scanning will be async, not blocking on link creation.
4. Architecture: app deployed on Vercel or Cloudflare using each platform's specific APIs directly (no Docker, no portability layer), with SaaS/managed Database (Supabase) and Redis (Upstash) — i.e. platform-native compute, managed data layer. "Self-Hosted" in the original doc's Dev section now means "AWESOME! runs its own instance," not "any third party can self-host this app" — third-party self-hostability is out of scope given this decision (see note below).
5. Unique-visitor counting: daily-rotating salted hash of IP+UA, never stored reversibly — confirmed approach.
6. **Custom/branded domains per team is now a must-have**, not a later nice-to-have. This affects the data model: domain becomes part of the slug's identity (composite key or domain-scoped slug uniqueness), and affects hosting choice (need to support multiple domains pointing at the same deployment — both Vercel and Cloudflare support custom domains, but this needs to be designed for from the start rather than retrofitted).
7. Preview page ("Advanced" tier item) — not needed, dropped from scope.
8. Dedupe HTTPS/HSTS and referrer-handling bullets that currently appear in both Core and Privacy sections.
9. 90-day retention confirmed; analytics event storage should use aggregation/rollups rather than storing raw per-click events indefinitely (fits both the retention policy and the 500 MB Supabase free-tier cap).
10. Supabase free-tier pause: accepted risk, will mitigate with a keep-alive ping rather than upgrading to paid immediately.

## Resolved: hosting approach

Decided against Docker/portability; the app will be built directly against the chosen platform's native APIs (Vercel or Cloudflare). Update the original doc's "Self-Hosted" bullet under Dev to reflect that it means AWESOME! operating its own instance, not a third-party self-hosting capability — as written it could be misread as an open-source-style self-host feature.

One follow-on consequence worth deciding explicitly: going platform-native (not portable) means Vercel and Cloudflare are not really an "or" at the code level — their native APIs differ enough (serverless functions vs. containers, different binding/config models, different custom-domain setup) that building against both simultaneously means maintaining two integration paths. Recommend picking one platform as the actual target rather than carrying "Vercel or Cloudflare" as an open option into implementation.

## Remaining gaps (noted, not yet decided)

- Click-count-based expiration, in addition to date-based (not yet discussed).

## Open question

Vercel and Cloudflare have different deployment models for Go (serverless functions vs. persistent containers). Still open: which one is the actual target platform?
