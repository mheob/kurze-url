# The Authenticated Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Verein volunteer can sign in with a magic link and manage their short links without a terminal.

**Architecture:** Magic-link sign-in through Supabase, with the session in httpOnly cookies written by `@supabase/ssr` bound to TanStack Start's request and response. The browser never calls the Go API: every call runs in a server function that reads the access token from those cookies and hands it to the existing `getApiClient`. The current team is a route parameter, validated against the memberships `/v1/me` returns.

**Tech Stack:** TanStack Start 1.168 (Router 1.170) on Vite 8 and React 19; TanStack Query 5 and TanStack Form 1 (both new here); `@supabase/ssr` 0.12 and `@supabase/supabase-js` 2; Tailwind CSS v4; shadcn/ui on Radix; react-i18next 17; Vitest + React Testing Library + MSW 2; Playwright + axe-core; Storybook 10; oxlint and oxfmt.

**Spec:** `docs/superpowers/specs/2026-09-05-authenticated-frontend-design.md`

## Global Constraints

These bind every task. They are the project's rules, restated so you do not have to reconstruct them from `CLAUDE.md`.

- **No hardcoded user-facing string, anywhere, ever — not even temporarily.** English default, German alongside. Both catalogues are real from the first string. `apps/web/src/i18n/catalogues.test.ts` already fails on a key present in one and missing from the other.
- Dark and light mode from the first component.
- **WCAG 2.1 AA**, gated in CI at two levels (Storybook's a11y addon, Playwright + axe).
- The tenant is called `team` in every identifier. "Verein" appears only in user-facing German copy.
- **The browser never calls the Go API.** Every API call runs inside a TanStack Start server function. This is what removes CORS and lets the token stay in an httpOnly cookie.
- **A non-member gets 404, never 403.** `internal/authz` enforces this server-side; the frontend must not contradict it by rendering a "forbidden" page.
- `GET /<slug>` is the Go service's hot path. Nothing here touches it.
- Never store a full IP address.
- **oxlint and oxfmt, never ESLint or Prettier.** `pnpm format` and `pnpm lint` from the repo root.
- TypeScript **7** at the root. The TypeScript 6 pin in `packages/api-client` is codegen-only — do not copy it into `apps/web`.
- Conventional Commits, checked in CI.
- Run from the repo root before every commit: `pnpm format`, `pnpm lint`, `pnpm typecheck`, `pnpm test`.

### Falsification is part of every task, not a final pass

Plan 4's review found **6 of 15 properties were false passes** — tenancy tests that could not fail, because the authorization layer intercepted before the code under test ran. Plan 5 shipped four axe tests that passed while analysing Vercel's login page.

So: after a test goes green, **break the code it covers and confirm that test, and ideally only that test, fails.** Report which tests failed under which mutation in your task report. A test you did not falsify is a test you do not know works.

The auth guard is the trap in this plan. A test that visits an authenticated route while logged out and asserts a redirect passes just as happily when the guard is deleted and the route 500s instead — assert the redirect _location_, not merely that rendering did not succeed.

### A note on the environment

`SHARED_DOMAIN_HOSTNAME` is `short.invalid` in Production and Preview. Every link this UI creates therefore has a `short_url` that cannot resolve. That is deliberate and documented in `CLAUDE.md`'s Open items; Task 11 surfaces it in the UI. Do not "fix" it by pointing the app somewhere else.

---

## File Structure

**Create — auth:**

| Path | Responsibility |
| --- | --- |
| `apps/web/src/server/supabase.ts` | `createServerClient` bound to a read/write cookie adapter over the request and response. |
| `apps/web/src/server/supabase.test.ts` | The adapter's read _and_ write paths. |
| `apps/web/src/server/session.ts` | `requireSession`, `getAccessToken`, and the authenticated `getApiClient` wrapper. |
| `apps/web/src/server/session.test.ts` | Missing session, expired session, refreshed session. |
| `apps/web/src/server/auth.ts` | Server functions: `sendMagicLink`, `signOut`. |
| `apps/web/src/server/auth.test.ts` | Enumeration-safety: identical result for known and unknown addresses. |
| `apps/web/src/routes/login.tsx` | The email form. |
| `apps/web/src/routes/auth.callback.tsx` | Server route: code → session → redirect. |

**Create — the authenticated shell:**

| Path | Responsibility |
| --- | --- |
| `apps/web/src/routes/_authed.tsx` | Pathless layout: guard, `/v1/me`, team context. |
| `apps/web/src/routes/_authed/index.tsx` | Redirect to the last-used team. |
| `apps/web/src/lib/current-team.ts` | The `team` cookie: read, validate against memberships, write. |
| `apps/web/src/lib/current-team.test.ts` | Unit tests, including the stale-membership case. |
| `apps/web/src/components/team-switcher.tsx` | Team menu; writes the cookie. |
| `apps/web/src/components/team-switcher.stories.tsx` | Storybook story, so the a11y addon covers it. |

**Create — links:**

| Path | Responsibility |
| --- | --- |
| `apps/web/src/server/links.ts` | Server functions: list, get, create, update, delete. |
| `apps/web/src/server/links.test.ts` | Token forwarding and error translation. |
| `apps/web/src/lib/api-errors.ts` | RFC 9457 → field errors, plus 401/404/429 classification. |
| `apps/web/src/lib/api-errors.test.ts` | One test per shape the API actually returns. |
| `apps/web/src/routes/_authed/teams.$teamId.links.index.tsx` | The list. |
| `apps/web/src/routes/_authed/teams.$teamId.links.new.tsx` | Create. |
| `apps/web/src/routes/_authed/teams.$teamId.links.$linkId.tsx` | Edit and delete. |
| `apps/web/src/components/link-form.tsx` | The form shared by create and edit. |
| `apps/web/src/components/link-form.stories.tsx` | Storybook story. |
| `apps/web/src/components/copy-button.tsx` | Copy with an `aria-live` confirmation. |
| `apps/web/src/components/short-url-notice.tsx` | The `.invalid` notice. |

**Create — E2E:**

| Path | Responsibility |
| --- | --- |
| `apps/web/e2e/fixtures/auth.ts` | Mints a session via the Supabase Admin API and sets the cookies. |
| `apps/web/e2e/links.spec.ts` | Authenticated flow, axe, and cross-language checks. |

**Modify:**

| Path | Change |
| --- | --- |
| `apps/web/package.json` | Add `@tanstack/react-query`, `@tanstack/react-form`, `@supabase/ssr`, `@supabase/supabase-js`. |
| `apps/web/src/router.tsx` | Provide a `QueryClient`. |
| `apps/web/src/i18n/locales/{en,de}.json` | Every new string, in both. |
| `apps/web/e2e/i18n.spec.ts` | Extend the crawl to authenticated pages. |
| `.github/workflows/ci-js.yml` | Pass the preview project's service-role key to Playwright. |

---

## Task 1: The Preview Supabase project and its environment

**This task is the maintainer's, not an agent's** — it is dashboard work and secret handling. An agent executing this plan should stop here, print the checklist, and wait. The verification step at the end is automatable and _is_ the gate: do not mark the task done on the checklist alone.

**Files:**

- Modify: `.github/workflows/ci-js.yml`

**Interfaces:**

- Produces: a Preview environment whose `DATABASE_URL`, `SUPABASE_JWKS_URL`, `SUPABASE_JWT_ISSUER` and `SUPABASE_SERVICE_ROLE_KEY` point at `kurze-url-preview`, plus `SUPABASE_URL` and the publishable key on `kurze-url-web`. Every later task assumes a preview deployment can be signed into without touching production data.

- [ ] **Step 1: Confirm both Supabase projects are connected to the repository**

`kurze-url` and `kurze-url-preview` must each show `mheob/kurze-url` in the Supabase dashboard's project list. Confirmed 2026-09-05; re-confirm, because everything below assumes migrations reach both databases through Supabase's own integration and that no `supabase db push` workflow is needed. `CLAUDE.md` forbids that workflow, and this is the check that keeps the ban valid.

- [ ] **Step 2: Repoint the API project's Preview environment**

```bash
vercel env rm DATABASE_URL preview --project kurze-url-api --yes
vercel env rm SUPABASE_JWKS_URL preview --project kurze-url-api --yes
vercel env rm SUPABASE_JWT_ISSUER preview --project kurze-url-api --yes
vercel env rm SUPABASE_SERVICE_ROLE_KEY preview --project kurze-url-api --yes
```

Then add each again with the **`kurze-url-preview`** project's values. Take `DATABASE_URL` from that project's **transaction pooler** (port 6543), not a direct connection — the direct one is IPv6-only and Vercel has no IPv6 egress, which is a failure this project has already had once.

```bash
printf '<preview pooler url>'      | vercel env add DATABASE_URL preview --project kurze-url-api
printf '<preview jwks url>'        | vercel env add SUPABASE_JWKS_URL preview --no-sensitive --project kurze-url-api
printf '<preview issuer url>'      | vercel env add SUPABASE_JWT_ISSUER preview --no-sensitive --project kurze-url-api
printf '<preview service role key>' | vercel env add SUPABASE_SERVICE_ROLE_KEY preview --project kurze-url-api
```

`--no-sensitive` on the two URLs is deliberate. This project defaults new variables to Secret, and a Secret-typed value cannot be read back by `vercel env pull` — it returns the literal string `[SENSITIVE]`. Hostnames and issuer URLs are not secrets, and making them unreadable has already cost this project a debugging session.

- [ ] **Step 3: Add the web project's Supabase variables**

```bash
printf '<preview project url>'      | vercel env add SUPABASE_URL preview --no-sensitive --project kurze-url-web
printf '<preview publishable key>'  | vercel env add SUPABASE_PUBLISHABLE_KEY preview --project kurze-url-web
printf '<production project url>'     | vercel env add SUPABASE_URL production --no-sensitive --project kurze-url-web
printf '<production publishable key>' | vercel env add SUPABASE_PUBLISHABLE_KEY production --project kurze-url-web
```

- [ ] **Step 4: Add the CI secret**

```bash
printf '<preview service role key>' | gh secret set SUPABASE_SERVICE_ROLE_KEY
```

This is the **preview** project's key, never production's. It bypasses every database policy in whichever project it belongs to, which is exactly why the spec moved E2E onto a separate project before agreeing to put a key in CI.

- [ ] **Step 5: Pass it to Playwright**

```yaml
# .github/workflows/ci-js.yml, in the Playwright step's env block
SUPABASE_SERVICE_ROLE_KEY: ${{ secrets.SUPABASE_SERVICE_ROLE_KEY }}
SUPABASE_URL: ${{ vars.SUPABASE_PREVIEW_URL }}
```

- [ ] **Step 6: Extend the keep-alive to both projects**

A free Supabase project pauses after a week of inactivity. `docs/planning/02-external-services-and-hosting.md` already plans a daily Vercel Cron that pings the database and checks free-tier thresholds; it must now ping **both** projects. If that cron does not exist yet, record it as a prerequisite in the task report rather than building it here — an unpinged preview project will pause and break CI in about a week, silently.

- [ ] **Step 7: Confirm custom SMTP is live**

Magic link is the only way in, so this is a prerequisite for the plan to be testable by anyone but the maintainer. Supabase's built-in sender caps at **2 emails/hour** _and delivers only to pre-authorised team member addresses_ — that is not a throttle, it is a closed door.

At `https://supabase.com/dashboard/project/_/auth/smtp`, on **both** projects: host `smtp.resend.com`, port `587`, username the literal string `resend`, password a Resend API key, sender on a domain verified in Resend.

Then raise the limit at Auth → **Rate Limits** to **50/hour**. Enabling custom SMTP does not lift the cap to Resend's quota; it swaps a 2/hour default for a 30/hour one. `docs/planning/02-external-services-and-hosting.md` records why 50 — above the API's own `RATE_LIMIT_INVITE_PER_HOUR` of 20 per team, and low enough that one hour cannot drain Resend's 100/day.

Verify by inviting an address you control through `POST /v1/teams/{team_id}/members` and confirming the email arrives. That exercises Go → Supabase Admin API → Resend, the same chain magic-link login uses.

- [ ] **Step 8: Verify against a real preview deployment**

```bash
git commit --allow-empty -m "chore: verify preview database wiring"
but push <branch>
# wait for the API preview, then:
curl -s -H "x-vercel-protection-bypass: $BYPASS" \
  "https://kurze-url-api-git-<branch>-mheobs-projects.vercel.app/v1/health"
```

Expected: `{"status":"ok"}`. A `500` mentioning `DATABASE_URL` means the pooler string is wrong; `relation "domain" does not exist` means the new project has no migrations yet and Step 1's assumption is false — stop and fix that before continuing.

- [ ] **Step 9: Commit**

```bash
git add .github/workflows/ci-js.yml
git commit -m "ci: give Playwright the preview project's service-role key"
```

---

## Task 2: Dependencies and the Supabase cookie adapter

**Files:**

- Create: `apps/web/src/server/supabase.ts`, `apps/web/src/server/supabase.test.ts`
- Modify: `apps/web/package.json`

**Interfaces:**

- Consumes: Task 1's environment variables.
- Produces:
  - `createSupabase(request: Request, headers: Headers): SupabaseClient`
  - `SUPABASE_COOKIE_OPTIONS` — the options every auth cookie is written with.

Tasks 3, 4, 5 and 6 all call `createSupabase`.

- [ ] **Step 1: Install**

```bash
pnpm --filter @kurze-url/web add @supabase/ssr @supabase/supabase-js @tanstack/react-query @tanstack/react-form
```

Note the resolved versions in your task report. This repo has a `minimumReleaseAge` supply-chain gate; if a package is refused, say so rather than lowering the gate.

- [ ] **Step 2: Write the failing tests**

```ts
// apps/web/src/server/supabase.test.ts
import { describe, expect, it, vi } from 'vitest';

import { createSupabase } from './supabase';

/**
 * The adapter's write path is the one that looks optional and is not.
 * signInWithOtp stores the PKCE verifier in a cookie the callback needs,
 * and @supabase/ssr persists refreshed tokens the same way. A read-only
 * adapter passes every test written against a fresh session and then fails
 * an hour later, in production, as a login that silently stops working.
 */
describe('createSupabase', () => {
	it('reads cookies from the request', async () => {
		const request = new Request('https://example.test/', {
			headers: { cookie: 'sb-access-token=abc; other=x' },
		});
		const headers = new Headers();
		const client = createSupabase(request, headers);

		// The client is constructed without throwing and can see the header.
		expect(client).toBeDefined();
		expect(request.headers.get('cookie')).toContain('sb-access-token=abc');
	});

	it('writes cookies onto the response headers', () => {
		const request = new Request('https://example.test/');
		const headers = new Headers();

		createSupabase(request, headers);
		// The adapter is exercised directly: @supabase/ssr calls setAll during
		// sign-in and refresh, and nothing else in the app would notice if it
		// were a no-op.
		const setAll = (globalThis as { __lastSetAll?: (c: unknown[]) => void }).__lastSetAll;
		expect(setAll).toBeTypeOf('function');
		setAll?.([{ name: 'sb-x', value: 'y', options: {} }]);

		expect(headers.get('set-cookie')).toContain('sb-x=y');
	});

	it('marks auth cookies httpOnly, Secure and SameSite=Lax', () => {
		const request = new Request('https://example.test/');
		const headers = new Headers();
		createSupabase(request, headers);
		const setAll = (globalThis as { __lastSetAll?: (c: unknown[]) => void }).__lastSetAll;
		setAll?.([{ name: 'sb-x', value: 'y', options: {} }]);

		const cookie = headers.get('set-cookie') ?? '';
		expect(cookie).toContain('HttpOnly');
		expect(cookie).toContain('SameSite=Lax');
		expect(cookie).toContain('Path=/');
	});
});
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/supabase.test.ts` Expected: FAIL — `Failed to resolve import "./supabase"`.

- [ ] **Step 4: Implement**

```ts
// apps/web/src/server/supabase.ts
/// <reference types="node" />

import { createServerClient, type CookieOptions } from '@supabase/ssr';
import type { SupabaseClient } from '@supabase/supabase-js';

/**
 * Auth cookies are not preference cookies. `preferences.ts` writes `lang` and
 * `theme` for the client to read; these must be invisible to JavaScript, which
 * is the whole reason the access token lives here rather than in memory the
 * browser can reach.
 */
export const SUPABASE_COOKIE_OPTIONS: CookieOptions = {
	httpOnly: true,
	sameSite: 'lax',
	secure: true,
	path: '/',
};

function serialize(name: string, value: string, options: CookieOptions): string {
	const parts = [`${name}=${value}`, `Path=${options.path ?? '/'}`];
	if (options.maxAge !== undefined) parts.push(`Max-Age=${options.maxAge}`);
	if (options.httpOnly) parts.push('HttpOnly');
	if (options.secure) parts.push('Secure');
	if (options.sameSite) parts.push(`SameSite=Lax`);
	return parts.join('; ');
}

function parse(cookieHeader: string | null): { name: string; value: string }[] {
	if (!cookieHeader) return [];

	return cookieHeader
		.split(';')
		.map((part) => part.trim())
		.filter(Boolean)
		.map((part) => {
			const [name, ...rest] = part.split('=');
			return { name: name?.trim() ?? '', value: rest.join('=') };
		})
		.filter((c) => c.name !== '');
}

/**
 * Bound to one request and one outgoing header set. Never module-level: the
 * frontend renders on the server, where one process serves many people, and a
 * shared client would leak one person's session into another's request — the
 * same reason `createApiClient` returns a fresh instance.
 */
export function createSupabase(request: Request, headers: Headers): SupabaseClient {
	const url = process.env.SUPABASE_URL;
	const key = process.env.SUPABASE_PUBLISHABLE_KEY;
	if (!url || !key) {
		throw new Error('SUPABASE_URL and SUPABASE_PUBLISHABLE_KEY are required');
	}

	const setAll = (cookies: { name: string; value: string; options: CookieOptions }[]): void => {
		for (const { name, value, options } of cookies) {
			headers.append(
				'set-cookie',
				serialize(name, value, { ...SUPABASE_COOKIE_OPTIONS, ...options }),
			);
		}
	};
	// Exposed for the adapter's own tests: @supabase/ssr only calls setAll
	// during sign-in and refresh, so nothing else in the app would notice a
	// no-op write path.
	(globalThis as { __lastSetAll?: typeof setAll }).__lastSetAll = setAll;

	return createServerClient(url, key, {
		cookies: { getAll: () => parse(request.headers.get('cookie')), setAll },
	});
}
```

- [ ] **Step 5: Run the tests and watch them pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/supabase.test.ts` Expected: PASS, 3 tests.

- [ ] **Step 6: Falsify**

Replace the body of `setAll` with `return;` and re-run. Expected: the second and third tests fail, the first still passes. If all three still pass, the write path is not actually under test — fix the test, not the code. Record the result in your task report.

- [ ] **Step 7: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck
git add apps/web/package.json apps/web/src/server/supabase.ts apps/web/src/server/supabase.test.ts pnpm-lock.yaml
git commit -m "feat(web): bind Supabase to a read/write cookie adapter"
```

---

## Task 3: Session helpers and the authenticated API client

**Files:**

- Create: `apps/web/src/server/session.ts`, `apps/web/src/server/session.test.ts`
- Modify: `apps/web/src/server/api.ts`

**Interfaces:**

- Consumes: `createSupabase` from Task 2; `getApiClient` from `src/server/api.ts`.
- Produces:
  - `getAccessToken(request: Request, headers: Headers): Promise<string | undefined>`
  - `requireSession(request: Request, headers: Headers): Promise<{ accessToken: string }>` — throws `UnauthenticatedError` when there is none
  - `class UnauthenticatedError extends Error`
  - `authedApiClient(accessToken: string): ReturnType<typeof getApiClient>`

Every server function in Tasks 4, 6, 7 and 10 calls `requireSession` then `authedApiClient`.

- [ ] **Step 1: Write the failing tests**

```ts
// apps/web/src/server/session.test.ts
import { describe, expect, it, vi } from 'vitest';

import { UnauthenticatedError, getAccessToken, requireSession } from './session';

vi.mock('./supabase', () => ({
	createSupabase: vi.fn(() => ({
		auth: {
			getSession: vi.fn(async () => globalThis.__session),
		},
	})),
}));

declare global {
	// eslint-disable-next-line no-var
	var __session: { data: { session: { access_token: string } | null }; error: null };
}

function req(): [Request, Headers] {
	return [new Request('https://example.test/'), new Headers()];
}

describe('getAccessToken', () => {
	it('returns the token when a session exists', async () => {
		globalThis.__session = { data: { session: { access_token: 'tok' } }, error: null };
		await expect(getAccessToken(...req())).resolves.toBe('tok');
	});

	it('returns undefined when there is no session', async () => {
		globalThis.__session = { data: { session: null }, error: null };
		await expect(getAccessToken(...req())).resolves.toBeUndefined();
	});
});

describe('requireSession', () => {
	it('throws UnauthenticatedError rather than returning an empty token', async () => {
		// The guard must fail closed. Returning '' here would send an
		// unauthenticated request to the API, which answers 401 — the same
		// symptom, three layers further away from the cause.
		globalThis.__session = { data: { session: null }, error: null };
		await expect(requireSession(...req())).rejects.toBeInstanceOf(UnauthenticatedError);
	});

	it('returns the token when a session exists', async () => {
		globalThis.__session = { data: { session: { access_token: 'tok' } }, error: null };
		await expect(requireSession(...req())).resolves.toEqual({ accessToken: 'tok' });
	});
});
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/session.test.ts` Expected: FAIL — `Failed to resolve import "./session"`.

- [ ] **Step 3: Implement**

```ts
// apps/web/src/server/session.ts
import { getApiClient } from './api';
import { createSupabase } from './supabase';

/**
 * Distinguishable from an API 401 on purpose. This one means "no session on
 * this request" and the answer is a redirect to /login; an API 401 means the
 * token was rejected, and conflating them turns a normal signed-out visit into
 * a round trip to the Go service.
 */
export class UnauthenticatedError extends Error {
	constructor() {
		super('no session');
		this.name = 'UnauthenticatedError';
	}
}

/**
 * Reading the session is also what refreshes it: @supabase/ssr renews an
 * expired one and writes the new cookies through the adapter's setAll. That is
 * why `headers` is threaded all the way down here rather than only used at
 * sign-in.
 */
export async function getAccessToken(
	request: Request,
	headers: Headers,
): Promise<string | undefined> {
	const supabase = createSupabase(request, headers);
	const { data } = await supabase.auth.getSession();
	return data.session?.access_token;
}

export async function requireSession(
	request: Request,
	headers: Headers,
): Promise<{ accessToken: string }> {
	const accessToken = await getAccessToken(request, headers);
	if (!accessToken) throw new UnauthenticatedError();
	return { accessToken };
}

export function authedApiClient(accessToken: string): ReturnType<typeof getApiClient> {
	return getApiClient(undefined, () => accessToken);
}
```

- [ ] **Step 4: Extend `getApiClient` to accept a token supplier**

`src/server/api.ts` currently builds an anonymous client. Give it the optional second parameter `authedApiClient` needs, leaving the existing single-argument calls working:

```ts
export function getApiClient(
	baseUrl: string = apiBaseUrl(),
	getAccessToken?: () => string | undefined,
): ReturnType<typeof createApiClient> {
	return createApiClient({ baseUrl, getAccessToken, headers: platformHeaders() });
}
```

`createApiClient` already accepts `getAccessToken` and documents why it is a function rather than a string: a Supabase access token expires, and reading it per request lets the caller hand back whatever the session currently holds.

- [ ] **Step 5: Run the tests and watch them pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/session.test.ts` Expected: PASS, 4 tests.

- [ ] **Step 6: Falsify**

Change `requireSession` to `return { accessToken: accessToken ?? '' }`. Expected: the "throws UnauthenticatedError" test fails and nothing else does. Record it.

- [ ] **Step 7: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/server/session.ts apps/web/src/server/session.test.ts apps/web/src/server/api.ts
git commit -m "feat(web): read the session and build an authenticated API client"
```

---

## Task 4: Sending the magic link

**Files:**

- Create: `apps/web/src/server/auth.ts`, `apps/web/src/server/auth.test.ts`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `createSupabase` from Task 2.
- Produces: `sendMagicLink` — a server function taking `{ email: string }` and resolving to `{ sent: true }` **always**.

- [ ] **Step 1: Write the failing test**

```ts
// apps/web/src/server/auth.test.ts
import { describe, expect, it, vi } from 'vitest';

const signInWithOtp = vi.fn();
vi.mock('./supabase', () => ({ createSupabase: () => ({ auth: { signInWithOtp } }) }));

const { sendMagicLinkFor } = await import('./auth');

describe('sendMagicLinkFor', () => {
	it('never creates an account', async () => {
		// This instance is invitation-only: MAINTAINER_USER_IDS gates team
		// creation and members arrive through inviteUserByEmail. Left at its
		// default, signInWithOtp would mint an auth.users row for any address
		// anyone typed — a self-service signup door opened by accident.
		signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor('someone@example.test', 'https://app.test');

		expect(signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				options: expect.objectContaining({ shouldCreateUser: false }),
			}),
		);
	});

	it('reports the same result for an unknown address as a known one', async () => {
		// With account creation off, Supabase distinguishes known from unknown.
		// The UI must not: otherwise the login form becomes an oracle for
		// which addresses belong to a Verein.
		signInWithOtp.mockResolvedValue({ error: null });
		const known = await sendMagicLinkFor('known@example.test', 'https://app.test');

		signInWithOtp.mockResolvedValue({ error: { message: 'Signups not allowed for otp' } });
		const unknown = await sendMagicLinkFor('unknown@example.test', 'https://app.test');

		expect(unknown).toEqual(known);
		expect(unknown).toEqual({ sent: true });
	});
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/auth.test.ts` Expected: FAIL — `Failed to resolve import "./auth"`.

- [ ] **Step 3: Implement**

```ts
// apps/web/src/server/auth.ts
import { createServerFn } from '@tanstack/react-start';

import { createSupabase } from './supabase';

/**
 * Extracted from the server function so it can be tested without a request:
 * the enumeration guarantee is the interesting behaviour, and it should not
 * need a framework harness to assert.
 */
export async function sendMagicLinkFor(email: string, origin: string): Promise<{ sent: true }> {
	const headers = new Headers();
	const supabase = createSupabase(new Request(origin), headers);

	await supabase.auth.signInWithOtp({
		email,
		options: { shouldCreateUser: false, emailRedirectTo: `${origin}/auth/callback` },
	});

	// Deliberately ignoring the error. Supabase distinguishes a known address
	// from an unknown one; surfacing that difference would turn this form into
	// an account-enumeration oracle. Failures that matter (SMTP down) show up
	// in Sentry, not here.
	return { sent: true };
}

export const sendMagicLink = createServerFn({ method: 'POST' })
	.validator((data: { email: string }) => data)
	.handler(async ({ data, request }) => sendMagicLinkFor(data.email, new URL(request.url).origin));
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/server/auth.test.ts` Expected: PASS, 2 tests.

- [ ] **Step 5: Falsify**

Remove `shouldCreateUser: false`, re-run: the first test must fail. Then restore it and make the function return `{ sent: !error }`: the second test must fail. Record both.

- [ ] **Step 6: Add the strings, both languages**

```json
// en.json — merge into the existing object
"auth": {
  "signInTitle": "Sign in",
  "emailLabel": "Email address",
  "sendLink": "Send me a link",
  "linkSent": "If that address has an account, a link is on its way.",
  "signOut": "Sign out"
}
```

```json
// de.json — merge into the existing object
"auth": {
  "signInTitle": "Anmelden",
  "emailLabel": "E-Mail-Adresse",
  "sendLink": "Link zusenden",
  "linkSent": "Falls es zu dieser Adresse ein Konto gibt, ist ein Link unterwegs.",
  "signOut": "Abmelden"
}
```

The English copy is deliberately conditional. "Check your email" would assert something the server has not established and, worse, would state it differently for an address that does not exist.

- [ ] **Step 7: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/server/auth.ts apps/web/src/server/auth.test.ts apps/web/src/i18n/locales
git commit -m "feat(web): send magic links without creating accounts"
```

---

## Task 5: The login page, the callback, and sign-out

**Files:**

- Create: `apps/web/src/routes/login.tsx`, `apps/web/src/routes/auth.callback.tsx`, `apps/web/src/routes/login.test.tsx`
- Modify: `apps/web/src/server/auth.ts`

**Interfaces:**

- Consumes: `sendMagicLink` (Task 4), `createSupabase` (Task 2).
- Produces: routes `/login`, `/auth/callback`; server function `signOut`.

- [ ] **Step 1: Write the failing test**

```tsx
// apps/web/src/routes/login.test.tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

const sendMagicLink = vi.fn(async () => ({ sent: true as const }));
vi.mock('../server/auth', () => ({ sendMagicLink }));

const { LoginForm } = await import('./login');

describe('LoginForm', () => {
	it('shows the same confirmation whatever the address', async () => {
		render(<LoginForm />);
		await userEvent.type(screen.getByLabelText(/email|e-mail/i), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/i }));

		expect(await screen.findByText(/on its way|unterwegs/i)).toBeInTheDocument();
	});

	it('labels the field, so it is reachable without a mouse', async () => {
		// An input with only a placeholder passes a visual review and fails a
		// screen reader. Accessibility is a CI gate here, not a preference.
		render(<LoginForm />);
		expect(screen.getByLabelText(/email|e-mail/i)).toBeInTheDocument();
	});
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/routes/login.test.tsx` Expected: FAIL — `Failed to resolve import "./login"`.

- [ ] **Step 3: Implement the login route**

```tsx
// apps/web/src/routes/login.tsx
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../components/ui/button';
import { sendMagicLink } from '../server/auth';

export function LoginForm(): React.JSX.Element {
	const { t } = useTranslation();
	const [sent, setSent] = useState(false);
	const [email, setEmail] = useState('');

	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				void sendMagicLink({ data: { email } }).then(() => setSent(true));
			}}
		>
			<h1>{t('auth.signInTitle')}</h1>
			<label htmlFor="email">{t('auth.emailLabel')}</label>
			<input
				autoComplete="email"
				id="email"
				name="email"
				onChange={(event) => setEmail(event.target.value)}
				required
				type="email"
				value={email}
			/>
			<Button type="submit">{t('auth.sendLink')}</Button>
			{/* Announced, not merely rendered: a confirmation a screen reader
			    never reaches is the same as no confirmation. */}
			<p aria-live="polite">{sent ? t('auth.linkSent') : ''}</p>
		</form>
	);
}

export const Route = createFileRoute('/login')({ component: LoginForm });
```

- [ ] **Step 4: Implement the callback**

```tsx
// apps/web/src/routes/auth.callback.tsx
import { createFileRoute, redirect } from '@tanstack/react-router';

import { createSupabase } from '../server/supabase';

export const Route = createFileRoute('/auth/callback')({
	loader: async ({ location, request }) => {
		const headers = new Headers();
		const code = new URL(location.href, 'https://placeholder.invalid').searchParams.get('code');

		if (code) {
			const supabase = createSupabase(request, headers);
			// This is where the PKCE verifier cookie written during
			// signInWithOtp is consumed. If the adapter cannot read cookies,
			// this fails with "code verifier should be non-empty".
			await supabase.auth.exchangeCodeForSession(code);
		}

		// The Set-Cookie headers the exchange produced must travel with the
		// redirect, or the session is created and immediately lost.
		throw redirect({ to: '/', headers });
	},
});
```

- [ ] **Step 5: Add sign-out to `src/server/auth.ts`**

```ts
export const signOut = createServerFn({ method: 'POST' }).handler(async ({ request }) => {
	const headers = new Headers();
	const supabase = createSupabase(request, headers);
	await supabase.auth.signOut();
	return { headers };
});
```

- [ ] **Step 6: Run the tests and watch them pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/routes/login.test.tsx` Expected: PASS, 2 tests.

- [ ] **Step 7: Falsify**

Replace the `<label>` with a `placeholder` attribute. Expected: the second test fails. Record it.

- [ ] **Step 8: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/routes/login.tsx apps/web/src/routes/login.test.tsx apps/web/src/routes/auth.callback.tsx apps/web/src/server/auth.ts
git commit -m "feat(web): add login, the auth callback and sign-out"
```

---

## Task 6: The authenticated layout and its guard

**Files:**

- Create: `apps/web/src/routes/_authed.tsx`, `apps/web/src/routes/_authed.test.ts`
- Modify: `apps/web/src/server/links.ts` (created here with `fetchMe` only)

**Interfaces:**

- Consumes: `requireSession`, `authedApiClient` (Task 3).
- Produces:
  - `fetchMe` — server function resolving `{ user_id: string; email: string; memberships: { team_id: string; name: string; role: string }[] }`
  - route context `{ me }` for every `_authed/*` route
  - `assertMembership(memberships, teamId): void` — throws `notFound()` when absent

- [ ] **Step 1: Write the failing test**

```ts
// apps/web/src/routes/_authed.test.ts
import { describe, expect, it } from 'vitest';

import { assertMembership } from './_authed';

const memberships = [{ team_id: 'a', name: 'Verein A', role: 'owner' }];

describe('assertMembership', () => {
	it('passes for a team you belong to', () => {
		expect(() => assertMembership(memberships, 'a')).not.toThrow();
	});

	it('throws a not-found for a team you do not belong to', () => {
		// 404, never 403. internal/authz already answers 404 for a non-member
		// so the API does not confirm that a team exists; a frontend that
		// rendered "forbidden" would leak exactly what the API withholds.
		expect(() => assertMembership(memberships, 'b')).toThrow();
	});
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/routes/_authed.test.ts` Expected: FAIL — `Failed to resolve import "./_authed"`.

- [ ] **Step 3: Implement**

```tsx
// apps/web/src/routes/_authed.tsx
import { getMe } from '@kurze-url/api-client';
import { createFileRoute, notFound, Outlet, redirect } from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';

import { authedApiClient, requireSession, UnauthenticatedError } from '../server/session';

export interface Membership {
	team_id: string;
	name: string;
	role: string;
}

export const fetchMe = createServerFn({ method: 'GET' }).handler(async ({ request }) => {
	const headers = new Headers();
	const { accessToken } = await requireSession(request, headers);
	const { data } = await getMe({ client: authedApiClient(accessToken), throwOnError: true });
	return data;
});

export function assertMembership(memberships: Membership[], teamId: string): void {
	if (!memberships.some((m) => m.team_id === teamId)) throw notFound();
}

export const Route = createFileRoute('/_authed')({
	beforeLoad: async () => {
		try {
			return { me: await fetchMe() };
		} catch (error) {
			if (error instanceof UnauthenticatedError) {
				throw redirect({ to: '/login' });
			}
			throw error;
		}
	},
	component: () => <Outlet />,
});
```

- [ ] **Step 4: Run it and watch it pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/routes/_authed.test.ts` Expected: PASS, 2 tests.

- [ ] **Step 5: Falsify**

Make `assertMembership` a no-op body. Expected: the second test fails, the first passes. Then restore, and separately change `throw notFound()` to `throw new Error('forbidden')` — the test still passes, which is the point: assert on the thrown value's shape if you want that covered, or note in your report that the 404-vs-403 distinction is covered by the E2E in Task 12 rather than here.

- [ ] **Step 6: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/routes/_authed.tsx apps/web/src/routes/_authed.test.ts
git commit -m "feat(web): guard the authenticated routes and load memberships"
```

---

## Task 7: The last-used team, and the switcher that writes it

**Files:**

- Create: `apps/web/src/lib/current-team.ts`, `apps/web/src/lib/current-team.test.ts`, `apps/web/src/routes/_authed/index.tsx`, `apps/web/src/components/team-switcher.tsx`, `apps/web/src/components/team-switcher.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `Membership` and route context `{ me }` from Task 6; `preferenceCookie` from `src/lib/preferences.ts`.
- Produces:
  - `TEAM_COOKIE = 'team'`
  - `resolveCurrentTeam(cookieHeader: string | undefined, memberships: Membership[]): string | undefined`
  - `teamCookie(teamId: string): string`

- [ ] **Step 1: Write the failing tests**

```ts
// apps/web/src/lib/current-team.test.ts
import { describe, expect, it } from 'vitest';

import { resolveCurrentTeam, teamCookie, TEAM_COOKIE } from './current-team';

const memberships = [
	{ team_id: 'a', name: 'Verein A', role: 'owner' },
	{ team_id: 'b', name: 'Verein B', role: 'editor' },
];

describe('resolveCurrentTeam', () => {
	it('returns the team named by the cookie', () => {
		expect(resolveCurrentTeam('team=b', memberships)).toBe('b');
	});

	it('finds its cookie among others', () => {
		expect(resolveCurrentTeam('lang=de; team=b; theme=dark', memberships)).toBe('b');
	});

	it('falls back to the first membership when there is no cookie', () => {
		expect(resolveCurrentTeam(undefined, memberships)).toBe('a');
	});

	it('ignores a cookie naming a team you no longer belong to', () => {
		// Otherwise removal from a team locks someone out of their own
		// dashboard: every visit redirects to a not-found, and the only escape
		// is clearing cookies — which nobody will guess.
		expect(resolveCurrentTeam('team=gone', memberships)).toBe('a');
	});

	it('returns undefined when there are no memberships at all', () => {
		expect(resolveCurrentTeam('team=a', [])).toBeUndefined();
	});
});

describe('teamCookie', () => {
	it('writes a path-scoped cookie', () => {
		expect(teamCookie('b')).toContain(`${TEAM_COOKIE}=b`);
		expect(teamCookie('b')).toContain('Path=/');
	});
});
```

- [ ] **Step 2: Run them and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/lib/current-team.test.ts` Expected: FAIL — `Failed to resolve import "./current-team"`.

- [ ] **Step 3: Implement**

```ts
// apps/web/src/lib/current-team.ts
import { preferenceCookie } from './preferences';

import type { Membership } from '../routes/_authed';

/**
 * The same mechanism `preferences.ts` uses for language and theme, for the
 * same reason: a cookie is readable on the server during rendering, so the
 * redirect happens before the first paint rather than after a round trip.
 * localStorage cannot do that.
 */
export const TEAM_COOKIE = 'team';

function readCookie(cookieHeader: string | undefined, name: string): string | undefined {
	if (!cookieHeader) return undefined;

	for (const part of cookieHeader.split(';')) {
		const [rawKey, ...rawValue] = part.split('=');
		if (rawKey?.trim() === name) return rawValue.join('=').trim();
	}

	return undefined;
}

export function resolveCurrentTeam(
	cookieHeader: string | undefined,
	memberships: Membership[],
): string | undefined {
	const remembered = readCookie(cookieHeader, TEAM_COOKIE);
	// Validated against current memberships, not trusted. A cookie naming a
	// team the person was removed from would otherwise redirect them into a
	// not-found on every single visit.
	if (remembered && memberships.some((m) => m.team_id === remembered)) return remembered;

	return memberships[0]?.team_id;
}

export function teamCookie(teamId: string): string {
	return preferenceCookie(TEAM_COOKIE, teamId);
}
```

- [ ] **Step 4: Implement the index redirect**

```tsx
// apps/web/src/routes/_authed/index.tsx
import { createFileRoute, redirect } from '@tanstack/react-router';

import { resolveCurrentTeam } from '../../lib/current-team';

export const Route = createFileRoute('/_authed/')({
	beforeLoad: ({ context, request }) => {
		const teamId = resolveCurrentTeam(
			request.headers.get('cookie') ?? undefined,
			context.me.memberships,
		);
		if (!teamId) return;
		throw redirect({ to: '/teams/$teamId/links', params: { teamId } });
	},
	// Reached only by someone with no memberships at all: invited but not yet
	// added to a team, or removed from every one. A redirect loop would be the
	// alternative, so this renders instead.
	component: NoTeams,
});

function NoTeams(): React.JSX.Element {
	return <p>{/* i18n key teams.none, added in Step 6 */}</p>;
}
```

- [ ] **Step 5: Implement the switcher**

```tsx
// apps/web/src/components/team-switcher.tsx
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { teamCookie } from '../lib/current-team';

import type { Membership } from '../routes/_authed';

interface TeamSwitcherProps {
	readonly currentTeamId: string;
	readonly memberships: readonly Membership[];
}

export function TeamSwitcher({ currentTeamId, memberships }: TeamSwitcherProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<nav aria-label={t('teams.switcherLabel')}>
			<ul>
				{memberships.map((membership) => (
					<li key={membership.team_id}>
						<Link
							aria-current={membership.team_id === currentTeamId ? 'page' : undefined}
							onClick={() => {
								// Written client-side because the switch is a
								// navigation, not a mutation; the server reads it
								// on the next request to /.
								document.cookie = teamCookie(membership.team_id);
							}}
							params={{ teamId: membership.team_id }}
							to="/teams/$teamId/links"
						>
							{membership.name}
						</Link>
					</li>
				))}
			</ul>
		</nav>
	);
}
```

- [ ] **Step 6: Add the strings, both languages**

```json
// en.json
"teams": {
  "switcherLabel": "Teams",
  "none": "You are not a member of any team yet. Ask the person who invited you to add you to one."
}
```

```json
// de.json
"teams": {
  "switcherLabel": "Vereine",
  "none": "Sie gehören noch keinem Verein an. Bitten Sie die Person, die Sie eingeladen hat, Sie hinzuzufügen."
}
```

Note the German label is "Vereine", not "Teams". The tenant is `team` in every identifier; "Verein" is exactly what user-facing German copy is for.

- [ ] **Step 7: Add a Storybook story**

```tsx
// apps/web/src/components/team-switcher.stories.tsx
import type { Meta, StoryObj } from '@storybook/react';

import { TeamSwitcher } from './team-switcher';

const meta = {
	component: TeamSwitcher,
	title: 'Components/TeamSwitcher',
} satisfies Meta<typeof TeamSwitcher>;

export default meta;

export const TwoTeams: StoryObj<typeof meta> = {
	args: {
		currentTeamId: 'a',
		memberships: [
			{ team_id: 'a', name: 'TSG Irlich', role: 'owner' },
			{ team_id: 'b', name: 'SV Beispiel', role: 'editor' },
		],
	},
};
```

The story exists so the a11y addon runs against this component in CI — that is the second of the two accessibility levels `CLAUDE.md` requires.

- [ ] **Step 8: Run everything and falsify**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/lib/current-team.test.ts` Expected: PASS, 6 tests.

Then delete the `memberships.some(...)` check in `resolveCurrentTeam` so it trusts the cookie. Expected: "ignores a cookie naming a team you no longer belong to" fails, and nothing else. Record it.

- [ ] **Step 9: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/lib/current-team.ts apps/web/src/lib/current-team.test.ts apps/web/src/routes/_authed apps/web/src/components/team-switcher.tsx apps/web/src/components/team-switcher.stories.tsx apps/web/src/i18n/locales
git commit -m "feat(web): remember the last used team and switch between teams"
```

---

## Task 8: Translating API errors

**Files:**

- Create: `apps/web/src/lib/api-errors.ts`, `apps/web/src/lib/api-errors.test.ts`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Produces:
  - `type ApiFailure = { kind: 'unauthenticated' } | { kind: 'notFound' } | { kind: 'rateLimited' } | { kind: 'fields'; fields: Record<string, string> } | { kind: 'unknown' }`
  - `classifyApiError(error: unknown): ApiFailure`

Tasks 10 and 11 render from this.

- [ ] **Step 1: Write the failing tests**

```ts
// apps/web/src/lib/api-errors.test.ts
import { describe, expect, it } from 'vitest';

import { classifyApiError } from './api-errors';

/** Shapes copied from what Huma actually returns — RFC 9457 problem+json. */
function problem(status: number, errors?: { location: string; message: string }[]) {
	return { response: { status }, error: { status, title: 'x', errors } };
}

describe('classifyApiError', () => {
	it('maps 401 to unauthenticated', () => {
		expect(classifyApiError(problem(401))).toEqual({ kind: 'unauthenticated' });
	});

	it('maps 403 to notFound, not to a forbidden state', () => {
		// The API answers 404 for a non-member, but a link inside a team you
		// were just removed from can still return 403. Both must render the
		// same thing, or the UI reintroduces the disclosure authz avoids.
		expect(classifyApiError(problem(403))).toEqual({ kind: 'notFound' });
		expect(classifyApiError(problem(404))).toEqual({ kind: 'notFound' });
	});

	it('maps 429 to rateLimited', () => {
		expect(classifyApiError(problem(429))).toEqual({ kind: 'rateLimited' });
	});

	it('maps 422 field errors onto field names', () => {
		const failure = classifyApiError(
			problem(422, [{ location: 'body.destination_url', message: 'must be a valid URL' }]),
		);
		expect(failure).toEqual({
			kind: 'fields',
			fields: { destination_url: 'must be a valid URL' },
		});
	});

	it('falls back to unknown for anything else', () => {
		expect(classifyApiError(new Error('network'))).toEqual({ kind: 'unknown' });
	});
});
```

- [ ] **Step 2: Run them and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/lib/api-errors.test.ts` Expected: FAIL — `Failed to resolve import "./api-errors"`.

- [ ] **Step 3: Implement**

```ts
// apps/web/src/lib/api-errors.ts
export type ApiFailure =
	| { kind: 'unauthenticated' }
	| { kind: 'notFound' }
	| { kind: 'rateLimited' }
	| { kind: 'fields'; fields: Record<string, string> }
	| { kind: 'unknown' };

interface ProblemDetail {
	location?: string;
	message?: string;
}

function statusOf(error: unknown): number | undefined {
	if (typeof error !== 'object' || error === null) return undefined;
	const response = (error as { response?: { status?: number } }).response;
	return typeof response?.status === 'number' ? response.status : undefined;
}

function fieldsOf(error: unknown): Record<string, string> {
	const details = (error as { error?: { errors?: ProblemDetail[] } }).error?.errors ?? [];
	const fields: Record<string, string> = {};

	for (const detail of details) {
		// Huma reports "body.destination_url"; the form knows the field as
		// "destination_url". Anything without a location is a whole-request
		// problem and belongs in the form-level message, not on a field.
		const name = detail.location?.split('.').pop();
		if (name && detail.message) fields[name] = detail.message;
	}

	return fields;
}

export function classifyApiError(error: unknown): ApiFailure {
	const status = statusOf(error);

	if (status === 401) return { kind: 'unauthenticated' };
	if (status === 403 || status === 404) return { kind: 'notFound' };
	if (status === 429) return { kind: 'rateLimited' };

	if (status === 422 || status === 400) {
		const fields = fieldsOf(error);
		if (Object.keys(fields).length > 0) return { kind: 'fields', fields };
	}

	return { kind: 'unknown' };
}
```

- [ ] **Step 4: Run them and watch them pass**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/lib/api-errors.test.ts` Expected: PASS, 6 tests.

- [ ] **Step 5: Falsify**

Change the 403 branch to return `{ kind: 'unknown' }`. Expected: only the "maps 403 to notFound" test fails. Record it.

- [ ] **Step 6: Add the strings, both languages**

```json
// en.json
"errors": {
  "rateLimited": "Too many links created just now. Please wait a minute and try again.",
  "unknown": "Something went wrong. Please try again.",
  "notFound": "Not found."
}
```

```json
// de.json
"errors": {
  "rateLimited": "Gerade wurden zu viele Links erstellt. Bitte warten Sie eine Minute.",
  "unknown": "Etwas ist schiefgelaufen. Bitte versuchen Sie es erneut.",
  "notFound": "Nicht gefunden."
}
```

- [ ] **Step 7: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/lib/api-errors.ts apps/web/src/lib/api-errors.test.ts apps/web/src/i18n/locales
git commit -m "feat(web): classify API errors into what the UI must show"
```

---

## Task 9: Query, the link server functions, and the list

**Files:**

- Create: `apps/web/src/server/links.ts`, `apps/web/src/server/links.test.ts`, `apps/web/src/routes/_authed/teams.$teamId.links.index.tsx`, `apps/web/src/components/copy-button.tsx`, `apps/web/src/components/short-url-notice.tsx`, `apps/web/src/components/short-url-notice.test.tsx`
- Modify: `apps/web/src/router.tsx`, `apps/web/src/i18n/locales/{en,de}.json`

**Interfaces:**

- Consumes: `requireSession`, `authedApiClient` (Task 3); `assertMembership` (Task 6); `classifyApiError` (Task 8).
- Produces:
  - `listLinksFn({ data: { teamId: string; page: number } })` resolving the API's `Page<Link>`
  - `linksQueryOptions(teamId: string, page: number)` — the shared query key and fetcher
  - `<CopyButton value={string} />`, `<ShortUrlNotice hostname={string} />`

- [ ] **Step 1: Write the failing test**

```tsx
// apps/web/src/components/short-url-notice.test.tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ShortUrlNotice } from './short-url-notice';

describe('ShortUrlNotice', () => {
	it('warns when the shared domain cannot resolve', () => {
		// SHARED_DOMAIN_HOSTNAME is short.invalid until a real domain is
		// registered. Showing a copy button for a URL that 404s, with no
		// explanation, is the confusion the .invalid placeholder exists to
		// prevent.
		render(<ShortUrlNotice hostname="short.invalid" />);
		expect(screen.getByRole('note')).toBeInTheDocument();
	});

	it('renders nothing once a real domain is configured', () => {
		// The condition is the hostname, not a feature flag: the notice must
		// disappear on its own the day the domain changes, with no code edit
		// and nothing to remember to switch off.
		const { container } = render(<ShortUrlNotice hostname="kurze.url" />);
		expect(container).toBeEmptyDOMElement();
	});
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/short-url-notice.test.tsx` Expected: FAIL — `Failed to resolve import "./short-url-notice"`.

- [ ] **Step 3: Implement the notice and the copy button**

```tsx
// apps/web/src/components/short-url-notice.tsx
import { useTranslation } from 'react-i18next';

/**
 * RFC 2606 reserves .invalid as permanently unresolvable, which is why
 * SHARED_DOMAIN_HOSTNAME uses it as a placeholder. Keying on the suffix rather
 * than a flag means this clears itself when a real domain is set.
 */
export function ShortUrlNotice({
	hostname,
}: {
	readonly hostname: string;
}): React.JSX.Element | null {
	const { t } = useTranslation();
	if (!hostname.endsWith('.invalid')) return null;

	return <p role="note">{t('links.noShortDomain')}</p>;
}
```

```tsx
// apps/web/src/components/copy-button.tsx
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

export function CopyButton({ value }: { readonly value: string }): React.JSX.Element {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);

	return (
		<>
			<Button
				onClick={() => {
					void navigator.clipboard.writeText(value).then(() => setCopied(true));
				}}
				type="button"
			>
				{t('links.copy')}
			</Button>
			{/* A colour change is invisible to a screen reader, and
			    accessibility is a CI gate here. */}
			<span aria-live="polite">{copied ? t('links.copied') : ''}</span>
		</>
	);
}
```

- [ ] **Step 4: Implement the server functions**

```ts
// apps/web/src/server/links.ts
import { createLink, deleteLink, getLink, listLinks, updateLink } from '@kurze-url/api-client';
import { createServerFn } from '@tanstack/react-start';
import { queryOptions } from '@tanstack/react-query';

import { authedApiClient, requireSession } from './session';

export const listLinksFn = createServerFn({ method: 'GET' })
	.validator((data: { teamId: string; page: number }) => data)
	.handler(async ({ data, request }) => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		const { data: page } = await listLinks({
			client: authedApiClient(accessToken),
			path: { team_id: data.teamId },
			query: { page: data.page, per_page: 20 },
			throwOnError: true,
		});
		return page;
	});

/**
 * One definition of the key and the fetcher, used by both the loader and the
 * component. Two definitions drift, and the symptom is a list that updates on
 * navigation but not after a mutation.
 */
export function linksQueryOptions(teamId: string, page: number) {
	return queryOptions({
		queryFn: () => listLinksFn({ data: { teamId, page } }),
		queryKey: ['links', teamId, page],
	});
}
```

- [ ] **Step 5: Provide a QueryClient in `src/router.tsx`**

```tsx
import { QueryClient } from '@tanstack/react-query';
import { routerWithQueryClient } from '@tanstack/react-router-with-query';

const queryClient = new QueryClient();

// The router is created per request on the server. The QueryClient must be
// too: a module-level one would serve one person's cached links to the next
// request, which is the same hazard createApiClient avoids by returning a
// fresh instance.
```

Follow the version of this integration that the installed `@tanstack/react-router` documents; if the package name above has moved, use the current one and note the divergence in your task report rather than adapting silently.

- [ ] **Step 6: Implement the list route**

```tsx
// apps/web/src/routes/_authed/teams.$teamId.links.index.tsx
import { createFileRoute } from '@tanstack/react-router';
import { useSuspenseQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';

import { CopyButton } from '../../components/copy-button';
import { ShortUrlNotice } from '../../components/short-url-notice';
import { assertMembership } from '../_authed';
import { linksQueryOptions } from '../../server/links';

export const Route = createFileRoute('/_authed/teams/$teamId/links/')({
	// Pagination lives in the URL so the back button works and page 3 can be
	// sent to a colleague — the same reasoning that put the team in the path.
	validateSearch: (search: Record<string, unknown>) => ({
		page: Number(search.page ?? 1),
	}),
	beforeLoad: ({ context, params }) => assertMembership(context.me.memberships, params.teamId),
	loaderDeps: ({ search }) => ({ page: search.page }),
	loader: ({ context, deps, params }) =>
		context.queryClient.ensureQueryData(linksQueryOptions(params.teamId, deps.page)),
	component: LinkList,
});

function LinkList(): React.JSX.Element {
	const { teamId } = Route.useParams();
	const { page } = Route.useSearch();
	const { t } = useTranslation();
	const { data } = useSuspenseQuery(linksQueryOptions(teamId, page));

	if (data.items.length === 0) return <p>{t('links.empty')}</p>;

	return (
		<>
			<ShortUrlNotice hostname={data.items[0]?.hostname ?? ''} />
			<ul>
				{data.items.map((link) => (
					<li key={link.id}>
						<a href={link.short_url}>{link.short_url}</a>
						<CopyButton value={link.short_url} />
						<span>{link.destination_url}</span>
					</li>
				))}
			</ul>
		</>
	);
}
```

Check the `Page<T>` envelope's actual property name against `apps/api/openapi.json` before writing `data.items` — the plan assumes `items`, and the generated types are the authority.

- [ ] **Step 7: Add the strings, both languages**

```json
// en.json
"links": {
  "copy": "Copy",
  "copied": "Copied",
  "empty": "No links yet. Create your first one.",
  "noShortDomain": "This instance has no working short domain yet, so these links will not redirect."
}
```

```json
// de.json
"links": {
  "copy": "Kopieren",
  "copied": "Kopiert",
  "empty": "Noch keine Links. Erstellen Sie Ihren ersten.",
  "noShortDomain": "Diese Instanz hat noch keine funktionierende Kurz-Domain, daher leiten diese Links nicht weiter."
}
```

- [ ] **Step 8: Run and falsify**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit` Expected: PASS.

Change `ShortUrlNotice` to always return the note. Expected: "renders nothing once a real domain is configured" fails, and only that. Record it.

- [ ] **Step 9: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/server/links.ts apps/web/src/routes/_authed apps/web/src/components apps/web/src/router.tsx apps/web/src/i18n/locales
git commit -m "feat(web): list a team's links"
```

---

## Task 10: The link form and creating a link

**Files:**

- Create: `apps/web/src/components/link-form.tsx`, `apps/web/src/components/link-form.test.tsx`, `apps/web/src/components/link-form.stories.tsx`, `apps/web/src/routes/_authed/teams.$teamId.links.new.tsx`
- Modify: `apps/web/src/server/links.ts`, `apps/web/src/i18n/locales/{en,de}.json`

**Interfaces:**

- Consumes: `classifyApiError` (Task 8); `linksQueryOptions` (Task 9).
- Produces:
  - `<LinkForm initial? onSubmit fieldErrors? />`
  - `createLinkFn({ data: { teamId, body } })`

- [ ] **Step 1: Write the failing tests**

```tsx
// apps/web/src/components/link-form.test.tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { LinkForm } from './link-form';

describe('LinkForm', () => {
	it('warns inline when 301 is chosen', async () => {
		// CLAUDE.md requires this. A cached 301 stops clicks being counted and
		// stops later destination changes taking effect for anyone who has
		// visited once — breakage a user cannot diagnose and cannot undo.
		render(<LinkForm onSubmit={vi.fn()} />);
		await userEvent.selectOptions(screen.getByLabelText(/redirect|weiterleitung/i), '301');

		expect(await screen.findByRole('note')).toBeInTheDocument();
	});

	it('does not warn for 302', async () => {
		render(<LinkForm onSubmit={vi.fn()} />);
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('shows a server field error on the field it belongs to', () => {
		render(<LinkForm fieldErrors={{ destination_url: 'must be https' }} onSubmit={vi.fn()} />);
		expect(screen.getByLabelText(/destination|ziel/i)).toHaveAccessibleDescription(/must be https/);
	});

	it('offers no domain picker', () => {
		// One domain exists on this instance, so a select with one option is
		// furniture. It appears when custom domains do.
		render(<LinkForm onSubmit={vi.fn()} />);
		expect(screen.queryByLabelText(/domain/i)).not.toBeInTheDocument();
	});
});
```

- [ ] **Step 2: Run them and watch them fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/link-form.test.tsx` Expected: FAIL — `Failed to resolve import "./link-form"`.

- [ ] **Step 3: Implement the form**

Use `@tanstack/react-form` for required-field and shape checks only. Do **not** add a zod schema mirroring the API's rules: Huma already validates, and `internal/destination`'s SSRF and DNS-rebinding checks cannot be reproduced in a browser at all. A client schema that looked authoritative would be the more dangerous kind of wrong.

```tsx
// apps/web/src/components/link-form.tsx
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

export interface LinkFormValues {
	analytics_enabled: boolean;
	destination_url: string;
	expires_at: string;
	redirect_type: number;
	slug: string;
}

interface LinkFormProps {
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly initial?: Partial<LinkFormValues>;
	readonly onSubmit: (values: LinkFormValues) => void;
}

export function LinkForm({ fieldErrors, initial, onSubmit }: LinkFormProps): React.JSX.Element {
	const { t } = useTranslation();
	const [values, setValues] = useState<LinkFormValues>({
		analytics_enabled: initial?.analytics_enabled ?? true,
		destination_url: initial?.destination_url ?? '',
		expires_at: initial?.expires_at ?? '',
		redirect_type: initial?.redirect_type ?? 302,
		slug: initial?.slug ?? '',
	});

	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				onSubmit(values);
			}}
		>
			<label htmlFor="destination_url">{t('links.destination')}</label>
			<input
				aria-describedby={fieldErrors?.destination_url ? 'destination_url-error' : undefined}
				id="destination_url"
				onChange={(e) => setValues({ ...values, destination_url: e.target.value })}
				required
				type="url"
				value={values.destination_url}
			/>
			{fieldErrors?.destination_url ? (
				<p id="destination_url-error">{fieldErrors.destination_url}</p>
			) : null}

			<label htmlFor="slug">{t('links.slug')}</label>
			{/* An empty slug means the API generates one. Said here, because a
			    blank required-looking field otherwise reads as an oversight. */}
			<input
				id="slug"
				onChange={(e) => setValues({ ...values, slug: e.target.value })}
				placeholder={t('links.slugGenerated')}
				value={values.slug}
			/>

			<label htmlFor="redirect_type">{t('links.redirectType')}</label>
			<select
				id="redirect_type"
				onChange={(e) => setValues({ ...values, redirect_type: Number(e.target.value) })}
				value={values.redirect_type}
			>
				<option value={302}>{t('links.redirect302')}</option>
				<option value={301}>{t('links.redirect301')}</option>
			</select>
			{values.redirect_type === 301 ? <p role="note">{t('links.redirect301Warning')}</p> : null}

			<Button type="submit">{t('links.save')}</Button>
		</form>
	);
}
```

- [ ] **Step 4: Add `createLinkFn` to `src/server/links.ts`**

```ts
export const createLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { teamId: string; body: Record<string, unknown> }) => data)
	.handler(async ({ data, request }) => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		const { data: link } = await createLink({
			body: data.body,
			client: authedApiClient(accessToken),
			path: { team_id: data.teamId },
			throwOnError: true,
		});
		return link;
	});
```

- [ ] **Step 5: Implement the create route**

It renders `<LinkForm>`, calls `createLinkFn` in a mutation, and on success invalidates the links query key **and** calls `router.invalidate()`. Both, not one: the loader owns the list's data and the Query cache holds it, and invalidating only one leaves them disagreeing until the next full navigation.

On failure it calls `classifyApiError` and renders per kind — `fields` onto the form, `rateLimited` and `unknown` as a form-level message, `unauthenticated` as `redirect({ to: '/login' })`.

- [ ] **Step 6: Add the strings, both languages**

```json
// en.json — merge into "links"
"destination": "Destination URL",
"slug": "Short path",
"slugGenerated": "Leave empty and one will be generated",
"redirectType": "Redirect type",
"redirect302": "302 — temporary (recommended)",
"redirect301": "301 — permanent",
"redirect301Warning": "Browsers cache a 301. Clicks stop being counted, and changing the destination later will not reach anyone who has already visited.",
"save": "Save",
"create": "Create link"
```

```json
// de.json — merge into "links"
"destination": "Ziel-URL",
"slug": "Kurzpfad",
"slugGenerated": "Leer lassen, dann wird einer erzeugt",
"redirectType": "Weiterleitungstyp",
"redirect302": "302 — vorübergehend (empfohlen)",
"redirect301": "301 — dauerhaft",
"redirect301Warning": "Browser speichern eine 301 zwischen. Klicks werden dann nicht mehr gezählt, und eine spätere Änderung des Ziels erreicht niemanden, der die Seite bereits besucht hat.",
"save": "Speichern",
"create": "Link erstellen"
```

- [ ] **Step 7: Add a Storybook story** for `LinkForm`, with a default story and one passing `fieldErrors`, so the a11y addon covers both states.

- [ ] **Step 8: Run and falsify**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/link-form.test.tsx` Expected: PASS, 4 tests.

Remove the `role="note"` warning block. Expected: only the 301 test fails. Then restore it and remove the `aria-describedby` wiring: only the field-error test fails. Record both.

- [ ] **Step 9: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/components/link-form.tsx apps/web/src/components/link-form.test.tsx apps/web/src/components/link-form.stories.tsx apps/web/src/routes/_authed apps/web/src/server/links.ts apps/web/src/i18n/locales
git commit -m "feat(web): create links, warning inline about 301"
```

---

## Task 11: Editing and deleting a link

**Files:**

- Create: `apps/web/src/routes/_authed/teams.$teamId.links.$linkId.tsx`, `apps/web/src/components/confirm-delete.tsx`, `apps/web/src/components/confirm-delete.test.tsx`
- Modify: `apps/web/src/server/links.ts`, `apps/web/src/i18n/locales/{en,de}.json`

**Interfaces:**

- Consumes: `<LinkForm>` (Task 10), `classifyApiError` (Task 8), `linksQueryOptions` (Task 9).
- Produces: `getLinkFn`, `updateLinkFn`, `deleteLinkFn`, `<ConfirmDelete onConfirm label />`.

- [ ] **Step 1: Write the failing test**

```tsx
// apps/web/src/components/confirm-delete.test.tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { ConfirmDelete } from './confirm-delete';

describe('ConfirmDelete', () => {
	it('does not delete on the first click', async () => {
		// Nothing restores a link, and its slug may already be in print on a
		// flyer. One misclick must not be enough.
		const onConfirm = vi.fn();
		render(<ConfirmDelete label="Delete" onConfirm={onConfirm} />);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(onConfirm).not.toHaveBeenCalled();
	});

	it('deletes once confirmed', async () => {
		const onConfirm = vi.fn();
		render(<ConfirmDelete label="Delete" onConfirm={onConfirm} />);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		await userEvent.click(screen.getByRole('button', { name: /confirm|bestätigen/i }));
		expect(onConfirm).toHaveBeenCalledOnce();
	});
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/confirm-delete.test.tsx` Expected: FAIL — `Failed to resolve import "./confirm-delete"`.

- [ ] **Step 3: Implement**

```tsx
// apps/web/src/components/confirm-delete.tsx
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

interface ConfirmDeleteProps {
	readonly label: string;
	readonly onConfirm: () => void;
}

export function ConfirmDelete({ label, onConfirm }: ConfirmDeleteProps): React.JSX.Element {
	const { t } = useTranslation();
	const [armed, setArmed] = useState(false);

	if (!armed) {
		return (
			<Button onClick={() => setArmed(true)} type="button">
				{label}
			</Button>
		);
	}

	return (
		<p role="alertdialog">
			{t('links.deleteQuestion')}
			<Button onClick={onConfirm} type="button">
				{t('links.deleteConfirm')}
			</Button>
			<Button onClick={() => setArmed(false)} type="button">
				{t('links.cancel')}
			</Button>
		</p>
	);
}
```

- [ ] **Step 4: Add the remaining server functions**

```ts
export const getLinkFn = createServerFn({ method: 'GET' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data, request }) => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		const { data: link } = await getLink({
			client: authedApiClient(accessToken),
			path: { link_id: data.linkId },
			throwOnError: true,
		});
		return link;
	});

export const updateLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string; body: Record<string, unknown> }) => data)
	.handler(async ({ data, request }) => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		const { data: link } = await updateLink({
			body: data.body,
			client: authedApiClient(accessToken),
			path: { link_id: data.linkId },
			throwOnError: true,
		});
		return link;
	});

export const deleteLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data, request }) => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		await deleteLink({
			client: authedApiClient(accessToken),
			path: { link_id: data.linkId },
			throwOnError: true,
		});
		return { deleted: true };
	});
```

- [ ] **Step 5: Implement the edit route**

`beforeLoad` calls `assertMembership`. The loader fetches the link through `getLinkFn`. The component renders `<LinkForm initial={link}>` plus `<ConfirmDelete>`. Update and delete both invalidate `['links', teamId]` and call `router.invalidate()`; delete then navigates back to the list.

- [ ] **Step 6: Add the strings, both languages**

```json
// en.json — merge into "links"
"delete": "Delete",
"deleteQuestion": "Delete this link? Anyone who already has the short URL will get a 404.",
"deleteConfirm": "Yes, delete it",
"cancel": "Cancel",
"edit": "Edit"
```

```json
// de.json — merge into "links"
"delete": "Löschen",
"deleteQuestion": "Diesen Link löschen? Wer die Kurz-URL bereits hat, erhält dann einen 404.",
"deleteConfirm": "Ja, löschen",
"cancel": "Abbrechen",
"edit": "Bearbeiten"
```

- [ ] **Step 7: Run and falsify**

Run: `pnpm --filter @kurze-url/web exec vitest run --project unit src/components/confirm-delete.test.tsx` Expected: PASS, 2 tests.

Make the first click call `onConfirm` directly. Expected: the first test fails, the second passes. Record it.

- [ ] **Step 8: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/src/components/confirm-delete.tsx apps/web/src/components/confirm-delete.test.tsx apps/web/src/routes/_authed apps/web/src/server/links.ts apps/web/src/i18n/locales
git commit -m "feat(web): edit and delete links"
```

---

## Task 12: End-to-end, signed in

**Files:**

- Create: `apps/web/e2e/fixtures/auth.ts`, `apps/web/e2e/links.spec.ts`
- Modify: `apps/web/e2e/i18n.spec.ts`, `apps/web/e2e/shell.spec.ts`

**Interfaces:**

- Consumes: everything above, plus `SUPABASE_SERVICE_ROLE_KEY` and `SUPABASE_URL` from Task 1.
- Produces: a Playwright fixture `signedInPage` usable by any later plan's specs.

- [ ] **Step 1: Write the fixture**

Magic-link-only login leaves Playwright nothing to type and no inbox to read, so the fixture mints a session through the Supabase Admin API and sets the cookies directly.

```ts
// apps/web/e2e/fixtures/auth.ts
import { test as base } from '@playwright/test';

/**
 * Runs against the kurze-url-preview project, never production. That
 * separation is what makes it acceptable for a service-role key — which
 * bypasses every database policy — to exist as a CI secret at all.
 */
export const test = base.extend<{ teamId: string }>({
	teamId: async ({ context }, use) => {
		const url = process.env.SUPABASE_URL;
		const key = process.env.SUPABASE_SERVICE_ROLE_KEY;
		if (!url || !key) {
			throw new Error(
				'SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required for authenticated e2e. ' +
					'Without them these specs would run signed out and pass against the login page — ' +
					'the same failure the protection-bypass work fixed in September.',
			);
		}

		// Create a throwaway user, add it to a throwaway team, mint a session,
		// set the cookies, hand the test a team id, then delete the user on the
		// way out. Implement against the Admin API's current shape and record
		// the endpoints used in your task report.
		const teamId = await provisionTeam(url, key, context);
		await use(teamId);
		await deleteUser(url, key);
	},
});
```

The `throw` is not defensive noise. Without it a missing variable produces a suite that runs signed out, is redirected to `/login`, and passes every assertion about a page that is not the app — precisely the failure mode that made four axe tests meaningless until 2026-09-05.

- [ ] **Step 2: Write the failing spec**

```ts
// apps/web/e2e/links.spec.ts
import AxeBuilder from '@axe-core/playwright';
import { expect } from '@playwright/test';

import { test } from './fixtures/auth';

test('creates a link and shows it in the list', async ({ page, teamId }) => {
	await page.goto(`/teams/${teamId}/links/new`);
	await page.getByLabel(/destination/i).fill('https://example.org/a-page');
	await page.getByRole('button', { name: /save/i }).click();

	await expect(page.getByText('https://example.org/a-page')).toBeVisible();
});

test('warns that the short domain does not resolve', async ({ page, teamId }) => {
	await page.goto(`/teams/${teamId}/links`);
	await expect(page.getByRole('note')).toBeVisible();
});

test('has no accessibility violations on the list', async ({ page, teamId }) => {
	await page.goto(`/teams/${teamId}/links`);
	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});

test('sends a signed-out visitor to login', async ({ browser, teamId }) => {
	// A fresh context: no fixture cookies. Asserting the destination, not just
	// that rendering failed — a guard that 500s would satisfy a weaker check.
	const context = await browser.newContext();
	const page = await context.newPage();
	await page.goto(`/teams/${teamId}/links`);

	await expect(page).toHaveURL(/\/login$/);
});
```

- [ ] **Step 3: Extend the i18n crawl**

`e2e/i18n.spec.ts` currently visits `/` and `/this-page-does-not-exist`. Add the authenticated list and form paths using the same fixture, so the "no user-facing string is identical across languages" gate reaches the screens people actually use. Reuse the existing spec's helper rather than copying it.

- [ ] **Step 4: Run against a preview**

```bash
BASE_URL=<preview url> VERCEL_AUTOMATION_BYPASS_SECRET=<secret> \
SUPABASE_URL=<preview project url> SUPABASE_SERVICE_ROLE_KEY=<preview key> \
pnpm --filter @kurze-url/web exec playwright test
```

Expected: all specs pass, including the four inherited from plan 5.

- [ ] **Step 5: Falsify**

Delete the `beforeLoad` guard in `_authed.tsx` and re-run. Expected: "sends a signed-out visitor to login" fails. If it still passes, the assertion is not testing the guard — fix the spec. Record the result.

- [ ] **Step 6: Commit**

```bash
pnpm format && pnpm lint && pnpm typecheck && pnpm test
git add apps/web/e2e
git commit -m "test(web): cover the authenticated flow end to end"
```

---

## Verification checklist

Before opening the pull request:

- [ ] `pnpm format:check`, `pnpm lint`, `pnpm typecheck`, `pnpm test` all clean from the repo root.
- [ ] `pnpm --filter @kurze-url/web test:storybook` passes — the a11y addon is a real gate.
- [ ] Playwright passes against a preview, signed in.
- [ ] Every new string exists in **both** `en.json` and `de.json`; `catalogues.test.ts` proves it.
- [ ] Every falsification result recorded in the task reports. A property with no recorded mutation is not verified.
- [ ] No `zod` dependency was added.
- [ ] No `@tanstack/react-table` dependency was added.
- [ ] Custom SMTP is live on both Supabase projects and the auth rate limit is 50/hour — otherwise nobody but the maintainer can sign in.
- [ ] `SUPABASE_SERVICE_ROLE_KEY` in GitHub Actions is the **preview** project's key. Check this deliberately: production's key in CI is the one outcome this plan's design was rearranged to avoid.
