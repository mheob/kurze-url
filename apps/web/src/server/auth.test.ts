import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice
 * `sendMagicLinkFor` actually reaches through. Typing the mock this way,
 * rather than casting an object literal through `unknown` to the real type,
 * is what keeps this file free of both `no-unsafe-type-assertion` and a
 * `globalThis` test seam: session.test.ts settled on `vi.hoisted` plus a
 * narrowly-typed fake client for the same reason, after a `globalThis` seam
 * in production code was rejected in review in favour of exporting a plain
 * function.
 */
interface FakeSupabaseClient {
	auth: {
		signInWithOtp: (options: {
			readonly email: string;
			readonly options: { readonly shouldCreateUser: boolean; readonly emailRedirectTo: string };
		}) => Promise<{ error: { message: string } | null }>;
	};
}

const mocks = vi.hoisted(() => ({
	// Only the flush test below inspects what this was given.
	appendResponseHeader: vi.fn(),
	signInWithOtp: vi.fn<FakeSupabaseClient['auth']['signInWithOtp']>(),
}));

vi.mock('./supabase', () => ({
	// Simulates the one thing this file's flush test needs from the real
	// adapter: @supabase/ssr writes the PKCE code verifier cookie into
	// `headers` synchronously, before `signInWithOtp`'s HTTP call even
	// resolves (see Finding 1's own reasoning in `auth.ts`).
	/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `_request` is `Request`,
	 * which nests a mutable `Headers` through its own `.headers` getter; `Readonly<>` is shallow
	 * and doesn't reach that nested property, unlike the bare `Headers` parameter beside it, which
	 * the check accepts once wrapped.
	 */
	createSupabase: (_request: Request, headers: Readonly<Headers>): FakeSupabaseClient => {
		headers.append('set-cookie', 'sb-pkce-code-verifier=abc; Path=/; HttpOnly');
		return { auth: { signInWithOtp: mocks.signInWithOtp } };
	},
}));

vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
}));

/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
 * register their replacements, which a module-scope `await import` is what expresses.
 */
const { ENUMERATION_TIMING_FLOOR_MS, sendMagicLinkFor } = await import('./auth');

describe('sendMagicLinkFor', () => {
	// Call history has to start empty in every test: the mocked `createSupabase` writes the same
	// refreshed cookie on every call, so without this an assertion on it would be satisfied by a
	// call an earlier test made.
	beforeEach(() => {
		mocks.appendResponseHeader.mockClear();
	});

	it('never creates an account', async () => {
		// This instance is invitation-only: MAINTAINER_USER_IDS gates team
		// creation and members arrive through inviteUserByEmail. Left at its
		// default, signInWithOtp would mint an auth.users row for any address
		// anyone typed — a self-service signup door opened by accident.
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor('someone@example.test', 'https://app.test');

		expect(mocks.signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				/* oxlint-disable-next-line typescript/no-unsafe-assignment -- vitest types
				 * `expect.objectContaining`'s return as `any`, so assigning it into a typed
				 * object literal is what every use of this matcher looks like.
				 */
				options: expect.objectContaining({ shouldCreateUser: false }),
			}),
		);
	});

	/**
	 * A CLI login sends a signed-out visitor to `/login?next=<consent path>`, and the magic link
	 * has to bring them back there. `next` rides in the callback URL Supabase is asked to put
	 * in the mail, encoded as one query value.
	 */
	it('puts a safe next into the callback URL', async () => {
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor(
			'someone@example.test',
			'https://app.test',
			'/oauth/consent?authorization_id=abc',
		);

		const next = encodeURIComponent('/oauth/consent?authorization_id=abc');
		expect(mocks.signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				/* oxlint-disable-next-line typescript/no-unsafe-assignment -- vitest types
				 * `expect.objectContaining`'s return as `any`, so assigning it into a typed
				 * object literal is what every use of this matcher looks like.
				 */
				options: expect.objectContaining({
					emailRedirectTo: `https://app.test/auth/callback?next=${next}`,
				}),
			}),
		);
	});

	/**
	 * The guard runs on the way out as well as on the way back (`/auth/callback`): a value that
	 * would be refused there has no business in a mail either, and a lone surrogate would make
	 * `encodeURIComponent` throw, which is a 500 where a refusal belongs.
	 */
	it.each([
		['an off-site address', '//evil.example'],
		['an absolute URL', 'https://evil.example/oauth/consent?x'],
		['a lone surrogate', '/oauth/consent?authorization_id=\uD800'],
	])('drops an unsafe next: %s', async (_name, next) => {
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await expect(
			sendMagicLinkFor('someone@example.test', 'https://app.test', next),
		).resolves.toStrictEqual({ sent: true });

		expect(mocks.signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				/* oxlint-disable-next-line typescript/no-unsafe-assignment -- vitest types
				 * `expect.objectContaining`'s return as `any`, so assigning it into a typed
				 * object literal is what every use of this matcher looks like.
				 */
				options: expect.objectContaining({ emailRedirectTo: 'https://app.test/auth/callback' }),
			}),
		);
	});

	it('asks for the plain callback URL when there is no next', async () => {
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor('someone@example.test', 'https://app.test');

		expect(mocks.signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				/* oxlint-disable-next-line typescript/no-unsafe-assignment -- vitest types
				 * `expect.objectContaining`'s return as `any`, so assigning it into a typed
				 * object literal is what every use of this matcher looks like.
				 */
				options: expect.objectContaining({ emailRedirectTo: 'https://app.test/auth/callback' }),
			}),
		);
	});

	it('reports the same result for an unknown address as a known one', async () => {
		// With account creation off, Supabase distinguishes known from unknown.
		// The UI must not: otherwise the login form becomes an oracle for
		// which addresses belong to a Verein.
		mocks.signInWithOtp.mockResolvedValue({ error: null });
		const known = await sendMagicLinkFor('known@example.test', 'https://app.test');

		mocks.signInWithOtp.mockResolvedValue({ error: { message: 'Signups not allowed for otp' } });
		const unknown = await sendMagicLinkFor('unknown@example.test', 'https://app.test');

		expect(unknown).toStrictEqual(known);
		expect(unknown).toStrictEqual({ sent: true });
	});

	it('holds a fixed latency floor on the fast-fail (unknown-address) path', async () => {
		// Real timers would make this suite slower by the floor on every run;
		// fake timers let us prove the wait happens without paying for it.
		vi.useFakeTimers();
		try {
			// The fast-fail case: Supabase rejects locally with no SMTP
			// round-trip, so the mocked call already resolves on the next
			// microtask tick, before any timer fires. Without a floor, that
			// would let the response leave immediately — the timing gap this
			// test exists to close.
			mocks.signInWithOtp.mockResolvedValue({
				error: { message: 'Signups not allowed for otp' },
			});

			let settled = false;
			/* oxlint-disable-next-line promise/prefer-await-to-then -- the point of this test is to
			 * observe whether the promise has settled *before* awaiting it; `await`ing it here would
			 * block until it resolves, which is exactly the premature-settling this test exists to
			 * catch. `.then()` attaches the marker without holding up the assertions below it.
			 */
			const pending = sendMagicLinkFor('unknown@example.test', 'https://app.test').then(
				(result: Readonly<{ sent: true }>) => {
					settled = true;
					return result;
				},
			);

			// Let the already-resolved signInWithOtp promise flush without
			// advancing the clock at all.
			await Promise.resolve();
			await Promise.resolve();
			expect(settled).toBe(false);

			await vi.advanceTimersByTimeAsync(ENUMERATION_TIMING_FLOOR_MS - 1);
			expect(settled).toBe(false);

			await vi.advanceTimersByTimeAsync(1);
			expect(settled).toBe(true);
			await expect(pending).resolves.toStrictEqual({ sent: true });
		} finally {
			vi.useRealTimers();
		}
	});

	/**
	 * Finding 1: `sendMagicLinkFor` built a `Headers` object, let
	 * `signInWithOtp` write the PKCE verifier cookie into it, and then
	 * discarded it — the verifier never reached the browser, so
	 * `/auth/callback`'s `exchangeCodeForSession` had nothing to consume and
	 * login was broken end to end. This test fails on exactly that: with the
	 * `flushSessionCookies(headers)` call removed, `appendResponseHeader` is
	 * never called because nothing ever forwards the cookie the mocked
	 * `createSupabase` wrote onto the real response.
	 */
	it('flushes the PKCE verifier cookie onto the real response', async () => {
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor('someone@example.test', 'https://app.test');

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'sb-pkce-code-verifier=abc; Path=/; HttpOnly',
		]);
	});
});
