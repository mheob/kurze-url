import type { PageMember } from '@kurze-url/api-client';
import { isRedirect } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { loadMembers } from './teams.$teamSlug.members';

/** The one method `loadMembers` reaches through on `context.queryClient`. */
interface FakeQueryClient {
	ensureQueryData: (options: unknown) => Promise<PageMember>;
}

function fakeQueryClient(ensureQueryData: FakeQueryClient['ensureQueryData']): FakeQueryClient {
	return { ensureQueryData };
}

/**
 * Captures whatever `fn` rejects with, instead of asserting inside a
 * try/catch: vitest's `no-conditional-expect` is error-level, and an
 * `expect` call inside a `catch` block only runs when something was actually
 * thrown — a `fn` that resolves would silently skip the assertion and the
 * test would pass for the wrong reason. Same helper as
 * `teams.$teamSlug.links.index.test.ts`'s own `loadLinks` suite.
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
 * `.options.to` without an unsafe cast. Same helper as `loadLinks`'s own
 * suite uses for the identical reason.
 *
 * @param error - The value caught from a rejected `loadMembers` call.
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

describe(loadMembers, () => {
	it('returns the page the query client produced', async () => {
		const page: PageMember = { items: [], page: 1, per_page: 100, total_count: 0 };
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadMembers` awaits; the fake has nothing to await itself.
		const queryClient = fakeQueryClient(async () => page);

		await expect(loadMembers(queryClient, 'team-1')).resolves.toBe(page);
	});

	/**
	 * The finding this fixes: a session that dies between `_authed.tsx`'s own
	 * `beforeLoad` check and this route's own fetch used to reach
	 * `errorComponent`, rendering dead-end inline text. Asserting only that
	 * `loadMembers` throws *something* would still pass if the redirect
	 * branch were deleted and the rethrow below fired instead. Asserting
	 * `isRedirect` and the destination is what tells those two apart.
	 */
	it('redirects an expired session to the login page instead of rendering an error', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadMembers` awaits; this fake throws before ever needing to await.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw Object.assign(new Error('unauthorized'), { status: 401 });
		});

		const error = await rejected(async () => loadMembers(queryClient, 'team-1'));

		expect(isRedirect(error)).toBe(true);
		expect(redirectTarget(error)).toBe('/login');
	});

	/**
	 * The list fails loudly on purpose (see `MembersError`'s own docstring): a
	 * non-401 failure must still reach `errorComponent` rather than being
	 * swallowed or redirected away, or a down API would look identical to an
	 * empty list.
	 */
	it('rethrows any other failure rather than redirecting', async () => {
		const boom = { status: 500 };
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadMembers` awaits; this fake throws before ever needing to await.
		const queryClient = fakeQueryClient(async () => {
			// oxlint-disable-next-line typescript/only-throw-error -- `boom` is a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw boom;
		});

		await expect(loadMembers(queryClient, 'team-1')).rejects.toBe(boom);
	});
});
