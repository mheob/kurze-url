import { useTranslation } from 'react-i18next';

import { NameForm } from './name-form';
import { NameList, type NamedItem, type NameNamespace, type NameRowError } from './name-list';

export interface NameManagementBodyProps {
	readonly canEdit: boolean;
	/** Shown on the create form's name field; `undefined` once creation succeeds or nothing has failed yet. */
	readonly createError: string | undefined;
	/** Remounts the create form on every successful create, clearing its field — see `useNameMutations`' own comment on `setCreateKey`. */
	readonly createKey: number;
	/** Focused once the page heading regains focus after a delete, so a keyboard user lands somewhere meaningful rather than at `<body>`. */
	readonly headingRef: React.RefObject<HTMLHeadingElement | null>;
	readonly items: readonly NamedItem[];
	/** Picks the page's copy: `folders.*` or `tags.*`. */
	readonly namespace: NameNamespace;
	readonly onCreate: (name: string) => void;
	readonly onDelete: (itemId: string) => void;
	readonly onDismissError: (itemId: string) => void;
	readonly onRename: (itemId: string, name: string) => Promise<boolean>;
	readonly rowError: NameRowError | null;
	readonly teamSlug: string;
}

/**
 * The presentational body of the folders and tags pages — pure and
 * prop-driven, the same idiom `MembersPageBody`/`AuditLogPageBody` already use
 * so a route's mutation wiring (`useNameMutations`) can be tested separately
 * from what it renders (folders-frontend final review, Minor 3).
 *
 * @param props - The component's props.
 * @param props.canEdit - Whether the caller may create, rename and delete.
 * @param props.createError - Shown on the create form's name field.
 * @param props.createKey - Remounts the create form on every successful create.
 * @param props.headingRef - Focused after a successful delete.
 * @param props.items - The team's folders or tags, already fetched by the caller.
 * @param props.namespace - Which of the two the page manages; picks the copy.
 * @param props.onCreate - Creates an item with the given name.
 * @param props.onDelete - Deletes the item with the given id.
 * @param props.onDismissError - Clears a row's error; called when its rename form is cancelled and when it is opened again.
 * @param props.onRename - Renames an item; resolves true on success, which closes the inline form.
 * @param props.rowError - The last failure, the row it happened on, and which action produced it.
 * @param props.teamSlug - The team's slug, for the links into the filtered list.
 * @returns The rendered page body.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `headingRef` is a `React.RefObject`, whose `.current` is deliberately mutable by React's own design; `Readonly<>` is shallow and cannot reach it.
export function NameManagementBody({
	canEdit: editor,
	createError,
	createKey,
	headingRef,
	items,
	namespace,
	onCreate,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: NameManagementBodyProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<>
			{/* tabIndex so a successful delete can move focus here — see
			    `useNameMutations`' `remove` mutation — even though a plain
			    heading is not normally in the tab order. */}
			<h1 ref={headingRef} tabIndex={-1}>
				{t(`${namespace}.heading`)}
			</h1>
			<p>{t(`${namespace}.intro`)}</p>
			{editor ? (
				<NameForm
					// Only after the first create, never on the form's own initial
					// mount (a page load) — the same distinction `NameRow`'s
					// `autoFocus` on its rename form draws, and why that one earns
					// the identical disable below: the rule can't tell a remount
					// triggered by the reader's own submit apart from the page-load
					// antipattern it actually guards against.
					// oxlint-disable-next-line jsx-a11y/no-autofocus
					autoFocus={createKey > 0}
					error={createError}
					key={createKey}
					label={t(`${namespace}.name`)}
					onSubmit={onCreate}
					submitLabel={t(`${namespace}.create`)}
				/>
			) : null}
			<NameList
				canEdit={editor}
				items={items}
				namespace={namespace}
				onDelete={onDelete}
				onDismissError={onDismissError}
				onRename={onRename}
				rowError={rowError}
				teamSlug={teamSlug}
			/>
		</>
	);
}
