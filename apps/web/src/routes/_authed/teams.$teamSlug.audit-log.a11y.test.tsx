import type { AuditEntry, Member } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import axe from 'axe-core';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { AuthedShell } from '../../components/authed-shell';
import { createI18n } from '../../i18n';
import type { AuditFilters } from '../../lib/audit-filters';
import type { Membership } from '../_authed';
import { AuditLogForbidden, AuditLogPageBody } from './teams.$teamSlug.audit-log.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- two causes, neither a declaration
   this file owns: `renderComposedPage` takes React's own `React.ReactNode`, a union that admits a
   mutable array `Readonly<>` cannot reach into, and `pageBody` takes an array of
   `@kurze-url/api-client`'s generated `AuditEntry`, whose properties are mutable — generated
   codegen output, never edited by hand. */

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
];

const MEMBERS: Member[] = [
	{
		created_at: '2026-01-01T00:00:00.000Z',
		email: 'vorstand@verein-a.example',
		role: 'owner',
		user_id: 'user-a',
	},
	{
		created_at: '2026-02-01T00:00:00.000Z',
		email: 'kasse@verein-a.example',
		role: 'admin',
		user_id: 'user-b',
	},
];

/**
 * Three entries chosen for what they make the table render, not for realism:
 * one with nested metadata (the disclosure button exists), one whose actor is
 * no longer in `MEMBERS` (the "a former member" fallback), and one with no
 * metadata at all (no disclosure button — the branch that renders a plain
 * cell instead of a control).
 */
const ENTRIES: AuditEntry[] = [
	{
		action: 'link.updated',
		actor_user_id: 'user-a',
		created_at: '2026-09-18T09:30:00.000Z',
		entity_id: 'link-a',
		entity_type: 'link',
		id: 3,
		metadata: { changed: ['slug'], slug: { from: 'alt', to: 'neu' } },
	},
	{
		action: 'team_member.removed',
		actor_user_id: 'user-gone',
		created_at: '2026-09-17T14:05:00.000Z',
		entity_id: 'member-c',
		entity_type: 'team_member',
		id: 2,
		metadata: { email: 'ehemalig@verein-a.example' },
	},
	{
		action: 'link.password_set',
		actor_user_id: 'user-b',
		created_at: '2026-09-16T08:00:00.000Z',
		entity_id: 'link-a',
		entity_type: 'link',
		id: 1,
		metadata: {},
	},
];

const NO_FILTERS: AuditFilters = { page: 1 };
const FILTERED: AuditFilters = { entityType: 'link', from: '2026-09-01', page: 1 };

/**
 * Renders the real page components — exported from
 * `teams.$teamSlug.audit-log.tsx` for exactly this reason — inside the real,
 * composed `AuthedShell`, exactly how `_authed.tsx` nests every route's
 * content inside `SidebarInset`, whose own `<main>` is the page's only
 * landmark. Testing a page body on its own, with no shell around it, reports
 * a missing landmark for a reason that has nothing to do with this page; the
 * statistics page's own a11y suite renders through the same harness for the
 * same reason.
 *
 * The memory router carries the routes this page and the shell link to.
 * `/teams/$teamSlug/audit-log` is among them because the pagination links
 * target this page itself.
 *
 * @param children - The page content to render inside the shell.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderComposedPage(children: React.ReactNode): ReturnType<typeof render> {
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
				{children}
			</AuthedShell>
		),
	});
	const auditLogRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/audit-log',
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
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
		routeTree: rootRoute.addChildren([auditLogRoute, linksRoute, domainsRoute, newTeamRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

/** The page size the API applies to this endpoint, as its envelope reports it. */
const PER_PAGE = 20;

/**
 * @param props - The page body's props worth varying between these cases.
 * @param props.entries - The entries to render; an empty array reaches an empty state.
 * @param props.filters - The filters as the route's search parameters hold them.
 * @param props.total - How many entries match across every page.
 * @returns The composed page body.
 */
function pageBody({
	entries,
	filters,
	total,
}: Readonly<{ entries: AuditEntry[]; filters: AuditFilters; total: number }>): React.JSX.Element {
	return (
		<AuditLogPageBody
			entries={entries}
			filters={filters}
			language="en"
			members={MEMBERS}
			onFiltersChange={vi.fn<(next: AuditFilters) => void>()}
			page={filters.page}
			perPage={PER_PAGE}
			teamSlug="verein-a"
			total={total}
		/>
	);
}

describe('the audit log page', () => {
	// The default ruleset, never narrowed with `.withTags()`: `region` — a
	// best-practice rule that only runs unnarrowed — is what caught the design
	// system wave's only critical defect, and the statistics page's own suite
	// carries the same note.
	it('has no axe violations under the default ruleset for a populated log', async () => {
		// `total` larger than one page, so the pagination renders a real "next
		// page" link alongside the disabled "previous page" — both branches of
		// that block are on screen for the scan.
		renderComposedPage(pageBody({ entries: ENTRIES, filters: NO_FILTERS, total: 45 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		expect(screen.getByRole('link', { name: 'Next page' })).toBeInTheDocument();
		expect(screen.getByRole('cell', { name: 'Link changed' })).toBeInTheDocument();

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the empty state', async () => {
		renderComposedPage(pageBody({ entries: [], filters: NO_FILTERS, total: 0 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		// Asserted before the scan for the same reason the populated case
		// asserts its table cell: an empty branch that rendered nothing at all
		// would have no violations either, and this test would pass on it.
		expect(screen.getByText('Nothing has happened in this team yet.')).toBeInTheDocument();
		expect(screen.getByLabelText('Entity')).toBeInTheDocument();

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the admin refusal', async () => {
		renderComposedPage(<AuditLogForbidden />);
		// `EmptyTitle` renders a plain `<div>`, so this state's own heading
		// exists only because a real `<h2>` was nested inside it — asking for
		// `level: 2` is what proves that heading survives, where `findByText`
		// would pass just as well against a bare `<div>`.
		await screen.findByRole('heading', { level: 2, name: 'This part of the team is for admins' });

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});

describe('the two empty states', () => {
	it('says nothing has happened yet when no filter is set', async () => {
		renderComposedPage(pageBody({ entries: [], filters: NO_FILTERS, total: 0 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		expect(screen.getByText('Nothing has happened in this team yet.')).toBeInTheDocument();
	});

	it('says the filters matched nothing when one is set', async () => {
		renderComposedPage(pageBody({ entries: [], filters: FILTERED, total: 0 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		expect(screen.getByText('No changes match these filters.')).toBeInTheDocument();
		// The filter bar stays on screen in this state: a reader who filtered
		// their way to nothing needs the control that got them there to get out.
		expect(screen.getByRole('button', { name: 'Clear the filters' })).toBeInTheDocument();
	});
});

describe('the pagination', () => {
	it('carries the active filters into the next page', async () => {
		renderComposedPage(pageBody({ entries: ENTRIES, filters: FILTERED, total: 45 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		// Paging must not silently clear what the reader filtered by — the
		// whole point of keeping the filters in the URL.
		expect(screen.getByRole('link', { name: 'Next page' })).toHaveAttribute(
			'href',
			'/teams/verein-a/audit-log?entityType=link&from=2026-09-01&page=2',
		);
	});

	/**
	 * A page number outlives the entries it was made on — that is the cost of
	 * putting the page in the URL, and a bookmark or a shared link is how a
	 * reader gets here. Without the pagination rendering on an empty page this
	 * would be a dead end: no rows, and nothing to click back to.
	 */
	it('still offers a way back from a page past the end', async () => {
		renderComposedPage(pageBody({ entries: [], filters: { page: 3 }, total: 45 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		expect(screen.getByRole('link', { name: 'Previous page' })).toHaveAttribute(
			'href',
			'/teams/verein-a/audit-log?page=2',
		);
		// …and not the copy that claims the team has no history at all, which
		// would be a plain falsehood over 45 real entries.
		expect(screen.queryByText('Nothing has happened in this team yet.')).not.toBeInTheDocument();
		expect(screen.getByText('No changes match these filters.')).toBeInTheDocument();
	});

	it('offers no next page once the last one is on screen', async () => {
		renderComposedPage(pageBody({ entries: ENTRIES, filters: NO_FILTERS, total: 3 }));
		await screen.findByRole('heading', { level: 1, name: 'History' });

		expect(screen.queryByRole('link', { name: 'Next page' })).not.toBeInTheDocument();
	});
});
