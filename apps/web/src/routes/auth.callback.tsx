import { createFileRoute, redirect } from '@tanstack/react-router';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { isSafeNext } from '../lib/safe-next';
import { flushSessionCookies } from '../server/session';
import { createSupabase } from '../server/supabase';

/**
 * What the code exchange tells the loader: whether it worked, and the request's `next` when
 * `isSafeNext` allows it. A safe `next` is set whether or not the exchange worked; only
 * `redirectAfterSignIn` decides what it means, and a visitor without a session is never sent where
 * it points, only back to `/login` carrying it.
 */
interface SignInOutcome {
	readonly next: string | undefined;
	readonly ok: boolean;
}

/**
 * This is where the PKCE verifier cookie written during `signInWithOtp` is
 * consumed. Most failures here — a reused code, an expired code, a missing
 * verifier — come back as `error` rather than a rejection, so the result is
 * reported rather than assumed: a caller that ignores it cannot tell a
 * failed exchange from a successful one, and would send a signed-out
 * visitor to the same place a signed-in one lands.
 *
 * `next` is read off the same request URL as the code and judged by
 * `isSafeNext`. It is part of the query string the visitor can write, so
 * this is the open-redirect guard's decisive check, not `sendMagicLinkFor`'s
 * earlier one.
 *
 * @returns Whether the exchange worked, and the request's `next` when `isSafeNext` allows it.
 */
async function exchangeCodeImpl(): Promise<SignInOutcome> {
	const request = getRequest();
	const { searchParams } = new URL(request.url);
	const requested = searchParams.get('next');
	const next = isSafeNext(requested) ? requested : undefined;
	const code = searchParams.get('code');
	if (code === null || code === '') return { next, ok: false };

	const headers = new Headers();
	const supabase = createSupabase(request, headers);
	const { error } = await supabase.auth.exchangeCodeForSession(code);
	// The Set-Cookie headers the exchange produced must reach the real
	// response, or the session is created and immediately lost — see
	// `flushSessionCookies`'s docstring for why this call is not optional.
	flushSessionCookies(headers);
	return { next, ok: error === null };
}

/**
 * Extracted from the server function for the same reason `sendMagicLinkFor` is: a test can call it
 * without a request (`createServerFn` needs a Start context, which Vitest has none of). Wrapped in
 * `createServerOnlyFn` for the reason that helper's docstring in `server/auth.ts` gives: not for
 * the build, which passes without it, but so the client build replaces it with a stub that throws,
 * keeping it and the server-only imports it reaches out of the browser bundle.
 */
export const exchangeCode = createServerOnlyFn(exchangeCodeImpl);

/**
 * Wrapped in a server function for the same isomorphic-safety reason as
 * `__root.tsx`'s `getPreferences` and `server/auth.ts`'s `sendMagicLink`:
 * `getRequest()` only works inside the server's per-request
 * AsyncLocalStorage, and this route's `loader` below also runs on the
 * client during an in-app navigation, not only on the initial request from
 * the magic-link email.
 */
const exchangeCodeForSession = createServerFn({ method: 'GET' }).handler(
	async (): Promise<SignInOutcome> => exchangeCode(),
);

/**
 * A failed exchange must not land where a successful one does — that
 * indistinguishability is exactly what let a signed-out visitor look
 * signed-in. It goes back to /login instead, with no detail on *why* it
 * failed, which keeps this from becoming a second enumeration oracle
 * alongside `sendMagicLinkFor`'s (reused vs. expired vs. missing verifier
 * all look the same from here).
 *
 * It carries a safe `next` back to /login, because a magic link opened in
 * another browser fails for want of the PKCE verifier, and the retry has to
 * end on the consent page the CLI is waiting on rather than on `/`. That
 * gives nothing away: `next` came from the visitor's own query string, and
 * every failure carries it the same way. The login page only hands it on to
 * the next magic link, so the guards judge it again before anything follows
 * it.
 *
 * After a successful exchange a safe `next` is followed with `href`, not
 * `to`: it is a path with a query string, and `to` is a route pattern.
 * Anything else lands on `/`, as before.
 *
 * @param outcome - What the code exchange reported.
 * @param outcome.next - The safe consent path the request carried, when there is one.
 * @param outcome.ok - Whether the exchange created a session.
 * @returns The redirect to throw.
 */
export function redirectAfterSignIn({ next, ok }: SignInOutcome): ReturnType<typeof redirect> {
	if (!ok)
		return redirect(next === undefined ? { to: '/login' } : { search: { next }, to: '/login' });
	return redirect(next === undefined ? { to: '/' } : { href: next });
}

export const Route = createFileRoute('/auth/callback')({
	loader: async () => {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		throw redirectAfterSignIn(await exchangeCodeForSession());
	},
});
