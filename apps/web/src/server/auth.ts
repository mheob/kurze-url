import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest, getRequestUrl } from '@tanstack/react-start/server';

import { isSafeNext } from '../lib/safe-next';
import { flushSessionCookies } from './session';
import { createSupabase } from './supabase';

// Stays here, ahead of the exports block below, even though that leaves this
// one `export const` flagged by import(exports-last): sendMagicLinkForImpl
// reads it by value, and eslint(no-use-before-define) checks a variable's
// textual position regardless of the enclosing function's hoisting, so
// moving this down would trade one lint rule's warning for the other. Every
// other export in this file moved cleanly.
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
/* oxlint-disable-next-line import/exports-last -- see the comment above this doc block: moving
 * this to the bottom with the other exports trades this warning for `eslint/no-use-before-define`
 * on `sendMagicLinkForImpl`'s reference to it, which checks textual position, not hoisting.
 */
export const ENUMERATION_TIMING_FLOOR_MS = 1000;

/**
 * `setTimeout`-backed, deliberately, rather than `node:timers/promises`'s own promise-returning
 * `setTimeout`: that version was tried, and `auth.test.ts`'s fake-timer tests broke under it —
 * `vi.useFakeTimers()`/`vi.advanceTimersByTimeAsync` patch the global `setTimeout` this wraps,
 * not whatever timer primitive Node's own promisified helper uses internally, so the floor this
 * exists to make testable stopped being controllable by the fake clock at all.
 *
 * @param ms - How long to wait.
 * @returns A promise that resolves after `ms` milliseconds.
 */
async function delay(ms: number): Promise<void> {
	/* oxlint-disable-next-line promise/avoid-new -- promisifying the callback-based global
	 * `setTimeout` requires `new Promise` here; there is no other way to `await` a timer while
	 * staying controllable by `vi.useFakeTimers()` (see the doc comment above).
	 */
	return new Promise((resolve) => {
		setTimeout(resolve, ms);
	});
}

/**
 * What the login form sends. `next` is whatever the browser's `/login?next=` held, so nothing
 * about it is trusted here: `sendMagicLinkFor` decides whether it may be followed.
 */
interface SendMagicLinkInput {
	readonly email: string;
	readonly next?: string;
}

/**
 * The URL the magic link brings the visitor back to. A CLI login sends a signed-out visitor to
 * `/login?next=<consent path>`, and `/auth/callback` has to know that path once the code is
 * exchanged, so it travels in the callback's own query string. Supabase admits that URL only
 * when its `additional_redirect_urls` entry for the callback ends in `**`, and otherwise
 * answers with the Site URL and no error (runbook item 4 of
 * `docs/superpowers/specs/2026-10-23-cli-design.md`; `supabase/config.toml` for the local stack).
 *
 * `next` is judged here, as well as when `/auth/callback` reads it back, so an unsafe value never
 * reaches a mail at all and `encodeURIComponent` is never given one it would throw on. An unsafe
 * `next` is dropped silently rather than refused: the form answers the same whatever it was given,
 * because an answer that varied would be one more thing to tell addresses apart by.
 *
 * @param origin - This request's origin.
 * @param next - The visitor's requested destination after sign-in, if any; honoured only when `isSafeNext` allows it.
 * @returns `<origin>/auth/callback`, with `?next=` and the encoded destination when there is a safe one.
 */
function callbackUrl(origin: string, next: string | undefined): string {
	const callback = `${origin}/auth/callback`;
	return isSafeNext(next) ? `${callback}?next=${encodeURIComponent(next)}` : callback;
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
 *
 * @param email - The address to send the magic link to; never confirmed to exist or not.
 * @param origin - This request's origin, used for the redirect URL and the synthetic request Supabase reads cookies from.
 * @param next - Where to send the visitor after the magic link signs them in, when `callbackUrl` accepts it; anything else is ignored.
 * @returns `{ sent: true }` unconditionally, whether or not the address is known.
 */
async function sendMagicLinkForImpl(
	email: string,
	origin: string,
	next?: string,
): Promise<{ sent: true }> {
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
			options: { emailRedirectTo: callbackUrl(origin, next), shouldCreateUser: false },
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
 * Which of a person's sessions a sign-out ends. `local` ends the one this browser holds and
 * leaves the rest alone, a CLI session included; `global` ends every session the person has. Both
 * were measured against the real server on 2026-10-23 (`docs/superpowers/specs/2026-10-23-cli-design.md`,
 * "Verified"). Supabase has a third scope, `others`, which this app never offers.
 */
type SignOutScope = 'global' | 'local';

/**
 * What the sidebar sends. The scope is optional so that a caller who says nothing gets the narrower
 * of the two, not the SDK's own default (`global`).
 */
interface SignOutInput {
	readonly scope?: SignOutScope;
}

/** What `signOutFor` is given once the validator has settled the scope. */
interface SignOutRequest {
	readonly scope: SignOutScope;
}

/**
 * Signs the visitor out with the scope the page asked for, and carries the cleared session cookies
 * onto the real response.
 *
 * `scope` is always passed, never left to the SDK: `signOut()` with no options is `global`, and
 * that default is what ended a person's CLI session every time they signed out of the web app.
 *
 * `flushSessionCookies` is not optional. Supabase's `signOut` asks the cookie adapter to *clear*
 * the session cookies, which is a `setAll` call like any other: written into the local `headers`,
 * and lost unless flushed. Without it the handler would run, the client would see a 200, and the
 * browser would keep sending the same still-valid session cookie on its next request: a sign-out
 * that silently does not sign anyone out.
 *
 * Only `global` reports a refused revoke. `auth-js` clears the local session and still returns
 * `{ error }` when the revoke call fails with anything but a 401, 403 or 404 (a 429, a 5xx, a
 * network failure), so for `global` the other sessions, the CLI's among them, may be alive while
 * everything looks signed out. That throws, before the flush, so the browser keeps the session the
 * server may still honour and the caller's error state shows instead of a navigation away.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param headers - The `Headers` the cleared cookies are written into; flushed before this returns.
 * @param scope - `local` to end only this browser's session, `global` to end all of them.
 * @returns Nothing; the work is the revocation and the flushed cookies.
 * @throws {Error} When `scope` is `global` and Supabase refused the revocation; nothing is flushed then.
 */
async function signOutForImpl(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Request` nests a mutable `Headers` through its own `.headers` getter, which `Readonly<>`, being shallow, does not reach; not a type this file can edit.
	request: Request,
	headers: Readonly<Headers>,
	scope: SignOutScope,
): Promise<void> {
	const supabase = createSupabase(request, headers);
	const { error } = await supabase.auth.signOut({ scope });
	// `local` stays lenient: whatever the revoke call said, the SDK has cleared this browser's session, which is all `local` promises.
	if (scope === 'global' && error !== null) {
		throw new Error('global sign-out failed', { cause: error });
	}
	flushSessionCookies(headers);
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
 * once `flushSessionCookies` (which reaches `appendResponseHeader` from
 * `@tanstack/react-start/server`) is called from inside this function,
 * `vite build`'s import-protection check fails with a real, hard build
 * error — verified by building both ways. The failure is specific to this
 * shape (a plain, separately-exported helper referenced *by name* from a
 * `createServerFn().handler()` in another file, done deliberately so the
 * enumeration guarantee is testable without a request) — `fetchMe`'s own
 * `flushSessionCookies` call (`routes/_authed.tsx`), written inline inside
 * its `.handler()` closure rather than delegated to a named helper, does
 * not trip it. `signOutFor` below has this function's shape and is wrapped
 * the same way. `createServerOnlyFn` is TanStack Start's own documented
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
	.validator((data: SendMagicLinkInput) => data)
	.handler(async ({ data }: { readonly data: SendMagicLinkInput }) =>
		sendMagicLinkFor(data.email, getRequestUrl().origin, data.next),
	);

/**
 * The validator of `signOut`. Exported for its test only: the server function wrapping it cannot be
 * called under Vitest.
 *
 * Reads its input as `unknown`, because nothing else checks what a request actually carried, and
 * `global` is only ever the exact string `global`. It is the one scope that ends sessions the
 * person did not sign out of here, so a missing, mistyped or crafted value (`others`, `GLOBAL`, a
 * number) settles on the narrower `local` rather than on something that reaches further, and
 * nothing throws: a validator that throws on a crafted request would put that request in Sentry.
 *
 * @param data - The request's data, untrusted whatever its declared type.
 * @returns `{ scope: 'global' }` when the request asked for exactly that, otherwise `{ scope: 'local' }`.
 */
export function readSignOutScope(data: unknown): SignOutRequest {
	const scope: unknown =
		typeof data === 'object' && data !== null ? Reflect.get(data, 'scope') : undefined;
	return { scope: scope === 'global' ? 'global' : 'local' };
}

/**
 * Extracted from the server function so the scope it hands Supabase, and the cookie flush that
 * makes the sign-out reach the browser, can be tested without a request.
 *
 * Wrapped in `createServerOnlyFn` for the reason `sendMagicLinkFor`'s docstring gives, because it
 * has the same shape: a named helper that reaches `flushSessionCookies` and is referenced from a
 * `createServerFn().handler()`. Under Vitest it is the identity function.
 */
export const signOutFor = createServerOnlyFn(signOutForImpl);

/**
 * `getRequest()` (not a `request` field on the handler's context — same
 * correction as `sendMagicLink` above) reads the incoming request from the
 * server's per-request AsyncLocalStorage, and the `Headers` are made here,
 * inline, for the reason `signOutFor` gives: it flushes them.
 *
 * Sign-out is local unless the caller says `global` (the sidebar's "Sign out
 * everywhere" box), because a global sign-out also ends the person's CLI
 * sessions, which a plain sign-out of the web app must not do. The validator
 * is typed with the caller's input but reads it as `unknown`
 * (`readSignOutScope`), and accepts no data at all, so `signOut()` is a local
 * sign-out too.
 */
export const signOut = createServerFn({ method: 'POST' })
	.validator((data: SignOutInput | undefined) => readSignOutScope(data))
	.handler(async ({ data }: { readonly data: SignOutRequest }) =>
		signOutFor(getRequest(), new Headers(), data.scope),
	);
