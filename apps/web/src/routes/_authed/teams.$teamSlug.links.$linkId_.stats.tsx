import type { LinkStats } from '@kurze-url/api-client';
import {
	createFileRoute,
	Link,
	Navigate,
	notFound,
	type SearchSchemaInput,
} from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { StatBreakdownCard } from '../../components/stat-breakdown-card';
import { StatRangePicker } from '../../components/stat-range-picker';
import { StatSeriesChart } from '../../components/stat-series-chart';
import { StatSummary } from '../../components/stat-summary';
import {
	Empty,
	EmptyContent,
	EmptyDescription,
	EmptyHeader,
	EmptyTitle,
} from '../../components/ui/empty';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { reportUnexpected } from '../../lib/observability';
import { parseStatsSearch, type StatsSearch } from '../../lib/stats-window';
import { usePreferences } from '../../lib/use-preferences';
import { getLinkFn, linkStatsQueryOptions } from '../../server/links';
import { requireTeamId } from '../_authed';
import { loadLink } from './teams.$teamSlug.links.$linkId';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below traces to
   `@kurze-url/api-client`'s generated `LinkStats`/`StatBreakdown`/`StatDay` types (whose nested
   arrays are mutable, generated codegen output never edited by hand) or to TanStack Router's own
   `validateSearch`/`beforeLoad`/`loaderDeps`/`loader` option shapes, none of which this file
   owns. */

/**
 * The three states the endpoint distinguishes, and the page must too.
 * Collapsing them is how this page would come to lie: a link with counting
 * switched off returns the same zeroes as a link nobody clicked.
 *
 * @param stats - The statistics document.
 * @returns Which of the three views to render.
 */
export function statsView(stats: LinkStats): 'data' | 'disabled' | 'empty' {
	if (!stats.analytics_enabled) return 'disabled';
	return stats.totals.clicks > 0 ? 'data' : 'empty';
}

/**
 * The one method `loadStats` below reaches through — a real `QueryClient`
 * satisfies this structurally via `query()`, so the loader needs no cast,
 * and this route's own test can pass a hand-built fake instead of a real
 * `QueryClient` (which cannot run directly under Vitest — same reasoning as
 * `LinkFetcher` in the sibling detail route). `query()`, not `ensureQueryData`:
 * the latter is `@deprecated` in the installed `@tanstack/query-core` in
 * favour of exactly this replacement, `query({ ...options, staleTime: 'static' })`
 * — see `loadStats`'s own docstring for why `staleTime` has to travel with it.
 */
interface StatsDataSource {
	readonly query: (
		options: ReturnType<typeof linkStatsQueryOptions> & { staleTime: 'static' },
	) => Promise<LinkStats>;
}

/**
 * `GET /v1/links/{id}/stats` carries the same `authz.LinkViewerScope` as
 * `GET /v1/links/{id}` (see `loadLink`, the sibling detail route), so a link
 * that doesn't exist or belongs to another team 404s from either endpoint.
 * Without this, whichever of the two `Promise.all` calls in the loader below
 * happens to reject first decided which page the visitor saw for the exact
 * same condition: this route's own bare "Not found." paragraph, still inside
 * the sidebar, if the stats fetch lost the race — or the root `NotFound`
 * page, via `loadLink`'s own handling, if the link fetch did. Normalizing
 * both to the router's `notFound()` here picks the second outcome
 * unconditionally, matching what already happens when only the link fetch
 * 404s — a missing/foreign link is one condition, and both fetches now agree
 * on the one page it renders.
 *
 * `query()`'s own default `staleTime` is `0` — every fetch through it would
 * be stale the instant it lands, unlike the deprecated `ensureQueryData`
 * this replaces (whose own default came from `linkStatsQueryOptions`, which
 * sets none, so it fell through to the client's global default of `0` too;
 * `'static'` is what the deprecation notice itself names as the
 * like-for-like replacement, and it is what keeps this loader's fetch from
 * being immediately eligible for a second, silent refetch the moment
 * anything else reads the same query key).
 *
 * @param queryClient - The query client to fetch through; only needs `query`.
 * @param linkId - The link's id, from the route's own path parameter.
 * @param window - The `from`/`to` bounds to request the statistics for.
 * @returns The fetched statistics document.
 */
export async function loadStats(
	queryClient: StatsDataSource,
	linkId: string,
	window: StatsSearch,
): Promise<LinkStats> {
	try {
		return await queryClient.query({
			...linkStatsQueryOptions(linkId, window),
			staleTime: 'static',
		});
	} catch (error) {
		const classified = classifyApiError(error);
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `notFound()` is its control flow, not an Error.
		if (classified.kind === 'notFound') throw notFound();
		throw error;
	}
}

// oxlint-disable-next-line sort-keys -- `validateSearch` has to stay declared before `loaderDeps`/`loader`: see the comment on it below.
export const Route = createFileRoute('/_authed/teams/$teamSlug/links/$linkId_/stats')({
	// Declared before loaderDeps/loader, not for readability: loaderDeps's own
	// `search` parameter is typed from this function's return type, and
	// moving it later makes that inference fall back to {}. The sibling links
	// index carries the same note for the same reason.
	validateSearch: (search: { from?: string; to?: string } & SearchSchemaInput): StatsSearch =>
		parseStatsSearch(search),
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	loaderDeps: ({ search }) => ({ from: search.from, to: search.to }),
	loader: async ({ context, deps, params }) => {
		// Two calls in parallel, the way `routes/index.tsx` already pairs its
		// own: the statistics document carries `link_id` and no slug, so
		// without the second the heading could not name the link. Both sides
		// now agree on a 404 — see `loadStats`'s own docstring.
		const [stats, link] = await Promise.all([
			loadStats(context.queryClient, params.linkId, deps),
			loadLink(getLinkFn, params.linkId),
		]);
		return { link, stats };
	},
	component: RouteComponent,
	errorComponent: StatsError,
});

/**
 * Same shape as `LinksError`/`DomainsError`: a route-level `errorComponent`
 * is the nearest one TanStack Router renders, so without reporting here a
 * 500 from this endpoint never reaches `RootErrorPage` and no event is ever
 * sent. A hand-edited URL with `from` later than `to` reaches here as a
 * 422 — that is the correct outcome, and the reason `parseStatsSearch`
 * deliberately does not enforce the ordering itself.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function StatsError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { link, stats } = Route.useLoaderData();
	const { language } = usePreferences();
	const navigate = Route.useNavigate();
	const { t } = useTranslation();
	const view = statsView(stats);
	const series = stats.series ?? [];

	return (
		<>
			<h1>{link.short_url}</h1>
			{/* oxlint-disable-next-line react/forbid-component-props -- no className here; this is
			    a plain navigational link back to the link's own detail page. */}
			<Link params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
				{t('stats.backToLink')}
			</Link>

			<StatRangePicker
				language={language}
				onChange={(next) => {
					void navigate({ search: next });
				}}
				today={new Date()}
				window={{ from: stats.from, to: stats.to }}
			/>

			{view === 'disabled' ? (
				<Empty>
					<EmptyHeader>
						<EmptyTitle>{t('stats.disabledTitle')}</EmptyTitle>
						<EmptyDescription>{t('stats.disabledBody')}</EmptyDescription>
					</EmptyHeader>
					<EmptyContent>
						<Link params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
							{t('stats.disabledAction')}
						</Link>
					</EmptyContent>
				</Empty>
			) : null}

			{view === 'empty' ? (
				<Empty>
					<EmptyHeader>
						<EmptyTitle>{t('stats.noClicksTitle')}</EmptyTitle>
						<EmptyDescription>{t('stats.noClicksBody')}</EmptyDescription>
					</EmptyHeader>
				</Empty>
			) : null}

			{view === 'data' ? (
				<>
					<StatSummary
						botStatus={stats.breakdowns.bot_status}
						language={language}
						qrVsRegular={stats.breakdowns.qr_vs_regular}
						totals={stats.totals}
					/>
					<StatSeriesChart from={stats.from} language={language} series={series} to={stats.to} />
					<div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
						<StatBreakdownCard
							breakdown={stats.breakdowns.browser}
							language={language}
							title={t('stats.browser')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.os}
							language={language}
							title={t('stats.os')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.device}
							language={language}
							title={t('stats.device')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.country}
							language={language}
							title={t('stats.country')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.referrer}
							language={language}
							title={t('stats.referrer')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.utm_source}
							language={language}
							title={t('stats.utmSource')}
						/>
					</div>
				</>
			) : null}
		</>
	);
}
