import { describe, expect, it, vi } from 'vitest';

import { UnauthenticatedError, getAccessToken, requireSession } from './session';

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
	createSupabase: vi.fn<(request: Request, headers: Headers) => FakeSupabaseClient>(),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
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
