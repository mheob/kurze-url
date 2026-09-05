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
 * consumed. If the adapter cannot read cookies, this fails with "code
 * verifier should be non-empty".
 */
const exchangeCodeForSession = createServerFn({ method: 'GET' }).handler(async () => {
	const request = getRequest();
	const code = new URL(request.url).searchParams.get('code');
	if (!code) return;

	const headers = new Headers();
	const supabase = createSupabase(request, headers);
	await supabase.auth.exchangeCodeForSession(code);
	// The Set-Cookie headers the exchange produced must reach the real
	// response, or the session is created and immediately lost — see
	// `flushSessionCookies`'s docstring for why this call is not optional.
	flushSessionCookies(headers);
});

export const Route = createFileRoute('/auth/callback')({
	loader: async () => {
		await exchangeCodeForSession();
		throw redirect({ to: '/' });
	},
});
