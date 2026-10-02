import type { PageLink } from '@kurze-url/api-client';
import { isRedirect } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { filterChangeSearch, loadLinks, parseLinksSearch } from './teams.$teamSlug.links.index';

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
 * Narrows the options `loadLinks` hands `ensureQueryData` down to its
 * `queryKey`, or `undefined` if it somehow has none — a type guard rather
 * than an `as` assertion, since oxlint's `no-unsafe-type-assertion` is
 * error-level and this is all "asks for the requested filter's query key"
 * needs to know about the shape. The `if` lives here rather than inside that
 * test's own callback for a second reason: oxlint's
 * `vitest/no-conditional-in-test` is error-level too, and this function is
 * declared outside every `it(...)` block.
 *
 * @param value - Whatever the fake `ensureQueryData` received.
 * @returns The `queryKey` property, or `undefined`.
 */
function queryKeyOf(value: unknown): unknown {
	if (typeof value === 'object' && value !== null && 'queryKey' in value) return value.queryKey;
	return undefined;
}

/**
 * Builds a fake `ensureQueryData` alongside a way to read back the
 * `queryKey` it was last called with — a closure variable, not a mutable
 * parameter a test would otherwise have to pass in, so there is nothing for
 * `typescript/prefer-readonly-parameter-types` (error-level) to catch either.
 *
 * @returns The fake to hand `fakeQueryClient`, and `capturedKey`, which reads back what it was called with.
 */
function capturingEnsureQueryData(): {
	readonly capturedKey: () => unknown;
	readonly ensureQueryData: FakeQueryClient['ensureQueryData'];
} {
	let key: unknown;
	return {
		capturedKey: () => key,
		// oxlint-disable-next-line typescript/require-await -- stands in for `FakeQueryClient.ensureQueryData`, which `loadLinks` awaits; the fake has nothing to await itself.
		ensureQueryData: async (options: unknown) => {
			key = queryKeyOf(options);
			return page();
		},
	};
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

		await expect(
			loadLinks(queryClient, 'team-a', {
				filter: { folder: { kind: 'all' }, tagId: undefined },
				page: 1,
			}),
		).resolves.toBe(data);
	});

	/**
	 * `loadLinks` gained a `filter` (Task 7) so the folder filter reaches the
	 * same query the loader warms — two definitions of "which links this page
	 * needs" would drift, the same reasoning `linksQueryOptions`'s own
	 * docstring gives for keying on `filter` at all. This pins that the filter
	 * actually reaches the query key, not only that `loadLinks` accepts it
	 * without using it.
	 *
	 * `capturingEnsureQueryData` is what actually reads `options.queryKey`;
	 * this test only ever reads the plain `capturedKey.value` property it
	 * wrote, so there is no `if`/`as` in the test's own callback for
	 * `vitest/no-conditional-in-test` or `typescript/no-unsafe-type-assertion`
	 * (both error-level) to catch.
	 */
	it("asks for the requested filter's query key", async () => {
		const { capturedKey, ensureQueryData } = capturingEnsureQueryData();
		const queryClient = fakeQueryClient(ensureQueryData);

		await loadLinks(queryClient, 'team-a', {
			filter: { folder: { kind: 'unfiled' }, tagId: undefined },
			page: 1,
		});

		expect(capturedKey()).toStrictEqual([
			'links',
			'team-a',
			1,
			{ folder: { kind: 'unfiled' }, tagId: undefined },
		]);
	});

	/** The tag half of the same pin: a tag the loader is handed must reach the query it warms. */
	it("asks for the requested tag's query key", async () => {
		const { capturedKey, ensureQueryData } = capturingEnsureQueryData();
		const queryClient = fakeQueryClient(ensureQueryData);

		await loadLinks(queryClient, 'team-a', {
			filter: { folder: { kind: 'all' }, tagId: 't1' },
			page: 1,
		});

		expect(capturedKey()).toStrictEqual([
			'links',
			'team-a',
			1,
			{ folder: { kind: 'all' }, tagId: 't1' },
		]);
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

		const error = await rejected(async () =>
			loadLinks(queryClient, 'team-a', {
				filter: { folder: { kind: 'all' }, tagId: undefined },
				page: 1,
			}),
		);

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

		await expect(
			loadLinks(queryClient, 'team-a', {
				filter: { folder: { kind: 'all' }, tagId: undefined },
				page: 1,
			}),
		).rejects.toBe(boom);
	});
});

describe(parseLinksSearch, () => {
	it('parses folder, page and tag together', () => {
		const tag = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';
		expect(parseLinksSearch({ folder: 'none', page: '3', tag })).toStrictEqual({
			folder: 'none',
			page: 3,
			tag,
		});
	});

	it('parses folder and page without a tag', () => {
		expect(parseLinksSearch({ folder: 'none', page: '2' })).toStrictEqual({
			folder: 'none',
			page: 2,
			tag: undefined,
		});
	});

	it('drops an invalid folder and falls back to page 1', () => {
		expect(parseLinksSearch({ folder: 'garbage' })).toStrictEqual({
			folder: undefined,
			page: 1,
			tag: undefined,
		});
	});

	/** A tag that is not a UUID is dropped, the way a malformed `folder` is — it never reaches the API call. */
	it('parses a tag UUID, lowercased, and drops anything else', () => {
		const id = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';
		expect(parseLinksSearch({ tag: id.toUpperCase() }).tag).toBe(id);
		expect(parseLinksSearch({ tag: 'garbage' }).tag).toBeUndefined();
		expect(parseLinksSearch({ tag: 'none' }).tag).toBeUndefined();
	});
});

describe(filterChangeSearch, () => {
	/** Pins Review Focus 5: a new filter always starts at page 1, even if the reader was deep in an old filter's pagination. */
	it('carries both filters and resets to page 1', () => {
		expect(filterChangeSearch({ folder: 'f1', tag: 't1' })).toStrictEqual({
			folder: 'f1',
			page: 1,
			tag: 't1',
		});
	});

	it('resets to page 1 for a folder alone', () => {
		expect(filterChangeSearch({ folder: 'f1' })).toStrictEqual({
			folder: 'f1',
			page: 1,
			tag: undefined,
		});
	});

	it('resets to page 1 for a tag alone', () => {
		expect(filterChangeSearch({ tag: 't1' })).toStrictEqual({
			folder: undefined,
			page: 1,
			tag: 't1',
		});
	});

	it('resets to page 1 once both filters are cleared', () => {
		expect(filterChangeSearch({})).toStrictEqual({ folder: undefined, page: 1, tag: undefined });
	});
});
