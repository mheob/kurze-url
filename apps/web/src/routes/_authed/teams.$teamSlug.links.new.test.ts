import type { Domain, PageDomain } from '@kurze-url/api-client';
import { describe, expect, it, vi } from 'vitest';

import type { LinkFormValues } from '../../components/link-form';
import {
	afterCreate,
	initialFolderId,
	initialTagIds,
	loadVerifiedDomains,
	toRequestBody,
} from './teams.$teamSlug.links.new';

const baseValues: LinkFormValues = {
	analytics_enabled: true,
	destination_url: 'https://example.org/',
	domain_id: '',
	expires_at: '',
	folder_id: '',
	redirect_type: 302,
	slug: '',
	tag_ids: [],
};

/**
 * A minimally-filled `Domain`, overridden per test — same shape `domains.test.ts` uses.
 *
 * @param overrides - Fields to override on the base fixture; `id` is required since the base has none.
 * @returns A fixture `Domain`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Domain` is a generated `@kurze-url/api-client` type; `Readonly<>` is shallow and can't reach its nested `records` object from this side of the codegen boundary.
function domain(overrides: Partial<Domain> & Pick<Domain, 'id'>): Domain {
	return {
		hostname: 'links.verein.test',
		records: {
			cname: { name: 'links', value: 'go.kurze-url.app' },
			txt: { name: '_kurze-url', value: 'abc123' },
		},
		team_id: 'team-a',
		verification_status: 'verified',
		verification_token: 'abc123',
		verified_at: '2026-09-06T00:00:00Z',
		...overrides,
	};
}

/**
 * This task's own explicit rule: "After a successful create, invalidate
 * both the links query key and the router... invalidating only one leaves
 * them disagreeing until the next full navigation." Falsified against
 * hand-built fakes — the same reasoning `loadLinks`'s own test file (Task 9)
 * gives for testing against a narrow interface instead of a real
 * `QueryClient`/router pair.
 *
 * Named `....new.test.ts`, not `....new.test.tsx`: this file has no JSX, so
 * there's no basename collision to worry about (conventions.md's landmine is
 * specifically a same-basename `.test.ts`/`.test.tsx` pair).
 */
describe(afterCreate, () => {
	it('invalidates both the links query cache and the router', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for `InvalidatableQueryClient.invalidateQueries`, which `afterCreate` awaits; the fake has nothing to await itself.
		const invalidateQueries = vi.fn(async (): Promise<void> => undefined);
		// oxlint-disable-next-line typescript/require-await -- same reason: stands in for `InvalidatableRouter.invalidate`, which `afterCreate` awaits.
		const invalidate = vi.fn(async (): Promise<void> => undefined);

		await afterCreate({ invalidateQueries }, { invalidate }, 'team-a');

		expect(invalidateQueries).toHaveBeenCalledExactlyOnceWith({ queryKey: ['links', 'team-a'] });
		expect(invalidate).toHaveBeenCalledOnce();
	});
});

/**
 * `LinkForm`'s own test proves the picker hands back the right
 * `LinkFormValues` — it says nothing about whether this route forwards that
 * value onto the wire, since nothing in that test touches `toRequestBody` at
 * all. Falsified: deleting the `domain_id` line from `toRequestBody` and
 * re-running the whole suite left every test passing, `LinkForm`'s included
 * — this is the test that closes that gap.
 */
describe(toRequestBody, () => {
	it('forwards a chosen domain_id to the request body', () => {
		expect(toRequestBody({ ...baseValues, domain_id: 'd1' })).toStrictEqual(
			expect.objectContaining({ domain_id: 'd1' }),
		);
	});

	it('maps an unset domain_id to undefined, same as slug and expires_at', () => {
		// So an unset picker keeps today's behaviour: falling through to the
		// API's own default, the instance's shared domain.
		expect(toRequestBody(baseValues)).toStrictEqual(
			expect.objectContaining({ domain_id: undefined }),
		);
	});

	it('sends the chosen folder and omits an empty one', () => {
		expect(toRequestBody({ ...baseValues, folder_id: 'f1' })).toMatchObject({ folder_id: 'f1' });
		expect(toRequestBody({ ...baseValues, folder_id: '' }).folder_id).toBeUndefined();
	});

	it('omits tag_ids when no tag was chosen', () => {
		expect(toRequestBody({ ...baseValues, tag_ids: [] })).not.toHaveProperty('tag_ids');
	});

	it('sends the chosen tag ids', () => {
		expect(toRequestBody({ ...baseValues, tag_ids: ['t1'] })).toMatchObject({ tag_ids: ['t1'] });
	});
});

/**
 * `links/new` preselects a folder from its own `folder` search parameter, but
 * only when the team actually has that folder: a stale link (a folder
 * deleted since the URL was bookmarked or shared) must not silently pin the
 * form onto an id the picker cannot render, which is why this checks against
 * the loaded `folders` list rather than trusting the search parameter alone.
 */
describe(initialFolderId, () => {
	it('preselects only a folder the team has', () => {
		expect(initialFolderId('f1', [{ id: 'f1', name: 'A' }])).toBe('f1');
		expect(initialFolderId('ffff', [{ id: 'f1', name: 'A' }])).toBe('');
		expect(initialFolderId(undefined, [{ id: 'f1', name: 'A' }])).toBe('');
	});
});

/**
 * The tag counterpart of `initialFolderId`: `?tag=<id>` (the link list's "New
 * link" button while a tag filter is active) preselects that tag, but only
 * when the team has it, so a stale or foreign id never becomes a chip.
 */
describe(initialTagIds, () => {
	it('preselects only a tag the team has', () => {
		expect(initialTagIds('t1', [{ id: 't1', name: 'A' }])).toStrictEqual(['t1']);
		expect(initialTagIds('tttt', [{ id: 't1', name: 'A' }])).toStrictEqual([]);
		expect(initialTagIds(undefined, [{ id: 't1', name: 'A' }])).toStrictEqual([]);
	});
});

/**
 * The picker only ever lists domains that actually work. A `pending`/`failed`
 * domain has no working DNS yet, so passing it through would let a link get
 * created on a hostname that doesn't redirect — falsified here against a
 * mixed-status page rather than left to the component to filter silently.
 */
describe(loadVerifiedDomains, () => {
	it('keeps only verified domains, normalised to id and hostname', async () => {
		const page: PageDomain = {
			items: [
				domain({ hostname: 'links.verein.test', id: 'd1', verification_status: 'verified' }),
				domain({ hostname: 'pending.verein.test', id: 'd2', verification_status: 'pending' }),
			],
			page: 1,
			per_page: 25,
			total_count: 2,
		};
		// oxlint-disable-next-line typescript/require-await -- stands in for `DomainsDataSource.ensureQueryData`, which `loadVerifiedDomains` awaits; the fake has nothing to await itself.
		const ensureQueryData = vi.fn(async (): Promise<PageDomain> => page);

		await expect(loadVerifiedDomains({ ensureQueryData }, 'team-a')).resolves.toStrictEqual([
			{ hostname: 'links.verein.test', id: 'd1' },
		]);
	});

	it('normalises a nil items slice to an empty list', async () => {
		// Huma serialises a nil Go slice as JSON `null` — same normalisation
		// `listDomainsFor`'s own callers already need.
		const page: PageDomain = { items: null, page: 1, per_page: 25, total_count: 0 };
		// oxlint-disable-next-line typescript/require-await -- same reason as above: stands in for `DomainsDataSource.ensureQueryData`.
		const ensureQueryData = vi.fn(async (): Promise<PageDomain> => page);

		await expect(loadVerifiedDomains({ ensureQueryData }, 'team-a')).resolves.toStrictEqual([]);
	});

	it('falls back to an empty list rather than blocking the create-link page', async () => {
		// The picker is an enhancement over the shared hostname the form
		// already falls back to; a failed domains fetch (an expired session,
		// a network hiccup) must not take the whole page down with it.
		// oxlint-disable-next-line typescript/require-await -- same reason as above: stands in for `DomainsDataSource.ensureQueryData`.
		const ensureQueryData = vi.fn(async (): Promise<PageDomain> => {
			throw new Error('boom');
		});

		await expect(loadVerifiedDomains({ ensureQueryData }, 'team-a')).resolves.toStrictEqual([]);
	});

	it('logs the swallowed error rather than failing completely silently', async () => {
		// Finding 6: the graceful fallback above must stay silent to the
		// *visitor*, not to every possible observer — otherwise a broken
		// domains fetch makes every later link land on the shared hostname
		// with nothing anywhere to notice it.
		const error = new Error('boom');
		// oxlint-disable-next-line typescript/require-await -- same reason as above: stands in for `DomainsDataSource.ensureQueryData`.
		const ensureQueryData = vi.fn(async (): Promise<PageDomain> => {
			throw error;
		});
		const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {
			// no-op: this test only cares that `console.error` was called with the swallowed error, not what it does with it.
		});

		await loadVerifiedDomains({ ensureQueryData }, 'team-a');

		expect(consoleError).toHaveBeenCalledWith(
			expect.stringContaining('loadVerifiedDomains'),
			error,
		);
		consoleError.mockRestore();
	});
});
