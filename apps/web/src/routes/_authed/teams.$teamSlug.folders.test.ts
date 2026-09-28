import type { PageFolder } from '@kurze-url/api-client';
import { isRedirect } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { loadFolders } from './teams.$teamSlug.folders';

/** The one method `loadFolders` reaches through on `context.queryClient`. */
interface FakeQueryClient {
	ensureQueryData: (options: unknown) => Promise<PageFolder>;
}

// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `PageFolder` is a generated `@kurze-url/api-client` type; `Readonly<>` is shallow and can't reach its nested `items` array from this side of the codegen boundary.
function page(overrides: Readonly<Partial<PageFolder>> = {}): PageFolder {
	return { items: [], page: 1, per_page: 100, total_count: 0, ...overrides };
}

function fakeQueryClient(ensureQueryData: FakeQueryClient['ensureQueryData']): FakeQueryClient {
	return { ensureQueryData };
}

/**
 * Captures whatever `fn` rejects with, instead of asserting inside a
 * try/catch — same reasoning as the identical helper in
 * `teams.$teamSlug.links.index.test.ts`.
 *
 * @param fn - The async operation expected to reject.
 * @returns The rejection reason, or `undefined` if `fn` resolved instead.
 */
async function rejected(fn: () => Promise<unknown>): Promise<unknown> {
	try {
		await fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

/**
 * A plain ternary, not an `expect` inside a conditional — same reasoning as
 * the identical helper in `teams.$teamSlug.links.index.test.ts`.
 *
 * @param error - The value caught from a rejected `loadFolders` call.
 * @returns The redirect's destination, or `undefined` if `error` is not a redirect.
 */
function redirectTarget(error: unknown): string | undefined {
	if (!isRedirect(error)) return undefined;

	// Narrowed at runtime rather than asserted: the router types `options.to`
	// as `any`, so trusting it would put an `any` into a `string | undefined`
	// and every caller would inherit it.
	const target: unknown = error.options.to;
	return typeof target === 'string' ? target : undefined;
}

describe(loadFolders, () => {
	it('returns the fetched page when the API call succeeds', async () => {
		const data = page({ total_count: 1 });
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadFolders` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => data);

		await expect(loadFolders(queryClient, 'team-a')).resolves.toBe(data);
	});

	it('redirects to /login when the API answers unauthenticated', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadFolders` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line eslint/no-throw-literal, typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw { status: 401 };
		});

		const error = await rejected(async () => loadFolders(queryClient, 'team-a'));

		expect(isRedirect(error)).toBe(true);
		expect(redirectTarget(error)).toBe('/login');
	});

	it('rethrows any other failure rather than redirecting', async () => {
		const boom = { status: 500 };
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadFolders` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line typescript/only-throw-error -- `boom` is a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw boom;
		});

		await expect(loadFolders(queryClient, 'team-a')).rejects.toBe(boom);
	});
});
