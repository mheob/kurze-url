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

import { createI18n } from '../../i18n';
import { FoldersPageBody, type FoldersPageBodyProps } from './teams.$teamSlug.folders';

/**
 * Named `....folders.page.test.tsx`, not `....folders.test.tsx` (which would
 * match `teams.$teamSlug.folders.test.ts`'s own basename save for the
 * extension) for the identical reason `teams.$teamSlug.links.index.error.test.tsx`
 * documents on itself: co-locating a `.test.ts` and a `.test.tsx` under the
 * exact same basename confuses oxlint's type-aware checker's own
 * `@testing-library/jest-dom` resolution for the `.tsx` file.
 */

const folders: Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Newsletter', team_id: 'team-a' },
];

/**
 * Renders the real `FoldersPageBody` — exported from
 * `teams.$teamSlug.folders.tsx` for exactly this reason, the same
 * `MembersPageBody`/`AuditLogRouteView` idiom the route's own file cites —
 * inside a minimal memory router: `FolderList` renders TanStack `<Link>`s,
 * the same reason `folder-list.test.tsx`'s own `renderList` needs one.
 *
 * @param overrides - Partial props to override on the default fixture.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `FoldersPageBodyProps` nests `@kurze-url/api-client`'s generated, mutable `Folder` type (`folders`) and a `React.RefObject` (`headingRef`), whose `.current` is deliberately mutable by React's own design; `Readonly<>`/`Partial<>` reach neither.
function renderBody(overrides: Partial<FoldersPageBodyProps> = {}): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<FoldersPageBody
				canEdit={overrides.canEdit ?? true}
				createError={overrides.createError}
				createKey={overrides.createKey ?? 0}
				folders={overrides.folders ?? folders}
				headingRef={overrides.headingRef ?? createRef<HTMLHeadingElement>()}
				onCreate={overrides.onCreate ?? vi.fn<(name: string) => void>()}
				onDelete={overrides.onDelete ?? vi.fn<(folderId: string) => void>()}
				onDismissError={overrides.onDismissError ?? vi.fn<(folderId: string) => void>()}
				onRename={
					overrides.onRename ??
					// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `FolderList` awaits; the fake has nothing to await itself.
					vi.fn<(folderId: string, name: string) => Promise<boolean>>(async () => true)
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

describe(FoldersPageBody, () => {
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
			rowError: { action: 'delete', folderId: 'f1', message: 'Could not delete this folder.' },
		});
		await expect(screen.findByRole('alert')).resolves.toHaveTextContent(
			'Could not delete this folder.',
		);
		expect(screen.getByRole('link', { name: 'Newsletter' })).toBeInTheDocument();
	});
});
