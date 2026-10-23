import {
	type AuthError,
	type AuthOAuthAuthorizationDetailsResponse,
	type AuthOAuthConsentResponse,
	isAuthApiError,
	isAuthSessionMissingError,
	type SupabaseClient,
} from '@supabase/supabase-js';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { flushSessionCookies } from './session';
import { createSupabase } from './supabase';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is either the `request: Request` parameter each `...For` function takes, or a Supabase
 * client or result: `Request` nests a mutable `Headers` through its own `.headers` getter, and the
 * Supabase types nest class instances (`AuthError`) that `Readonly<>`, being shallow, does not
 * reach. Neither type is ours to edit.
 */

/**
 * What the consent page is told. `unauthenticated` and `invalid` are states to render, not
 * failures: a signed-out visitor goes to `/login`, and an expired or unknown authorization is an
 * ordinary thing for a person to arrive with, so neither throws and neither belongs in Sentry.
 * Both mean the auth server answered and said no. The auth server failing to answer (a network
 * error, a 5xx) is neither of them: it is thrown, see `isRefusal`.
 */
type ConsentState =
	| {
			readonly kind: 'consent';
			readonly authorizationId: string;
			readonly clientName: string;
			readonly email: string;
	  }
	| { readonly kind: 'invalid' }
	| { readonly kind: 'redirect'; readonly url: string }
	| { readonly kind: 'unauthenticated' };

/** What the page is told once the person has pressed Allow or Deny. */
type ConsentDecision =
	| { readonly kind: 'invalid' }
	| { readonly kind: 'redirect'; readonly url: string }
	| { readonly kind: 'unauthenticated' };

/** What a caller of the `decideConsent` server function may send: the two actions and an id. */
interface ConsentDecisionInput {
	readonly action: 'approve' | 'deny';
	readonly authorizationId: string;
}

/**
 * What `decideConsentFor` is given once the validator has run: strings, because that is all a
 * request can be known to carry. `ConsentDecisionInput` narrows what callers may send; this is
 * deliberately wider, so the check that refuses anything else lives in one place that can be
 * tested with the values a crafted request would hold.
 */
interface ConsentDecisionRequest {
	readonly action: string;
	readonly authorizationId: string;
}

const INVALID = { kind: 'invalid' } as const;
const UNAUTHENTICATED = { kind: 'unauthenticated' } as const;

/** The lowest HTTP status that is a server failing, as opposed to a server refusing a request. */
const FIRST_SERVER_ERROR_STATUS = 500;

/**
 * The redirect after a decision is the page's to follow, not the SDK's: the server function answers
 * with `redirect_url` and `/oauth/consent` sends the browser there, so the SDK is told not to
 * navigate. On the server it would not anyway (it only does so when `isBrowser()`), and this keeps
 * the call correct if it ever ran in one.
 */
const RETURN_REDIRECT_URL = { skipBrowserRedirect: true } as const;

/**
 * @param value - Anything a response or a request may hold.
 * @returns True for a string with at least one character.
 */
function isNonEmptyString(value: unknown): value is string {
	return typeof value === 'string' && value !== '';
}

/**
 * Whether an id may be given to the SDK at all. The id comes from a URL the visitor can edit, and
 * the SDK puts it into a request path with the visitor's access token attached. `encodeURIComponent`
 * keeps `../../user` one segment, but it leaves the dot segments `.` and `..` alone, which a URL
 * parser resolves instead of sending, and it throws on a lone surrogate, which would be a 500 where
 * a refusal belongs.
 *
 * @param id - The authorization id as the request carried it.
 * @returns True when `encodeURIComponent(id)` is safe to send as one path segment.
 */
function isUsableId(id: string): boolean {
	return id !== '' && id !== '.' && id !== '..' && !/\p{Surrogate}/u.test(id);
}

/**
 * Reads one field of a request's data without trusting its shape. The validators below are typed
 * with the caller's own input type, but what arrives is whatever the request carried: no `data` at
 * all, a number, an object. Each of those becomes an empty string, which `isUsableId` refuses.
 *
 * @param data - The validator's input, untrusted.
 * @param key - The field to read.
 * @returns The field when it is a string, otherwise an empty string.
 */
function readString(data: unknown, key: string): string {
	if (typeof data !== 'object' || data === null) return '';
	const value: unknown = Reflect.get(data, key);
	return typeof value === 'string' ? value : '';
}

/**
 * @param url - What the response holds as `redirect_url`.
 * @returns A redirect to it, or `invalid` when there is none: Supabase answers an id it cannot place
 *   with something that is neither the details nor a redirect, and the page must never be told to
 *   follow `undefined`.
 */
function redirectTo(url: unknown): ConsentDecision {
	return isNonEmptyString(url) ? { kind: 'redirect', url } : INVALID;
}

/**
 * Whether an `{ error }` result means the auth server answered and said no. `@supabase/auth-js`
 * never throws for its own failures: it returns them, a refused connection as an
 * `AuthRetryableFetchError` and a 5xx as that or as an `AuthApiError` with a status of 500 or
 * more. Only an `AuthApiError` below 500 is the server judging the request (a token it refuses, an
 * authorization it cannot place), which is the person's situation and a state to render; anything
 * else is the auth server or the path to it failing, and showing that as "the link has expired"
 * would hide an outage from the one place that reports it.
 *
 * @param error - The `error` of a result the SDK returned.
 * @returns True for an `AuthApiError` whose status is below 500.
 */
function isRefusal(error: AuthError): boolean {
	return isAuthApiError(error) && error.status < FIRST_SERVER_ERROR_STATUS;
}

/**
 * @param error - The `error` of an authorization call's result.
 * @returns `invalid` when the auth server refused (`isRefusal`).
 * @throws {AuthError} The error itself, unchanged, when it is anything else: the route's error
 *   boundary reports it to Sentry, where a swallowed one would leave a Supabase outage looking like
 *   a page of expired links.
 */
function invalidUnlessFailed(error: AuthError): typeof INVALID {
	if (isRefusal(error)) return INVALID;
	throw error;
}

/**
 * `getUser` rather than `getSession`: it asks Supabase whether the session is still good, where
 * `getSession` only reads the cookie, and approving a CLI login on a revoked session would hand
 * out a token to someone who is no longer signed in. It is also what refreshes an expiring session
 * into `headers`, so the caller must flush them.
 *
 * Signed out is no user without an error, the SDK's own "no session" error, or the auth server
 * refusing the token with a 4xx. Any other error is the auth server failing (`isRefusal`) and is
 * thrown: reading it as "signed out" would send a person whose session is fine to `/login`, and
 * hide the outage.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param headers - Written into if the session is refreshed; the caller must flush it.
 * @returns A client bound to this request, or undefined when nobody is signed in.
 * @throws {AuthError} The `error` of `getUser`, unchanged, when the auth server failed to answer.
 */
async function signedInClient(
	request: Request,
	headers: Readonly<Headers>,
): Promise<SupabaseClient | undefined> {
	const supabase = createSupabase(request, headers);
	const { data, error } = await supabase.auth.getUser();
	if (error === null) return data.user === null ? undefined : supabase;
	if (isAuthSessionMissingError(error) || isRefusal(error)) return undefined;
	throw error;
}

/**
 * Supabase answers an authorization that still needs a decision with its details, and one the
 * person has consented to already (a second `kurze-url login` for the same client and scope) with
 * only `redirect_url`: `authorization_id` is what tells the two apart. Measured against the real
 * server on 2026-10-23 (`docs/superpowers/specs/2026-10-23-cli-design.md`, "Verified").
 *
 * @param result - Supabase's answer to `getAuthorizationDetails`.
 * @returns The state the page renders or follows; `invalid` when the auth server refused.
 * @throws {AuthError} The result's error, unchanged, when the auth server failed to answer
 *   (`isRefusal`).
 */
function consentStateOf(result: AuthOAuthAuthorizationDetailsResponse): ConsentState {
	if (result.error !== null) return invalidUnlessFailed(result.error);
	const { data } = result;
	if (!('authorization_id' in data)) return redirectTo(data.redirect_url);
	if (!isNonEmptyString(data.authorization_id)) return INVALID;
	return {
		authorizationId: data.authorization_id,
		clientName: data.client.name,
		email: data.user.email,
		kind: 'consent',
	};
}

/**
 * @param result - Supabase's answer to `approveAuthorization` or `denyAuthorization`.
 * @returns The CLI's redirect URL (the code on approval, `access_denied` on denial), or `invalid`
 *   when Supabase refused, which for a decision means the authorization is gone or already used.
 * @throws {AuthError} The result's error, unchanged, when the auth server failed to answer
 *   (`isRefusal`).
 */
function decisionOf(result: AuthOAuthConsentResponse): ConsentDecision {
	return result.error === null
		? redirectTo(result.data.redirect_url)
		: invalidUnlessFailed(result.error);
}

/**
 * The `...For` / `...Fn` split of `server/auth.ts` and `server/folders.ts`: the server function
 * itself calls `getRequest()`, which throws "No Start context found" under Vitest, so the testable
 * half takes the request and the `Headers` as parameters.
 *
 * An id that is empty (the "no `authorization_id` in the URL" case) or that `isUsableId` refuses is
 * answered before Supabase is constructed, as `invalid`: there is nothing to ask. The id the SDK is
 * given is encoded, so it stays one path segment.
 *
 * `flushSessionCookies` sits in `finally`, not after the work, because a refresh `getUser` wrote
 * has already used up the old refresh token: if the call after it throws and the cookies are
 * dropped, the browser keeps a token Supabase has seen used, which it treats as reuse once its
 * grace period ends.
 *
 * What throws is the auth server failing, not the SDK throwing: `@supabase/auth-js` returns its own
 * failures as `{ error }`, so `signedInClient` and `consentStateOf` turn every one that is not a 4xx
 * `AuthApiError` (or, for `getUser`, a missing session) into an exception, and nothing here catches
 * it. It is a real failure and reaches Sentry through the route's error boundary.
 *
 * Wrapped in `createServerOnlyFn` for the same import-protection reason as `sendMagicLinkFor`.
 */
const loadConsentFor = createServerOnlyFn(
	async (
		request: Request,
		headers: Readonly<Headers>,
		authorizationId: string,
	): Promise<ConsentState> => {
		if (!isUsableId(authorizationId)) return INVALID;
		try {
			const supabase = await signedInClient(request, headers);
			if (supabase === undefined) return UNAUTHENTICATED;
			return consentStateOf(
				await supabase.auth.oauth.getAuthorizationDetails(encodeURIComponent(authorizationId)),
			);
		} finally {
			flushSessionCookies(headers);
		}
	},
);

/**
 * Decides one authorization, and re-checks the session to do it: the page may have been open for
 * longer than the session lasts, and an `unauthenticated` answer is what sends the person back to
 * `/login` with the same `next` instead of showing a failure after they pressed Allow.
 *
 * Only the exact strings `approve` and `deny` are actions. Anything else is `invalid` and decides
 * nothing: a deny is as irreversible as an approval (the CLI is told `access_denied` and the
 * authorization is spent), so a request that does not match the types must not be read as either.
 * Ids, flushing and exceptions are as in `loadConsentFor`: a 4xx `AuthApiError` from the decision
 * is `invalid`, and any other error from it is thrown.
 */
const decideConsentFor = createServerOnlyFn(
	async (
		request: Request,
		headers: Readonly<Headers>,
		{ action, authorizationId }: Readonly<ConsentDecisionRequest>,
	): Promise<ConsentDecision> => {
		if (!isUsableId(authorizationId) || (action !== 'approve' && action !== 'deny')) return INVALID;
		const id = encodeURIComponent(authorizationId);
		try {
			const supabase = await signedInClient(request, headers);
			if (supabase === undefined) return UNAUTHENTICATED;
			const { oauth } = supabase.auth;
			return decisionOf(
				await (action === 'approve'
					? oauth.approveAuthorization(id, RETURN_REDIRECT_URL)
					: oauth.denyAuthorization(id, RETURN_REDIRECT_URL)),
			);
		} finally {
			flushSessionCookies(headers);
		}
	},
);

/**
 * The validator of `loadConsent`. Exported for its test only: the server function wrapping it
 * cannot be called under Vitest.
 *
 * @param data - The request's data, untrusted whatever its declared type.
 * @returns The authorization id when it is a string, otherwise an empty string, which
 *   `loadConsentFor` answers as `invalid` without calling Supabase.
 */
function readAuthorizationId(data: unknown): string {
	return readString(data, 'authorizationId');
}

/**
 * The validator of `decideConsent`. It settles the shape (two strings) and leaves their meaning to
 * `decideConsentFor`, so an unknown action travels on as it was sent and is refused there. Exported
 * for its test only.
 *
 * @param data - The request's data, untrusted whatever its declared type.
 * @returns The action and the authorization id, each an empty string where the request had no string.
 */
function readDecision(data: unknown): ConsentDecisionRequest {
	return { action: readString(data, 'action'), authorizationId: readAuthorizationId(data) };
}

/**
 * `getRequest()` and a fresh `Headers` inline, not inside `loadConsentFor`, as in `signOut`.
 * `GET`, though it is not side-effect free: for an authorization the person has consented to
 * already, GoTrue answers `redirect_url` and mints a new authorization code on that very request.
 * That is harmless here because every redirect URI registered for the CLI is a loopback address
 * (`127.0.0.1`), so only a listener on the person's own machine can receive a code, and a reload or
 * a back button just mints another. What the request never does is decide anything: only
 * `decideConsent` grants or refuses.
 *
 * The validator is typed with the caller's input but reads it as `unknown` (`readAuthorizationId`),
 * because nothing else checks what a request actually carried.
 */
const loadConsent = createServerFn({ method: 'GET' })
	.validator((data: { readonly authorizationId: string }) => ({
		authorizationId: readAuthorizationId(data),
	}))
	.handler(async ({ data }: { readonly data: { readonly authorizationId: string } }) =>
		loadConsentFor(getRequest(), new Headers(), data.authorizationId),
	);

/**
 * `POST`: this one grants or refuses access to the person's account, so it is never a link. The
 * validator reads its input as `unknown` (`readDecision`) like `loadConsent`'s, and
 * `decideConsentFor` refuses an action that is not exactly `approve` or `deny`.
 */
const decideConsent = createServerFn({ method: 'POST' })
	.validator((data: ConsentDecisionInput) => readDecision(data))
	.handler(async ({ data }: { readonly data: ConsentDecisionRequest }) =>
		decideConsentFor(getRequest(), new Headers(), data),
	);

export {
	decideConsent,
	decideConsentFor,
	loadConsent,
	loadConsentFor,
	readAuthorizationId,
	readDecision,
};
export type { ConsentDecision, ConsentState };
