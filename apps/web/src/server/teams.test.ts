import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';

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
	createSupabase: vi.fn<(request: Request, headers: Headers) => FakeSupabaseClient>(),
	getResponse: vi.fn<() => FakeResponse>(() => ({ headers: { append: () => undefined } })),
}));

vi.mock('./supabase', () => ({ createSupabase: mocks.createSupabase }));
vi.mock('@tanstack/react-start/server', () => ({ getResponse: mocks.getResponse }));

/**
 * `createTeamFor`, not `createTeamFn`: the server function calls
 * `getRequest()`, which throws "No Start context found" outside a real
 * request — exactly what Vitest is.
 */
const { createTeamFor } = await import('./teams');

/**
 * Reading the session is what refreshes an expiring one, and the refreshed
 * cookies are written into the `headers` argument. Simulated as a synchronous
 * side effect of `createSupabase`, the way `links.test.ts` does it.
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

const request = new Request('https://example.test/');

afterEach(() => {
	vi.unstubAllEnvs();
});

describe('createTeamFor', () => {
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

		const team = await createTeamFor(request, 'Verein A');

		expect(seenAuth).toBe('Bearer tok');
		expect(seenBody).toEqual({ name: 'Verein A' });
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

		await expect(createTeamFor(request, 'Verein A')).rejects.toBeDefined();
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
			headers: { append: (_name, value) => appended.push(value) },
		});
		server.use(
			http.post('http://api.test/v1/teams', () =>
				HttpResponse.json({ id: 'team-1', name: 'Verein A', role: 'owner' }, { status: 201 }),
			),
		);

		await createTeamFor(request, 'Verein A');

		expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
	});
});
