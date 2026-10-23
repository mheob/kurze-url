import {
	type AuthError,
	AuthApiError,
	AuthRetryableFetchError,
	AuthSessionMissingError,
	AuthUnknownError,
} from '@supabase/supabase-js';
import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is a `Request` parameter (the mock's own): `Request` nests a mutable `Headers` through its
 * own `.headers` getter, and `Readonly<>` is shallow — it does not reach that nested property,
 * unlike a bare `Headers` parameter, which the check does accept once wrapped.
 */

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice the consent functions reach
 * through, typed the way `auth.test.ts` types its own fake. `data` is loose because the real
 * result is a union of the details, the redirect and `null`, and each test hands over exactly the
 * shape it is about. `error` is the SDK's own `AuthError` and the tests build it from the SDK's
 * own classes: `@supabase/auth-js` never throws for a failure of the auth server, it returns the
 * error, and what the code does with one depends on its class and status.
 */
interface FakeResult {
	readonly data: Readonly<Record<string, unknown>> | null;
	readonly error: AuthError | null;
}

/** What `getUser` answers: the user, or none and why. */
interface FakeUserResult {
	readonly data: { readonly user: Readonly<{ id: string }> | null };
	readonly error: AuthError | null;
}

interface FakeDecisionOptions {
	readonly skipBrowserRedirect: boolean;
}

interface FakeSupabaseClient {
	readonly auth: {
		readonly getUser: () => Promise<FakeUserResult>;
		readonly oauth: {
			readonly approveAuthorization: (
				authorizationId: string,
				options: FakeDecisionOptions,
			) => Promise<FakeResult>;
			readonly denyAuthorization: (
				authorizationId: string,
				options: FakeDecisionOptions,
			) => Promise<FakeResult>;
			readonly getAuthorizationDetails: (authorizationId: string) => Promise<FakeResult>;
		};
	};
}

const mocks = vi.hoisted(() => ({
	// Only the flush assertions below inspect what this was given.
	appendResponseHeader: vi.fn(),
	approveAuthorization: vi.fn<FakeSupabaseClient['auth']['oauth']['approveAuthorization']>(),
	createSupabase: vi.fn<(request: Request, headers: Readonly<Headers>) => FakeSupabaseClient>(),
	denyAuthorization: vi.fn<FakeSupabaseClient['auth']['oauth']['denyAuthorization']>(),
	getAuthorizationDetails: vi.fn<FakeSupabaseClient['auth']['oauth']['getAuthorizationDetails']>(),
	getUser: vi.fn<FakeSupabaseClient['auth']['getUser']>(),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
}));

const { decideConsentFor, loadConsentFor, readAuthorizationId, readDecision } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
	 * register their replacements, which a module-scope `await import` is what expresses.
	 */
	await import('./oauth-consent');

const AUTHORIZATION_ID = 'auth-123';
const REDIRECT_URL = 'http://127.0.0.1:53682/callback?code=abc&state=xyz';
const SESSION_COOKIE = 'sb-test-auth-token=refreshed; Path=/; HttpOnly';

/** What `getAuthorizationDetails` answers while the person still has to decide. */
const DETAILS: FakeResult = {
	data: {
		authorization_id: AUTHORIZATION_ID,
		client: {
			id: 'client-1',
			logo_uri: '',
			name: 'kurze-url CLI',
			uri: 'https://kurze-url.app',
		},
		redirect_uri: 'http://127.0.0.1:53682/callback',
		scope: 'openid',
		user: { email: 'someone@example.test', id: 'user-1' },
	},
	error: null,
};

/** What it answers once the person has consented already: no `authorization_id` at all. */
const ALREADY_CONSENTED: FakeResult = { data: { redirect_url: REDIRECT_URL }, error: null };

/**
 * The auth server answered and said no: an authorization it cannot place or that is spent. The
 * status is a representative 4xx; the exact one GoTrue uses for an unknown id is not measured, and
 * the code under test does not depend on it.
 */
const SERVER_REFUSAL: FakeResult = {
	data: null,
	error: new AuthApiError('authorization not found', 404, undefined),
};

const SIGNED_IN: FakeUserResult = { data: { user: { id: 'user-1' } }, error: null };
const SIGNED_OUT: FakeUserResult = { data: { user: null }, error: null };

/**
 * Every way `getUser` says nobody is signed in: no user and no error, the SDK's own "no session"
 * error, and the auth server refusing the access token with a 4xx (an expired or revoked one).
 */
const SIGNED_OUT_ANSWERS: readonly (readonly [string, FakeUserResult])[] = [
	['no user and no error', SIGNED_OUT],
	['an AuthSessionMissingError', { data: { user: null }, error: new AuthSessionMissingError() }],
	[
		'a 403 AuthApiError for a bad token',
		{ data: { user: null }, error: new AuthApiError('invalid JWT', 403, 'bad_jwt') },
	],
	[
		'a 401 AuthApiError',
		{ data: { user: null }, error: new AuthApiError('unauthorized', 401, undefined) },
	],
];

/**
 * Failures that are the auth server failing, not the person's situation: the SDK returns them as
 * `{ error }` and never throws them, so the code under test has to. The first is what a refused
 * connection or a timeout becomes; a 503 reaches it as the same class, because the SDK wraps 500
 * to 504 and 520 to 530 as retryable and uses `AuthApiError` for the other 5xx; the third row is
 * that `AuthApiError` shape at its lowest status, 500, and the next its 503 form.
 */
const AUTH_OUTAGES: readonly (readonly [string, AuthError])[] = [
	['a network failure', new AuthRetryableFetchError('fetch failed', 0)],
	['a retryable 503', new AuthRetryableFetchError('Service Unavailable', 503)],
	['an AuthApiError with status 500', new AuthApiError('internal error', 500, undefined)],
	['an AuthApiError with status 503', new AuthApiError('unavailable', 503, undefined)],
	['an AuthUnknownError', new AuthUnknownError('not JSON', new SyntaxError('Unexpected token'))],
];

/** The statuses below 500 that are all "the server answered and refused", edges included. */
const REFUSAL_STATUSES = [400, 403, 404, 422, 499] as const;

const OPTIONS = { skipBrowserRedirect: true } as const;

/**
 * Ids that no request can legitimately carry. The two dot segments would be resolved by the URL
 * parser rather than sent as a name, and a lone surrogate is what makes `encodeURIComponent` throw,
 * which would be a 500 where a refusal belongs.
 */
const UNUSABLE_IDS = [
	['a dot segment', '.'],
	['a parent dot segment', '..'],
	['a lone surrogate', 'abc\uD800'],
] as const;

/**
 * `'../../user'` is what the SDK, which puts the id into the URL path as it is, would resolve to
 * `/auth/v1/user` and send the person's access token to. Encoded, it is one path segment.
 */
const TRAVERSAL_ID = '../../user';
const TRAVERSAL_ID_ENCODED = '..%2F..%2Fuser';

/**
 * What `@supabase/ssr` does whenever `getUser` refreshes a session: a cookie lands in the `Headers`
 * the caller passed in, and nothing carries it onward but a flush. Called from each `describe`'s
 * own `beforeEach`, because the lint profile keeps hooks inside a `describe`.
 */
function arrangeFakeSupabase(): void {
	vi.resetAllMocks();
	mocks.createSupabase.mockImplementation(
		(_request: Request, headers: Readonly<Headers>): FakeSupabaseClient => {
			headers.append('set-cookie', SESSION_COOKIE);
			return {
				auth: {
					getUser: mocks.getUser,
					oauth: {
						approveAuthorization: mocks.approveAuthorization,
						denyAuthorization: mocks.denyAuthorization,
						getAuthorizationDetails: mocks.getAuthorizationDetails,
					},
				},
			};
		},
	);
	mocks.getUser.mockResolvedValue(SIGNED_IN);
}

describe('loadConsentFor', () => {
	const request = new Request('https://app.test/oauth/consent');

	beforeEach(arrangeFakeSupabase);

	it('answers unauthenticated without a session, and never asks for the details', async () => {
		mocks.getUser.mockResolvedValue(SIGNED_OUT);

		await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual({
			kind: 'unauthenticated',
		});

		expect(mocks.getAuthorizationDetails).not.toHaveBeenCalled();
	});

	it.each(SIGNED_OUT_ANSWERS)(
		'answers unauthenticated when getUser answers %s, and never asks for the details',
		async (_name, answer) => {
			mocks.getUser.mockResolvedValue(answer);

			await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual(
				{ kind: 'unauthenticated' },
			);

			expect(mocks.getAuthorizationDetails).not.toHaveBeenCalled();
		},
	);

	/**
	 * The auth server being down is not a signed-out person, and it is not an expired link either:
	 * it is a failure, which has to leave as an exception (the route's error boundary reports it
	 * to Sentry) and not as a state to render. The refreshed cookie still leaves, because the token
	 * it replaces is spent.
	 */
	it.each(AUTH_OUTAGES)(
		'rejects with the failure when getUser answers %s, asks for nothing more, and still flushes',
		async (_name, failure) => {
			mocks.getUser.mockResolvedValue({ data: { user: null }, error: failure });

			await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).rejects.toBe(failure);

			expect(mocks.getAuthorizationDetails).not.toHaveBeenCalled();
			expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
				SESSION_COOKIE,
			]);
		},
	);

	/**
	 * Review Focus 2: a missing or empty `authorization_id` is a state to render, not a failure,
	 * and it is answered before Supabase is even constructed.
	 */
	it('answers invalid for an empty authorization id, without any Supabase call', async () => {
		await expect(loadConsentFor(request, new Headers(), '')).resolves.toStrictEqual({
			kind: 'invalid',
		});

		expect(mocks.createSupabase).not.toHaveBeenCalled();
		expect(mocks.getUser).not.toHaveBeenCalled();
		expect(mocks.getAuthorizationDetails).not.toHaveBeenCalled();
	});

	it('answers invalid when Supabase answers the details with an error', async () => {
		mocks.getAuthorizationDetails.mockResolvedValue(SERVER_REFUSAL);

		await expect(loadConsentFor(request, new Headers(), 'unknown-id')).resolves.toStrictEqual({
			kind: 'invalid',
		});

		expect(mocks.getAuthorizationDetails).toHaveBeenCalledExactlyOnceWith('unknown-id');
	});

	it.each(REFUSAL_STATUSES)(
		'answers invalid when the details come back with an AuthApiError of status %i',
		async (status) => {
			mocks.getAuthorizationDetails.mockResolvedValue({
				data: null,
				error: new AuthApiError('refused', status, undefined),
			});

			await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual(
				{ kind: 'invalid' },
			);
		},
	);

	it('answers consent with the client name and the signed-in address', async () => {
		mocks.getAuthorizationDetails.mockResolvedValue(DETAILS);

		await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual({
			authorizationId: AUTHORIZATION_ID,
			clientName: 'kurze-url CLI',
			email: 'someone@example.test',
			kind: 'consent',
		});
	});

	/**
	 * Measured against the real server on 2026-10-23: a second authorize after a consent was given
	 * answers with `redirect_url` and no `authorization_id`, and the page has to go there at once.
	 */
	it('answers redirect when the person has consented already', async () => {
		mocks.getAuthorizationDetails.mockResolvedValue(ALREADY_CONSENTED);

		await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual({
			kind: 'redirect',
			url: REDIRECT_URL,
		});
	});

	/**
	 * Reading the session is what refreshes an expiring one, so every answer, the refusals
	 * included, has to carry the refreshed cookie out; without the flush the browser keeps the old
	 * refresh token and the next request signs the person out.
	 */
	it.each([
		['unauthenticated', SIGNED_OUT, DETAILS],
		['invalid', SIGNED_IN, SERVER_REFUSAL],
		['consent', SIGNED_IN, DETAILS],
		['redirect', SIGNED_IN, ALREADY_CONSENTED],
	] as const)('flushes the session cookies for %s', async (_kind, user, details) => {
		mocks.getUser.mockResolvedValue(user);
		mocks.getAuthorizationDetails.mockResolvedValue(details);

		await loadConsentFor(request, new Headers(), AUTHORIZATION_ID);

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			SESSION_COOKIE,
		]);
	});

	/**
	 * The SDK returns the auth server's own failures as `{ error }` instead of throwing them, so a
	 * Supabase outage would read as "the link has expired" and never reach Sentry. Anything that is
	 * not the server answering 4xx therefore leaves as an exception, which is a real failure and may
	 * reach Sentry; the refreshed session cookie still leaves, because the token it replaces is spent.
	 */
	it.each(AUTH_OUTAGES)(
		'rejects with the failure when the details come back with %s, and still flushes',
		async (_name, failure) => {
			mocks.getAuthorizationDetails.mockResolvedValue({ data: null, error: failure });

			await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).rejects.toBe(failure);

			expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
				SESSION_COOKIE,
			]);
		},
	);

	it.each(UNUSABLE_IDS)(
		'answers invalid for %s as the authorization id, without any Supabase call',
		async (_name, authorizationId) => {
			await expect(loadConsentFor(request, new Headers(), authorizationId)).resolves.toStrictEqual({
				kind: 'invalid',
			});

			expect(mocks.createSupabase).not.toHaveBeenCalled();
		},
	);

	/**
	 * The id comes from a URL the visitor can edit, and the SDK interpolates it into a request path
	 * with the visitor's access token attached.
	 */
	it('hands the SDK the authorization id encoded, so it cannot climb out of its path', async () => {
		mocks.getAuthorizationDetails.mockResolvedValue(SERVER_REFUSAL);

		await loadConsentFor(request, new Headers(), TRAVERSAL_ID);

		expect(mocks.getAuthorizationDetails).toHaveBeenCalledExactlyOnceWith(TRAVERSAL_ID_ENCODED);
	});

	/**
	 * What `/auth/v1/user` answers to the traversal above: neither the details nor a redirect. It
	 * must not become a `redirect` to `undefined`, which the page would hand to
	 * `window.location.assign`.
	 */
	it.each([
		['neither authorization_id nor redirect_url', { email: 'someone@example.test', id: 'user-1' }],
		['an empty redirect_url', { redirect_url: '' }],
		['an empty authorization_id', { ...DETAILS.data, authorization_id: '' }],
	] as const)('answers invalid when the details carry %s', async (_name, data) => {
		mocks.getAuthorizationDetails.mockResolvedValue({ data, error: null });

		await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).resolves.toStrictEqual({
			kind: 'invalid',
		});
	});
});

describe('decideConsentFor', () => {
	const request = new Request('https://app.test/oauth/consent');

	beforeEach(arrangeFakeSupabase);

	it('approves with skipBrowserRedirect and answers redirect to the CLI', async () => {
		mocks.approveAuthorization.mockResolvedValue({
			data: { redirect_url: REDIRECT_URL },
			error: null,
		});

		await expect(
			decideConsentFor(request, new Headers(), {
				action: 'approve',
				authorizationId: AUTHORIZATION_ID,
			}),
		).resolves.toStrictEqual({ kind: 'redirect', url: REDIRECT_URL });

		expect(mocks.approveAuthorization).toHaveBeenCalledExactlyOnceWith(AUTHORIZATION_ID, OPTIONS);
		expect(mocks.denyAuthorization).not.toHaveBeenCalled();
	});

	it('denies with skipBrowserRedirect and answers redirect to the CLI', async () => {
		const denied = 'http://127.0.0.1:53682/callback?error=access_denied&state=xyz';
		mocks.denyAuthorization.mockResolvedValue({ data: { redirect_url: denied }, error: null });

		await expect(
			decideConsentFor(request, new Headers(), {
				action: 'deny',
				authorizationId: AUTHORIZATION_ID,
			}),
		).resolves.toStrictEqual({ kind: 'redirect', url: denied });

		expect(mocks.denyAuthorization).toHaveBeenCalledExactlyOnceWith(AUTHORIZATION_ID, OPTIONS);
		expect(mocks.approveAuthorization).not.toHaveBeenCalled();
	});

	/**
	 * Review Focus 4: the session can lapse between loading the page and clicking. The decision
	 * must say so, so the page can send the person to `/login` with the same `next`, and neither
	 * Supabase call may be made without a session.
	 */
	it.each(['approve', 'deny'] as const)(
		'answers unauthenticated for %s without a session, and decides nothing',
		async (action) => {
			mocks.getUser.mockResolvedValue(SIGNED_OUT);

			await expect(
				decideConsentFor(request, new Headers(), { action, authorizationId: AUTHORIZATION_ID }),
			).resolves.toStrictEqual({ kind: 'unauthenticated' });

			expect(mocks.approveAuthorization).not.toHaveBeenCalled();
			expect(mocks.denyAuthorization).not.toHaveBeenCalled();
		},
	);

	it.each(SIGNED_OUT_ANSWERS)(
		'answers unauthenticated when getUser answers %s, and decides nothing',
		async (_name, answer) => {
			mocks.getUser.mockResolvedValue(answer);

			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'approve',
					authorizationId: AUTHORIZATION_ID,
				}),
			).resolves.toStrictEqual({ kind: 'unauthenticated' });

			expect(mocks.approveAuthorization).not.toHaveBeenCalled();
			expect(mocks.denyAuthorization).not.toHaveBeenCalled();
		},
	);

	it.each(AUTH_OUTAGES)(
		'rejects with the failure when getUser answers %s, decides nothing, and still flushes',
		async (_name, failure) => {
			mocks.getUser.mockResolvedValue({ data: { user: null }, error: failure });

			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'approve',
					authorizationId: AUTHORIZATION_ID,
				}),
			).rejects.toBe(failure);

			expect(mocks.approveAuthorization).not.toHaveBeenCalled();
			expect(mocks.denyAuthorization).not.toHaveBeenCalled();
			expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
				SESSION_COOKIE,
			]);
		},
	);

	it.each(['approve', 'deny'] as const)(
		'answers invalid when Supabase answers %s with an error',
		async (action) => {
			mocks.approveAuthorization.mockResolvedValue(SERVER_REFUSAL);
			mocks.denyAuthorization.mockResolvedValue(SERVER_REFUSAL);

			await expect(
				decideConsentFor(request, new Headers(), { action, authorizationId: AUTHORIZATION_ID }),
			).resolves.toStrictEqual({ kind: 'invalid' });
		},
	);

	it.each(REFUSAL_STATUSES)(
		'answers invalid when the decision comes back with an AuthApiError of status %i',
		async (status) => {
			const refusal = { data: null, error: new AuthApiError('refused', status, undefined) };
			mocks.approveAuthorization.mockResolvedValue(refusal);
			mocks.denyAuthorization.mockResolvedValue(refusal);

			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'approve',
					authorizationId: AUTHORIZATION_ID,
				}),
			).resolves.toStrictEqual({ kind: 'invalid' });
			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'deny',
					authorizationId: AUTHORIZATION_ID,
				}),
			).resolves.toStrictEqual({ kind: 'invalid' });
		},
	);

	/**
	 * Same rule as the load: the SDK hands the auth server's failure back as `{ error }`, and only
	 * a 4xx answer from it means the authorization is gone. A 5xx or a failed connection must not
	 * be shown as "the link has expired" after the person pressed Allow.
	 */
	it.each(AUTH_OUTAGES)(
		'rejects with the failure when approving comes back with %s, and still flushes',
		async (_name, failure) => {
			mocks.approveAuthorization.mockResolvedValue({ data: null, error: failure });

			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'approve',
					authorizationId: AUTHORIZATION_ID,
				}),
			).rejects.toBe(failure);

			expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
				SESSION_COOKIE,
			]);
		},
	);

	it.each(AUTH_OUTAGES)(
		'rejects with the failure when denying comes back with %s, and still flushes',
		async (_name, failure) => {
			mocks.denyAuthorization.mockResolvedValue({ data: null, error: failure });

			await expect(
				decideConsentFor(request, new Headers(), {
					action: 'deny',
					authorizationId: AUTHORIZATION_ID,
				}),
			).rejects.toBe(failure);

			expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
				SESSION_COOKIE,
			]);
		},
	);

	it.each(['approve', 'deny'] as const)(
		'answers invalid for %s with an empty authorization id, without any Supabase call',
		async (action) => {
			await expect(
				decideConsentFor(request, new Headers(), { action, authorizationId: '' }),
			).resolves.toStrictEqual({ kind: 'invalid' });

			expect(mocks.createSupabase).not.toHaveBeenCalled();
			expect(mocks.approveAuthorization).not.toHaveBeenCalled();
			expect(mocks.denyAuthorization).not.toHaveBeenCalled();
		},
	);

	it.each([
		['redirect', SIGNED_IN, { data: { redirect_url: REDIRECT_URL }, error: null }],
		['invalid', SIGNED_IN, SERVER_REFUSAL],
		['unauthenticated', SIGNED_OUT, SERVER_REFUSAL],
	] as const)('flushes the session cookies for %s', async (_kind, user, answer) => {
		mocks.getUser.mockResolvedValue(user);
		mocks.approveAuthorization.mockResolvedValue(answer);

		await decideConsentFor(request, new Headers(), {
			action: 'approve',
			authorizationId: AUTHORIZATION_ID,
		});

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			SESSION_COOKIE,
		]);
	});

	/**
	 * Denying is irreversible too (the CLI gets `access_denied` and the authorization is spent), so
	 * an action that is neither of the two is refused instead of being read as one of them.
	 */
	it.each(['', 'grant', 'APPROVE', 'approve '])(
		'answers invalid for the action %j, and decides nothing',
		async (action) => {
			await expect(
				decideConsentFor(request, new Headers(), { action, authorizationId: AUTHORIZATION_ID }),
			).resolves.toStrictEqual({ kind: 'invalid' });

			expect(mocks.createSupabase).not.toHaveBeenCalled();
			expect(mocks.approveAuthorization).not.toHaveBeenCalled();
			expect(mocks.denyAuthorization).not.toHaveBeenCalled();
		},
	);

	it.each(UNUSABLE_IDS)(
		'answers invalid for %s as the authorization id, without any Supabase call',
		async (_name, authorizationId) => {
			await expect(
				decideConsentFor(request, new Headers(), { action: 'approve', authorizationId }),
			).resolves.toStrictEqual({ kind: 'invalid' });

			expect(mocks.createSupabase).not.toHaveBeenCalled();
		},
	);

	it('hands the SDK the authorization id encoded when approving', async () => {
		mocks.approveAuthorization.mockResolvedValue(SERVER_REFUSAL);

		await decideConsentFor(request, new Headers(), {
			action: 'approve',
			authorizationId: TRAVERSAL_ID,
		});

		expect(mocks.approveAuthorization).toHaveBeenCalledExactlyOnceWith(
			TRAVERSAL_ID_ENCODED,
			OPTIONS,
		);
	});

	it('hands the SDK the authorization id encoded when denying', async () => {
		mocks.denyAuthorization.mockResolvedValue(SERVER_REFUSAL);

		await decideConsentFor(request, new Headers(), {
			action: 'deny',
			authorizationId: TRAVERSAL_ID,
		});

		expect(mocks.denyAuthorization).toHaveBeenCalledExactlyOnceWith(TRAVERSAL_ID_ENCODED, OPTIONS);
	});

	it.each([
		['no redirect_url', {}],
		['an empty redirect_url', { redirect_url: '' }],
	] as const)('answers invalid when the decision comes back with %s', async (_name, data) => {
		mocks.approveAuthorization.mockResolvedValue({ data, error: null });

		await expect(
			decideConsentFor(request, new Headers(), {
				action: 'approve',
				authorizationId: AUTHORIZATION_ID,
			}),
		).resolves.toStrictEqual({ kind: 'invalid' });
	});
});

/**
 * The server functions' validators. TanStack Start hands them whatever the request carried, typed
 * as the caller's own type, so each of them reads its input as `unknown` and settles on strings:
 * the `...For` functions then decide whether those strings mean anything.
 */
describe('readAuthorizationId', () => {
	it('reads the authorization id of well-formed input', () => {
		expect(readAuthorizationId({ authorizationId: AUTHORIZATION_ID })).toBe(AUTHORIZATION_ID);
	});

	it.each([
		['no input', undefined],
		['null', null],
		['a string', AUTHORIZATION_ID],
		['an object without the field', {}],
		['a number', { authorizationId: 42 }],
		['an object', { authorizationId: { toString: () => AUTHORIZATION_ID } }],
		['null in the field', { authorizationId: null }],
	] as const)('settles on an empty string for %s', (_name, input) => {
		expect(readAuthorizationId(input)).toBe('');
	});
});

describe('readDecision', () => {
	it('reads the action and the authorization id of well-formed input', () => {
		expect(readDecision({ action: 'deny', authorizationId: AUTHORIZATION_ID })).toStrictEqual({
			action: 'deny',
			authorizationId: AUTHORIZATION_ID,
		});
	});

	it('leaves an unknown action for decideConsentFor to refuse', () => {
		expect(readDecision({ action: 'grant', authorizationId: AUTHORIZATION_ID })).toStrictEqual({
			action: 'grant',
			authorizationId: AUTHORIZATION_ID,
		});
	});

	it.each([
		['no input', undefined],
		['null', null],
		['a string', 'approve'],
		['an empty object', {}],
		['a number for each field', { action: 1, authorizationId: 2 }],
	] as const)('settles on empty strings for %s', (_name, input) => {
		expect(readDecision(input)).toStrictEqual({ action: '', authorizationId: '' });
	});
});
