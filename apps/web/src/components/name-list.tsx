import { Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ConfirmDelete } from './confirm-delete';
import { NameForm } from './name-form';
import { Button } from './ui/button';

interface NameListProps {
	readonly canEdit: boolean;
	readonly items: readonly NamedItem[];
	/** Picks the copy (`folders.*` or `tags.*`) and the search parameter the name links to. */
	readonly namespace: NameNamespace;
	readonly onDelete: (itemId: string) => void;
	/** Clears this item's row error — called when its rename form is cancelled and when it is opened again, so a stale error from a previous attempt never lingers or reappears under the wrong action. */
	readonly onDismissError: (itemId: string) => void;
	readonly onRename: (itemId: string, name: string) => Promise<boolean>;
	readonly rowError: NameRowError | null;
	readonly teamSlug: string;
}

interface NameRowProps extends Omit<NameListProps, 'items'> {
	readonly item: NamedItem;
}

function NameRow({
	canEdit,
	item,
	namespace,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: NameRowProps): React.JSX.Element {
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
	// (further down) — see `NameRowError`'s own docstring for why matching by
	// `itemId` alone used to let one bleed into the other's slot.
	const renameError =
		rowError?.itemId === item.id && rowError.action === 'rename' ? rowError.message : undefined;
	const deleteError =
		rowError?.itemId === item.id && rowError.action === 'delete' ? rowError.message : undefined;

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
		onDismissError(item.id);
	};

	return (
		<li>
			{editing ? (
				<NameForm
					// This moves focus straight into the field the moment the row's own
					// "Rename" button is clicked — a user-triggered focus change, not the
					// page-load `autofocus` antipattern `jsx-a11y/no-autofocus` exists to
					// catch. The rule can't tell a custom prop of the same name apart
					// from the native DOM attribute it actually guards against.
					// oxlint-disable-next-line jsx-a11y/no-autofocus
					autoFocus
					error={renameError}
					initialName={item.name}
					label={t(`${namespace}.name`)}
					onCancel={close}
					onSubmit={(name) => {
						void (async () => {
							const saved = await onRename(item.id, name);
							if (saved) close();
						})();
					}}
					submitLabel={t(`${namespace}.save`)}
				/>
			) : (
				<>
					<Link
						params={{ teamSlug }}
						search={namespace === 'folders' ? { folder: item.id } : { tag: item.id }}
						to="/teams/$teamSlug/links"
					>
						{item.name}
					</Link>
					{canEdit ? (
						<>
							<Button
								aria-label={t(`${namespace}.renameLabel`, { name: item.name })}
								onClick={() => {
									// Clears a leftover error before the form opens — otherwise
									// a delete failure from an earlier action on this same row
									// would still match `item.id` and render inside the
									// rename field the instant it opens, and a previous rename
									// failure would reappear on a fresh attempt that hasn't
									// failed yet.
									onDismissError(item.id);
									setEditing(true);
								}}
								ref={renameButton}
								variant="ghost"
							>
								{t(`${namespace}.rename`)}
							</Button>
							<ConfirmDelete
								label={t(`${namespace}.deleteLabel`, { name: item.name })}
								onConfirm={() => {
									onDelete(item.id);
								}}
								question={t(`${namespace}.deleteQuestion`, { name: item.name })}
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
 * The team's folders or tags, alphabetically as the API returns them, each
 * linking to its filtered link list. Editors and up also rename and delete; a
 * viewer gets no control the API would refuse.
 *
 * @param props - The list's props.
 * @param props.canEdit - Whether the caller may rename and delete.
 * @param props.items - The team's folders or tags.
 * @param props.namespace - Which of the two the list shows; picks the copy and the filter each name links to.
 * @param props.onDelete - Deletes the item with the given id.
 * @param props.onDismissError - Clears a row's error; called when its rename form is cancelled and when it is opened again.
 * @param props.onRename - Renames an item; resolves true on success, which closes the inline form.
 * @param props.rowError - The last failure, the row it happened on, and which action produced it.
 * @param props.teamSlug - The team's slug, for the links into the filtered list.
 * @returns The list.
 */
export function NameList({
	canEdit,
	items,
	namespace,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: NameListProps): React.JSX.Element {
	const { t } = useTranslation();

	if (items.length === 0) {
		return (
			<p>
				{t(`${namespace}.empty`)} {canEdit ? t(`${namespace}.emptyEditorHint`) : null}
			</p>
		);
	}

	return (
		<ul>
			{items.map((item) => (
				<NameRow
					canEdit={canEdit}
					item={item}
					key={item.id}
					namespace={namespace}
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
 * What a folder and a tag have in common for this list: an id to act on and a
 * name to show. The generated `Folder` and `Tag` types both satisfy it, and
 * unlike them it is readonly, so no component here needs a
 * `prefer-readonly-parameter-types` disable for it.
 *
 * Declared at the bottom, not beside `NameListProps` above which uses it:
 * `import/exports-last` requires every export contiguous at the end of the
 * file, and a type-only interface has no runtime evaluation order to
 * respect — TypeScript hoists it — so there is no cost to moving it here,
 * the same tradeoff `lib/folders.ts` documents for its own top-of-file
 * exports.
 */
export interface NamedItem {
	readonly id: string;
	readonly name: string;
}

/** Which kind of item a management component works on; it doubles as the i18n key head and the query-key head. */
export type NameNamespace = 'folders' | 'tags';

/**
 * A row-scoped failure, tagged with which action produced it: a rename
 * failure belongs on the open rename form, and a delete failure belongs on
 * the closed row's own alert — the two must never show through the other's
 * slot. Before this tag existed, `rowError` matched by `itemId` alone, so a
 * delete failure rendered inside the rename field the moment that row's
 * rename form was opened afterwards, and a rename failure survived Escape as
 * a stale `role="alert"` next to the closed row.
 *
 * Declared at the bottom, for the same `import/exports-last` reason as
 * `NamedItem` above.
 */
export interface NameRowError {
	readonly action: 'delete' | 'rename';
	readonly itemId: string;
	readonly message: string;
}
