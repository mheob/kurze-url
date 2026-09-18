import type { Link, LinkStats, StatBreakdown, StatDay } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	Link as RouterLink,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import axe from 'axe-core';
import { I18nextProvider, useTranslation } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { AuthedShell } from '../../components/authed-shell';
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
import { createI18n } from '../../i18n';
import type { StatsWindow } from '../../lib/stats-window';
import type { Membership } from '../_authed';
import { statsView } from './teams.$teamSlug.links.$linkId_.stats.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below traces to
   `@kurze-url/api-client`'s generated `Link`/`LinkStats`/`StatBreakdown`/`StatDay` types, whose
   nested arrays and properties are mutable — generated codegen output, never edited by hand. */

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
];

const LINK: Link = {
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
};

const TOTALS_ZERO = { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 };
const EMPTY_BREAKDOWN: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: null,
};
const EMPTY_BREAKDOWNS = {
	bot_status: EMPTY_BREAKDOWN,
	browser: EMPTY_BREAKDOWN,
	country: EMPTY_BREAKDOWN,
	device: EMPTY_BREAKDOWN,
	os: EMPTY_BREAKDOWN,
	qr_vs_regular: EMPTY_BREAKDOWN,
	referrer: EMPTY_BREAKDOWN,
	utm_source: EMPTY_BREAKDOWN,
};

const SERIES: StatDay[] = [
	{
		clicks: 40,
		date: '2026-08-25',
		human_clicks: 32,
		human_unique_visitors: 28,
		unique_visitors: 30,
	},
	{ clicks: 0, date: '2026-08-26', human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
	{
		clicks: 55,
		date: '2026-08-27',
		human_clicks: 40,
		human_unique_visitors: 35,
		unique_visitors: 38,
	},
];

// A dimension whose top ten don't cover everything — carries an "other" row.
const BREAKDOWN_WITH_OTHER: StatBreakdown = {
	other_clicks: 12,
	other_unique_visitors: 9,
	other_values: 4,
	values: [
		{ clicks: 80, unique_visitors: 60, value: 'Chrome' },
		{ clicks: 20, unique_visitors: 15, value: 'Firefox' },
	],
};

// A dimension whose top ten is the whole picture — no "other" row.
const BREAKDOWN_WITHOUT_OTHER: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 60, unique_visitors: 40, value: 'macOS' },
		{ clicks: 40, unique_visitors: 30, value: 'Windows' },
	],
};

// The two dimensions the API only ever reports as a binary split.
const BOT_STATUS: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 750, unique_visitors: 700, value: 'human' },
		{ clicks: 250, unique_visitors: 200, value: 'bot' },
	],
};
const QR_VS_REGULAR: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 300, unique_visitors: 280, value: 'qr' },
		{ clicks: 700, unique_visitors: 620, value: 'regular' },
	],
};

const DATA_STATS: LinkStats = {
	analytics_enabled: true,
	breakdowns: {
		bot_status: BOT_STATUS,
		browser: BREAKDOWN_WITH_OTHER,
		country: BREAKDOWN_WITHOUT_OTHER,
		device: BREAKDOWN_WITHOUT_OTHER,
		os: BREAKDOWN_WITHOUT_OTHER,
		qr_vs_regular: QR_VS_REGULAR,
		referrer: BREAKDOWN_WITH_OTHER,
		utm_source: BREAKDOWN_WITHOUT_OTHER,
	},
	from: '2026-08-25',
	link_id: 'link-a',
	series: SERIES,
	to: '2026-08-27',
	totals: { clicks: 1000, human_clicks: 750, human_unique_visitors: 700, unique_visitors: 900 },
};

const DISABLED_STATS: LinkStats = {
	analytics_enabled: false,
	breakdowns: EMPTY_BREAKDOWNS,
	from: '2026-08-25',
	link_id: 'link-a',
	series: [],
	to: '2026-08-27',
	totals: TOTALS_ZERO,
};

const EMPTY_STATS: LinkStats = {
	analytics_enabled: true,
	breakdowns: EMPTY_BREAKDOWNS,
	from: '2026-08-25',
	link_id: 'link-a',
	series: [],
	to: '2026-08-27',
	totals: TOTALS_ZERO,
};

/**
 * Stands in for `RouteComponent` in `teams.$teamSlug.links.$linkId_.stats.tsx`,
 * which isn't exported — Task 11 only consumes that route, it doesn't modify
 * it, so this reproduces its exact composition for the state under test
 * instead of exporting it just to make this test possible. Real components
 * throughout, nothing mocked or stubbed.
 *
 * @param props - The component's props.
 * @param props.link - The link the statistics belong to.
 * @param props.stats - The statistics document.
 * @param props.teamSlug - The team slug, for the "back to link" route params.
 * @returns The rendered page body, exactly as the route composes it.
 */
function StatsPageBody({
	link,
	stats,
	teamSlug,
}: {
	readonly link: Link;
	readonly stats: LinkStats;
	readonly teamSlug: string;
}): React.JSX.Element {
	const { t } = useTranslation();
	const view = statsView(stats);
	const series = stats.series ?? [];

	return (
		<>
			<h1>{link.short_url}</h1>
			{/* oxlint-disable-next-line react/forbid-component-props -- plain navigational link, same as the route's own. */}
			<RouterLink params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
				{t('stats.backToLink')}
			</RouterLink>

			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={new Date('2026-09-18T00:00:00Z')}
				window={{ from: stats.from, to: stats.to }}
			/>

			{view === 'disabled' ? (
				<Empty>
					<EmptyHeader>
						<EmptyTitle>{t('stats.disabledTitle')}</EmptyTitle>
						<EmptyDescription>{t('stats.disabledBody')}</EmptyDescription>
					</EmptyHeader>
					<EmptyContent>
						<RouterLink params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
							{t('stats.disabledAction')}
						</RouterLink>
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
						language="en"
						qrVsRegular={stats.breakdowns.qr_vs_regular}
						totals={stats.totals}
					/>
					<StatSeriesChart from={stats.from} language="en" series={series} to={stats.to} />
					<div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
						<StatBreakdownCard
							breakdown={stats.breakdowns.browser}
							language="en"
							title={t('stats.browser')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.os}
							language="en"
							title={t('stats.os')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.device}
							language="en"
							title={t('stats.device')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.country}
							language="en"
							title={t('stats.country')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.referrer}
							language="en"
							title={t('stats.referrer')}
						/>
						<StatBreakdownCard
							breakdown={stats.breakdowns.utm_source}
							language="en"
							title={t('stats.utmSource')}
						/>
					</div>
				</>
			) : null}
		</>
	);
}

/**
 * Renders the composed page body inside the real, composed `AuthedShell` —
 * exactly how `_authed.tsx` nests every route's content inside
 * `SidebarInset`, whose own `<main>` is the page's only landmark; the same
 * reason `authed-shell.a11y.test.tsx` renders the real shell rather than a
 * bare fragment. Testing the page body on its own, with no shell around it,
 * would report a missing landmark for a reason that has nothing to do with
 * this page.
 *
 * @param stats - The statistics document to render the page body for.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderComposedPage(stats: LinkStats): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<AuthedShell
				currentTeamSlug="verein-a"
				isMaintainer={false}
				memberships={memberships}
				onSignOut={vi.fn<() => void>()}
				signingOut={false}
				theme="light"
			>
				<StatsPageBody link={LINK} stats={stats} teamSlug="verein-a" />
			</AuthedShell>
		),
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const linkDetailRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/$linkId',
	});
	const domainsRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/domains',
	});
	const newTeamRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/new-team',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute, linkDetailRoute, domainsRoute, newTeamRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('the statistics page', () => {
	// Finding: see `task-11-report.md` for whether axe reported anything real
	// here. `region` — a best-practice rule that only runs without
	// `.withTags()` — is what caught the design-system wave's only critical
	// defect (AuthedShell's own chrome sitting in no landmark), which is why
	// this suite never narrows the ruleset.
	it('has no axe violations under the default ruleset for the data state', async () => {
		renderComposedPage(DATA_STATS);
		await screen.findByRole('heading', { level: 1, name: 'https://kurze.url/abc123' });

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the disabled state', async () => {
		renderComposedPage(DISABLED_STATS);
		await screen.findByText('Click counting is off for this link');

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the empty state', async () => {
		renderComposedPage(EMPTY_STATS);
		await screen.findByText('No clicks in this window');

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});
