import { describe, expect, it, vi } from 'vitest';

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
			email: string;
			options: { shouldCreateUser: boolean; emailRedirectTo: string };
		}) => Promise<{ error: { message: string } | null }>;
	};
}

const mocks = vi.hoisted(() => ({
	signInWithOtp: vi.fn<FakeSupabaseClient['auth']['signInWithOtp']>(),
}));

vi.mock('./supabase', () => ({
	createSupabase: (): FakeSupabaseClient => ({ auth: { signInWithOtp: mocks.signInWithOtp } }),
}));

const { ENUMERATION_TIMING_FLOOR_MS, sendMagicLinkFor } = await import('./auth');

describe('sendMagicLinkFor', () => {
	it('never creates an account', async () => {
		// This instance is invitation-only: MAINTAINER_USER_IDS gates team
		// creation and members arrive through inviteUserByEmail. Left at its
		// default, signInWithOtp would mint an auth.users row for any address
		// anyone typed — a self-service signup door opened by accident.
		mocks.signInWithOtp.mockResolvedValue({ error: null });

		await sendMagicLinkFor('someone@example.test', 'https://app.test');

		expect(mocks.signInWithOtp).toHaveBeenCalledWith(
			expect.objectContaining({
				options: expect.objectContaining({ shouldCreateUser: false }),
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

		expect(unknown).toEqual(known);
		expect(unknown).toEqual({ sent: true });
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
			const pending = sendMagicLinkFor('unknown@example.test', 'https://app.test').then(
				(result) => {
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
			await expect(pending).resolves.toEqual({ sent: true });
		} finally {
			vi.useRealTimers();
		}
	});
});
