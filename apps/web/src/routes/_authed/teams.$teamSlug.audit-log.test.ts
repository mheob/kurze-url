import type { PageAuditEntry, PageMember } from '@kurze-url/api-client';
import { isNotFound } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { loadAuditLogPage } from './teams.$teamSlug.audit-log';

// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `PageAuditEntry` is a generated `@kurze-url/api-client` type; `Readonly<>` is shallow and can't reach its nested `items` array from this side of the codegen boundary.
function logPage(overrides: Readonly<Partial<PageAuditEntry>> = {}): PageAuditEntry {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same reason as `logPage` above: `PageMember` is generated codegen output with a mutable nested `items` array.
function memberPage(overrides: Readonly<Partial<PageMember>> = {}): PageMember {
	return { items: [], page: 1, per_page: 100, total_count: 0, ...overrides };
}

/**
 * Captures whatever `fn` rejects with, instead of asserting inside a
 * try/catch: vitest's `no-conditional-expect` is error-level, and an
 * `expect` call inside a `catch` block only runs when something was actually
 * thrown — a `fn` that resolves would silently skip the assertion and the
 * test would pass for the wrong reason. Same helper, same reasoning, as
 * `teams.$teamSlug.links.index.test.ts`.
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
 * A loader whose log fetch fails with `status` and whose member fetch
 * succeeds — which is what the API really does to a member below admin: the
 * member list is viewer-level, so only the audit read is refused.
 *
 * @param status - The HTTP status the log fetch fails with.
 * @returns The loader's dependencies, ready to pass to `loadAuditLogPage`.
 */
function refusedBy(status: number): Parameters<typeof loadAuditLogPage>[0] {
	return {
		// oxlint-disable-next-line typescript/require-await -- stands in for a fetch `loadAuditLogPage` awaits; the fake has nothing to await itself.
		fetchLog: async () => {
			// oxlint-disable-next-line eslint/no-throw-literal, typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw { status };
		},
		// oxlint-disable-next-line typescript/require-await -- same as `fetchLog` above.
		fetchMembers: async () => memberPage(),
	};
}

describe(loadAuditLogPage, () => {
	it('unwraps both envelopes into the props the page body takes', async () => {
		const entry = {
			action: 'link.created',
			created_at: '2026-09-18T09:30:00.000Z',
			entity_type: 'link',
			id: 7,
			metadata: {},
		};
		const member = {
			created_at: '2026-01-01T00:00:00.000Z',
			email: 'vorstand@verein-a.example',
			role: 'owner',
			user_id: 'user-a',
		};

		await expect(
			loadAuditLogPage({
				// oxlint-disable-next-line typescript/require-await -- stands in for a fetch `loadAuditLogPage` awaits; the fake has nothing to await itself.
				fetchLog: async () => logPage({ items: [entry], page: 2, total_count: 45 }),
				// oxlint-disable-next-line typescript/require-await -- same as `fetchLog` above.
				fetchMembers: async () => memberPage({ items: [member], total_count: 1 }),
			}),
		).resolves.toStrictEqual({
			entries: [entry],
			forbidden: false,
			members: [member],
			page: 2,
			total: 45,
		});
	});

	/**
	 * Huma serialises a nil Go slice as JSON `null`, so a team with no history
	 * at all — the exact state `audit.emptyUnfiltered` exists for — receives
	 * `items: null` on both envelopes. `AuditEntryTable` declares a non-null
	 * array, so without the coalescing this test pins, that team's page
	 * crashes instead of rendering its empty state.
	 */
	it('coalesces a null items array on both envelopes', async () => {
		await expect(
			loadAuditLogPage({
				// oxlint-disable-next-line typescript/require-await -- stands in for a fetch `loadAuditLogPage` awaits; the fake has nothing to await itself.
				fetchLog: async () => logPage({ items: null }),
				// oxlint-disable-next-line typescript/require-await -- same as `fetchLog` above.
				fetchMembers: async () => memberPage({ items: null }),
			}),
		).resolves.toStrictEqual({
			entries: [],
			forbidden: false,
			members: [],
			page: 1,
			total: 0,
		});
	});

	/**
	 * The ordering this pins is the whole reason the 403 branch is written
	 * before the `classifyApiError` one: that function reports `notFound` for
	 * a 403 as well as a 404 (`api-errors.test.ts` pins it as "maps 403 to
	 * notFound, not to a forbidden state"), so reading it first would send a
	 * member below admin to the "page not found" page instead of the
	 * explanation `audit.forbiddenTitle` exists to give them. Asserting
	 * `{ forbidden: true }` rather than merely "did not throw" is what makes
	 * that distinction the thing under test.
	 */
	it('reports the admin refusal for a 403 rather than a not-found', async () => {
		await expect(loadAuditLogPage(refusedBy(403))).resolves.toStrictEqual({ forbidden: true });
	});

	/**
	 * A non-member gets 404 from `authz`, which deliberately never confirms
	 * that the team exists. Normalising it to the router's own `notFound()`
	 * puts them on the root's not-found page — the same page `requireTeamId`
	 * already sends them to — rather than this route's error boundary.
	 */
	it('normalises a 404 to the notFound the router understands', async () => {
		const error = await rejected(async () => loadAuditLogPage(refusedBy(404)));

		expect(isNotFound(error)).toBe(true);
	});

	/**
	 * Anything else must still reach `errorComponent` and be reported: a log
	 * that silently rendered empty on a failed request would look exactly like
	 * a team that has done nothing.
	 */
	it('rethrows any other failure', async () => {
		const boom = { status: 500 };

		await expect(
			loadAuditLogPage({
				// oxlint-disable-next-line typescript/require-await -- stands in for a fetch `loadAuditLogPage` awaits; the fake has nothing to await itself.
				fetchLog: async () => {
					// oxlint-disable-next-line typescript/only-throw-error -- `boom` is a deliberate fake API failure standing in for a rejected fetch, not a real error.
					throw boom;
				},
				// oxlint-disable-next-line typescript/require-await -- same as `fetchLog` above.
				fetchMembers: async () => memberPage(),
			}),
		).rejects.toBe(boom);
	});
});
