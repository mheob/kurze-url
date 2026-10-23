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
 * shape it is about.
 */
interface FakeResult {
	readonly data: Readonly<Record<string, unknown>> | null;
	readonly error: Readonly<{ message: string }> | null;
}

interface FakeDecisionOptions {
	readonly skipBrowserRedirect: boolean;
}

interface FakeSupabaseClient {
	readonly auth: {
		readonly getUser: () => Promise<{
			readonly data: { readonly user: Readonly<{ id: string }> | null };
		}>;
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

const SERVER_REFUSAL: FakeResult = { data: null, error: { message: 'authorization not found' } };

const SIGNED_IN = { data: { user: { id: 'user-1' } } } as const;
const SIGNED_OUT = { data: { user: null } } as const;

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
	 * A thrown exception is a real failure and may reach Sentry, so it is not turned into
	 * `invalid`; the refreshed session cookie still leaves, though, because the token it replaces
	 * is spent.
	 */
	it('lets an exception from Supabase through, and still flushes the cookies', async () => {
		const failure = new Error('network down');
		mocks.getAuthorizationDetails.mockRejectedValue(failure);

		await expect(loadConsentFor(request, new Headers(), AUTHORIZATION_ID)).rejects.toBe(failure);

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			SESSION_COOKIE,
		]);
	});

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

	it('lets an exception from Supabase through, and still flushes the cookies', async () => {
		const failure = new Error('network down');
		mocks.denyAuthorization.mockRejectedValue(failure);

		await expect(
			decideConsentFor(request, new Headers(), {
				action: 'deny',
				authorizationId: AUTHORIZATION_ID,
			}),
		).rejects.toBe(failure);

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
