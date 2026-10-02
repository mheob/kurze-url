import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import { UNFILED_SEARCH_VALUE } from '../lib/folders';
import { Field, FieldLabel } from './ui/field';
import { NativeSelect, NativeSelectOption } from './ui/native-select';

/**
 * What a filter's options need of a folder or a tag: an id to send back and a
 * name to show. A structural subset of the generated `Folder` and `Tag`
 * types, which both satisfy it, spelled out because the generated types'
 * properties are not `readonly` and `typescript/prefer-readonly-parameter-types`
 * would otherwise want a suppression here — the same shape `link-form.tsx`
 * takes its own `folders` in.
 */
type FilterOption = Readonly<{ id: string; name: string }>;

export interface LinkFilterBarProps {
	/** The active `folder` search value: absent for "all folders", `UNFILED_SEARCH_VALUE` for "no folder", otherwise a folder id. */
	readonly folder: string | undefined;
	/**
	 * The team's folders, or `undefined` while the folders query has not
	 * resolved. Either way "All folders" and "No folder" are offered, since
	 * neither depends on the team's actual folders.
	 */
	readonly folders: readonly FilterOption[] | undefined;
	/**
	 * Reports the whole next filter, never just the field that changed: the
	 * untouched one is passed through as it was, so the caller never has to
	 * remember the other half. A field left `undefined` means "all".
	 */
	readonly onChange: (next: Readonly<{ folder?: string; tag?: string }>) => void;
	/** The active `tag` search value: absent for "all tags", otherwise a tag id. */
	readonly tag: string | undefined;
	/** The team's tags, or `undefined` while the tags query has not resolved. */
	readonly tags: readonly FilterOption[] | undefined;
}

/**
 * The link list's two filters, folder and tag, side by side. They combine, so
 * a change to one reports the other unchanged. Presentational, like
 * `AuditFilterBar`: it neither fetches nor owns the filters, it only reports
 * the reader's choices upward, and what a change does to the page number is
 * the caller's decision (`filterChangeSearch` resets it to 1).
 *
 * Both selects are `NativeSelect` rather than the design system's popup-based
 * `Select`, for the reason `AuditFilterBar` gives for its own: the control
 * stays keyboard- and screen-reader-complete without a second implementation,
 * and `userEvent.selectOptions` keeps working.
 *
 * @param props - The component's props.
 * @param props.folder - The active `folder` search value.
 * @param props.folders - The team's folders, or `undefined` while they have not loaded yet.
 * @param props.onChange - Reports the whole next filter.
 * @param props.tag - The active `tag` search value.
 * @param props.tags - The team's tags, or `undefined` while they have not loaded yet.
 * @returns The rendered filter bar.
 */
export function LinkFilterBar({
	folder,
	folders,
	onChange,
	tag,
	tags,
}: LinkFilterBarProps): React.JSX.Element {
	const { t } = useTranslation();
	const folderId = useId();
	const tagId = useId();

	return (
		<div className="flex flex-col gap-4 sm:flex-row">
			<Field>
				<FieldLabel htmlFor={folderId}>{t('links.folderFilter')}</FieldLabel>
				<NativeSelect
					id={folderId}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						const { value } = event.target;
						onChange({ folder: value === '' ? undefined : value, tag });
					}}
					value={folder ?? ''}
				>
					<NativeSelectOption value="">{t('links.folderAll')}</NativeSelectOption>
					<NativeSelectOption value={UNFILED_SEARCH_VALUE}>
						{t('links.folderNone')}
					</NativeSelectOption>
					{(folders ?? []).map((candidate) => (
						<NativeSelectOption key={candidate.id} value={candidate.id}>
							{candidate.name}
						</NativeSelectOption>
					))}
				</NativeSelect>
			</Field>

			<Field>
				<FieldLabel htmlFor={tagId}>{t('links.tagFilter')}</FieldLabel>
				<NativeSelect
					id={tagId}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						const { value } = event.target;
						onChange({ folder, tag: value === '' ? undefined : value });
					}}
					value={tag ?? ''}
				>
					<NativeSelectOption value="">{t('links.tagAll')}</NativeSelectOption>
					{(tags ?? []).map((candidate) => (
						<NativeSelectOption key={candidate.id} value={candidate.id}>
							{candidate.name}
						</NativeSelectOption>
					))}
				</NativeSelect>
			</Field>
		</div>
	);
}
