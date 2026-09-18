import type { Link, LinkStats } from '@kurze-url/api-client';
import { isNotFound } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { loadLink } from './teams.$teamSlug.links.$linkId';
import { loadStats, loadStatsPage, statsView } from './teams.$teamSlug.links.$linkId_.stats.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   test file doesn't own: the generated `@kurze-url/api-client` `Link`/`LinkStats` types, whose
   nested arrays are mutable and can't be marked readonly from this side of the codegen boundary. */

const TOTALS_ZERO = { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 };
const EMPTY_BREAKDOWNS = {
	bot_status: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	browser: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	country: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	device: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	os: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	qr_vs_regular: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	referrer: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	utm_source: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
};

const STATS_FIXTURE: LinkStats = {
	analytics_enabled: true,
	breakdowns: EMPTY_BREAKDOWNS,
	from: '2026-08-20',
	link_id: 'link-a',
	series: [],
	to: '2026-09-18',
	totals: TOTALS_ZERO,
};

/**
 * Same shape as `teams.$teamSlug.links.$linkId.test.ts`'s own `link()` helper — duplicated here
 * rather than imported, the same way that file's fixtures aren't shared across test files
 * elsewhere in this app.
 *
 * @param overrides - Fields to override on the default fixture.
 * @returns A minimal, valid `Link`.
 */
function link(overrides: Partial<Link> = {}): Link {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00.000Z',
		created_by: 'user-a',
		destination_url: 'https://example.org',
		domain_id: 'domain-a',
		expires_at: null,
		folder_id: 'folder-a',
		has_password: false,
		hostname: 'kurze.url',
		id: 'link-a',
		redirect_type: 302,
		short_url: 'https://kurze.url/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'team-a',
		updated_at: '2026-01-01T00:00:00.000Z',
		...overrides,
	};
}

/**
 * Same reasoning as `teams.$teamSlug.links.index.test.ts`'s identical helper: asserting on a
 * returned value, unconditionally, instead of inside a try/catch — `no-conditional-expect` is
 * error-level, and an `expect` inside `catch` silently skips when nothing throws.
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
 * A `query` fake that always rejects with a given status — captures `status`, so unlike an
 * inline `() => Promise.reject({ status: 404 })` it isn't flagged as a closure that captures
 * nothing.
 *
 * @param status - The HTTP status code the rejection carries.
 * @returns A `query` fake matching `loadStats`'s expected shape, which always rejects.
 */
function rejectingQueryWith(status: number): () => Promise<LinkStats> {
	// oxlint-disable-next-line typescript/require-await -- must return a `Promise` to satisfy the declared return type; the body never reaches an `await`.
	return async () => {
		// oxlint-disable-next-line eslint/no-throw-literal, typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
		throw { status };
	};
}

/**
 * A `fetchLink` fake that always rejects with a given status — same reasoning as
 * `rejectingQueryWith` above, for `loadLink`'s own parameter shape.
 *
 * @param status - The HTTP status code the rejection carries.
 * @returns A fetcher matching `loadLink`'s expected shape, which always rejects.
 */
function rejectingFetchLinkWith(
	status: number,
): (options: Readonly<{ data: Readonly<{ linkId: string }> }>) => Promise<Link> {
	// oxlint-disable-next-line typescript/require-await -- must return a `Promise` to satisfy `loadLink`'s `LinkFetcher`-shaped parameter; the body never reaches an `await`.
	return async () => {
		// oxlint-disable-next-line eslint/no-throw-literal, typescript/only-throw-error -- a deliberate fake API failure standing in for a rejected fetch, not a real error.
		throw { status };
	};
}

describe(statsView, () => {
	// The single easiest thing to get wrong on this page. An empty document
	// from a link with counting off means "not counted", not "not clicked",
	// and a chart of zeroes there would be a false statement.
	it('reports counting as off rather than showing empty charts', () => {
		expect(
			statsView({
				analytics_enabled: false,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('disabled');
	});

	it('reports an empty window when counting is on and nothing was clicked', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('empty');
	});

	it('reports data whenever there is at least one click', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: { ...TOTALS_ZERO, clicks: 1 },
			}),
		).toBe('data');
	});

	// Final-review finding: the endpoint does not zero anything when counting
	// is switched off — `AnalyticsEnabled` comes from the link row, the
	// totals/series/breakdowns come from the rollup, independently. A team
	// that collected clicks and later unticked the analytics checkbox keeps
	// every one of them in this document, so gating 'disabled' on the flag
	// alone would hide real data for up to 90 days. This is 'data', not
	// 'disabled' — `StatsPageBody` is what adds the banner explaining why.
	it('reports data (not disabled) when counting is off but historical clicks remain', () => {
		expect(
			statsView({
				analytics_enabled: false,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: { ...TOTALS_ZERO, clicks: 5000 },
			}),
		).toBe('data');
	});
});

describe(loadStats, () => {
	it('returns the fetched stats when the API call succeeds', async () => {
		// oxlint-disable-next-line typescript/require-await -- must satisfy `StatsDataSource.query`, which returns a `Promise<LinkStats>`; nothing here needs an `await`.
		const query = async (): Promise<LinkStats> => STATS_FIXTURE;

		await expect(loadStats({ query }, 'link-a', {})).resolves.toBe(STATS_FIXTURE);
	});

	/**
	 * `GET /v1/links/{id}/stats` carries the same `authz.LinkViewerScope` as
	 * `GET /v1/links/{id}` — a link the caller isn't in, or that doesn't
	 * exist, 404s from this endpoint exactly the way it does from `loadLink`
	 * (`teams.$teamSlug.links.$linkId.test.ts` asserts the identical thing for
	 * that fetch). Asserting `isNotFound`, not a bare `.toThrow()`, is what
	 * would catch a regression back to this route's own bare error text.
	 */
	it('throws a router not-found when the stats endpoint answers not-found', async () => {
		const error = await rejected(async () =>
			loadStats({ query: rejectingQueryWith(404) }, 'link-a', {}),
		);

		expect(isNotFound(error)).toBe(true);
	});

	it('rethrows any other failure rather than swallowing it', async () => {
		const boom = { status: 500 };
		// oxlint-disable-next-line typescript/require-await -- same reason as `rejectingQueryWith`: `StatsDataSource.query` returns a `Promise<LinkStats>`.
		const query = async (): Promise<LinkStats> => {
			// oxlint-disable-next-line typescript/only-throw-error -- `boom` is a deliberate fake API failure standing in for a rejected fetch, not a real error.
			throw boom;
		};

		await expect(loadStats({ query }, 'link-a', {})).rejects.toBe(boom);
	});
});

/**
 * The property fix round 1 found missing: this route's `loader` runs
 * `loadStats`/`loadLink` in one `Promise.all`, and before this fix only
 * `loadLink` converted its own 404 into the router's `notFound()` — a 404
 * from the stats fetch fell through unconverted, so whichever of the two
 * settled first decided which page a visitor saw for the exact same
 * condition (a link that doesn't exist, or belongs to another team). These
 * two tests drive each side of that race independently and assert they
 * land on the identical outcome.
 */
describe('a missing link 404s the same way regardless of which fetch reports it', () => {
	it('is a router not-found when the stats fetch is the one that 404s', async () => {
		const error = await rejected(async () =>
			Promise.all([
				loadStats({ query: rejectingQueryWith(404) }, 'link-a', {}),
				// oxlint-disable-next-line typescript/require-await -- must satisfy `loadLink`'s `LinkFetcher`-shaped parameter; nothing here needs an `await`.
				loadLink(async () => link(), 'link-a'),
			]),
		);

		expect(isNotFound(error)).toBe(true);
	});

	it('is a router not-found when the link fetch is the one that 404s', async () => {
		const error = await rejected(async () =>
			Promise.all([
				// oxlint-disable-next-line typescript/require-await -- must satisfy `StatsDataSource.query`, which returns a `Promise<LinkStats>`; nothing here needs an `await`.
				loadStats({ query: async () => STATS_FIXTURE }, 'link-a', {}),
				loadLink(rejectingFetchLinkWith(404), 'link-a'),
			]),
		);

		expect(isNotFound(error)).toBe(true);
	});
});

describe(loadStatsPage, () => {
	it('reports the loaded link together with its statistics', async () => {
		// oxlint-disable-next-line typescript/require-await -- must satisfy `StatsDataSource.query`, which returns a `Promise<LinkStats>`; nothing here needs an `await`.
		const query = async (): Promise<LinkStats> => STATS_FIXTURE;
		// oxlint-disable-next-line typescript/require-await -- must satisfy `loadLink`'s `LinkFetcher`-shaped parameter; nothing here needs an `await`.
		const fetchLink = async (): Promise<Link> => link();

		const result = await loadStatsPage({
			fetchLink,
			linkId: 'link-a',
			queryClient: { query },
			window: {},
		});

		expect(result.stats).toBe(STATS_FIXTURE);
		expect(result.link).toStrictEqual(link());
	});

	/**
	 * Residual finding 4: `RouteComponent` used to read `new Date()` at
	 * render time, and the server and the browser each call that
	 * independently — a server render landing just before midnight and
	 * hydrating just after resolves `matchingPreset` differently on each
	 * side, flipping the preset button's `aria-pressed` between the SSR
	 * markup and the first client render. Asserting the returned `today` is
	 * a `Date` bracketed by the call's own start and end is what proves it
	 * comes from this one loader run rather than from a second, independent
	 * clock read downstream.
	 */
	it('returns the instant it ran at, not one the caller has to supply', async () => {
		const before = Date.now();
		// oxlint-disable-next-line typescript/require-await -- must satisfy `StatsDataSource.query`, which returns a `Promise<LinkStats>`; nothing here needs an `await`.
		const query = async (): Promise<LinkStats> => STATS_FIXTURE;
		// oxlint-disable-next-line typescript/require-await -- must satisfy `loadLink`'s `LinkFetcher`-shaped parameter; nothing here needs an `await`.
		const fetchLink = async (): Promise<Link> => link();

		const { today } = await loadStatsPage({
			fetchLink,
			linkId: 'link-a',
			queryClient: { query },
			window: {},
		});
		const after = Date.now();

		expect(today).toBeInstanceOf(Date);
		expect(today.getTime()).toBeGreaterThanOrEqual(before);
		expect(today.getTime()).toBeLessThanOrEqual(after);
	});
});
