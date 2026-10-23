import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

/**
 * Deliberately not the real `SupabaseClient` shape: only the slice the callback reaches through,
 * for the same reason `server/auth.test.ts` types its fake that way.
 */
interface FakeSupabaseClient {
	auth: {
		exchangeCodeForSession: (code: string) => Promise<{ error: { message: string } | null }>;
	};
}

const mocks = vi.hoisted(() => ({
	appendResponseHeader: vi.fn(),
	exchangeCodeForSession: vi.fn<FakeSupabaseClient['auth']['exchangeCodeForSession']>(),
	getRequest: vi.fn<() => Request>(),
}));

vi.mock('../server/supabase', () => ({
	createSupabase: (): FakeSupabaseClient => ({
		auth: { exchangeCodeForSession: mocks.exchangeCodeForSession },
	}),
}));

vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
	getRequest: mocks.getRequest,
}));

/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
 * register their replacements, which a module-scope `await import` is what expresses.
 */
const { exchangeCode, redirectAfterSignIn } = await import('./auth.callback');

/**
 * Where a redirect points, as it was given to `redirect`. `unknown` fields: the router types a
 * redirect's options loosely, and what is asserted on is their value, not their type.
 */
interface RedirectTarget {
	readonly href: unknown;
	readonly search: unknown;
	readonly to: unknown;
}

/**
 * Runs what the loader runs, minus the server-function hop that needs a Start context: the code
 * exchange for a request to `url`, then the redirect its outcome calls for. Navigation in TanStack
 * Router is a redirect the loader throws, and this reports where it points.
 *
 * @param url - The callback URL the magic link opened, with its query string.
 * @returns The redirect's `href`, `search` and `to`.
 */
async function redirectOf(url: string): Promise<RedirectTarget> {
	mocks.getRequest.mockReturnValue(new Request(url));
	const { options } = redirectAfterSignIn(await exchangeCode());
	return { href: options.href, search: options.search, to: options.to };
}

/**
 * @param url - The callback URL the magic link opened, with its query string.
 * @returns What the loader redirects to: an `href`, or a `to`, as it was given to `redirect`.
 */
async function destinationOf(url: string): Promise<unknown> {
	const { href, to } = await redirectOf(url);
	return href ?? to;
}

const CONSENT_NEXT = '/oauth/consent?authorization_id=abc';

/** A `next` of each kind `isSafeNext` refuses, by what it would point to. */
const UNSAFE_NEXTS = [
	['another site', 'https://evil.example'],
	['an off-site path that starts with two slashes', '//evil.example/oauth/consent?x'],
	['a path of this app that is not the consent page', '/links'],
	['an empty value', ''],
	// `redirect()` cannot put a character above U+00FF in a `Location` header: it throws, after
	// the session was created, so such a value has to be refused before it gets that far.
	['a path with a character above U+00FF', '/oauth/consent?authorization_id=abc&label=€'],
	['a path with an emoji', '/oauth/consent?authorization_id=abc&label=\u{1F600}'],
] as const;

const ORIGIN = 'https://app.test';

describe('the auth callback', () => {
	beforeEach(() => {
		mocks.appendResponseHeader.mockClear();
		mocks.exchangeCodeForSession.mockReset();
	});

	/**
	 * A CLI login's consent page needs a session, and the magic link is how it gets one. The visitor
	 * must land back on the consent URL they started from, not on `/`.
	 */
	it('sends a successful sign-in on to a safe next', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });
		const next = encodeURIComponent(CONSENT_NEXT);

		await expect(destinationOf(`${ORIGIN}/auth/callback?code=c1&next=${next}`)).resolves.toBe(
			CONSENT_NEXT,
		);
		expect(mocks.exchangeCodeForSession).toHaveBeenCalledExactlyOnceWith('c1');
	});

	it('sends a successful sign-in with no next to the home page', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });

		await expect(destinationOf(`${ORIGIN}/auth/callback?code=c1`)).resolves.toBe('/');
	});

	// The query string is the visitor's to write, so `next` is judged here again, not only when the
	// mail is sent: this is the open-redirect guard's second and decisive check.
	it.each(UNSAFE_NEXTS)('ignores a next that points to %s', async (_name, next) => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });

		await expect(
			destinationOf(`${ORIGIN}/auth/callback?code=c1&next=${encodeURIComponent(next)}`),
		).resolves.toBe('/');
	});

	// Raw bytes that are not UTF-8 (a lone surrogate, `%ED%A0%80`) never reach `isSafeNext` as they
	// were written: `URLSearchParams` decodes them to U+FFFD, which the guard refuses like any other
	// non-ASCII character.
	it('ignores a next whose raw bytes are not valid UTF-8', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });

		await expect(
			destinationOf(`${ORIGIN}/auth/callback?code=c1&next=%2Foauth%2Fconsent%3Fx%3D%ED%A0%80`),
		).resolves.toBe('/');
	});

	/**
	 * The failed exchange and the missing code are the two ways in without a session. Neither may
	 * follow `next`, but both carry a safe one back to `/login`: a magic link opened in another
	 * browser has no PKCE verifier there, so its exchange fails, and without `next` the second
	 * attempt would end on `/` while the CLI waits for the consent page. The login page only hands
	 * `next` on to the next magic link, where both guards judge it again.
	 *
	 * Every reason an exchange fails lands on the same URL, so the redirect is no enumeration
	 * oracle: a reused code, an expired one and a missing verifier look alike from here.
	 */
	it.each([
		'invalid flow state, no valid flow state found',
		'code challenge does not match previously saved code verifier',
		'Email link is invalid or has expired',
	])('sends a failed exchange (%s) to the login page with a safe next', async (message) => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: { message } });
		const next = encodeURIComponent(CONSENT_NEXT);

		await expect(redirectOf(`${ORIGIN}/auth/callback?code=c1&next=${next}`)).resolves.toStrictEqual(
			{ href: undefined, search: { next: CONSENT_NEXT }, to: '/login' },
		);
	});

	it('sends a callback with no code to the login page with a safe next', async () => {
		const next = encodeURIComponent(CONSENT_NEXT);

		await expect(redirectOf(`${ORIGIN}/auth/callback?next=${next}`)).resolves.toStrictEqual({
			href: undefined,
			search: { next: CONSENT_NEXT },
			to: '/login',
		});
		expect(mocks.exchangeCodeForSession).not.toHaveBeenCalled();
	});

	it.each(UNSAFE_NEXTS)(
		'sends a failed exchange to the login page without a next that points to %s',
		async (_name, next) => {
			mocks.exchangeCodeForSession.mockResolvedValue({ error: { message: 'bad code' } });

			await expect(
				redirectOf(`${ORIGIN}/auth/callback?code=c1&next=${encodeURIComponent(next)}`),
			).resolves.toStrictEqual({ href: undefined, search: undefined, to: '/login' });
		},
	);

	it('sends a failed exchange with no next to the login page', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: { message: 'bad code' } });

		await expect(redirectOf(`${ORIGIN}/auth/callback?code=c1`)).resolves.toStrictEqual({
			href: undefined,
			search: undefined,
			to: '/login',
		});
	});
});
