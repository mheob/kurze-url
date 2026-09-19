import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is a `Request` parameter (a mock's own, or one msw's resolver destructures as `{ request }`):
 * `Request` nests a mutable `Headers` through its own `.headers` getter, and `Readonly<>` is
 * shallow — it does not reach that nested property, unlike a bare `Headers` parameter, which the
 * check does accept once wrapped (see the mock's `headers` parameter below). Copied from
 * `links.test.ts`, which documents the same finding for the same reason.
 */

/**
 * Deliberately not the real `SupabaseClient` shape — only the slice
 * `requireSession` reaches through, via `getAccessToken`. Same narrowing
 * rationale as `links.test.ts`'s own fake.
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
 * `listAuditLogFor` (not `listAuditLogFn`, the `createServerFn` wrapping it):
 * the server function itself calls `getRequest()` internally, which reads
 * from the server's per-request `AsyncLocalStorage` and throws "No Start
 * context found" outside of a real request — exactly the environment Vitest
 * runs in. `listAuditLogFor` takes a `Request` as a plain parameter instead,
 * which is what makes it callable here at all; see its docstring in
 * `audit-log.ts`.
 */
const { listAuditLogFor } =
	/* oxlint-disable-next-line node/no-top-level-await -- this file is a Vitest test entry, never
	 * `require(esm)`'d by anything; the dynamic import has to run after the `vi.mock` call above
	 * registers its replacement, which a module-scope `await import` is what expresses.
	 */
	await import('./audit-log');

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

// The assertion that matters here is not "it fetches" but "it sends the day
// bounds the endpoint needs": `to` as midnight would silently hide the whole
// last day the reader chose, and no other test in this plan can see that.
describe('listAuditLogFor', () => {
	it('sends the chosen days as whole-day instants', async () => {
		withSession('tok');

		let seenUrl: URL | undefined;
		server.use(
			http.get('*/v1/teams/:teamId/audit-log', ({ request: apiRequest }) => {
				seenUrl = new URL(apiRequest.url);
				return HttpResponse.json({ items: [], page: 2, per_page: 20, total_count: 0 });
			}),
		);

		await listAuditLogFor(new Request('https://web.test/'), 'team-1', {
			from: '2026-03-01',
			page: 2,
			to: '2026-03-31',
		});

		expect(seenUrl?.searchParams.get('from')).toBe('2026-03-01T00:00:00.000Z');
		expect(seenUrl?.searchParams.get('to')).toBe('2026-03-31T23:59:59.999Z');
		expect(seenUrl?.searchParams.get('page')).toBe('2');
	});
});
