# URL Shortener — Planning Index

Status as of **2026-09-01**: initial planning pass complete. All nine documents in this folder are consistent with each other; cross-references between them are current.

The condensed, agent-facing version of everything below lives in the code repo's `CLAUDE.md`. This index is the human-facing map.

## What the project is

An open-source, multi-tenant URL shortener for German non-profit associations ("Vereine"), run as **one shared instance** operated by the maintainer(s) rather than self-hosted per Verein. MIT licensed. Built free-tier-first: Supabase, Upstash, Vercel, Google Safe Browsing, Resend, Sentry, Better Stack — every service chosen has a permanent free tier, and every decision was checked against staying inside it.

## The documents

| # | Document | What it settles |
| --- | --- | --- |
| 01 | `01-architecture.md` | System components, redirect data flow (incl. the password-protected branch), per-link 301/302, security-by-design MVP list, privacy-first analytics, CLI OAuth/PKCE |
| 02 | `02-external-services-and-hosting.md` | Every external service and its free-tier ceiling: Supabase, Upstash, Vercel, Cloudflare (rejected), Safe Browsing, Vercel Domain API, Resend, alert thresholds, Sentry, Better Stack |
| 03 | `03-frontend.md` | TanStack Start stack, shadcn/ui on Radix (and why not Base UI), Tremor, server-side QR, i18n EN+DE, WCAG 2.1 AA, dark/light, Storybook, full testing strategy |
| 04 | `04-backend-architecture.md` | Vercel Go Framework Preset, chi, Huma (code-first OpenAPI), sqlc (not GORM), Supabase-CLI-only migrations, custom Redis rate limiting, project layout |
| 05 | `05-database-schema.md` | Complete schema, `team` naming, analytics rollup model, Redis-based unique-visitor dedup, audit log, the RLS decision, indexes, password protection |
| 06 | `06-api-design.md` | `/v1` versioning, JWKS auth, pagination/filtering conventions, the public redirect surface, team invitations, full endpoint list |
| 07 | `07-repo-structure-and-tooling.md` | Monorepo layout, two Vercel projects from one repo, build skipping, Related Projects, migrations in CD, CI workflows, secrets, goreleaser |
| 08 | `08-legal-and-compliance.md` | Impressum (DDG §5), Datenschutzerklärung, AVV with participating Vereine, EU region selection, cookie/consent note |
| — | `planning-url-shortener.md` | The original feature list (Dev / Core / Advanced tiers) everything traces back to |
| — | `planning-feedback-2026-08-14.md` | First-pass feedback on that original list; superseded by docs 01–08, kept for history |

## Decisions worth remembering

Findings from research that actually changed a decision, rather than just confirming one:

- **Cloudflare was dropped** because Containers require the paid Workers plan — Vercel it is.
- **Supabase supports OAuth 2.1 but not the Device Authorization Grant**, so CLI auth is Authorization Code + PKCE with a loopback redirect.
- **Tremor is built on Radix**, so shadcn/ui runs on Radix here even though Base UI became shadcn's default in July 2026.
- **Supabase's built-in mail sender caps at 2 emails/hour**, which is why Resend (custom SMTP) entered the stack at all.
- **Supabase Branching is billed per hour** and isn't covered by the Spend Cap, so per-PR preview databases are out; migrations run on merge to `main`.
- **Vercel's Hobby plan keeps runtime logs for one hour**, which is what turns Sentry from optional into necessary.
- **UptimeRobot's free tier became personal/non-commercial-only in Dec 2024**, so uptime monitoring goes to Better Stack instead.
- **Password protection was moved back into MVP scope** after being briefly mis-tiered as advanced — it's Core in the original feature list, and retrofitting security later is worse than designing it now.
- **RLS is deliberately off**: a service-role connection bypasses it anyway, so authorization lives in the Go backend where it can actually be enforced.

## What is not settled

Carried into implementation as explicit open items — all also listed in `CLAUDE.md`:

1. Backups (Supabase free tier has none) — mitigation not yet designed.
2. Signup gate for the shared instance: open self-service vs. maintainer approval.
3. Concrete rate-limit values (mechanism is decided).
4. `audit_log.action` taxonomy.
5. Alert notification channel (email vs. webhook).
6. Legal texts, plus two flagged questions for a lawyer (interstitial-page Impressum; controller role for click analytics).
7. `public.profile` table — only if the frontend needs it.
8. Notification for the "existing user added to a second team" path.

## Next step

Implementation in Claude Code, starting from `CLAUDE.md` in the code repo. Suggested order is in that file — in short: schema and migrations first, then the redirect path (the architectural spine), then CRUD, then the frontend.
