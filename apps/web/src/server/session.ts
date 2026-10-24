import { createServerOnlyFn } from '@tanstack/react-start';
import { appendResponseHeader, setResponseHeader } from '@tanstack/react-start/server';

import { getApiClient } from './api';
import { createSupabase } from './supabase';

/**
 * The three headers `@supabase/ssr` passes `setAll` every time it writes
 * session cookies (`applyServerStorage` in its `dist/main/cookies.js`): a
 * created or refreshed session, sign-out's cleared cookies, and the PKCE
 * verifier cookie `signInWithOtp` sets. Their values are `Cache-Control:
 * private, no-cache, no-store, must-revalidate, max-age=0`, `Expires: 0` and
 * `Pragma: no-cache`. Named here as `Headers` keys, so lowercase and
 * case-insensitive, and exactly this set: the cookie adapter copies into
 * `headers` whatever the library hands it, but what leaves for the response is
 * decided in `flushSessionCookies`.
 */
const SESSION_CACHE_HEADERS = ['cache-control', 'expires', 'pragma'] as const;

/**
 * Distinguishable from an API 401 on purpose. This one means "no session on
 * this request" and the answer is a redirect to /login; an API 401 means the
 * token was rejected, and conflating them turns a normal signed-out visit into
 * a round trip to the Go service.
 */
export class UnauthenticatedError extends Error {
	public constructor() {
		super('no session');
		this.name = 'UnauthenticatedError';
	}
}

/**
 * Reading the session is also what refreshes it: @supabase/ssr renews an
 * expired one and writes the new cookies through the adapter's setAll. That is
 * why `headers` is threaded all the way down here rather than only used at
 * sign-in.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param headers - Written into if the session is refreshed; the caller must flush it.
 * @returns The current access token, or undefined when there is no session.
 */
export async function getAccessToken(
	/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Request` nests a
	 * mutable `Headers` through its own `.headers` getter, and `Readonly<>` is shallow: it does
	 * not reach that nested property, so `Readonly<Request>` still fails this check (verified —
	 * unlike a bare `Headers` parameter below, which the check does accept once wrapped).
	 */
	request: Request,
	headers: Readonly<Headers>,
): Promise<string | undefined> {
	const supabase = createSupabase(request, headers);
	const { data } = await supabase.auth.getSession();
	return data.session?.access_token;
}

/**
 * Fails closed: an empty or missing token throws rather than falling through
 * to an "authenticated" request the API will 401 anyway. That 401 would be the
 * same symptom, three layers further from the cause.
 *
 * @param request - The incoming request, forwarded to `getAccessToken`.
 * @param headers - Forwarded to `getAccessToken`; the caller must flush it.
 * @returns The current session's access token.
 */
export async function requireSession(
	/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same cause as
	 * `getAccessToken`'s `request` parameter above.
	 */
	request: Request,
	headers: Readonly<Headers>,
): Promise<{ accessToken: string }> {
	const accessToken = await getAccessToken(request, headers);
	if (accessToken === undefined || accessToken === '') throw new UnauthenticatedError();
	return { accessToken };
}

/**
 * Turns "no session" into `null` before the answer leaves the server.
 *
 * A server function's thrown error does not survive the trip to the browser
 * with its identity. On every client-side navigation the server function is a
 * real HTTP round trip, and TanStack serialises a thrown error with its
 * `ShallowErrorPlugin` (`@tanstack/router-core`'s
 * `ssr/serializer/ShallowErrorPlugin.js`), which keeps the message and nothing
 * else: an `UnauthenticatedError` arrives as `new Error('no session')`, its
 * name and class gone. Measured on 2026-10-24 with the installed
 * `router-core@1.171.38` and `seroval@1.6.12`: through the router's default
 * plugins the name comes back as `Error`; through seroval alone it would have
 * survived. A check by class or by name in a route's `beforeLoad` or `loader`
 * therefore only ever worked on the first, server-rendered pass, and signing
 * out of the web app, which navigates to `/` client-side, rendered the error
 * page.
 *
 * So the question is answered here, inside the server function's handler,
 * where `instanceof` is still reliable, and only a value crosses the
 * boundary. Every other failure is rethrown unchanged.
 *
 * @param read - The read that may find no session, e.g. one that calls `requireSession`.
 * @returns What `read` returned, or `null` when it threw `UnauthenticatedError`.
 */
export async function nullWithoutSession<T>(read: () => Promise<T>): Promise<T | null> {
	try {
		return await read();
	} catch (error) {
		if (error instanceof UnauthenticatedError) return null;
		throw error;
	}
}

/**
 * The one place callers turn a known-good token into an API client. Every
 * authenticated server function calls `requireSession` then this.
 *
 * @param accessToken - A token already known to be valid, e.g. from `requireSession`.
 * @returns An API client authenticated with `accessToken`.
 */
export function authedApiClient(accessToken: string): ReturnType<typeof getApiClient> {
	return getApiClient(undefined, () => accessToken);
}

/**
 * `createSupabase(request, headers)` writes cookies into the `Headers`
 * object the caller passed in — never into the actual HTTP response.
 * Nothing carries that on its own: a caller that builds a throwaway
 * `Headers`, hands it to `createSupabase`, and never does anything else
 * with it has silently thrown away every cookie Supabase wrote. That
 * includes a brand new session (the `/auth/callback` exchange, sign-out's
 * cleared cookies) and, just as easily missed, a *refreshed* one — reading
 * a session via `getAccessToken`/`requireSession` is itself what renews an
 * expiring one, so this bites on a long-lived tab under normal use, not
 * only at sign-in. **If a caller forgets this call, the symptom is a login
 * or refresh that appears to succeed and then silently reverts to
 * signed-out** — there is no error, no failed request, just a cookie the
 * browser never received.
 *
 * Call it once per request, after the last `createSupabase`-backed call
 * whose cookies matter (typically right after `getAccessToken` /
 * `requireSession`, or after `supabase.auth.exchangeCodeForSession` /
 * `supabase.auth.signOut`), passing the same `Headers` instance that was
 * threaded into `createSupabase`:
 *
 * ```ts
 * const headers = new Headers();
 * const { accessToken } = await requireSession(request, headers);
 * flushSessionCookies(headers);
 * ```
 *
 * The cache headers travel with the cookies, and that half is as load-bearing
 * as the cookies themselves. `@supabase/ssr` sends `Cache-Control`, `Expires`
 * and `Pragma` (`SESSION_CACHE_HEADERS`) every time it writes session cookies,
 * a new or refreshed session, sign-out's cleared cookies and the PKCE verifier
 * included, so that a CDN or reverse proxy never caches a response carrying
 * one person's session cookie and serves it to someone else. The cookie
 * adapter faithfully lands them in the local `Headers`, so a flush that
 * forwarded `set-cookie` alone dropped them there, with no error and no
 * visible difference: the cookie arrived, and the response in front of it was
 * left cacheable. **A response that carries a fresh session cookie must never
 * be cacheable**, which is what forwarding them restores.
 *
 * They go out only when a cookie goes out, mirroring `@supabase/ssr`, which
 * sends them only together with cookies (`applyServerStorage` returns before
 * `setAll` when it has none to write, so the library never produces the
 * other combination). A `Headers` holding cache headers and no cookie is
 * therefore not a state the library reaches, and forwarding it would overwrite
 * a `Cache-Control` the response already had, on a response that has no
 * session cookie to protect. Each of the three is written only if it is
 * present, because a `setAll` can carry cookies without them:
 * `clearAuthCookiesAtScopes` passes `{}`, and so does a hand-built `Headers`
 * in a test. `hasSentHeaders` (in `createStorageFromOptions`) is not the cause,
 * since a second `setAll` on the same client brings none but the local
 * `Headers` keeps the first call's values.
 *
 * Implementation note on *why* `appendResponseHeader` for `set-cookie` rather
 * than `setResponseHeader`/`setResponseHeaders`: `set-cookie` is the one
 * header where multiple values are legitimate, not a bug, and both of those
 * replace. `setResponseHeaders` (plural), given a `Headers`, gathers every
 * `set-cookie` value it holds and hands them all to `setResponseHeader`, so
 * both helpers end in the singular one. That keeps every cookie it was given,
 * but it first deletes any `set-cookie` the response already carries and marks
 * the header as replaced rather than merged — safe the first time this is
 * called in a request, but a second call (a route that reads the session more
 * than once) would wipe out the first call's cookies rather than add to them.
 * `appendResponseHeader` merges into what is already there, so this stays
 * correct no matter how many times, or where, it is called within one request.
 *
 * The cache headers are the opposite case, so they use `setResponseHeader`.
 * For anything but `set-cookie` it is `Headers.set` on the response's pending
 * state, and the value replaces whatever the response held: each of the three
 * carries a single value, so replacing is the intent. `appendResponseHeader`
 * would join them onto an existing `Cache-Control` as `<earlier>, private,
 * no-cache, ...` (both are read from `@tanstack/start-server-core` 1.169.43's
 * `internal-request-response.js`), and calling it twice would repeat the
 * value. Replacing also makes a second flush in the same request idempotent
 * for these three, where it is additive for cookies. Do not "tidy" the two
 * calls into one helper: `setResponseHeader('set-cookie', …)` is the
 * replace-every-cookie bug above, and `appendResponseHeader` for the cache
 * headers is the duplicated-value one.
 *
 * "Merges" is not "concatenates": values that share a name, domain and path
 * collapse to the last one written, which is what a browser would end up with
 * after applying them in order. Every distinct cookie is kept, so a refreshed
 * auth-token cookie (`sb-<project-ref>-auth-token`, sometimes chunked as `.0`,
 * `.1`) replaces the stale copy an earlier flush put on the response instead of
 * both reaching the browser, and no cookie another flush wrote is lost.
 * (Before `@tanstack/start-server-core` 1.169.41 took away the accessor for the
 * per-request response, this function appended raw values to that response's
 * own `Headers`, so duplicates were sent; the browser's end state was the same.)
 *
 * Wrapped in `createServerOnlyFn`, the same fix `sendMagicLinkFor` already
 * needed in `server/auth.ts`, for a related but distinct reason: when this
 * was written, this module's `isUnauthenticatedError` (replaced on 2026-10-24
 * by `nullWithoutSession`, which only server-function handlers call) was
 * referenced directly from `routes/_authed.tsx`'s `beforeLoad` and
 * `routes/index.tsx`'s loader — code that runs on the client too, unlike a
 * `createServerFn().handler()` body, so it is never stripped from the client
 * bundle. The wrapper stays so that the next such import cannot break the
 * build the same way. That live,
 * client-reachable reference to this module is what makes Rolldown resolve
 * this whole file for the client build, including this function's own
 * top-level `appendResponseHeader` and `setResponseHeader` import — even
 * though `flushSessionCookies` itself is only ever called from inside
 * `.handler()` closures. Confirmed by building both ways when this file
 * imported `appendResponseHeader` alone: unwrapped, `pnpm --filter
 * @kurze-url/web build` fails with the same `[import-protection]` error
 * `sendMagicLinkFor`'s docstring describes ("Import denied in client
 * environment" for `@tanstack/react-start/server`), reported against the
 * `appendResponseHeader` call below; wrapped, it passes. After
 * `setResponseHeader` joined the import only the wrapped build was re-run, and
 * it passes. The unwrapped failure was not repeated for it: import protection
 * is keyed on the module path, `@tanstack/react-start/server`, which did not
 * change.
 */
export const flushSessionCookies = createServerOnlyFn((headers: Readonly<Headers>): void => {
	const cookies = headers.getSetCookie();
	if (cookies.length === 0) return;
	appendResponseHeader('set-cookie', cookies);
	for (const name of SESSION_CACHE_HEADERS) {
		const value = headers.get(name);
		if (value !== null) setResponseHeader(name, value);
	}
});
