import type { Member } from '@kurze-url/api-client';
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
import type { Membership } from '../_authed';
import { MembersPageBody } from './teams.$teamSlug.members.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below traces to
   `@kurze-url/api-client`'s generated `Member` type, whose properties are mutable — generated
   codegen output, never edited by hand — or to React's own `React.ReactNode`, a union that admits
   a mutable array `Readonly<>` cannot reach into. Neither is a declaration this file owns. */

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
];

/**
 * Two members, one an owner and the other an admin: the owner is this team's
 * sole owner, so its row renders a disabled select plus `members.soleOwner`'s
 * explanation and no remove control (`isSoleOwner`), while the admin's row
 * renders a live select and a remove control (`canManageMember`) — together
 * they put every control this page can render for an admin-or-above actor on
 * screen for the scan.
 */
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
 * Renders the real page body — exported from `teams.$teamSlug.members.tsx`
 * for exactly this reason — inside the real, composed `AuthedShell`, exactly
 * how `_authed.tsx` nests every route's content inside `SidebarInset`, whose
 * own `<main>` is the page's only landmark. Testing a page body on its own,
 * with no shell around it, reports a missing landmark for a reason that has
 * nothing to do with this page; `teams.$teamSlug.audit-log.a11y.test.tsx`
 * renders through the same harness for the same reason.
 *
 * The memory router carries the routes the shell links to, including
 * `/teams/$teamSlug/members` itself.
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
	const membersRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/members',
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
	const auditLogRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/audit-log',
	});
	const newTeamRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/new-team',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([
			membersRoute,
			linksRoute,
			domainsRoute,
			auditLogRoute,
			newTeamRoute,
		]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('the members page', () => {
	// The default ruleset, never narrowed with `.withTags()`: `region` — a
	// best-practice rule that only runs unnarrowed — is what caught the design
	// system wave's only critical defect, and the audit log and statistics
	// pages' own suites carry the same note.
	it('has no axe violations under the default ruleset for an admin', async () => {
		renderComposedPage(
			<MembersPageBody
				currentRole="owner"
				currentUserId="user-a"
				failedUserId={null}
				inviteFailure={null}
				invitePending={false}
				inviteResult={null}
				members={MEMBERS}
				mutationFailure={null}
				onInvite={vi.fn<(values: { email: string; role: string }) => void>()}
				onRemove={vi.fn<(userId: string) => void>()}
				onRoleChange={vi.fn<(userId: string, role: string) => void>()}
				pendingUserId={null}
				removedEmail={null}
				roleChanged={false}
			/>,
		);
		await screen.findByRole('heading', { level: 1, name: 'Who has access' });

		// Asserted before the scan, the same reason the audit log's own suite
		// asserts its table cell first: a branch that silently rendered nothing
		// would have no violations either, and this test would pass on it.
		expect(screen.getByRole('combobox', { name: 'Role for kasse@verein-a.example' })).toBeEnabled();
		expect(screen.getByRole('button', { name: 'Remove' })).toBeInTheDocument();
		expect(screen.getByText('A team must always have at least one owner.')).toBeInTheDocument();

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset with both success lines showing', async () => {
		// The two `<output>` lines this task added render together here so the
		// scan covers both — `roleChanged` and `removedEmail` are otherwise
		// mutually exclusive in the real page (only one mutation is ever in
		// flight at a time), but nothing stops both being true at once as far
		// as `MembersPageBody`'s own props are concerned, and axe should still
		// have nothing to say about it.
		renderComposedPage(
			<MembersPageBody
				currentRole="owner"
				currentUserId="user-a"
				failedUserId={null}
				inviteFailure={null}
				invitePending={false}
				inviteResult={null}
				members={MEMBERS}
				mutationFailure={null}
				onInvite={vi.fn<(values: { email: string; role: string }) => void>()}
				onRemove={vi.fn<(userId: string) => void>()}
				onRoleChange={vi.fn<(userId: string, role: string) => void>()}
				pendingUserId={null}
				removedEmail="ehemalig@verein-a.example"
				roleChanged
			/>,
		);
		await screen.findByRole('heading', { level: 1, name: 'Who has access' });

		expect(screen.getAllByRole('status')).toHaveLength(2);
		expect(screen.getByText('Role updated.')).toBeInTheDocument();
		expect(screen.getByText('ehemalig@verein-a.example was removed.')).toBeInTheDocument();

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});
