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
		signOut: (options: {
			readonly scope: 'global' | 'local';
		}) => Promise<{ error: { message: string; status: number } | null }>;
	};
}

/** What `signOut` returns when the revoke call fails: an `AuthError` carries a message and a status. */
const REVOKE_REFUSED = { message: 'Too Many Requests', status: 429 };

/** What the fake `signOut` writes into the `Headers` it was built with: the session cookie, cleared. */
const CLEARED_SESSION_COOKIE = 'sb-session=; Max-Age=0; Path=/';

const mocks = vi.hoisted(() => ({
	// Only the flush test below inspects what this was given.
	appendResponseHeader: vi.fn(),
	signInWithOtp: vi.fn<FakeSupabaseClient['auth']['signInWithOtp']>(),
	signOut: vi.fn<FakeSupabaseClient['auth']['signOut']>(),
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
		return {
			auth: {
				signInWithOtp: mocks.signInWithOtp,
				// Like the real client, the sign-out clears the session cookies through the cookie
				// adapter, which lands them in the `Headers` this was built with.
				signOut: async (options) => {
					headers.append('set-cookie', CLEARED_SESSION_COOKIE);
					return mocks.signOut(options);
				},
			},
		};
	},
}));

vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
}));

/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
 * register their replacements, which a module-scope `await import` is what expresses.
 */
const auth = await import('./auth');
const { ENUMERATION_TIMING_FLOOR_MS, readSignOutScope, sendMagicLinkFor, signOutFor } = auth;

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
		['a non-ASCII character', '/oauth/consent?authorization_id=abc&label=\u20AC'],
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

/**
 * Signs out through `signOutFor` the way the server function does: a request, a fresh
 * `Headers`, and the scope the validator settled on.
 *
 * @param scope - The scope to sign out with.
 * @returns What `signOutFor` returns.
 */
async function signOutWith(scope: 'global' | 'local'): Promise<void> {
	await signOutFor(new Request('https://app.test'), new Headers(), scope);
}

describe('signOutFor', () => {
	// The mocked `createSupabase` writes the same cookies on every call, so call history has to
	// start empty in every test, as in the suite above.
	beforeEach(() => {
		mocks.appendResponseHeader.mockClear();
		mocks.signOut.mockReset();
		mocks.signOut.mockResolvedValue({ error: null });
	});

	/**
	 * A browser sign-out with `local` leaves a CLI session alive and one with `global` ends it too
	 * (measured 2026-10-23, `docs/superpowers/specs/2026-10-23-cli-design.md`, "Verified"), so the
	 * scope the page asked for is the one that must reach Supabase, not the SDK's own default.
	 */
	it('signs out this session only when asked for local', async () => {
		await signOutWith('local');

		expect(mocks.signOut).toHaveBeenCalledExactlyOnceWith({ scope: 'local' });
	});

	it('signs out every session when asked for global', async () => {
		await signOutWith('global');

		expect(mocks.signOut).toHaveBeenCalledExactlyOnceWith({ scope: 'global' });
	});

	/**
	 * `auth-js`'s `_signOut` clears the local session and still returns `{ error }` when the revoke
	 * call fails with anything but a 401, 403 or 404 (a 429, a 5xx, a network failure). For
	 * `global` that is a sign-out that did not do what it promised: the other sessions, the CLI's
	 * among them, are still alive. So it must not look like success, and the cleared cookies must
	 * not be flushed either, or the browser would drop a session the server still honours.
	 */
	it('rejects, and flushes nothing, when a global sign-out is refused', async () => {
		mocks.signOut.mockResolvedValue({ error: REVOKE_REFUSED });

		await expect(signOutWith('global')).rejects.toThrow('global sign-out failed');

		expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
	});

	/**
	 * `local` promises only that this browser's session is gone, and the SDK has cleared it whatever
	 * the revoke call answered, so the error changes nothing: the page signs out and the cookies go.
	 */
	it('still signs out, and flushes, when a local sign-out is refused', async () => {
		mocks.signOut.mockResolvedValue({ error: REVOKE_REFUSED });

		await expect(signOutWith('local')).resolves.toBeUndefined();

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'sb-pkce-code-verifier=abc; Path=/; HttpOnly',
			CLEARED_SESSION_COOKIE,
		]);
	});

	it('flushes the cleared session cookies after a successful global sign-out', async () => {
		await signOutWith('global');

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'sb-pkce-code-verifier=abc; Path=/; HttpOnly',
			CLEARED_SESSION_COOKIE,
		]);
	});

	/**
	 * The cleared session cookies land in the local `Headers`, and only `flushSessionCookies`
	 * carries them onto the real response. Without it the handler runs, the client sees a 200, and
	 * the browser keeps sending a session cookie that is still good: a sign-out that signs nobody
	 * out.
	 */
	it('flushes the cleared session cookies onto the real response', async () => {
		await signOutWith('local');

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'sb-pkce-code-verifier=abc; Path=/; HttpOnly',
			CLEARED_SESSION_COOKIE,
		]);
	});
});

describe('readSignOutScope', () => {
	it.each([
		['no data at all', undefined],
		['null', null],
		['data without a scope', {}],
		['a scope of undefined', { scope: undefined }],
	])('defaults to local for %s', (_name: string, data: unknown) => {
		expect(readSignOutScope(data)).toStrictEqual({ scope: 'local' });
	});

	it('keeps a scope of local', () => {
		expect(readSignOutScope({ scope: 'local' })).toStrictEqual({ scope: 'local' });
	});

	it('keeps a scope of global', () => {
		expect(readSignOutScope({ scope: 'global' })).toStrictEqual({ scope: 'global' });
	});

	/**
	 * `global` is the one scope that ends sessions the person did not sign out of here, so it is
	 * only ever the exact string. Supabase has a third scope, `others`, which would end every
	 * session but this one; a crafted request must not reach it, and a mistyped one must not reach
	 * `global` by accident.
	 */
	it.each([
		['the third Supabase scope', 'others'],
		['a different case', 'GLOBAL'],
		['a padded value', ' global'],
		['a number', 1],
		['a boolean', true],
		['an object', { scope: 'global' }],
	])('treats %s as local', (_name: string, scope: unknown) => {
		expect(readSignOutScope({ scope })).toStrictEqual({ scope: 'local' });
	});
});
