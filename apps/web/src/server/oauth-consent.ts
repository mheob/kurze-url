import type {
	AuthOAuthAuthorizationDetailsResponse,
	AuthOAuthConsentResponse,
	SupabaseClient,
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

/** The input of `decideConsentFor` and of the `decideConsent` server function. */
interface ConsentDecisionInput {
	readonly action: 'approve' | 'deny';
	readonly authorizationId: string;
}

const INVALID = { kind: 'invalid' } as const;
const UNAUTHENTICATED = { kind: 'unauthenticated' } as const;

/**
 * The redirect after a decision is the page's to follow, not the SDK's: the server function answers
 * with `redirect_url` and `/oauth/consent` sends the browser there, so the SDK is told not to
 * navigate. On the server it would not anyway (it only does so when `isBrowser()`), and this keeps
 * the call correct if it ever ran in one.
 */
const RETURN_REDIRECT_URL = { skipBrowserRedirect: true } as const;

/**
 * `getUser` rather than `getSession`: it asks Supabase whether the session is still good, where
 * `getSession` only reads the cookie, and approving a CLI login on a revoked session would hand
 * out a token to someone who is no longer signed in. It is also what refreshes an expiring session
 * into `headers`, so the caller must flush them.
 *
 * @param request - The incoming request, read for its session cookies.
 * @param headers - Written into if the session is refreshed; the caller must flush it.
 * @returns A client bound to this request, or undefined when nobody is signed in.
 */
async function signedInClient(
	request: Request,
	headers: Readonly<Headers>,
): Promise<SupabaseClient | undefined> {
	const supabase = createSupabase(request, headers);
	const { data } = await supabase.auth.getUser();
	return data.user === null ? undefined : supabase;
}

/**
 * Supabase answers an authorization that still needs a decision with its details, and one the
 * person has consented to already (a second `kurze-url login` for the same client and scope) with
 * only `redirect_url`: `authorization_id` is what tells the two apart. Measured against the real
 * server on 2026-10-23 (`docs/superpowers/specs/2026-10-23-cli-design.md`, "Verified").
 *
 * @param result - Supabase's answer to `getAuthorizationDetails`.
 * @returns The state the page renders or follows.
 */
function consentStateOf(result: AuthOAuthAuthorizationDetailsResponse): ConsentState {
	if (result.error !== null) return INVALID;
	const { data } = result;
	if ('authorization_id' in data) {
		return {
			authorizationId: data.authorization_id,
			clientName: data.client.name,
			email: data.user.email,
			kind: 'consent',
		};
	}
	return { kind: 'redirect', url: data.redirect_url };
}

/**
 * @param result - Supabase's answer to `approveAuthorization` or `denyAuthorization`.
 * @returns The CLI's redirect URL (the code on approval, `access_denied` on denial), or `invalid`
 *   when Supabase refused, which for a decision means the authorization is gone or already used.
 */
function decisionOf(result: AuthOAuthConsentResponse): ConsentDecision {
	return result.error === null ? { kind: 'redirect', url: result.data.redirect_url } : INVALID;
}

/**
 * The `...For` / `...Fn` split of `server/auth.ts` and `server/folders.ts`: the server function
 * itself calls `getRequest()`, which throws "No Start context found" under Vitest, so the testable
 * half takes the request and the `Headers` as parameters.
 *
 * An empty id is answered before Supabase is constructed: it is the "no `authorization_id` in the
 * URL" case, and there is nothing to ask. `flushSessionCookies` sits in `finally`, not after the
 * work, because a refresh `getUser` wrote has already used up the old refresh token: if the call
 * after it throws and the cookies are dropped, the browser keeps a token Supabase has seen used,
 * which it treats as reuse once its grace period ends. The exception itself is not caught. It is a
 * real failure and may reach Sentry.
 *
 * Wrapped in `createServerOnlyFn` for the same import-protection reason as `sendMagicLinkFor`.
 */
const loadConsentFor = createServerOnlyFn(
	async (
		request: Request,
		headers: Readonly<Headers>,
		authorizationId: string,
	): Promise<ConsentState> => {
		if (authorizationId === '') return INVALID;
		try {
			const supabase = await signedInClient(request, headers);
			if (supabase === undefined) return UNAUTHENTICATED;
			return consentStateOf(await supabase.auth.oauth.getAuthorizationDetails(authorizationId));
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
 * Only the exact string `approve` approves. Anything else denies, so a request that does not match
 * the types can never grant access by accident. Flushing and exceptions are as in `loadConsentFor`.
 */
const decideConsentFor = createServerOnlyFn(
	async (
		request: Request,
		headers: Readonly<Headers>,
		{ action, authorizationId }: Readonly<ConsentDecisionInput>,
	): Promise<ConsentDecision> => {
		if (authorizationId === '') return INVALID;
		try {
			const supabase = await signedInClient(request, headers);
			if (supabase === undefined) return UNAUTHENTICATED;
			const { oauth } = supabase.auth;
			return decisionOf(
				await (action === 'approve'
					? oauth.approveAuthorization(authorizationId, RETURN_REDIRECT_URL)
					: oauth.denyAuthorization(authorizationId, RETURN_REDIRECT_URL)),
			);
		} finally {
			flushSessionCookies(headers);
		}
	},
);

/**
 * `getRequest()` and a fresh `Headers` inline, not inside `loadConsentFor`, as in `signOut`.
 * `GET`: loading the consent state changes nothing, so a reload or a back button is harmless.
 */
const loadConsent = createServerFn({ method: 'GET' })
	.validator((data: { readonly authorizationId: string }) => data)
	.handler(async ({ data }: { readonly data: { readonly authorizationId: string } }) =>
		loadConsentFor(getRequest(), new Headers(), data.authorizationId),
	);

/** `POST`: this one grants or refuses access to the person's account, so it is never a link. */
const decideConsent = createServerFn({ method: 'POST' })
	.validator((data: ConsentDecisionInput) => data)
	.handler(async ({ data }: { readonly data: ConsentDecisionInput }) =>
		decideConsentFor(getRequest(), new Headers(), data),
	);

export { decideConsent, decideConsentFor, loadConsent, loadConsentFor };
export type { ConsentDecision, ConsentState };
