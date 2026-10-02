import type { Folder } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { createRef } from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { NameManagementBody, type NameManagementBodyProps } from './name-management-body';

const folders: Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Newsletter', team_id: 'team-a' },
];

/**
 * Renders the real `NameManagementBody` — the same
 * `MembersPageBody`/`AuditLogRouteView` idiom the folders and tags routes
 * both rely on — inside a minimal memory router: `NameList` renders TanStack
 * `<Link>`s, the same reason `name-list.test.tsx`'s own `renderList` needs
 * one.
 *
 * @param overrides - Partial props to override on the default fixture.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `NameManagementBodyProps` nests a `React.RefObject` (`headingRef`), whose `.current` is deliberately mutable by React's own design; `Readonly<>`/`Partial<>` does not reach it.
function renderBody(overrides: Partial<NameManagementBodyProps> = {}): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<NameManagementBody
				canEdit={overrides.canEdit ?? true}
				createError={overrides.createError}
				createKey={overrides.createKey ?? 0}
				headingRef={overrides.headingRef ?? createRef<HTMLHeadingElement>()}
				items={overrides.items ?? folders}
				namespace={overrides.namespace ?? 'folders'}
				onCreate={overrides.onCreate ?? vi.fn<(name: string) => void>()}
				onDelete={overrides.onDelete ?? vi.fn<(itemId: string) => void>()}
				onDismissError={overrides.onDismissError ?? vi.fn<(itemId: string) => void>()}
				onRename={
					overrides.onRename ??
					// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `NameList` awaits; the fake has nothing to await itself.
					vi.fn<(itemId: string, name: string) => Promise<boolean>>(async () => true)
				}
				rowError={overrides.rowError ?? null}
				teamSlug={overrides.teamSlug ?? 'verein'}
			/>
		),
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe(NameManagementBody, () => {
	/**
	 * The design spec's own rule: "Role gating on the link pages... The
	 * folders page gates by role from the start". A viewer must get no
	 * control the API would refuse, not merely a disabled one.
	 */
	it('does not show a viewer the create form', async () => {
		renderBody({ canEdit: false });
		// The router resolves its match asynchronously even for a trivial
		// route, so this waits for the heading first — a synchronous
		// `queryByRole` here would pass vacuously before anything rendered at
		// all, proving nothing about the create form specifically.
		await screen.findByRole('heading', { name: 'Folders' });
		expect(screen.queryByRole('textbox', { name: 'Folder name' })).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Create folder' })).not.toBeInTheDocument();
	});

	it('shows an editor the create form', async () => {
		renderBody({ canEdit: true });
		await expect(
			screen.findByRole('textbox', { name: 'Folder name' }),
		).resolves.toBeInTheDocument();
	});

	it('lands a create error on the name field', async () => {
		renderBody({ createError: 'A folder with this name already exists.' });
		await expect(
			screen.findByRole('textbox', { name: 'Folder name' }),
		).resolves.toHaveAccessibleDescription('A folder with this name already exists.');
	});

	/** The failure-surface table's own row: "delete: any error -> role="alert" next to the list, with the row left in place". */
	it('shows a delete error as an alert with the row left in place', async () => {
		renderBody({
			rowError: { action: 'delete', itemId: 'f1', message: 'Could not delete this folder.' },
		});
		await expect(screen.findByRole('alert')).resolves.toHaveTextContent(
			'Could not delete this folder.',
		);
		expect(screen.getByRole('link', { name: 'Newsletter' })).toBeInTheDocument();
	});

	it('words the page in the tags copy for the tags namespace', async () => {
		renderBody({ items: [{ id: 't1', name: 'Presse' }], namespace: 'tags' });
		await expect(screen.findByRole('heading', { name: 'Tags' })).resolves.toBeInTheDocument();
		expect(screen.getByRole('textbox', { name: 'Tag name' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Create tag' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Presse' })).toBeInTheDocument();
	});
});
