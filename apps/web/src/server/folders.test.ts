import type { PageFolder } from '@kurze-url/api-client';
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
const { createFolderFor, deleteFolderFor, listFoldersFor, prefetchFolders, renameFolderFor } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
	 * register their replacements, which a module-scope `await import` is what expresses.
	 */
	await import('./folders');

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

describe('folder server functions', () => {
	afterEach(() => {
		vi.unstubAllEnvs();
	});

	it('lists every folder in one page of 100', async () => {
		withSession('token-a');
		let seen: URL | undefined;
		server.use(
			http.get('*/v1/teams/team-a/folders', ({ request }) => {
				seen = new URL(request.url);
				return HttpResponse.json({
					items: [
						{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
					],
					page: 1,
					per_page: 100,
					total_count: 1,
				});
			}),
		);

		const page = await listFoldersFor(new Request('http://localhost/'), 'team-a');

		expect(seen?.searchParams.get('per_page')).toBe('100');
		expect(page.items?.[0]?.name).toBe('Sommerfest');
	});

	it('creates, renames and deletes with the name in the body', async () => {
		withSession('token-a');
		const bodies: unknown[] = [];
		server.use(
			http.post('*/v1/teams/team-a/folders', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json(
					{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
					{ status: 201 },
				);
			}),
			http.patch('*/v1/folders/f1', async ({ request }) => {
				bodies.push(await request.json());
				return HttpResponse.json({
					created_at: '2026-09-26T00:00:00Z',
					id: 'f1',
					name: 'Newsletter',
					team_id: 'team-a',
				});
			}),
			http.delete('*/v1/folders/f1', () => new HttpResponse(null, { status: 204 })),
		);

		await createFolderFor(new Request('http://localhost/'), 'team-a', 'Sommerfest');
		await renameFolderFor(new Request('http://localhost/'), 'f1', 'Newsletter');
		await deleteFolderFor(new Request('http://localhost/'), 'f1');

		expect(bodies).toStrictEqual([{ name: 'Sommerfest' }, { name: 'Newsletter' }]);
	});

	it('rejects on an API error instead of resolving empty', async () => {
		withSession('token-a');
		server.use(
			http.post('*/v1/teams/team-a/folders', () =>
				HttpResponse.json({ status: 409, title: 'Conflict' }, { status: 409 }),
			),
		);

		// The status, not just "something was thrown": nameFailureOf
		// (lib/names.ts) reads it straight off the rejection with statusOf,
		// so a rejection that resolved with the wrong shape would leave every
		// folder write misclassified as unknown rather than "name taken", and
		// `rejects.toBeDefined()` alone would not have caught that.
		let caught: unknown;
		try {
			await createFolderFor(new Request('http://localhost/'), 'team-a', 'Sommerfest');
		} catch (error) {
			caught = error;
		}

		expect(statusOf(caught)).toBe(409);
	});
});

/**
 * `prefetchFolders` reaches through a narrow `{ ensureQueryData }` shape, not
 * through `listFoldersFor`/MSW — same reasoning as `loadVerifiedDomains`'s own
 * tests in `teams.$teamSlug.links.new.test.ts`: a hand-built fake proves the
 * "never throws, always logs" contract without standing up a real query
 * client or a network mock.
 */
describe(prefetchFolders, () => {
	it('resolves once the query client resolves', async () => {
		const page: PageFolder = { items: [], page: 1, per_page: 100, total_count: 0 };
		// oxlint-disable-next-line typescript/require-await -- stands in for `FoldersDataSource.ensureQueryData`, which `prefetchFolders` awaits; the fake has nothing to await itself.
		const ensureQueryData = vi.fn(async (): Promise<PageFolder> => page);

		await expect(prefetchFolders({ ensureQueryData }, 'team-a')).resolves.toBeUndefined();
	});

	it('resolves without throwing and logs when the query client rejects', async () => {
		const error = new Error('boom');
		// oxlint-disable-next-line typescript/require-await -- same reason as above: stands in for `FoldersDataSource.ensureQueryData`.
		const ensureQueryData = vi.fn(async (): Promise<PageFolder> => {
			throw error;
		});
		const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {
			// no-op: this test only cares that `console.error` was called with the swallowed error, not what it does with it.
		});

		await expect(prefetchFolders({ ensureQueryData }, 'team-a')).resolves.toBeUndefined();

		expect(consoleError).toHaveBeenCalledWith(expect.stringContaining('prefetchFolders'), error);
		consoleError.mockRestore();
	});
});
