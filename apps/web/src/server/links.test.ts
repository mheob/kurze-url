import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice
 * `requireSession` reaches through, via `getAccessToken`. Same narrowing
 * rationale as `auth.test.ts` and `session.test.ts`'s own fakes.
 */
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
	createSupabase: vi.fn<(request: Request, headers: Headers) => FakeSupabaseClient>(),
	// Defaulted so the first test doesn't need its own setup — only the flush
	// test below overrides this to inspect what was appended.
	getResponse: vi.fn<() => FakeResponse>(() => ({ headers: { append: () => undefined } })),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

vi.mock('@tanstack/react-start/server', () => ({
	getResponse: mocks.getResponse,
}));

/**
 * `listLinksFor` (not `listLinksFn`, the `createServerFn` wrapping it): the
 * server function itself calls `getRequest()` internally, which reads from
 * the server's per-request `AsyncLocalStorage` and throws "No Start context
 * found" outside of a real request — exactly the environment Vitest runs
 * in. `listLinksFor` takes a `Request` as a plain parameter instead, which
 * is what makes it callable here at all; see its docstring in `links.ts`.
 */
const { listLinksFor } = await import('./links');

/**
 * `createSupabase` also writes a refreshed session's cookies into the
 * `headers` argument it's given — `getSession` is what triggers that refresh
 * in the real `@supabase/ssr` adapter. Simulated here the same way
 * `auth.test.ts` simulates the PKCE verifier write: synchronously, as a side
 * effect of the mocked `createSupabase` call itself.
 */
function withSession(accessToken: string): void {
	mocks.createSupabase.mockImplementation((_request, headers) => {
		headers.append('set-cookie', 'sb-access-token=refreshed; Path=/; HttpOnly');
		return {
			auth: {
				getSession: vi.fn(async () => ({
					data: { session: { access_token: accessToken } },
					error: null,
				})),
			},
		};
	});
}

interface FakePage {
	items: unknown[];
	page: number;
	per_page: number;
	total_count: number;
}

function page(overrides: Partial<FakePage> = {}): FakePage {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

const request = new Request('https://example.test/');

afterEach(() => {
	vi.unstubAllEnvs();
});

describe('listLinksFor', () => {
	it("fetches a page of a team's links from the API", async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');

		let seenAuth: string | null = null;
		let seenQuery: string | null = null;
		server.use(
			http.get('http://api.test/v1/teams/team-a/links', ({ request: apiRequest }) => {
				seenAuth = apiRequest.headers.get('authorization');
				seenQuery = new URL(apiRequest.url).search;
				return HttpResponse.json(
					page({
						items: [
							{
								analytics_enabled: true,
								created_at: '2026-01-01T00:00:00Z',
								created_by: 'user-1',
								destination_url: 'https://example.org/',
								domain_id: 'domain-1',
								expires_at: null,
								folder_id: 'folder-1',
								has_password: false,
								hostname: 'short.invalid',
								id: 'link-1',
								redirect_type: 302,
								short_url: 'https://short.invalid/abc123',
								slug: 'abc123',
								state: 'active',
								tags: [],
								team_id: 'team-a',
								updated_at: '2026-01-01T00:00:00Z',
							},
						],
						total_count: 1,
					}),
				);
			}),
		);

		const result = await listLinksFor(request, 'team-a', 1);

		expect(seenAuth).toBe('Bearer tok');
		expect(seenQuery).toBe('?page=1&per_page=20');
		// This is the property name the plan's own sample code guessed at —
		// `data.items` — checked against `PageLink` in
		// `packages/api-client/src/generated/types.gen.ts` and
		// `apps/api/openapi.json`'s `PageLink` schema. It happens to be
		// correct, but nullable (`Array<Link> | null`, since Huma serialises a
		// nil Go slice as JSON `null`), which this assertion pins down too.
		expect(result.items).toHaveLength(1);
		expect(result.items?.[0]?.short_url).toBe('https://short.invalid/abc123');
		expect(result.total_count).toBe(1);
	});

	/**
	 * Finding: `listLinksFor` reads the session via `requireSession`, and that
	 * read is itself what refreshes an expiring one — writing new cookies
	 * into the `Headers` object threaded through. Without the
	 * `flushSessionCookies(headers)` call, those cookies are built and then
	 * discarded: this test fails on exactly that, because nothing ever
	 * forwards the cookie the mocked `createSupabase` wrote onto the real
	 * response. See conventions.md: "Skipping it is invisible until an hour
	 * after login, in production."
	 */
	it('flushes refreshed session cookies onto the real response', async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');
		server.use(http.get('http://api.test/v1/teams/team-a/links', () => HttpResponse.json(page())));

		const appended: string[] = [];
		mocks.getResponse.mockReturnValue({
			headers: { append: (name, value) => appended.push(`${name}: ${value}`) },
		});

		await listLinksFor(request, 'team-a', 1);

		expect(appended).toEqual(['set-cookie: sb-access-token=refreshed; Path=/; HttpOnly']);
	});
});
