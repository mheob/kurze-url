import type { Folder } from '@kurze-url/api-client';
import { Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ConfirmDelete } from './confirm-delete';
import { FolderForm } from './folder-form';
import { Button } from './ui/button';

interface FolderListProps {
	readonly canEdit: boolean;
	readonly folders: readonly Folder[];
	readonly onDelete: (folderId: string) => void;
	/** Clears this folder's row error — called when its rename form is cancelled and when it is opened again, so a stale error from a previous attempt never lingers or reappears under the wrong action. */
	readonly onDismissError: (folderId: string) => void;
	readonly onRename: (folderId: string, name: string) => Promise<boolean>;
	readonly rowError: FolderRowError | null;
	readonly teamSlug: string;
}

interface FolderRowProps extends Omit<FolderListProps, 'folders'> {
	readonly folder: Folder;
}

// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `folder` carries `@kurze-url/api-client`'s generated `Folder` type, whose properties are not marked readonly; that is generated codegen output, never edited by hand.
function FolderRow({
	canEdit,
	folder,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: FolderRowProps): React.JSX.Element {
	const { t } = useTranslation();
	const [editing, setEditing] = useState(false);
	const renameButton = useRef<HTMLButtonElement>(null);
	// A plain ref, not a second piece of state: the effect below only ever
	// reads and clears it, never triggers a render from it — oxlint's
	// `react/set-state-in-effect` flags a `setState` call inside an effect,
	// and mutating a ref instead is what this row needs anyway, since nothing
	// here should re-render off this flag alone.
	const restoreFocus = useRef(false);
	// Tagged by action, not shown interchangeably: a rename failure belongs on
	// the open form (below), a delete failure on the closed row's own alert
	// (further down) — see `FolderRowError`'s own docstring for why matching by
	// `folderId` alone used to let one bleed into the other's slot.
	const renameError =
		rowError?.folderId === folder.id && rowError.action === 'rename' ? rowError.message : undefined;
	const deleteError =
		rowError?.folderId === folder.id && rowError.action === 'delete' ? rowError.message : undefined;

	// Focus returns to the row's own Rename button once the inline form closes,
	// so a keyboard user is not dropped at the top of the page.
	useEffect(() => {
		if (!editing && restoreFocus.current) {
			renameButton.current?.focus();
			restoreFocus.current = false;
		}
	}, [editing]);

	const close = (): void => {
		restoreFocus.current = true;
		setEditing(false);
		// Otherwise a failed rename's own error outlives the form it belongs
		// to: Escape (or the Cancel button) used to leave that message
		// rendered as a stale `role="alert"` next to the now-closed row, since
		// nothing before this call ever cleared `rowError` itself.
		onDismissError(folder.id);
	};

	return (
		<li>
			{editing ? (
				<FolderForm
					// This moves focus straight into the field the moment the row's own
					// "Rename" button is clicked — a user-triggered focus change, not the
					// page-load `autofocus` antipattern `jsx-a11y/no-autofocus` exists to
					// catch. The rule can't tell a custom prop of the same name apart
					// from the native DOM attribute it actually guards against.
					// oxlint-disable-next-line jsx-a11y/no-autofocus
					autoFocus
					error={renameError}
					initialName={folder.name}
					label={t('folders.name')}
					onCancel={close}
					onSubmit={(name) => {
						void (async () => {
							const saved = await onRename(folder.id, name);
							if (saved) close();
						})();
					}}
					submitLabel={t('folders.save')}
				/>
			) : (
				<>
					<Link params={{ teamSlug }} search={{ folder: folder.id }} to="/teams/$teamSlug/links">
						{folder.name}
					</Link>
					{canEdit ? (
						<>
							<Button
								aria-label={t('folders.renameLabel', { name: folder.name })}
								onClick={() => {
									// Clears a leftover error before the form opens — otherwise
									// a delete failure from an earlier action on this same row
									// would still match `folder.id` and render inside the
									// rename field the instant it opens, and a previous rename
									// failure would reappear on a fresh attempt that hasn't
									// failed yet.
									onDismissError(folder.id);
									setEditing(true);
								}}
								ref={renameButton}
								variant="ghost"
							>
								{t('folders.rename')}
							</Button>
							<ConfirmDelete
								label={t('folders.deleteLabel', { name: folder.name })}
								onConfirm={() => {
									onDelete(folder.id);
								}}
								question={t('folders.deleteQuestion', { name: folder.name })}
							/>
						</>
					) : null}
					{deleteError === undefined ? null : <p role="alert">{deleteError}</p>}
				</>
			)}
		</li>
	);
}

/**
 * The team's folders, alphabetically as the API returns them, each linking to
 * its filtered link list. Editors and up also rename and delete; a viewer gets
 * no control the API would refuse.
 *
 * @param props - The list's props.
 * @param props.canEdit - Whether the caller may rename and delete.
 * @param props.folders - The team's folders.
 * @param props.onDelete - Deletes the folder with the given id.
 * @param props.onDismissError - Clears a row's error; called when its rename form is cancelled and when it is opened again.
 * @param props.onRename - Renames a folder; resolves true on success, which closes the inline form.
 * @param props.rowError - The last failure, the row it happened on, and which action produced it.
 * @param props.teamSlug - The team's slug, for the links into the filtered list.
 * @returns The list.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `folders` carries `@kurze-url/api-client`'s generated `Folder` type, whose properties are not marked readonly; that is generated codegen output, never edited by hand.
export function FolderList({
	canEdit,
	folders,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: FolderListProps): React.JSX.Element {
	const { t } = useTranslation();

	if (folders.length === 0) {
		return (
			<p>
				{t('folders.empty')} {canEdit ? t('folders.emptyEditorHint') : null}
			</p>
		);
	}

	return (
		<ul>
			{/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `folder` is the generated `Folder` type; see the disable above on this component's own `folders` prop. */}
			{folders.map((folder) => (
				<FolderRow
					canEdit={canEdit}
					folder={folder}
					key={folder.id}
					onDelete={onDelete}
					onDismissError={onDismissError}
					onRename={onRename}
					rowError={rowError}
					teamSlug={teamSlug}
				/>
			))}
		</ul>
	);
}

/**
 * A row-scoped failure, tagged with which action produced it: a rename
 * failure belongs on the open rename form, and a delete failure belongs on
 * the closed row's own alert — the two must never show through the other's
 * slot. Before this tag existed, `rowError` matched by `folderId` alone, so a
 * delete failure rendered inside the rename field the moment that row's
 * rename form was opened afterwards, and a rename failure survived Escape as
 * a stale `role="alert"` next to the closed row.
 *
 * Declared at the bottom, not beside `FolderListProps` above which uses it:
 * `import/exports-last` requires every export contiguous at the end of the
 * file, and a type-only interface has no runtime evaluation order to
 * respect — TypeScript hoists it — so there is no cost to moving it here,
 * the same tradeoff `lib/folders.ts` documents for its own top-of-file
 * exports.
 */
export interface FolderRowError {
	readonly action: 'delete' | 'rename';
	readonly folderId: string;
	readonly message: string;
}
