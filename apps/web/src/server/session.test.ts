import { describe, expect, it, vi } from 'vitest';

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

/**
 * Only the slice `flushSessionCookies` reaches through: a response object
 * whose `headers` supports `append`, same narrowing rationale as
 * `FakeSupabaseClient` above.
 */
interface FakeResponse {
	headers: { append: (name: string, value: string) => void };
}

const mocks = vi.hoisted(() => ({
	createSupabase: vi.fn<(request: Request, headers: Headers) => FakeSupabaseClient>(),
	getResponse: vi.fn<() => FakeResponse>(),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

vi.mock('@tanstack/react-start/server', () => ({
	getResponse: mocks.getResponse,
}));

function req(): [Request, Headers] {
	return [new Request('https://example.test/'), new Headers()];
}

function withSession(accessToken: string | null): void {
	mocks.createSupabase.mockReturnValue({
		auth: {
			getSession: vi.fn(async () => ({
				data: { session: accessToken ? { access_token: accessToken } : null },
				error: null,
			})),
		},
	});
}

describe('getAccessToken', () => {
	it('returns the token when a session exists', async () => {
		withSession('tok');
		await expect(getAccessToken(...req())).resolves.toBe('tok');
	});

	it('returns undefined when there is no session', async () => {
		withSession(null);
		await expect(getAccessToken(...req())).resolves.toBeUndefined();
	});
});

describe('requireSession', () => {
	it('throws UnauthenticatedError rather than returning an empty token', async () => {
		// The guard must fail closed. Returning '' here would send an
		// unauthenticated request to the API, which answers 401 — the same
		// symptom, three layers further away from the cause.
		withSession(null);
		await expect(requireSession(...req())).rejects.toBeInstanceOf(UnauthenticatedError);
	});

	it('returns the token when a session exists', async () => {
		withSession('tok');
		await expect(requireSession(...req())).resolves.toEqual({ accessToken: 'tok' });
	});
});

describe('flushSessionCookies', () => {
	/**
	 * The defect this guards against: `createSupabase(request, headers)`
	 * writes into a `Headers` object nothing else reads. A no-op
	 * `flushSessionCookies` — or one that reads `headers` but never calls
	 * through to the real response — would pass every other test in this
	 * file (they never inspect `headers` after the call) while leaving a
	 * freshly created or refreshed session with nowhere to go. This test
	 * fails on exactly that: an empty `appended` array.
	 */
	it('appends every Set-Cookie the adapter wrote onto the real response', () => {
		const appended: string[] = [];
		mocks.getResponse.mockReturnValue({
			headers: { append: (name, value) => appended.push(`${name}: ${value}`) },
		});

		const adapterHeaders = new Headers();
		adapterHeaders.append('set-cookie', 'sb-access-token=abc; Path=/; HttpOnly');
		adapterHeaders.append('set-cookie', 'sb-refresh-token=def; Path=/; HttpOnly');

		flushSessionCookies(adapterHeaders);

		expect(appended).toEqual([
			'set-cookie: sb-access-token=abc; Path=/; HttpOnly',
			'set-cookie: sb-refresh-token=def; Path=/; HttpOnly',
		]);
	});

	it('does nothing to the response when the adapter wrote no cookies', () => {
		const appended: string[] = [];
		mocks.getResponse.mockReturnValue({
			headers: { append: (name, value) => appended.push(`${name}: ${value}`) },
		});

		flushSessionCookies(new Headers());

		expect(appended).toEqual([]);
	});
});
