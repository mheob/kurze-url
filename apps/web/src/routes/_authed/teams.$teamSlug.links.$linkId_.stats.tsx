import type { Link, LinkStats } from '@kurze-url/api-client';
import {
	createFileRoute,
	Link as RouterLink,
	Navigate,
	notFound,
	type SearchSchemaInput,
} from '@tanstack/react-router';
import { TriangleAlertIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { StatBreakdownCard } from '../../components/stat-breakdown-card';
import { StatRangePicker } from '../../components/stat-range-picker';
import { StatRecordedJump } from '../../components/stat-recorded-jump';
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
import type { Language } from '../../lib/preferences';
import { parseStatsSearch, type StatsSearch, type StatsWindow } from '../../lib/stats-window';
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
 * Collapsing them is how this page would come to lie — in either direction.
 * `analytics_enabled: false` does not mean the document is empty: the
 * endpoint reads it from the link row and the totals/series/breakdowns from
 * the rollup independently, so a team that collected thousands of clicks and
 * later switched counting off keeps every one of them in this response.
 * Gating `'disabled'` on the flag alone would hide real, already-recorded
 * data behind an empty state for up to 90 days, until retention deletes it —
 * exactly the lie this function exists to prevent, just pointed the other
 * way. `'disabled'` is therefore reserved for the case an empty document
 * actually means "not counted": the flag is off *and* there is nothing to
 * show. Counting-off-with-history renders as ordinary `'data'`;
 * `StatsPageBody` is what adds the persistent banner explaining why.
 *
 * @param stats - The statistics document.
 * @returns Which of the three views to render.
 */
export function statsView(stats: LinkStats): 'data' | 'disabled' | 'empty' {
	if (!stats.analytics_enabled && stats.totals.clicks === 0) return 'disabled';
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

/**
 * Composes `loadStats` and `loadLink` — each independently testable, see
 * their own docstrings — with the one instant both the server and the
 * client render this page for. `RouteComponent` used to read that instant
 * from `new Date()` at render time instead, and the server and the browser
 * each call that separately: a server render landing just before midnight
 * that hydrates just after resolves `matchingPreset` differently on each
 * side, flipping the preset button's `aria-pressed` between the SSR markup
 * and the first client render. Threading it through the loader's own
 * return value — the way `format.ts` already takes its language as an
 * argument rather than reading a global — is what makes both renders agree.
 *
 * Two calls in parallel, the way `routes/index.tsx` already pairs its own:
 * the statistics document carries `link_id` and no slug, so without the
 * second the heading could not name the link. Both sides now agree on a
 * 404 — see `loadStats`'s own docstring.
 *
 * `fetchLink` stays a parameter — the same reason `loadLink`'s own
 * `LinkFetcher` shape does in the sibling detail route — so a test can pass
 * a fake instead of the real `getLinkFn`, whose `createServerFn` cannot run
 * directly under Vitest. Bundled into one object rather than four positional
 * parameters: `eslint(max-params)` caps at three, and this already has four
 * independent things to name.
 *
 * @param options - The dependencies and inputs this loader composes.
 * @param options.fetchLink - The server function to fetch the link through; only needs `loadLink`'s narrow shape.
 * @param options.linkId - The link's id, from the route's own path parameter.
 * @param options.queryClient - The query client to fetch the statistics through; only needs `query`.
 * @param options.window - The `from`/`to` bounds to request the statistics for.
 * @returns The link, its statistics, and the one instant both renders see.
 */
export async function loadStatsPage(
	options: Readonly<{
		fetchLink: Parameters<typeof loadLink>[0];
		linkId: string;
		queryClient: StatsDataSource;
		window: StatsSearch;
	}>,
): Promise<{ link: Link; stats: LinkStats; today: Date }> {
	const [stats, link] = await Promise.all([
		loadStats(options.queryClient, options.linkId, options.window),
		loadLink(options.fetchLink, options.linkId),
	]);
	// Read once, here, rather than left for `RouteComponent` to read at
	// render time — that's the fix this function exists for (see the
	// docstring above). The accepted cost: a tab left open across local
	// midnight without navigating keeps this stale `today` until the next
	// loader run, so the calendar disables today's own date
	// (`stat-range-picker.tsx`'s `disabled: { after: today }`) and a preset
	// button computes a window ending yesterday. It self-heals on the next
	// navigation — a live clock instead would just reintroduce the
	// server/client mismatch this was written to prevent.
	return { link, stats, today: new Date() };
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
	loader: async ({ context, deps, params }) =>
		loadStatsPage({
			fetchLink: getLinkFn,
			linkId: params.linkId,
			queryClient: context.queryClient,
			window: deps,
		}),
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

export interface StatsPageBodyProps {
	readonly language: Language;
	readonly link: Link;
	readonly onWindowChange: (window: StatsWindow) => void;
	readonly stats: LinkStats;
	readonly teamSlug: string;
	// Injected rather than read from the clock, same reasoning as
	// `StatRangePicker`'s own `today` prop: it is what lets this render the
	// same way on every call, in Storybook, in the a11y suite and here.
	readonly today: Date;
}

/**
 * The presentational body of a link's statistics page — pure and
 * prop-driven, the same idiom `LinkList`/`LinkForm`/`AuthedShell` already
 * use so a route's router/mutation wiring can be tested separately from what
 * it renders. `RouteComponent` below owns that wiring (`Route.useParams`,
 * `Route.useLoaderData`, `usePreferences`, `Route.useNavigate`) and passes
 * plain data and a callback in here.
 *
 * `teams.$teamSlug.links.$linkId_.stats.a11y.test.tsx` renders this exact
 * component, with no router loader or query client involved — so a
 * composition change made here (a heading level, a dropped back-link, a
 * reordered breakdown) is caught by that suite in the same place production
 * renders it, rather than against a second, hand-kept copy that could drift
 * out from under it unnoticed.
 *
 * @param props - The component's props.
 * @param props.language - The active language, for number and date formatting.
 * @param props.link - The link the statistics belong to.
 * @param props.onWindowChange - Called with the newly chosen window, from `StatRangePicker`.
 * @param props.stats - The statistics document.
 * @param props.teamSlug - The team slug, for the "back to link" route params.
 * @param props.today - The current instant, injected for testability.
 * @returns The rendered page body.
 */
export function StatsPageBody({
	language,
	link,
	onWindowChange,
	stats,
	teamSlug,
	today,
}: StatsPageBodyProps): React.JSX.Element {
	const { t } = useTranslation();
	const view = statsView(stats);
	const series = stats.series ?? [];
	// The condition is the field alone, with nothing about the window in it,
	// and that is an invariant rather than an oversight. Reaching either empty
	// view requires `totals.clicks === 0`; every recorded click writes a
	// `total` row (`analytics.Dimensions.Rows` always emits one) and the
	// upsert only ever adds a positive count — so a servable row inside the
	// requested window would have made the totals positive and neither empty
	// view would be on screen. In an empty view a present `recorded` is
	// therefore always outside the window, and an overlap check here would
	// guard a state the data model cannot produce.
	const recorded = stats.recorded;

	return (
		<>
			<h1>{link.short_url}</h1>
			{/* oxlint-disable-next-line react/forbid-component-props -- no className here; this is
			    a plain navigational link back to the link's own detail page. */}
			<RouterLink params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
				{t('stats.backToLink')}
			</RouterLink>

			<StatRangePicker
				language={language}
				onChange={onWindowChange}
				today={today}
				window={{ from: stats.from, to: stats.to }}
			/>

			{view === 'disabled' ? (
				<Empty>
					<EmptyHeader>
						{/* `EmptyTitle` hardcodes a `<div>` with no `render` prop, so a real
						    `<h2>` nests inside it rather than reaching for `role="heading"`,
						    which `jsx-a11y/prefer-tag-over-role` refuses on a `<div>` — the
						    same pattern `StatSummary`/`StatSeriesChart`/`StatBreakdownCard`
						    already use for `CardTitle`. Without it, this state's whole
						    heading outline is the page's single `<h1>`. */}
						<EmptyTitle>
							<h2>{t('stats.disabledTitle')}</h2>
						</EmptyTitle>
						<EmptyDescription>
							{t(recorded === undefined ? 'stats.disabledBody' : 'stats.disabledElsewhere')}
						</EmptyDescription>
					</EmptyHeader>
					<EmptyContent>
						{recorded === undefined ? null : (
							<StatRecordedJump language={language} onSelect={onWindowChange} recorded={recorded} />
						)}
						<RouterLink params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
							{t('stats.disabledAction')}
						</RouterLink>
					</EmptyContent>
				</Empty>
			) : null}

			{view === 'empty' ? (
				<Empty>
					<EmptyHeader>
						{/* Same reason as the 'disabled' state just above. */}
						<EmptyTitle>
							<h2>{t('stats.noClicksTitle')}</h2>
						</EmptyTitle>
						<EmptyDescription>
							{t(recorded === undefined ? 'stats.noClicksBody' : 'stats.noClicksElsewhere')}
						</EmptyDescription>
					</EmptyHeader>
					{recorded === undefined ? null : (
						<EmptyContent>
							<StatRecordedJump language={language} onSelect={onWindowChange} recorded={recorded} />
						</EmptyContent>
					)}
				</Empty>
			) : null}

			{view === 'data' ? (
				<>
					{!stats.analytics_enabled ? (
						// Counting is off, but this document still carries clicks recorded
						// before it was switched off — `statsView`'s own docstring explains
						// why that is 'data', not 'disabled'. Rendering the figures with no
						// explanation would let a reader assume they're still growing; this
						// banner is what keeps that assumption from forming. Same markup as
						// `ShortUrlNotice`, the other persistent, visible warning banner in
						// this app.
						<p
							className="flex items-center gap-2 border border-destructive/50 bg-destructive/10 p-3 text-sm text-foreground"
							role="note"
						>
							<TriangleAlertIcon aria-hidden />
							{t('stats.countingOffBanner')}
						</p>
					) : null}
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

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { link, stats, today } = Route.useLoaderData();
	const { language } = usePreferences();
	const navigate = Route.useNavigate();

	return (
		<StatsPageBody
			language={language}
			link={link}
			onWindowChange={(next) => {
				void navigate({ search: next });
			}}
			stats={stats}
			teamSlug={teamSlug}
			today={today}
		/>
	);
}
