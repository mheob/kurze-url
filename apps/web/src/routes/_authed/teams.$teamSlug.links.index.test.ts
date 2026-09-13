import type { PageLink } from '@kurze-url/api-client';
import { isRedirect } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { loadLinks } from './teams.$teamSlug.links.index';

/** The one method `loadLinks` reaches through on `context.queryClient`. */
interface FakeQueryClient {
	ensureQueryData: (options: unknown) => Promise<PageLink>;
}

// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `PageLink` is a generated `@kurze-url/api-client` type; `Readonly<>` is shallow and can't reach its nested `items` array from this side of the codegen boundary.
function page(overrides: Readonly<Partial<PageLink>> = {}): PageLink {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

function fakeQueryClient(ensureQueryData: FakeQueryClient['ensureQueryData']): FakeQueryClient {
	return { ensureQueryData };
}

/**
 * Captures whatever `fn` rejects with, instead of asserting inside a
 * try/catch: vitest's `no-conditional-expect` is error-level, and an
 * `expect` call inside a `catch` block only runs when something was actually
 * thrown — a `fn` that resolves would silently skip the assertion and the
 * test would pass for the wrong reason. Asserting on this function's return
 * value, unconditionally, is what keeps "did it throw at all" and "what did
 * it throw" both covered. Async counterpart to the same helper in
 * `routes/_authed.test.ts`.
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
 * A plain ternary, not an `expect` inside a conditional: this narrows
 * `error` via `isRedirect`'s type predicate so the test below can assert on
 * `.options.to` without an unsafe cast, and without tripping
 * `no-conditional-expect` by putting the `expect` call itself inside an
 * `if`.
 *
 * @param error - The value caught from a rejected `loadLinks` call.
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

describe(loadLinks, () => {
	it('returns the fetched page when the API call succeeds', async () => {
		const data = page({ total_count: 1 });
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadLinks` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => data);

		await expect(loadLinks(queryClient, 'team-a', 1)).resolves.toBe(data);
	});

	/**
	 * The finding this fixes: a session that dies between `_authed.tsx`'s own
	 * `beforeLoad` check and this route's own fetch used to reach
	 * `errorComponent`, rendering dead-end inline text. Asserting only that
	 * `loadLinks` throws *something* would still pass if the redirect branch
	 * were deleted and the rethrow below fired instead — the route would then
	 * 500 on a plain `{ status: 401 }` object rather than redirect. Asserting
	 * `isRedirect` and the destination is what tells those two apart; see
	 * "Fix round 1" in task-9-report.md for the falsification that confirms
	 * this test actually depends on the redirect branch.
	 */
	it('redirects to /login when the API answers unauthenticated', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadLinks` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line eslint/no-throw-literal, typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw { status: 401 };
		});

		const error = await rejected(async () => loadLinks(queryClient, 'team-a', 1));

		expect(isRedirect(error)).toBe(true);
		expect(redirectTarget(error)).toBe('/login');
	});

	/**
	 * The list fails loudly on purpose (see `LinksError`'s own docstring): a
	 * non-401 failure must still reach `errorComponent` rather than being
	 * swallowed or redirected away, or a down API would look identical to an
	 * empty list.
	 */
	it('rethrows any other failure rather than redirecting', async () => {
		const boom = { status: 500 };
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadLinks` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line typescript/only-throw-error -- `boom` is a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw boom;
		});

		await expect(loadLinks(queryClient, 'team-a', 1)).rejects.toBe(boom);
	});
});
