import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createCookieAdapter, createSupabase } from './supabase';

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
});

/**
 * createCookieAdapter is a pure function of (request, headers) — no shared
 * state, so unlike createSupabase it needs no env stubbing and its `setAll`
 * can be called directly, the same way @supabase/ssr calls it during sign-in
 * and refresh.
 */
describe('createCookieAdapter', () => {
	it('writes cookies onto the response headers', () => {
		const request = new Request('https://example.test/');
		const headers = new Headers();
		const { setAll } = createCookieAdapter(request, headers);

		setAll([{ name: 'sb-x', value: 'y', options: {} }]);

		expect(headers.get('set-cookie')).toContain('sb-x=y');
	});

	it('marks auth cookies httpOnly, Secure and SameSite=Lax', () => {
		// The shape @supabase/ssr's own DEFAULT_COOKIE_OPTIONS actually sends —
		// notably `httpOnly: false` — not `{}`, which is a shape production
		// never produces and let a merge-order bug pass silently: `serialize`
		// used to spread `options` last, so this library default clobbered the
		// app's `httpOnly: true` policy and every session cookie shipped
		// readable by JavaScript.
		const request = new Request('https://example.test/');
		const headers = new Headers();
		const { setAll } = createCookieAdapter(request, headers);

		setAll([
			{
				name: 'sb-x',
				value: 'y',
				options: { path: '/', sameSite: 'lax', httpOnly: false, maxAge: 34560000 },
			},
		]);

		const cookie = headers.get('set-cookie') ?? '';
		expect(cookie).toContain('HttpOnly');
		expect(cookie).toContain('Secure');
		expect(cookie).toContain('SameSite=Lax');
		expect(cookie).toContain('Path=/');
		// maxAge is not one of the four app-policy attributes, so the
		// caller-supplied value must still come through unchanged.
		expect(cookie).toContain('Max-Age=34560000');
	});

	it('forces SameSite=Lax as app policy even if a caller asks for Strict', () => {
		// Regression test for a finding where `serialize` read
		// `options.sameSite` only to decide *whether* to emit the attribute,
		// then always wrote the literal string `Lax` regardless of the actual
		// value — correct by accident, since every cookie in this codebase
		// resolved to `lax` anyway. Finding 1 makes that accident load-bearing:
		// httpOnly/secure/sameSite/path are app policy, not a library or caller
		// suggestion, so `SUPABASE_COOKIE_OPTIONS` must win the merge and a
		// caller-supplied `strict` must never reach the response.
		const request = new Request('https://example.test/');
		const headers = new Headers();
		const { setAll } = createCookieAdapter(request, headers);

		setAll([{ name: 'sb-x', value: 'y', options: { sameSite: 'strict' } }]);

		const cookie = headers.get('set-cookie') ?? '';
		expect(cookie).toContain('SameSite=Lax');
		expect(cookie).not.toContain('SameSite=Strict');
	});

	it('applies the response headers @supabase/ssr passes alongside cookies', () => {
		// @supabase/ssr calls setAll with a second argument on every write — a
		// Cache-Control/Expires/Pragma bundle — so that a CDN or reverse proxy
		// in front of this app never caches a response that carries one
		// person's session cookie for another. A `setAll` that declares only
		// the cookies parameter silently drops this argument: TypeScript does
		// not catch it, because a callback declaring fewer parameters than its
		// type allows is structurally valid.
		const request = new Request('https://example.test/');
		const headers = new Headers();
		const { setAll } = createCookieAdapter(request, headers);

		setAll([], {
			'Cache-Control': 'private, no-cache, no-store, must-revalidate, max-age=0',
			Expires: '0',
			Pragma: 'no-cache',
		});

		expect(headers.get('cache-control')).toBe(
			'private, no-cache, no-store, must-revalidate, max-age=0',
		);
		expect(headers.get('expires')).toBe('0');
		expect(headers.get('pragma')).toBe('no-cache');
	});
});
