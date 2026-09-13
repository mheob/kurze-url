import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

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
const { claimDomainFor, deleteDomainFor, listDomainsFor, verifyDomainFor } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
	 * register their replacements, which a module-scope `await import` is what expresses.
	 */
	await import('./domains');

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

const request = new Request('https://example.test/');

describe('domains', () => {
	afterEach(() => {
		vi.unstubAllEnvs();
	});

	describe('listDomainsFor', () => {
		it('lists the team’s domains as the signed-in caller', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');

			let seenAuth: string | null = null;
			server.use(
				http.get('http://api.test/v1/teams/team-a/domains', ({ request: apiRequest }) => {
					seenAuth = apiRequest.headers.get('authorization');
					return HttpResponse.json({ items: [], page: 1, per_page: 25, total_count: 0 });
				}),
			);

			const page = await listDomainsFor(request, 'team-a');

			expect(seenAuth).toBe('Bearer tok');
			expect(page.total_count).toBe(0);
		});

		it('rejects when the API refuses', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			server.use(
				http.get('http://api.test/v1/teams/team-a/domains', () =>
					HttpResponse.json({ detail: 'not a member' }, { status: 404 }),
				),
			);

			await expect(listDomainsFor(request, 'team-a')).rejects.toBeDefined();
		});

		it('carries a refreshed session cookie onto the response', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			const appended: string[] = [];
			mocks.getResponse.mockReturnValueOnce({
				headers: {
					append: (_name, value) => {
						appended.push(value);
					},
				},
			});
			server.use(
				http.get('http://api.test/v1/teams/team-a/domains', () =>
					HttpResponse.json({ items: [], page: 1, per_page: 25, total_count: 0 }),
				),
			);

			await listDomainsFor(request, 'team-a');

			expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
		});
	});

	describe('claimDomainFor', () => {
		it('posts the hostname as the signed-in caller', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');

			let seenBody: unknown = null;
			server.use(
				http.post('http://api.test/v1/teams/team-a/domains', async ({ request: apiRequest }) => {
					seenBody = await apiRequest.json();
					return HttpResponse.json({ hostname: 'links.verein.test', id: 'd1' }, { status: 201 });
				}),
			);

			await claimDomainFor(request, 'team-a', 'links.verein.test');

			expect(seenBody).toStrictEqual({ hostname: 'links.verein.test' });
		});

		it('rejects when the API refuses', async () => {
			// throwOnError. Without it a 422 for an apex resolves to
			// { data: undefined, error } and the route reports a claim that was
			// never created.
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			server.use(
				http.post('http://api.test/v1/teams/team-a/domains', () =>
					HttpResponse.json({ detail: 'apex' }, { status: 422 }),
				),
			);

			await expect(claimDomainFor(request, 'team-a', 'verein.test')).rejects.toBeDefined();
		});

		it('carries a refreshed session cookie onto the response', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			const appended: string[] = [];
			mocks.getResponse.mockReturnValueOnce({
				headers: {
					append: (_name, value) => {
						appended.push(value);
					},
				},
			});
			server.use(
				http.post('http://api.test/v1/teams/team-a/domains', () =>
					HttpResponse.json({ hostname: 'links.verein.test', id: 'd1' }, { status: 201 }),
				),
			);

			await claimDomainFor(request, 'team-a', 'links.verein.test');

			expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
		});
	});

	describe('verifyDomainFor', () => {
		it('triggers verification as the signed-in caller', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');

			let seenAuth: string | null = null;
			let seenMethod = '';
			server.use(
				http.post('http://api.test/v1/domains/d1/verify', ({ request: apiRequest }) => {
					seenAuth = apiRequest.headers.get('authorization');
					seenMethod = apiRequest.method;
					return HttpResponse.json({
						domain: {
							hostname: 'links.verein.test',
							id: 'd1',
							records: {
								cname: { name: 'links', value: 'go.kurze-url.app' },
								txt: { name: '_kurze-url', value: 'abc123' },
							},
							team_id: 'team-a',
							verification_status: 'pending',
							verification_token: 'abc123',
							verified_at: null,
						},
						reason: 'token_missing',
					});
				}),
			);

			const result = await verifyDomainFor(request, 'd1');

			expect(seenAuth).toBe('Bearer tok');
			expect(seenMethod).toBe('POST');
			// Not widened to `string`: a switch in Task 13 over this union must stay
			// exhaustive, and `expect().toBe` here only compiles because the return
			// type still carries the literal union.
			expect(result.reason).toBe('token_missing');
		});

		it('rejects when the API refuses', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			server.use(
				http.post('http://api.test/v1/domains/d1/verify', () =>
					HttpResponse.json({ detail: 'not found' }, { status: 404 }),
				),
			);

			await expect(verifyDomainFor(request, 'd1')).rejects.toBeDefined();
		});

		it('carries a refreshed session cookie onto the response', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			const appended: string[] = [];
			mocks.getResponse.mockReturnValueOnce({
				headers: {
					append: (_name, value) => {
						appended.push(value);
					},
				},
			});
			server.use(
				http.post('http://api.test/v1/domains/d1/verify', () =>
					HttpResponse.json({
						domain: {
							hostname: 'links.verein.test',
							id: 'd1',
							records: {
								cname: { name: 'links', value: 'go.kurze-url.app' },
								txt: { name: '_kurze-url', value: 'abc123' },
							},
							team_id: 'team-a',
							verification_status: 'verified',
							verification_token: 'abc123',
							verified_at: '2026-09-06T00:00:00Z',
						},
						reason: 'token_missing',
					}),
				),
			);

			await verifyDomainFor(request, 'd1');

			expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
		});
	});

	describe('deleteDomainFor', () => {
		it('deletes the domain as the signed-in caller', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');

			let seenAuth: string | null = null;
			let seenMethod = '';
			server.use(
				http.delete('http://api.test/v1/domains/d1', ({ request: apiRequest }) => {
					seenAuth = apiRequest.headers.get('authorization');
					seenMethod = apiRequest.method;
					return new HttpResponse(null, { status: 204 });
				}),
			);

			await deleteDomainFor(request, 'd1');

			expect(seenAuth).toBe('Bearer tok');
			expect(seenMethod).toBe('DELETE');
		});

		it('rejects when the API refuses', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			server.use(
				http.delete('http://api.test/v1/domains/d1', () =>
					HttpResponse.json({ detail: 'not found' }, { status: 404 }),
				),
			);

			await expect(deleteDomainFor(request, 'd1')).rejects.toBeDefined();
		});

		it('carries a refreshed session cookie onto the response', async () => {
			vi.stubEnv('API_HOST', 'http://api.test');
			withSession('tok');
			const appended: string[] = [];
			mocks.getResponse.mockReturnValueOnce({
				headers: {
					append: (_name, value) => {
						appended.push(value);
					},
				},
			});
			server.use(
				http.delete('http://api.test/v1/domains/d1', () => new HttpResponse(null, { status: 204 })),
			);

			await deleteDomainFor(request, 'd1');

			expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
		});
	});
});
