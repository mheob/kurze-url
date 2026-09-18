import type { LinkStats } from '@kurze-url/api-client';
import { createFileRoute, Link, Navigate, type SearchSchemaInput } from '@tanstack/react-router';
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
		// without the second the heading could not name the link.
		const [stats, link] = await Promise.all([
			// oxlint-disable-next-line typescript/no-deprecated -- every other loader in this app reaches `ensureQueryData` through its own narrow `...DataSource` interface (e.g. `LinksDataSource` in `teams.$teamSlug.links.index.tsx`), which incidentally hides the method's `@deprecated` overload from the type checker; this loader calls it directly on the real `QueryClient`, so the same deprecated signature is visible here.
			context.queryClient.ensureQueryData(linkStatsQueryOptions(params.linkId, deps)),
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
