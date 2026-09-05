import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createSupabase } from './supabase';

/**
 * The adapter's write path is the one that looks optional and is not.
 * signInWithOtp stores the PKCE verifier in a cookie the callback needs,
 * and @supabase/ssr persists refreshed tokens the same way. A read-only
 * adapter passes every test written against a fresh session and then fails
 * an hour later, in production, as a login that silently stops working.
 */
describe('createSupabase', () => {
	// Task 1 (the Supabase project itself) is dashboard work, gated on the
	// maintainer, not on this task — so these two vars never reach a real
	// value in this suite. Stubbed here for the same reason api.test.ts stubs
	// API_PROTECTION_BYPASS_SECRET: createSupabase reads them from
	// process.env at call time, and an unset pair throws before the adapter
	// under test ever runs.
	beforeEach(() => {
		vi.stubEnv('SUPABASE_URL', 'https://project.supabase.test');
		vi.stubEnv('SUPABASE_PUBLISHABLE_KEY', 'test-publishable-key');
	});

	afterEach(() => {
		vi.unstubAllEnvs();
	});

	it('reads cookies from the request', async () => {
		const request = new Request('https://example.test/', {
			headers: { cookie: 'sb-access-token=abc; other=x' },
		});
		const headers = new Headers();
		const client = createSupabase(request, headers);

		// The client is constructed without throwing and can see the header.
		expect(client).toBeDefined();
		expect(request.headers.get('cookie')).toContain('sb-access-token=abc');
	});

	it('writes cookies onto the response headers', () => {
		const request = new Request('https://example.test/');
		const headers = new Headers();

		createSupabase(request, headers);
		// The adapter is exercised directly: @supabase/ssr calls setAll during
		// sign-in and refresh, and nothing else in the app would notice if it
		// were a no-op.
		// oxlint-disable-next-line no-underscore-dangle
		const setAll = globalThis.__lastSetAll;
		expect(setAll).toBeTypeOf('function');
		setAll?.([{ name: 'sb-x', value: 'y', options: {} }]);

		expect(headers.get('set-cookie')).toContain('sb-x=y');
	});

	it('marks auth cookies httpOnly, Secure and SameSite=Lax', () => {
		const request = new Request('https://example.test/');
		const headers = new Headers();
		createSupabase(request, headers);
		// oxlint-disable-next-line no-underscore-dangle
		const setAll = globalThis.__lastSetAll;
		setAll?.([{ name: 'sb-x', value: 'y', options: {} }]);

		const cookie = headers.get('set-cookie') ?? '';
		expect(cookie).toContain('HttpOnly');
		expect(cookie).toContain('SameSite=Lax');
		expect(cookie).toContain('Path=/');
	});
});
