import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	Outlet,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../../i18n';
import type { AuditFilters } from '../../lib/audit-filters';
import { AuditLogRouteView, type AuditLogPageData } from './teams.$teamSlug.audit-log.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- the one finding in this file is
   `renderRouteView`'s `options`, which nests `AuditLogPageData` and through it
   `@kurze-url/api-client`'s generated `AuditEntry`/`Member`, whose own properties are mutable:
   `Readonly<>` is shallow and cannot reach them from this side of the codegen boundary. */

/**
 * What the loader returns for a team with one page of history. The entry
 * itself does not matter here — this suite is about what the route does with
 * the filter bar's `onChange`, not about the table.
 */
const DATA: AuditLogPageData = {
	entries: [
		{
			action: 'link.created',
			actor_user_id: 'user-a',
			created_at: '2026-09-18T09:30:00.000Z',
			entity_id: 'link-a',
			entity_type: 'link',
			id: 1,
			metadata: {},
		},
	],
	forbidden: false,
	members: [
		{
			created_at: '2026-01-01T00:00:00.000Z',
			email: 'vorstand@verein-a.example',
			role: 'owner',
			user_id: 'user-a',
		},
	],
	page: 1,
	perPage: 20,
	total: 1,
};

/**
 * Renders the real `AuditLogRouteView` inside a minimal memory router, the
 * same scaffolding `new-team.test.tsx` uses to drive its own route component:
 * a router is needed because the page renders TanStack `Link`s, and nothing
 * else about the route tree is.
 *
 * `navigate` is captured rather than forwarded to the router. What this suite
 * has to prove is the payload the route hands the router — the *whole* next
 * `AuditFilters`, not a partial patch — because that object is what ends up in
 * the URL, and the URL is the only reason the filters live in search
 * parameters at all. Where TanStack then writes that object into the address
 * bar is already covered by the pagination `href` assertions in the a11y
 * suite.
 *
 * @param options - What to render this route view for.
 * @param options.data - What the loader returned.
 * @param options.filters - The filters the search parameters already hold.
 * @returns The recorded `navigate` payloads, and Testing Library's render result.
 */
function renderRouteView(
	options: Readonly<{ data: AuditLogPageData; filters?: AuditFilters }>,
): { navigated: AuditFilters[] } & ReturnType<typeof render> {
	const navigated: AuditFilters[] = [];
	const rootRoute = createRootRoute({ component: () => <Outlet /> });
	const auditLogRoute = createRoute({
		component: () => (
			<AuditLogRouteView
				data={options.data}
				filters={options.filters ?? { page: 1 }}
				language="en"
				// oxlint-disable-next-line typescript/require-await -- stands in for the route's own `navigate`; this fake records its argument and has nothing to await.
				navigate={async (navigateOptions) => {
					navigated.push(navigateOptions.search);
				}}
				teamSlug="verein-a"
			/>
		),
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/audit-log',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/teams/verein-a/audit-log'] }),
		routeTree: rootRoute.addChildren([auditLogRoute]),
	});

	return {
		navigated,
		...render(
			<I18nextProvider i18n={createI18n('en')}>
				<RouterProvider router={router} />
			</I18nextProvider>,
		),
	};
}

describe('the audit log route', () => {
	/**
	 * The one line this covers is what makes a narrowed view shareable: a
	 * chosen filter goes into the URL rather than into component state, so a
	 * reader can bookmark it, back-button out of it, or send it to another
	 * admin. Task 5 covers the bar *emitting* `onChange`; without this, nothing
	 * covered the route *consuming* it, and deleting that line would leave a
	 * page whose filters visibly work right up until the reader copies the
	 * address.
	 */
	it('puts a chosen filter into the search parameters', async () => {
		const user = userEvent.setup();
		const { navigated } = renderRouteView({ data: DATA });
		await screen.findByRole('heading', { level: 1, name: 'History' });

		await user.selectOptions(screen.getByLabelText('Entity'), 'link');

		expect(navigated).toStrictEqual([{ entityType: 'link', page: 1 }]);
	});

	/**
	 * The whole object, never a patch: a second choice has to arrive carrying
	 * the first, or narrowing by person after narrowing by entity would quietly
	 * drop the entity filter from the URL. `page` comes back to 1 in the same
	 * payload, because a filtered view still holding a page number from the
	 * unfiltered result is a reader looking at an empty page for no visible
	 * reason.
	 */
	it('carries the filters already set alongside the new one', async () => {
		const user = userEvent.setup();
		const { navigated } = renderRouteView({
			data: DATA,
			filters: { entityType: 'link', page: 3 },
		});
		await screen.findByRole('heading', { level: 1, name: 'History' });

		await user.selectOptions(screen.getByLabelText('Person'), 'user-a');

		expect(navigated).toStrictEqual([{ actor: 'user-a', entityType: 'link', page: 1 }]);
	});

	/**
	 * The other half of the route's choice: a refused read renders the
	 * explanation and no controls at all, rather than a filter bar that could
	 * only produce the same refusal again.
	 */
	it('renders the refusal instead of the page when the read was refused', async () => {
		renderRouteView({ data: { forbidden: true } });
		await screen.findByRole('heading', { level: 2, name: 'This part of the team is for admins' });

		expect(screen.queryByLabelText('Entity')).not.toBeInTheDocument();
	});
});
