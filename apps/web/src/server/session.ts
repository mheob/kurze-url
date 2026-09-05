import { getResponse } from '@tanstack/react-start/server';

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

/**
 * Fails closed: an empty or missing token throws rather than falling through
 * to an "authenticated" request the API will 401 anyway. That 401 would be the
 * same symptom, three layers further from the cause.
 */
export async function requireSession(
	request: Request,
	headers: Headers,
): Promise<{ accessToken: string }> {
	const accessToken = await getAccessToken(request, headers);
	if (!accessToken) throw new UnauthenticatedError();
	return { accessToken };
}

/**
 * The one place callers turn a known-good token into an API client. Every
 * authenticated server function calls `requireSession` then this.
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
 * Implementation note on *why* `getResponse().headers.append` rather than
 * `@tanstack/start-server-core`'s `setResponseHeader`/`setResponseHeaders`:
 * there is no exported `appendResponseHeader`, and `set-cookie` is the one
 * header where multiple values are legitimate, not a bug. `setResponseHeaders`
 * (plural) calls the underlying `Headers.set`, which would silently discard
 * every cookie but the last if more than one is pending. `setResponseHeader`
 * (singular) does support multiple values, but only by first deleting any
 * `set-cookie` the response already carries — safe the first time this is
 * called in a request, but a second call (a route that reads the session
 * more than once) would wipe out the first call's cookies rather than add to
 * them. `getResponse()` returns the same per-request response object those
 * helpers mutate, and its `.headers` is a plain `Headers` — `.append` adds
 * without touching what is already there, so this stays correct no matter
 * how many times, or where, it is called within one request.
 */
export function flushSessionCookies(headers: Headers): void {
	const response = getResponse();
	for (const cookie of headers.getSetCookie()) {
		response.headers.append('set-cookie', cookie);
	}
}
