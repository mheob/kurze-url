import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is a `Request` parameter (a mock's own, or one msw's resolver destructures as `{ request }`):
 * `Request` nests a mutable `Headers` through its own `.headers` getter, and `Readonly<>` is
 * shallow — it does not reach that nested property, unlike a bare `Headers` parameter, which the
 * check does accept once wrapped (see the mock's `headers` parameter below). Copied from
 * `audit-log.test.ts`, which documents the same finding for the same reason.
 */

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice
 * `requireSession` reaches through, via `getAccessToken`. Same narrowing
 * rationale as `audit-log.test.ts`'s own fake.
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
	createSupabase: vi.fn<(request: Request, headers: Readonly<Headers>) => FakeSupabaseClient>(),
	// Defaulted so the test doesn't need its own setup — `flushSessionCookies`
	// still calls this internally even when the test never inspects what it
	// appended.
	getResponse: vi.fn<() => FakeResponse>(() => ({ headers: { append: () => undefined } })),
}));

vi.mock('./supabase', () => ({
	createSupabase: mocks.createSupabase,
}));

vi.mock('@tanstack/react-start/server', () => ({
	getResponse: mocks.getResponse,
}));

/**
 * `listMembersFor`, not the `createServerFn` wrapping it, for the reason
 * `audit-log.test.ts` documents at its own import: the server function calls
 * `getRequest()` internally, which throws "No Start context found" outside a
 * real request — which is exactly what Vitest is.
 */
const { addMemberFor, listMembersFor, removeMemberFor, updateMemberRoleFor } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` call above
	 * registers its replacement, which a module-scope `await import` is what expresses.
	 */
	await import('./members');

/**
 * @param accessToken - The token the faked session should report.
 */
function withSession(accessToken: string): void {
	mocks.createSupabase.mockImplementation((_request: Request, _headers: Readonly<Headers>) => ({
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
	}));
}

/**
 * Answers the members endpoint with `count` members out of `totalCount`.
 *
 * @param count - How many members the page carries.
 * @param totalCount - What the envelope claims the team has in total.
 */
function withMembers(count: number, totalCount: number): void {
	server.use(
		http.get('*/v1/teams/:teamId/members', () =>
			HttpResponse.json({
				items: Array.from({ length: count }, (_value, index) => ({
					email: `member-${index}@example.org`,
					role: 'viewer',
					user_id: `user-${index}`,
				})),
				page: 1,
				per_page: 100,
				total_count: totalCount,
			}),
		),
	);
}

/**
 * The assertion that matters here is not "it fetches" but "a truncated read
 * says so": this list exists to name an audit entry's actor, and a member
 * past the API's cap is rendered as a former member of the team — a wrong
 * answer that looks exactly like a right one, in front of the readers this
 * page exists for.
 */
describe('listMembersFor', () => {
	it('reports a member list the API truncated', async () => {
		withSession('tok');
		withMembers(100, 142);

		const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {
			// no-op: this test only cares that the condition was reported, not what the log does with it.
		});

		await listMembersFor(new Request('https://web.test/'), 'team-1');

		expect(consoleError).toHaveBeenCalledWith(expect.stringContaining('listMembersFor'));
		expect(consoleError).toHaveBeenCalledWith(expect.stringContaining('142'));
		consoleError.mockRestore();
	});

	it('stays quiet when the whole team came back', async () => {
		withSession('tok');
		withMembers(3, 3);

		const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {
			// no-op: see above.
		});

		await listMembersFor(new Request('https://web.test/'), 'team-1');

		expect(consoleError).not.toHaveBeenCalled();
		consoleError.mockRestore();
	});
});

describe('addMemberFor', () => {
	it('sends the invite and reports whether an email went out', async () => {
		withSession('tok');
		server.use(
			http.post('*/v1/teams/:teamId/members', () =>
				HttpResponse.json(
					{
						created_at: '2026-09-20T09:00:00Z',
						email: 'neu@verein.test',
						invited: false,
						role: 'editor',
						user_id: 'u2',
					},
					{ status: 201 },
				),
			),
		);

		const added = await addMemberFor(new Request('https://web.test/'), 'team-1', {
			email: 'neu@verein.test',
			role: 'editor',
		});

		expect(added.invited).toBe(false);
		expect(added.user_id).toBe('u2');
	});

	/**
	 * The assertion that matters here: without `throwOnError: true` the
	 * generated client resolves to `{ data: undefined, error }` instead of
	 * rejecting, and the page would report a refused invitation as a success.
	 */
	it('throws rather than resolving when the invite is refused', async () => {
		withSession('tok');
		server.use(
			http.post('*/v1/teams/:teamId/members', () =>
				HttpResponse.json({ detail: 'already a member' }, { status: 409 }),
			),
		);

		await expect(
			addMemberFor(new Request('https://web.test/'), 'team-1', {
				email: 'neu@verein.test',
				role: 'editor',
			}),
		).rejects.toThrow();
	});
});

describe('updateMemberRoleFor', () => {
	it('resolves with nothing when a role change succeeds', async () => {
		withSession('tok');
		server.use(
			http.patch(
				'*/v1/teams/:teamId/members/:userId',
				() => new HttpResponse(null, { status: 204 }),
			),
		);

		await expect(
			updateMemberRoleFor(new Request('https://web.test/'), 'team-1', {
				role: 'admin',
				userId: 'u2',
			}),
		).resolves.toBeUndefined();
	});
});

describe('removeMemberFor', () => {
	it('resolves with nothing when a removal succeeds', async () => {
		withSession('tok');
		server.use(
			http.delete(
				'*/v1/teams/:teamId/members/:userId',
				() => new HttpResponse(null, { status: 204 }),
			),
		);

		await expect(
			removeMemberFor(new Request('https://web.test/'), 'team-1', 'u2'),
		).resolves.toBeUndefined();
	});
});
