import { Combobox as BaseCombobox } from '@base-ui/react';
import { XIcon } from 'lucide-react';
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { normalizeName } from '../lib/names';
import { TAGS_PER_LINK } from '../lib/tags';
import { Button } from './ui/button';
import {
	Combobox,
	ComboboxChip,
	ComboboxChips,
	ComboboxChipsInput,
	ComboboxContent,
	ComboboxEmpty,
	ComboboxItem,
	ComboboxList,
	useComboboxAnchor,
} from './ui/combobox';
import { Field, FieldDescription, FieldError, FieldLabel } from './ui/field';

/** A list entry: a team tag, or the one entry that creates the typed name. */
interface PickerItem extends TagOption {
	readonly creatable?: boolean;
}

export interface TagOption {
	readonly id: string;
	readonly name: string;
}

/** What creating a tag from the picker resolved to. */
export type TagCreateResult = { readonly tag: TagOption } | { readonly error: string };

export interface TagPickerProps {
	/** Whether the caller may create tags (editor and up). */
	readonly canCreate: boolean;
	/** Ids whose tag is known to be gone (only computed once the tags have loaded). */
	readonly deletedIds: ReadonlySet<string>;
	/** A field error from the server, e.g. the tag-gone 422. */
	readonly error?: string;
	/** The input's id, for the visible <label>. */
	readonly inputId: string;
	readonly label: string;
	/** Names for chips whose tag is not among `options` (e.g. from link.tags). */
	readonly knownNames: ReadonlyMap<string, string>;
	readonly onChange: (ids: readonly string[]) => void;
	readonly onCreate: (name: string) => Promise<TagCreateResult>;
	/** The team's tags, as loaded (may be empty while unavailable). */
	readonly options: readonly TagOption[];
	/** The chosen tag ids, in order. */
	readonly value: readonly string[];
}

/**
 * The link form's tag field: chosen tags as chips, a filter input that offers
 * the team's other tags, and, for editors, an entry that creates the typed
 * name as a new tag. The value is a list of ids; names come from `options`,
 * then `knownNames`, so a chip still reads right while the tags are
 * unavailable.
 *
 * Creating is offered only when no tag already has the typed name in any
 * case, so typing "presse" next to "Presse" picks the existing tag instead of
 * asking the API for a duplicate. At `TAGS_PER_LINK` chips the list stays
 * closed and a hint tied to the input says why.
 *
 * `ComboboxChip` from `ui/combobox.tsx` renders its remove button without an
 * accessible name, and that file is generator output, so each chip renders
 * Base UI's own `ChipRemove` instead, labelled from `links.tagRemove`.
 *
 * @param props - The picker's props.
 * @param props.canCreate - Whether the caller may create tags (editor and up).
 * @param props.deletedIds - Ids whose tag is known to be gone; their chips say so.
 * @param props.error - A field error from the server, e.g. the tag-gone 422.
 * @param props.inputId - The input's id, which the visible label points at.
 * @param props.label - The field's visible label.
 * @param props.knownNames - Names for chips whose tag is not among `options`.
 * @param props.onChange - Called with the new list of chosen ids.
 * @param props.onCreate - Creates a tag by name; resolves to the tag or a message to show.
 * @param props.options - The team's tags, as loaded.
 * @param props.value - The chosen tag ids, in order.
 * @returns The labelled tag field.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlySet` (`deletedIds`) and `ReadonlyMap` (`knownNames`) are TypeScript's immutable collection types; the rule does not recognise them as readonly, the same limitation `audit-actor.ts` documents.
export function TagPicker({
	canCreate,
	deletedIds,
	error,
	inputId,
	knownNames,
	label,
	onChange,
	onCreate,
	options,
	value,
}: TagPickerProps): React.JSX.Element {
	const { t } = useTranslation();
	const anchor = useComboboxAnchor();
	const capId = useId();
	const errorId = useId();
	const input = useRef<HTMLInputElement>(null);
	const [query, setQuery] = useState('');
	const [open, setOpen] = useState(false);
	const [createError, setCreateError] = useState<string | undefined>(undefined);

	// A create resolves after the user may have changed the chips, so it adds
	// to the latest value rather than the one it started from.
	const latestValue = useRef(value);
	useEffect(() => {
		latestValue.current = value;
	}, [value]);

	const atCap = value.length >= TAGS_PER_LINK;
	// Reaching the cap while the list is open closes it for good; otherwise
	// removing a chip later would reopen a list nobody asked for.
	if (atCap && open) setOpen(false);

	const selected = useMemo(() => {
		const names = new Map(options.map((option) => [option.id, option.name]));
		return value.map((id): PickerItem => ({ id, name: names.get(id) ?? knownNames.get(id) ?? id }));
	}, [knownNames, options, value]);

	const items = useMemo((): PickerItem[] => {
		if (atCap) return [];
		const chosen = new Set(value);
		const available: PickerItem[] = options.filter((option) => !chosen.has(option.id));
		const name = canCreate ? normalizeName(query) : undefined;
		if (name === undefined) return available;
		const lowered = name.toLowerCase();
		const exists = options.some((option) => option.name.toLowerCase() === lowered);
		return exists ? available : [...available, { creatable: true, id: `create:${lowered}`, name }];
	}, [atCap, canCreate, options, query, value]);

	/**
	 * Creates the typed name as a tag and chooses it, or keeps the failure to
	 * show on the field. Focus goes back to the input either way.
	 *
	 * @param name - The normalized name to create.
	 */
	async function create(name: string): Promise<void> {
		setCreateError(undefined);
		const result = await onCreate(name);
		if ('tag' in result) {
			const current = latestValue.current;
			if (!current.includes(result.tag.id)) onChange([...current, result.tag.id]);
			setQuery('');
		} else {
			setCreateError(result.error);
		}
		input.current?.focus();
	}

	const messages = [error, createError].filter(
		(message): message is string => message !== undefined,
	);
	const invalid = messages.length > 0;
	const describedBy = [atCap ? capId : undefined, invalid ? errorId : undefined]
		.filter((id): id is string => id !== undefined)
		.join(' ');

	return (
		<Field data-invalid={invalid}>
			<FieldLabel htmlFor={inputId}>{label}</FieldLabel>
			<Combobox
				autoHighlight
				inputValue={query}
				isItemEqualToValue={(item: PickerItem, chosen: PickerItem) => item.id === chosen.id}
				itemToStringLabel={(item: PickerItem) => item.name}
				items={items}
				multiple
				onInputValueChange={setQuery}
				onOpenChange={(next: boolean) => {
					setOpen(next && !atCap);
				}}
				onValueChange={(
					next: readonly PickerItem[],
					details: Readonly<{ cancel: () => void; reason: BaseCombobox.Root.ChangeEventReason }>,
				) => {
					// With the list closed, Escape in Base UI's input clears every
					// chip at once. In a form, that is a keystroke people press to
					// dismiss things, so it removes nothing here; chips go one at a
					// time, by their remove button or Backspace.
					if (details.reason === 'escape-key') {
						details.cancel();
						return;
					}
					const creatable = next.find((item) => item.creatable === true);
					if (creatable) {
						void create(creatable.name);
						return;
					}
					setCreateError(undefined);
					onChange(next.map((item) => item.id));
				}}
				open={open && !atCap}
				value={selected}
			>
				<ComboboxChips ref={anchor}>
					{selected.map((tag) => {
						const removeLabel = t('links.tagRemove', { name: tag.name });
						return (
							<ComboboxChip key={tag.id} showRemove={false}>
								<span>{tag.name}</span>
								{deletedIds.has(tag.id) ? <span>{t('links.tagDeleted')}</span> : null}
								<BaseCombobox.ChipRemove
									aria-label={removeLabel}
									data-slot="combobox-chip-remove"
									// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Base UI's `render`-prop composition idiom (`useRender`'s "Migrating from Radix UI" guide): this is the element `ChipRemove` clones and merges its own props onto, exactly as the generated `ComboboxChip` renders its own. A stable reference would need a `useMemo` around a one-line static element per chip, ten at most.
									render={<Button size="icon-xs" variant="ghost" />}
								>
									<XIcon aria-hidden />
								</BaseCombobox.ChipRemove>
							</ComboboxChip>
						);
					})}
					<ComboboxChipsInput
						aria-describedby={describedBy === '' ? undefined : describedBy}
						aria-invalid={invalid ? true : undefined}
						id={inputId}
						placeholder={t('links.tagsPlaceholder')}
						ref={input}
					/>
				</ComboboxChips>
				<ComboboxContent anchor={anchor}>
					<ComboboxEmpty>{t('links.tagsNoMatch')}</ComboboxEmpty>
					<ComboboxList aria-label={label}>
						{(item: PickerItem) => (
							<ComboboxItem key={item.id} value={item}>
								{item.creatable === true ? t('links.tagCreate', { name: item.name }) : item.name}
							</ComboboxItem>
						)}
					</ComboboxList>
				</ComboboxContent>
			</Combobox>
			{atCap ? <FieldDescription id={capId}>{t('links.tagsAtCap')}</FieldDescription> : null}
			{invalid ? (
				<FieldError errors={messages.map((message) => ({ message }))} id={errorId} />
			) : null}
		</Field>
	);
}
