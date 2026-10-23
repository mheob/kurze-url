import { createFileRoute, redirect } from '@tanstack/react-router';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { isSafeNext } from '../lib/safe-next';
import { flushSessionCookies } from '../server/session';
import { createSupabase } from '../server/supabase';

/**
 * What the code exchange tells the loader: whether it worked, and, only when it did, the safe
 * `next` to go on to. `next` is never set for a failed exchange, so a visitor without a session
 * cannot be sent anywhere `next` points.
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
 * @returns Whether the exchange worked, and the `next` to follow when it did and `isSafeNext` allows it.
 */
async function exchangeCodeImpl(): Promise<SignInOutcome> {
	const request = getRequest();
	const { searchParams } = new URL(request.url);
	const code = searchParams.get('code');
	if (code === null || code === '') return { next: undefined, ok: false };

	const headers = new Headers();
	const supabase = createSupabase(request, headers);
	const { error } = await supabase.auth.exchangeCodeForSession(code);
	// The Set-Cookie headers the exchange produced must reach the real
	// response, or the session is created and immediately lost — see
	// `flushSessionCookies`'s docstring for why this call is not optional.
	flushSessionCookies(headers);
	const next = searchParams.get('next');
	return { next: error === null && isSafeNext(next) ? next : undefined, ok: error === null };
}

/**
 * Extracted from the server function for the same reason `sendMagicLinkFor` is, and wrapped in
 * `createServerOnlyFn` for the same two: a test can call it without a request (`createServerFn`
 * needs a Start context, which Vitest has none of), and the client build elides it instead of
 * tripping import protection on the server-only imports it reaches. See `sendMagicLinkFor`'s
 * docstring in `server/auth.ts` for the long version.
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
 * A failed exchange must not land here where a successful one does —
 * that indistinguishability is exactly what let a signed-out visitor
 * look signed-in. Sending it back to /login instead, with no detail on
 * *why* it failed, keeps this from becoming a second enumeration
 * oracle alongside `sendMagicLinkFor`'s (reused vs. expired vs. missing
 * verifier all look the same from here). `next` does not soften that: it
 * is only ever set for a successful exchange.
 *
 * A safe `next` is followed with `href`, not `to`: it is a path with a query
 * string, and `to` is a route pattern. Anything else lands on `/`, as before.
 *
 * @param outcome - What the code exchange reported.
 * @param outcome.next - The safe path to follow, when there is one.
 * @param outcome.ok - Whether the exchange created a session.
 * @returns The redirect to throw.
 */
export function redirectAfterSignIn({ next, ok }: SignInOutcome): ReturnType<typeof redirect> {
	return redirect(next === undefined ? { to: ok ? '/' : '/login' } : { href: next });
}

export const Route = createFileRoute('/auth/callback')({
	loader: async () => {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		throw redirectAfterSignIn(await exchangeCodeForSession());
	},
});
