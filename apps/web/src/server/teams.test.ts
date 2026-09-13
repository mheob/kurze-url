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
 * `createTeamFor`, not `createTeamFn`: the server function calls
 * `getRequest()`, which throws "No Start context found" outside a real
 * request — exactly what Vitest is.
 */
/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` calls above
 * register their replacements, which a module-scope `await import` is what expresses.
 */
const { createTeamFor } = await import('./teams');

/**
 * Reading the session is what refreshes an expiring one, and the refreshed
 * cookies are written into the `headers` argument. Simulated as a synchronous
 * side effect of `createSupabase`, the way `links.test.ts` does it.
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

describe('createTeamFor', () => {
	afterEach(() => {
		vi.unstubAllEnvs();
	});

	it('posts the name to the API as the signed-in caller', async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');

		let seenAuth: string | null = null;
		let seenBody: unknown = null;
		server.use(
			http.post('http://api.test/v1/teams', async ({ request: apiRequest }) => {
				seenAuth = apiRequest.headers.get('authorization');
				seenBody = await apiRequest.json();
				return HttpResponse.json(
					{ id: 'team-1', name: 'Verein A', role: 'owner' },
					{ status: 201 },
				);
			}),
		);

		const team = await createTeamFor(request, 'Verein A', 'verein-a');

		expect(seenAuth).toBe('Bearer tok');
		expect(seenBody).toStrictEqual({ name: 'Verein A', slug: 'verein-a' });
		expect(team.id).toBe('team-1');
	});

	/**
	 * `throwOnError` is what makes this true. The generated client's default
	 * never rejects: a 403 would resolve to `{ data: undefined, error }`, and
	 * the route's `onSuccess` would then navigate to `/teams/undefined/links`
	 * as though a team had been created. A non-maintainer reaching this
	 * endpoint is the expected case, not an exotic one — the guard in front of
	 * it is a route, and routes can be typed into the address bar.
	 */
	it('rejects when the API refuses, rather than reporting a team that does not exist', async () => {
		vi.stubEnv('API_HOST', 'http://api.test');
		withSession('tok');

		server.use(
			http.post('http://api.test/v1/teams', () =>
				HttpResponse.json(
					{ detail: 'team creation is limited to the instance maintainers' },
					{
						status: 403,
					},
				),
			),
		);

		await expect(createTeamFor(request, 'Verein A', 'verein-a')).rejects.toBeDefined();
	});

	/**
	 * The failure this app has already shipped twice: the refreshed session
	 * cookies are written into a local `Headers` and are lost unless flushed
	 * onto the real response. Silent — the request succeeds, and the visitor is
	 * signed out on their next navigation instead.
	 */
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
			http.post('http://api.test/v1/teams', () =>
				HttpResponse.json({ id: 'team-1', name: 'Verein A', role: 'owner' }, { status: 201 }),
			),
		);

		await createTeamFor(request, 'Verein A', 'verein-a');

		expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
	});
});
