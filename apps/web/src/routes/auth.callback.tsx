import { createFileRoute, redirect } from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { flushSessionCookies } from '../server/session';
import { createSupabase } from '../server/supabase';

/**
 * Wrapped in a server function for the same isomorphic-safety reason as
 * `__root.tsx`'s `getPreferences` and `server/auth.ts`'s `sendMagicLink`:
 * `getRequest()` only works inside the server's per-request
 * AsyncLocalStorage, and this route's `loader` below also runs on the
 * client during an in-app navigation, not only on the initial request from
 * the magic-link email.
 *
 * This is where the PKCE verifier cookie written during `signInWithOtp` is
 * consumed. Most failures here — a reused code, an expired code, a missing
 * verifier — come back as `error` rather than a rejection, so the result is
 * reported rather than assumed: a caller that ignores it cannot tell a
 * failed exchange from a successful one, and would send a signed-out
 * visitor to the same place a signed-in one lands.
 */
const exchangeCodeForSession = createServerFn({ method: 'GET' }).handler(
	async (): Promise<{ ok: boolean }> => {
		const request = getRequest();
		const code = new URL(request.url).searchParams.get('code');
		if (!code) return { ok: false };

		const headers = new Headers();
		const supabase = createSupabase(request, headers);
		const { error } = await supabase.auth.exchangeCodeForSession(code);
		// The Set-Cookie headers the exchange produced must reach the real
		// response, or the session is created and immediately lost — see
		// `flushSessionCookies`'s docstring for why this call is not optional.
		flushSessionCookies(headers);
		return { ok: error === null };
	},
);

export const Route = createFileRoute('/auth/callback')({
	loader: async () => {
		const { ok } = await exchangeCodeForSession();
		// A failed exchange must not land here where a successful one does —
		// that indistinguishability is exactly what let a signed-out visitor
		// look signed-in. Sending it back to /login instead, with no detail on
		// *why* it failed, keeps this from becoming a second enumeration
		// oracle alongside `sendMagicLinkFor`'s (reused vs. expired vs. missing
		// verifier all look the same from here).
		throw redirect({ to: ok ? '/' : '/login' });
	},
});
