# The Authenticated Frontend — Design

**Status:** approved 2026-09-05 **Amends:** `docs/planning/02-external-services-and-hosting.md` (Supabase Preview strategy), `CLAUDE.md` (the `db push` prohibition, narrowed — see "Migrations reach two projects now").

The sixth implementation spec, and the second for the frontend. Five plans have merged:

- **Plan 1** — foundation and the redirect path.
- **Plan 2** — tenancy, authorization, audit, and team invitations.
- **Plan 3** — links and the shared domain.
- **Plan 4** — folders and tags.
- **Plan 5** — the web shell.

The API exposes 24 operations. The frontend has two route files and no way to sign in, so every one of those operations is reachable only by `curl`. This plan closes that gap for the operations an everyday user needs: log in, see your links, create, edit and delete them.

## Goal

A Verein volunteer can sign in and manage their short links without a terminal.

## Scope

### In scope

- Magic-link sign-in, the callback, session storage, sign-out.
- An authenticated layout: route guard, `/v1/me`, team switcher.
- Link list, create, edit, delete, scoped to a team in the URL.
- A second Supabase project for the Preview environment, and the migration path that keeps it current.

### Out of scope, and where each lands

- **Folders and tags UI** — plan 7. Both are optional fields on a link; the list shows tags read-only because the API returns them.
- **Members, roles, invitations UI** — plan 7. The API is built; these are owner-only screens, not everyday use.
- **Audit log view** — plan 7.
- **Custom domains** — a later plan. Until then one domain exists, so the form shows no domain picker.
- **Link password, QR, stats** — a later plan; the API does not have them yet.
- **The CLI** — `apps/cli` stays a `.gitkeep`.

## Global constraints

Inherited and not re-litigated here:

- The browser never calls the Go API. Every call runs server-side, which is what removes the CORS question and lets the access token live in an httpOnly cookie. `apps/web/src/server/api.ts` already documents this and accepts a token supplier.
- No hardcoded user-facing string. English and German ship together.
- WCAG 2.1 AA, gated in CI at two levels.
- A non-member gets 404, never 403 — matching `internal/authz`.

## Sign-in is magic link, and only magic link

### The decision

`signInWithOtp` with `shouldCreateUser: false`.

### Why not passwords

Volunteers running a Verein are the users. Passwords mean reset flows, strength rules, and breach exposure, all carried by a maintainer who does this in their spare time. Magic link also matches the invitation path that already exists: `supabase.InviteUser` sends a link the person clicks, so there is one mental model, not two.

### Why `shouldCreateUser: false` is load-bearing

This instance is invitation-only. `MAINTAINER_USER_IDS` gates team creation and members arrive through `inviteUserByEmail`. Left at its default, magic link would mint an `auth.users` row for any address anyone typed — a self-service signup door opened by accident, in a product that deliberately has none.

### The enumeration consequence

With account creation off, Supabase distinguishes a known address from an unknown one. The UI must not: both answers are "if that address has an account, a link is on its way." The distinction stays server-side.

## The session lives in cookies, written on both legs

`@supabase/ssr`'s `createServerClient` is bound to a cookie adapter over TanStack Start's request and response.

The adapter must **write** as well as read, and this is the part that looks optional and is not. `signInWithOtp` stores the PKCE verifier in a cookie; the callback needs it to exchange the code. Separately, the client refreshes a stale session on read and persists the new tokens as a side effect. A read-only adapter passes every test written against a fresh session and fails an hour later, in production, as a login that silently stops working.

Sign-out clears the cookies and returns to the public shell.

## The team is in the URL

`/teams/$teamId/links`, not a cookie.

A pasted URL then shows the same team to everyone, two tabs can hold two teams, and the loader knows the team before it fetches. A cookie makes the page depend on hidden state: the URL you send a colleague may show them something else, and two tabs overwrite each other's selection.

`$teamId` is validated in the layout against the memberships from `/v1/me`. A team you do not belong to renders not-found, so the frontend does not leak the existence of teams the API hides.

## Routes

```
routes/
  login.tsx                                  public — email form
  auth.callback.tsx                          server route — code → session → redirect
  _authed.tsx                                pathless layout — guard + /v1/me
  _authed/index.tsx                          → redirect to a team
  _authed/teams.$teamId.links.index.tsx      list
  _authed/teams.$teamId.links.new.tsx        create
  _authed/teams.$teamId.links.$linkId.tsx    edit
```

`_authed` is pathless: it wraps without adding a segment, and does the guard and the `/v1/me` fetch once rather than per page.

Create and edit are routes, not dialogs. Deep-linkable, back-button-correct, and each is a page a test can visit directly instead of reaching through three clicks. A dialog is a later refinement if the flow feels heavy.

## Data flow

Reads go through loaders: the loader calls `queryClient.ensureQueryData`, the component reads the same key. Data is fetched before render, so the list server-renders with content. The shell is already SSR; a client-fetched list would undo that.

Writes go through mutations: a server function does the work, then invalidates the query key **and** calls `router.invalidate()`, so loader-owned data and the Query cache cannot disagree.

Pagination lives in search params. The API is offset/limit with a typed `Page[T]`; `?page=2` in the URL means the back button works and page 3 can be sent to someone. Same reasoning as the team in the path.

New dependencies: `@tanstack/react-query`, `@tanstack/react-form`, `@supabase/ssr`, `@supabase/supabase-js`.

`@tanstack/react-table` is **not** added. The link list is a paginated list, not a data grid. Table earns its place with analytics.

## Validation stays thin on the client

TanStack Form covers required fields and obvious shapes for immediate feedback. Everything else is the API's answer.

No zod schema mirroring the API's rules. Huma already validates and returns RFC 9457, and a second copy of those rules drifts silently — `internal/destination` especially, whose SSRF and DNS-rebinding checks the browser cannot replicate at all. A client-side schema that looked authoritative would be the more dangerous kind of wrong.

## The link surfaces

**List:** short URL with copy, destination, state, created-at, tags read-only. Actions: edit, delete, copy. An empty state points at creating the first link rather than rendering an empty table.

**Form** (shared by create and edit): `destination_url` — the only required field — plus `slug`, `redirect_type`, `expires_at`, `analytics_enabled`. An empty slug means the API generates one, and the placeholder says so; a blank required-looking field otherwise reads as an oversight.

No `domain_id` picker. One domain exists; a select with one option is furniture. It appears when custom domains do.

**301 carries an inline warning**, next to the choice and not in a tooltip. A cached 301 stops clicks being counted and stops later destination changes taking effect for anyone who visited once — the kind of quiet breakage a user cannot diagnose.

**Delete confirms.** Nothing restores a link, and its slug may be in print.

## The dead short domain is surfaced, not hidden

`SHARED_DOMAIN_HOSTNAME` is `short.invalid` (see CLAUDE.md's Open items), so every link this UI creates has a `short_url` that cannot resolve.

The list and the post-create confirmation carry a translated notice saying the instance has no working short domain yet. The condition is `hostname` ending in `.invalid` — not a feature flag — so the notice disappears by itself the day a real domain is configured. Nothing to remember to switch off.

The alternative, showing a dead URL with a copy button and no explanation, is the confusion `short.invalid` was chosen to avoid.

## Error handling, four cases

- **Session gone mid-use.** A refresh failure surfaces as 401. Redirect to `/login` carrying the intended URL, so the magic link returns the person where they were.
- **Field errors.** RFC 9457 detail maps onto form fields by pointer. A rejected destination lights up the destination field, not a banner about "an error".
- **Not yours, or absent.** 403 and 404 both render not-found, per the 404-not-403 rule.
- **Rate limited.** `RATE_LIMIT_LINK_CREATE_PER_MIN` means 429 is expected, and gets its own message saying to wait.

The link list fails loudly when the API is down. This is deliberately unlike `fetchHealth`, which degrades to `unreachable` so a footer cannot break the page: a list that silently renders empty looks exactly like a team with no links, which is worse than an error.

## Preview gets its own Supabase project

### The decision

A second Supabase project backs the Preview environment. Preview deployments of both Vercel projects point at it.

### Why this changes now

Preview has shared the production database since the beginning, and it did not matter while plan 5's E2E only read public pages. Plan 6's E2E mints sessions and creates links, so every pull request would write users, teams, links and `audit_log` rows into production.

It also shrinks a risk this plan otherwise takes on. The Playwright fixture needs a service-role key in GitHub Actions; with a separate project, that key bypasses every policy in a **disposable** database rather than the real one.

### What it costs

- The free tier allows two active projects. This takes the second slot, so nothing else can have it.
- A free project pauses after a week of inactivity, which would break CI unpredictably. Doc 02 already plans a daily Vercel Cron keep-alive; it pings both projects.
- Preview environment variables on both Vercel projects are repointed: `DATABASE_URL`, `SUPABASE_JWKS_URL`, `SUPABASE_JWT_ISSUER`, `SUPABASE_SERVICE_ROLE_KEY`, and the web project's `SUPABASE_URL` and publishable key.

### Migrations reach two projects now

Supabase's GitHub integration deploys to one production project. The preview project needs its own path, and the documentation does not say whether two projects can connect to the same repository — so the first task establishes it rather than assuming.

**If two projects can connect:** use that. Supabase's integration stays the only thing applying migrations, and CLAUDE.md's one-owner rule is untouched.

**If they cannot:** a CI job runs `supabase db push` against the preview project only. This collides with CLAUDE.md's flat prohibition on a `db push` workflow, and the collision must be resolved in the document rather than in silence. The rule exists so that **production** schema state has one owner; a push that can only ever reach the preview project does not give production a second owner. If this fallback is taken, CLAUDE.md is amended to say so explicitly, naming the production project as the thing the rule protects.

## Testing

Three layers exist and are used as they are: Vitest + RTL + MSW, Storybook with the a11y addon failing CI, Playwright + axe against previews. The E2E layer only began genuinely testing this app on 2026-09-05, so plan 6 is the first plan that can rely on it.

**Falsification is mandatory.** Plan 4 found 6 of 15 properties were false passes, and plan 5 shipped four axe tests that passed while analysing Vercel's login page. Every property here is checked by breaking the code it covers and confirming the test fails. The auth guard is the obvious trap: a test that visits an authenticated route logged out and expects a redirect passes just as happily when the guard is deleted and the page 500s instead.

**Units** cover what carries the risk: the cookie adapter's read _and write_ paths, the `beforeLoad` guard, `$teamId` validation resolving to not-found, and the RFC 9457 to field-error mapping. MSW fakes both the Go API and Supabase's token endpoint.

**E2E** mints a session through the Supabase Admin API in a Playwright fixture and sets the cookies directly — magic link leaves nothing for a browser to type and no inbox to read. Against the preview project, the fixture may create and delete freely.

With authenticated pages reachable, the existing i18n spec extends past `/` and the 404 page, and axe runs against the screens people actually use.

## Deployment

No new Vercel project. New Preview and Production variables on `kurze-url-web`: `SUPABASE_URL` and the publishable key. `SUPABASE_SERVICE_ROLE_KEY` for the preview project becomes a GitHub Actions secret.

Custom SMTP must be live before this ships. Supabase's built-in sender caps at 2 emails/hour **and only delivers to pre-authorised team addresses** — with magic link as the only way in, that is not a throttle, it is a closed door for everyone but the maintainer. Doc 02 records Resend, the 50/hour Supabase limit to set alongside it, and the 100/day ceiling to watch.

## Open questions

- Whether two Supabase projects can connect to one GitHub repository. Settled by the first task; determines whether CLAUDE.md needs amending.
- The shared short domain remains unresolved (CLAUDE.md Open items). This plan surfaces the consequence rather than waiting on it.
- Whether the team switcher needs a "last used team" cookie for `_authed/index.tsx`, or whether redirecting to the first membership is enough. Deferred until there is a user with two teams.
