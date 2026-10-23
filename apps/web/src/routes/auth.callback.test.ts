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
 * Runs what the loader runs, minus the server-function hop that needs a Start context: the code
 * exchange for a request to `url`, then the redirect its outcome calls for. Navigation in TanStack
 * Router is a redirect the loader throws, and this reports where it points.
 *
 * @param url - The callback URL the magic link opened, with its query string.
 * @returns What the loader redirects to: an `href`, or a `to`, as it was given to `redirect`.
 */
async function destinationOf(url: string): Promise<string | undefined> {
	mocks.getRequest.mockReturnValue(new Request(url));
	const { options } = redirectAfterSignIn(await exchangeCode());
	return options.href ?? options.to;
}

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
		const next = encodeURIComponent('/oauth/consent?authorization_id=abc');

		await expect(destinationOf(`${ORIGIN}/auth/callback?code=c1&next=${next}`)).resolves.toBe(
			'/oauth/consent?authorization_id=abc',
		);
		expect(mocks.exchangeCodeForSession).toHaveBeenCalledExactlyOnceWith('c1');
	});

	it('sends a successful sign-in with no next to the home page', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });

		await expect(destinationOf(`${ORIGIN}/auth/callback?code=c1`)).resolves.toBe('/');
	});

	// The query string is the visitor's to write, so `next` is judged here again, not only when the
	// mail is sent: this is the open-redirect guard's second and decisive check.
	it.each([
		['another site', 'https://evil.example'],
		['an off-site path that starts with two slashes', '//evil.example/oauth/consent?x'],
		['a path of this app that is not the consent page', '/links'],
		['an empty value', ''],
	])('ignores a next that points to %s', async (_name, next) => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: null });

		await expect(
			destinationOf(`${ORIGIN}/auth/callback?code=c1&next=${encodeURIComponent(next)}`),
		).resolves.toBe('/');
	});

	// The failed exchange and the missing code are the two ways in without a session: neither may
	// honour `next`, and neither may say which one it was.
	it('sends a failed exchange to the login page whatever next says', async () => {
		mocks.exchangeCodeForSession.mockResolvedValue({ error: { message: 'bad code' } });
		const next = encodeURIComponent('/oauth/consent?authorization_id=abc');

		await expect(destinationOf(`${ORIGIN}/auth/callback?code=c1&next=${next}`)).resolves.toBe(
			'/login',
		);
	});

	it('sends a callback with no code to the login page whatever next says', async () => {
		const next = encodeURIComponent('/oauth/consent?authorization_id=abc');

		await expect(destinationOf(`${ORIGIN}/auth/callback?next=${next}`)).resolves.toBe('/login');
		expect(mocks.exchangeCodeForSession).not.toHaveBeenCalled();
	});
});
