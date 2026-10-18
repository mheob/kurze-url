import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

import {
	UnauthenticatedError,
	flushSessionCookies,
	getAccessToken,
	requireSession,
} from './session';

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice
 * `getAccessToken` actually reaches through. Typing the mock this way, rather
 * than casting an object literal through `unknown` to the real type, is what
 * keeps this file free of both `no-unsafe-type-assertion` and a `globalThis`
 * test seam: Task 2 had a global seam in production code rejected in review
 * in favour of exporting a plain function, and a mutable global shared across
 * this file's tests would be the same shortcut in a different spot.
 */
interface FakeSupabaseClient {
	auth: {
		getSession: () => Promise<{
			data: { session: { access_token: string } | null };
			error: null;
		}>;
	};
}

const mocks = vi.hoisted(() => ({
	appendResponseHeader: vi.fn<(name: string, value: string | readonly string[]) => void>(),
	/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `request` is `Request`,
	 * which nests a mutable `Headers` through its own `.headers` getter; `Readonly<>` is shallow
	 * and doesn't reach that nested property, unlike the bare `Headers` parameter beside it, which
	 * the check accepts once wrapped.
	 */
	createSupabase: vi.fn<(request: Request, headers: Readonly<Headers>) => FakeSupabaseClient>(),
	setResponseHeader: vi.fn<(name: string, value: string | readonly string[]) => void>(),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

// Exposes the two helpers `flushSessionCookies` uses and nothing else on purpose. Vitest throws
// when code reads an export a mock factory did not define, so a flush that reached for
// `setResponseHeaders` or `setCookie` fails here instead of passing against a mock that quietly
// allows it. `setResponseHeader` is on the list because the cache headers need it, and it is also
// the helper that would replace every cookie already on the response if it were given
// `set-cookie`; the missing export no longer guards that, so the `set-cookie` tests below assert
// that nothing but the three cache headers ever reaches it.
vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
	setResponseHeader: mocks.setResponseHeader,
}));

function req(): [Request, Headers] {
	return [new Request('https://example.test/'), new Headers()];
}

function withSession(accessToken: string | null): void {
	mocks.createSupabase.mockReturnValue({
		auth: {
			/* oxlint-disable-next-line typescript/require-await -- this mock stands in for
			 * `createSupabase`'s real `getSession`, which is genuinely async; the body has
			 * nothing to await, but the return type must stay `Promise<...>` to match.
			 */
			getSession: vi.fn(async () => ({
				data: { session: accessToken !== null ? { access_token: accessToken } : null },
				error: null,
			})),
		},
	});
}

describe(getAccessToken, () => {
	it('returns the token when a session exists', async () => {
		withSession('tok');
		await expect(getAccessToken(...req())).resolves.toBe('tok');
	});

	it('returns undefined when there is no session', async () => {
		withSession(null);
		await expect(getAccessToken(...req())).resolves.toBeUndefined();
	});
});

describe(requireSession, () => {
	it('throws UnauthenticatedError rather than returning an empty token', async () => {
		// The guard must fail closed. Returning '' here would send an
		// unauthenticated request to the API, which answers 401 — the same
		// symptom, three layers further away from the cause.
		withSession(null);
		await expect(requireSession(...req())).rejects.toBeInstanceOf(UnauthenticatedError);
	});

	it('returns the token when a session exists', async () => {
		withSession('tok');
		await expect(requireSession(...req())).resolves.toStrictEqual({ accessToken: 'tok' });
	});
});

describe(flushSessionCookies, () => {
	beforeEach(() => {
		mocks.appendResponseHeader.mockClear();
		mocks.setResponseHeader.mockClear();
	});

	/**
	 * The defect this guards against: `createSupabase(request, headers)`
	 * writes into a `Headers` object nothing else reads. A no-op
	 * `flushSessionCookies` — or one that reads `headers` but never calls
	 * through to the real response — would pass every other test in this
	 * file (they never inspect `headers` after the call) while leaving a
	 * freshly created or refreshed session with nowhere to go. This test
	 * fails on exactly that: `appendResponseHeader` is never called.
	 */
	it('hands every set-cookie value to appendResponseHeader in one call', () => {
		const adapterHeaders = new Headers();
		adapterHeaders.append('set-cookie', 'a=1; Path=/');
		adapterHeaders.append('set-cookie', 'b=2; Path=/');

		flushSessionCookies(adapterHeaders);

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'a=1; Path=/',
			'b=2; Path=/',
		]);
	});

	/**
	 * `setResponseHeader('set-cookie', …)` would replace what the response
	 * already carries, so a route that reads the session twice would lose the
	 * first flush's cookies. Each flush has to reach `appendResponseHeader` as
	 * its own call, and the mock does expose `setResponseHeader` (the cache
	 * headers need it), so the last assertion is what stands in for the
	 * missing-export guard. Neither flush carries a cache header, so nothing at
	 * all may reach `setResponseHeader`, which covers `set-cookie` in any casing.
	 */
	it('appends again on a second flush instead of replacing', () => {
		flushSessionCookies(new Headers([['set-cookie', 'a=1; Path=/']]));
		flushSessionCookies(new Headers([['set-cookie', 'b=2; Path=/']]));

		expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(1, 'set-cookie', ['a=1; Path=/']);
		expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(2, 'set-cookie', ['b=2; Path=/']);
		expect(mocks.setResponseHeader).not.toHaveBeenCalled();
	});

	it('writes nothing when there is no set-cookie header', () => {
		flushSessionCookies(new Headers());

		expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
	});

	/**
	 * What `@supabase/ssr` hands `setAll` next to the cookies of a refreshed or
	 * new session (`applyServerStorage`, `dist/main/cookies.js`), landed in the
	 * local `Headers` by `createCookieAdapter`. They exist so that no CDN or
	 * reverse proxy caches a response carrying one person's session cookie and
	 * serves it to someone else. Dropping them, which is what `flushSessionCookies`
	 * did while it forwarded `set-cookie` alone, leaves the cookie on a response
	 * that anything in front of this app is free to cache.
	 *
	 * `setResponseHeader` replaces, which is right for headers that carry one
	 * value. `appendResponseHeader` here would make `Cache-Control` read
	 * `<whatever was there>, private, no-cache, ...`, and the exactly-once
	 * assertion on it is what rules that out.
	 */
	it('forwards the cache headers next to the cookies', () => {
		const adapterHeaders = new Headers();
		adapterHeaders.append('set-cookie', 'a=1; Path=/');
		adapterHeaders.set('cache-control', 'private, no-cache, no-store, must-revalidate, max-age=0');
		adapterHeaders.set('expires', '0');
		adapterHeaders.set('pragma', 'no-cache');

		flushSessionCookies(adapterHeaders);

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'a=1; Path=/',
		]);
		expect(mocks.setResponseHeader).toHaveBeenCalledTimes(3);
		expect(mocks.setResponseHeader).toHaveBeenCalledWith(
			'cache-control',
			'private, no-cache, no-store, must-revalidate, max-age=0',
		);
		expect(mocks.setResponseHeader).toHaveBeenCalledWith('expires', '0');
		expect(mocks.setResponseHeader).toHaveBeenCalledWith('pragma', 'no-cache');
	});

	it('forwards each cache header that is present and skips the ones that are not', () => {
		flushSessionCookies(
			new Headers([
				['set-cookie', 'a=1; Path=/'],
				['cache-control', 'no-store'],
			]),
		);

		expect(mocks.setResponseHeader).toHaveBeenCalledExactlyOnceWith('cache-control', 'no-store');
	});

	/**
	 * "Exactly the set `@supabase/ssr` uses": the adapter copies whatever the
	 * library passes into this `Headers`, but forwarding is a decision made here.
	 * A header outside the three would be replacing something on a response this
	 * function has no business touching.
	 */
	it('forwards nothing but the three cache headers', () => {
		flushSessionCookies(
			new Headers([
				['set-cookie', 'a=1; Path=/'],
				['cache-control', 'no-store'],
				['content-type', 'text/plain'],
				['x-anything', 'else'],
			]),
		);

		expect(mocks.setResponseHeader).toHaveBeenCalledExactlyOnceWith('cache-control', 'no-store');
	});

	it('writes only the cookies when no cache header came with them', () => {
		flushSessionCookies(new Headers([['set-cookie', 'a=1; Path=/']]));

		expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
			'a=1; Path=/',
		]);
		expect(mocks.setResponseHeader).not.toHaveBeenCalled();
	});

	it('writes nothing at all when the Headers is empty', () => {
		flushSessionCookies(new Headers());

		expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
		expect(mocks.setResponseHeader).not.toHaveBeenCalled();
	});

	/**
	 * Mirrors `@supabase/ssr`, which never produces this state: it sends the
	 * cache headers only together with cookies (`applyServerStorage` returns
	 * before `setAll` when it has none to write). Forwarding them anyway would
	 * overwrite a `Cache-Control` the response already had, on a response that
	 * carries no session cookie to protect.
	 */
	it('does not forward cache headers when no cookie came with them', () => {
		flushSessionCookies(
			new Headers([
				['cache-control', 'private, no-cache, no-store, must-revalidate, max-age=0'],
				['expires', '0'],
				['pragma', 'no-cache'],
			]),
		);

		expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
		expect(mocks.setResponseHeader).not.toHaveBeenCalled();
	});
});
