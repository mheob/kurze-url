import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest, getRequestUrl } from '@tanstack/react-start/server';

import { flushSessionCookies } from './session';
import { createSupabase } from './supabase';

/**
 * Floor under `sendMagicLinkFor`'s response time. Defends the same
 * enumeration guarantee as the discarded error below, but for *timing*
 * rather than *shape*: with `shouldCreateUser: false`, an unknown address
 * fails fast (Supabase rejects it locally, no SMTP round-trip) while a known
 * one waits on a real Resend send. Left alone, that gap — easily hundreds of
 * milliseconds — lets anyone time this public form to learn which addresses
 * belong to a Verein. ~1s comfortably swamps the fast-fail path while
 * staying invisible on a flow that already waits on an email.
 */
export const ENUMERATION_TIMING_FLOOR_MS = 1000;

function delay(ms: number): Promise<void> {
	return new Promise((resolve) => {
		setTimeout(resolve, ms);
	});
}

/**
 * The `Headers` built here are *not* discarded, even though this is a
 * synthetic `new Request(origin)` with none of the real visitor's cookies to
 * read: `signInWithOtp`'s PKCE flow writes the code verifier cookie through
 * the adapter's `setAll` before the OTP HTTP request is even sent, and that
 * write lands in `headers` regardless of whether the address turns out to be
 * known or unknown. `flushSessionCookies` below carries it onto the real
 * response — skip that call and `/auth/callback`'s `exchangeCodeForSession`
 * finds no verifier and throws on every single login.
 *
 * Flushed after the race settles, never instead of it, and unconditionally
 * on both the known- and unknown-address paths: the verifier is written
 * before Supabase knows whether the address exists, so doing this any other
 * way would reopen the timing/shape side-channel the floor and the
 * discarded error below exist to close.
 */
async function sendMagicLinkForImpl(email: string, origin: string): Promise<{ sent: true }> {
	const headers = new Headers();
	const supabase = createSupabase(new Request(origin), headers);

	// Race the real call against the timing floor with `allSettled`, not
	// `Promise.all`/a plain `await`: both must be awaited so the response
	// cannot leave before the floor elapses no matter which path Supabase
	// took, and a rejection from `signInWithOtp` must not skip the floor or
	// escape this function.
	await Promise.allSettled([
		supabase.auth.signInWithOtp({
			email,
			options: { shouldCreateUser: false, emailRedirectTo: `${origin}/auth/callback` },
		}),
		delay(ENUMERATION_TIMING_FLOOR_MS),
	]);

	flushSessionCookies(headers);

	// Deliberately ignoring the error. Supabase distinguishes a known address
	// from an unknown one; surfacing that difference would turn this form into
	// an account-enumeration oracle. Failures that matter (SMTP down) show up
	// in Sentry, not here.
	return { sent: true };
}

/**
 * Extracted from the server function so it can be tested without a request:
 * the enumeration guarantee is the interesting behaviour, and it should not
 * need a framework harness to assert.
 *
 * Wrapped in `createServerOnlyFn` — not merely for its runtime guard, which
 * is not what's load-bearing here (Vitest never compiles this file with
 * TanStack Start's Vite plugin, so in every test `createServerOnlyFn` is
 * exactly the identity function `@tanstack/start-fn-stubs` defines it as,
 * and calling `sendMagicLinkFor` directly behaves exactly as if this wrapper
 * were not here at all). The wrap exists for the *production client build*:
 * once `flushSessionCookies` (which reaches `getResponse` from
 * `@tanstack/react-start/server`) is called from inside this function,
 * `vite build`'s import-protection check fails with a real, hard build
 * error — verified by building both ways. The failure is specific to this
 * shape (a plain, separately-exported helper referenced *by name* from a
 * `createServerFn().handler()` in another file, done deliberately so the
 * enumeration guarantee is testable without a request) — `signOut`'s and
 * `/auth/callback`'s own `flushSessionCookies` calls, written inline inside
 * their `.handler()` closures rather than delegated to a named helper, do
 * not trip it. `createServerOnlyFn` is TanStack Start's own documented
 * answer to exactly this case (its compiler recognises the call and elides
 * it from the client bundle instead of leaving the reachable import for
 * Rolldown to trip over) — it is also the fix suggested by the import-
 * protection error's own message.
 */
export const sendMagicLinkFor = createServerOnlyFn(sendMagicLinkForImpl);

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

/**
 * `getRequest()` (not a `request` field on the handler's context — same
 * correction as `sendMagicLink` above) reads the incoming request from the
 * server's per-request AsyncLocalStorage.
 *
 * Supabase's `signOut` asks the cookie adapter to *clear* the session
 * cookies, which is a `setAll` call like any other — written into the local
 * `headers` here, and lost unless flushed. Without `flushSessionCookies`,
 * this handler would run, the client would see a 200, and the browser would
 * keep sending the same still-valid session cookie on its next request: a
 * sign-out that silently does not sign anyone out.
 */
export const signOut = createServerFn({ method: 'POST' }).handler(async () => {
	const headers = new Headers();
	const supabase = createSupabase(getRequest(), headers);
	await supabase.auth.signOut();
	flushSessionCookies(headers);
});
