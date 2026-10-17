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
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

// Exposes `appendResponseHeader` and nothing else on purpose. Vitest throws when code reads an
// export a mock factory did not define, so a `flushSessionCookies` that reached for
// `setResponseHeader` (which replaces every cookie already on the response) fails here instead
// of passing against a mock that quietly allows it.
vi.mock('@tanstack/react-start/server', () => ({
	appendResponseHeader: mocks.appendResponseHeader,
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
	 * its own call, and the module mock above has no `setResponseHeader` to
	 * fall back on.
	 */
	it('appends again on a second flush instead of replacing', () => {
		flushSessionCookies(new Headers([['set-cookie', 'a=1; Path=/']]));
		flushSessionCookies(new Headers([['set-cookie', 'b=2; Path=/']]));

		expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(1, 'set-cookie', ['a=1; Path=/']);
		expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(2, 'set-cookie', ['b=2; Path=/']);
	});

	it('writes nothing when there is no set-cookie header', () => {
		flushSessionCookies(new Headers());

		expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
	});
});
