import { createServerFn } from '@tanstack/react-start';
import { getRequestUrl } from '@tanstack/react-start/server';

import { createSupabase } from './supabase';

/**
 * Extracted from the server function so it can be tested without a request:
 * the enumeration guarantee is the interesting behaviour, and it should not
 * need a framework harness to assert.
 *
 * The `Headers` built here are discarded: `signInWithOtp` only ever asks
 * @supabase/ssr to *set* a cookie (the PKCE code verifier for the flow the
 * `/auth/callback` route in a later task completes), never to read one back
 * mid-call, so there is nothing for a real request's cookies to feed on this
 * path.
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

/**
 * `getRequestUrl` (not a `request` field on the handler's context — this
 * version of `createServerFn` has no such field, only `data`/`context`/
 * `method`) reads the incoming request from the server's per-request
 * AsyncLocalStorage, the same isomorphic-safe seam `__root.tsx`'s
 * `getPreferences` uses for `getRequestHeader`.
 */
export const sendMagicLink = createServerFn({ method: 'POST' })
	.validator((data: { email: string }) => data)
	.handler(async ({ data }) => sendMagicLinkFor(data.email, getRequestUrl().origin));
