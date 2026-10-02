import type { Tag } from '@kurze-url/api-client';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { statusOf } from '../lib/api-errors';
import { server } from '../test/msw';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is a `Request` parameter (a mock's own, or one msw's resolver destructures as `{ request }`):
 * `Request` nests a mutable `Headers` through its own `.headers` getter, and `Readonly<>` is
 * shallow — it does not reach that nested property, unlike a bare `Headers` parameter, which the
 * check does accept once wrapped (see the mock's `headers` parameter below).
 */

/** Only the slice `requireSession` reaches through. Same narrowing as `links.test.ts`. */
interface FakeSupabaseClient {
	auth: {
		getSession: () => Promise<{
			data: { session: { access_token: string } | null };
			error: null;
		}>;
	};
}

/** Only the slice `flushSessionCookies` reaches through. */
interface FakeResponse {
	headers: { append: (name: string, value: string) => void };
}

const mocks = vi.hoisted(() => ({
	createSupabase: vi.fn<(request: Request, headers: Readonly<Headers>) => FakeSupabaseClient>(),
	getResponse: vi.fn<() => FakeResponse>(() => ({ headers: { append: () => undefined } })),
}));

vi.mock('./supabase', () => ({ createSupabase: mocks.createSupabase }));
vi.mock('@tanstack/react-start/server', () => ({ getResponse: mocks.getResponse }));

/**
 * The `*For` half of each pair, never the `createServerFn`-wrapped `*Fn`:
 * the latter calls `getRequest()` internally, which throws "No Start context
 * found" outside a real request — exactly what Vitest is. See `domains.ts`.
 */
const { createTagFor, deleteTagFor, listTagsFor, prefetchTags, renameTagFor, tagsQueryOptions } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
	 * register their replacements, which a module-scope `await import` is what expresses.
	 */
	await import('./tags');

/**
 * Reading the session is what refreshes an expiring one, and the refreshed
 * cookies are written into the `headers` argument. Simulated as a synchronous
 * side effect of `createSupabase`, the way `links.test.ts` and `teams.test.ts`
 * do it.
 *
 * @param accessToken - The token the faked session should report.
 */
function withSession(accessToken: string): void {
	mocks.createSupabase.mockImplementation((_request: Request, headers: Readonly<Headers>) => {
		headers.append('set-cookie', 'sb-access-token=refreshed; Path=/; HttpOnly');
		return {
			auth: {
				/* oxlint-disable-next-line typescript/require-await -- this mock stands in for
				 * `createSupabase`'s real `getSession`, which is genuinely async; the body has
				 * nothing to await, but the return type must stay `Promise<...>` to match.
				 */
				getSession: vi.fn(async () => ({
					data: { session: { access_token: accessToken } },
					error: null,
				})),
			},
		};
	});
}

/**
 * One page of a team's tags the way the API would serve it: 100 to a page, the
 * last page carrying the remainder. The conditional lives here rather than in
 * the MSW resolver because `vitest/no-conditional-in-test` (error-level in
 * this repo) reaches into test bodies, and a resolver is one.
 *
 * @param page - The 1-based page number requested.
 * @param total - How many tags the team holds in all.
 * @returns The response body for that page.
 */
function tagPage(
	page: number,
	total: number,
): {
	readonly items: readonly {
		readonly id: string;
		readonly name: string;
		readonly team_id: string;
	}[];
	readonly page: number;
	readonly per_page: number;
	readonly total_count: number;
} {
	const size = Math.max(0, Math.min(100, total - (page - 1) * 100));
	const items = Array.from({ length: size }, (_, index) => ({
		id: `t${page}-${index}`,
		name: `T${page}-${index}`,
		team_id: 'team-a',
	}));
	return { items, page, per_page: 100, total_count: total };
}

describe('tag server functions', () => {
	afterEach(() => {
		vi.unstubAllEnvs();
	});

	it('fetches the second page when the team has more than 100 tags', async () => {
		withSession('token-a');
		const pages: string[] = [];
		const perPages: string[] = [];
		server.use(
			http.get('*/v1/teams/team-a/tags', ({ request }) => {
				const url = new URL(request.url);
				// `String(null)` is `'null'`, which fails the assertions below, so a request
				// that omitted either parameter cannot slip through as an empty string.
				const requested = url.searchParams.get('page');
				pages.push(String(requested));
				perPages.push(String(url.searchParams.get('per_page')));
				return HttpResponse.json(tagPage(Number(requested), 120));
			}),
		);

		const tags = await listTagsFor(new Request('http://localhost/'), 'team-a');

		expect(pages).toStrictEqual(['1', '2']);
		expect(perPages).toStrictEqual(['100', '100']);
		expect(tags).toHaveLength(120);
		expect(tags[0]?.id).toBe('t1-0');
		expect(tags[119]?.id).toBe('t2-19');
	});

	it('stops after one page when everything fits', async () => {
		withSession('token-a');
		let calls = 0;
		server.use(
			http.get('*/v1/teams/team-a/tags', () => {
				calls += 1;
				return HttpResponse.json({
					items: [{ id: 't1', name: 'Presse', team_id: 'team-a' }],
					page: 1,
					per_page: 100,
					total_count: 1,
				});
			}),
		);

		await expect(listTagsFor(new Request('http://localhost/'), 'team-a')).resolves.toHaveLength(1);
		expect(calls).toBe(1);
	});

	it('treats a null item list as an empty team', async () => {
		withSession('token-a');
		server.use(
			http.get('*/v1/teams/team-a/tags', () =>
				HttpResponse.json({ items: null, page: 1, per_page: 100, total_count: 0 }),
			),
		);

		await expect(listTagsFor(new Request('http://localhost/'), 'team-a')).resolves.toStrictEqual(
			[],
		);
	});

	it('creates, renames and deletes with the name in the body', async () => {
		withSession('token-a');
		const bodies: unknown[] = [];
		server.use(
			http.post('*/v1/teams/team-a/tags', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json({ id: 't1', name: 'Presse', team_id: 'team-a' }, { status: 201 });
			}),
			http.patch('*/v1/tags/t1', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json({ id: 't1', name: 'Newsletter', team_id: 'team-a' });
			}),
			http.delete('*/v1/tags/t1', () => new HttpResponse(null, { status: 204 })),
		);

		const created = await createTagFor(new Request('http://localhost/'), 'team-a', 'Presse');
		const renamed = await renameTagFor(new Request('http://localhost/'), 't1', 'Newsletter');
		await deleteTagFor(new Request('http://localhost/'), 't1');

		expect(bodies).toStrictEqual([{ name: 'Presse' }, { name: 'Newsletter' }]);
		expect(created.id).toBe('t1');
		expect(renamed.name).toBe('Newsletter');
	});

	it('rejects on an API error instead of resolving empty', async () => {
		withSession('token-a');
		server.use(
			http.post('*/v1/teams/team-a/tags', () =>
				HttpResponse.json({ status: 409, title: 'Conflict' }, { status: 409 }),
			),
		);

		// The status, not just "something was thrown": nameFailureOf
		// (lib/names.ts) reads it straight off the rejection with statusOf,
		// so a rejection that resolved with the wrong shape would leave every
		// tag write misclassified as unknown rather than "name taken", and
		// `rejects.toBeDefined()` alone would not have caught that.
		let caught: unknown;
		try {
			await createTagFor(new Request('http://localhost/'), 'team-a', 'Presse');
		} catch (error) {
			caught = error;
		}

		expect(statusOf(caught)).toBe(409);
	});

	it('keys the query on the team', () => {
		expect(tagsQueryOptions('team-a').queryKey).toStrictEqual(['tags', 'team-a']);
	});
});

/**
 * `prefetchTags` reaches through a narrow `{ ensureQueryData }` shape, not
 * through `listTagsFor`/MSW — same reasoning as `prefetchFolders`' own tests:
 * a hand-built fake proves the "never throws, always logs" contract without
 * standing up a real query client or a network mock.
 */
describe(prefetchTags, () => {
	it('resolves once the query client resolves', async () => {
		const tags: Tag[] = [];
		// oxlint-disable-next-line typescript/require-await -- stands in for `TagsDataSource.ensureQueryData`, which `prefetchTags` awaits; the fake has nothing to await itself.
		const ensureQueryData = vi.fn(async (): Promise<Tag[]> => tags);

		await expect(prefetchTags({ ensureQueryData }, 'team-a')).resolves.toBeUndefined();
	});

	it('resolves without throwing and logs when the query client rejects', async () => {
		const error = new Error('boom');
		// oxlint-disable-next-line typescript/require-await -- same reason as above: stands in for `TagsDataSource.ensureQueryData`.
		const ensureQueryData = vi.fn(async (): Promise<Tag[]> => {
			throw error;
		});
		const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {
			// no-op: this test only cares that `console.error` was called with the swallowed error, not what it does with it.
		});

		await expect(prefetchTags({ ensureQueryData }, 'team-a')).resolves.toBeUndefined();

		expect(consoleError).toHaveBeenCalledWith(expect.stringContaining('prefetchTags'), error);
		consoleError.mockRestore();
	});
});
