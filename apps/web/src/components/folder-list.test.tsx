import type { Folder } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { FolderForm } from './folder-form';
import { FolderList } from './folder-list';

const folders: Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Newsletter', team_id: 'team-a' },
	{ created_at: '2026-09-26T00:00:00Z', id: 'f2', name: 'Sommerfest', team_id: 'team-a' },
];

interface RenderListOptions {
	readonly canEdit?: boolean;
	readonly onDelete?: (folderId: string) => void;
	readonly onRename?: (folderId: string, name: string) => Promise<boolean>;
	readonly rowError?: Readonly<{ folderId: string; message: string }> | null;
}

/**
 * `FolderList` renders TanStack Router `<Link>` elements for each folder, the
 * same reason `link-list.test.tsx`'s own `renderWith` needs a router in
 * context — see that file's docstring.
 *
 * @param overrides - Partial props to override on the default fixture.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderList(overrides: RenderListOptions = {}): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<FolderList
				canEdit={overrides.canEdit ?? false}
				folders={folders}
				onDelete={overrides.onDelete ?? vi.fn<(folderId: string) => void>()}
				onRename={
					overrides.onRename ??
					// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `FolderList` awaits; the fake has nothing to await itself.
					vi.fn<(folderId: string, name: string) => Promise<boolean>>(async () => true)
				}
				rowError={overrides.rowError ?? null}
				teamSlug="verein"
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

describe(FolderList, () => {
	it('shows a viewer the folders and no controls', async () => {
		renderList({ canEdit: false });
		await expect(screen.findByRole('link', { name: 'Sommerfest' })).resolves.toHaveAttribute(
			'href',
			expect.stringContaining('folder=f2'),
		);
		expect(screen.queryByRole('button', { name: /rename/iu })).toBeNull();
		expect(screen.queryByRole('button', { name: /delete/iu })).toBeNull();
	});

	it('renames inline, moving focus into the field and back to the button', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `FolderList` awaits; the fake has nothing to await itself.
		const onRename = vi.fn<(folderId: string, name: string) => Promise<boolean>>(async () => true);
		renderList({ canEdit: true, onRename });
		await screen.findByRole('link', { name: 'Sommerfest' });
		await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
		const field = screen.getByRole('textbox', { name: 'Folder name' });
		expect(field).toHaveFocus();
		await userEvent.clear(field);
		await userEvent.type(field, 'Sommerfest 2027{Enter}');
		expect(onRename).toHaveBeenCalledWith('f2', 'Sommerfest 2027');
		await expect(
			screen.findByRole('button', { name: 'Rename folder Sommerfest' }),
		).resolves.toHaveFocus();
	});

	it('cancels a rename on Escape without calling onRename', async () => {
		// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `FolderList` awaits; the fake has nothing to await itself.
		const onRename = vi.fn<(folderId: string, name: string) => Promise<boolean>>(async () => true);
		renderList({ canEdit: true, onRename });
		await screen.findByRole('link', { name: 'Sommerfest' });
		await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
		await userEvent.keyboard('{Escape}');
		expect(onRename).not.toHaveBeenCalled();
		expect(screen.getByRole('button', { name: 'Rename folder Sommerfest' })).toHaveFocus();
	});

	it('keeps the rename open when it fails, showing the row error', async () => {
		renderList({
			canEdit: true,
			// oxlint-disable-next-line typescript/require-await -- stands in for a rename call `FolderList` awaits; the fake has nothing to await itself.
			onRename: vi.fn<(folderId: string, name: string) => Promise<boolean>>(async () => false),
			rowError: { folderId: 'f2', message: 'A folder with this name already exists.' },
		});
		await screen.findByRole('link', { name: 'Sommerfest' });
		await userEvent.click(screen.getByRole('button', { name: 'Rename folder Sommerfest' }));
		await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), 'x{Enter}');
		expect(screen.getByText('A folder with this name already exists.')).toBeVisible();
		expect(screen.getByRole('textbox', { name: 'Folder name' })).toBeVisible();
	});

	it('deletes only after confirming', async () => {
		const onDelete = vi.fn<(folderId: string) => void>();
		renderList({ canEdit: true, onDelete });
		await screen.findByRole('link', { name: 'Sommerfest' });
		await userEvent.click(screen.getByRole('button', { name: 'Delete folder Sommerfest' }));
		expect(onDelete).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole('button', { name: /yes, delete it/iu }));
		expect(onDelete).toHaveBeenCalledWith('f2');
	});
});

describe(FolderForm, () => {
	it('refuses a blank name on the client', async () => {
		const onSubmit = vi.fn<(name: string) => void>();
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<FolderForm label="Folder name" onSubmit={onSubmit} submitLabel="Create folder" />
			</I18nextProvider>,
		);
		await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), '   {Enter}');
		expect(onSubmit).not.toHaveBeenCalled();
		expect(screen.getByText('Enter a name of 1 to 60 characters.')).toBeVisible();
	});
});
