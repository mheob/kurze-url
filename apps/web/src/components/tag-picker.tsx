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

/**
 * Adds tags' names to a map of names by id, leaving the given map unchanged.
 *
 * @param names - The names known so far.
 * @param tags - The tags whose names to add.
 * @returns A new map holding both.
 */
function withNames(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly, the same limitation `audit-actor.ts` documents.
	names: ReadonlyMap<string, string>,
	tags: readonly TagOption[],
): ReadonlyMap<string, string> {
	return new Map([...names, ...tags.map((tag): [string, string] => [tag.id, tag.name])]);
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
	/**
	 * Shows the chips and nothing to operate: the input, every chip remove
	 * button and the keyboard removal of a chip are all off. A disabled
	 * `fieldset` around the picker reaches the input and the buttons but not
	 * the chips themselves, which are focusable `div`s that remove themselves
	 * on Backspace, so the picker is told directly.
	 */
	readonly disabled?: boolean;
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
 * then `knownNames`, then any name the picker has seen before, so a chip
 * still reads right while the tags are unavailable or after its tag is gone.
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
 * @param props.disabled - Turns the whole picker off, chips included; false when absent.
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
	disabled = false,
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
	const root = useRef<HTMLDivElement>(null);
	const input = useRef<HTMLInputElement>(null);
	const [query, setQuery] = useState('');
	const [open, setOpen] = useState(false);
	const [createError, setCreateError] = useState<string | undefined>(undefined);
	const [pending, setPending] = useState(false);
	// Every tag name this picker has seen, in `options` or from a create it
	// made. Nothing is ever dropped, so a chip keeps its name after its tag
	// leaves `options`: deleted meanwhile and refetched, or not refetched yet.
	const [seenNames, setSeenNames] = useState<ReadonlyMap<string, string>>(() =>
		withNames(new Map(), options),
	);
	const [seenOptions, setSeenOptions] = useState(options);
	if (seenOptions !== options) {
		setSeenOptions(options);
		// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly, the same limitation `audit-actor.ts` documents.
		setSeenNames((names) => withNames(names, options));
	}

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
		return value.map((id): PickerItem => ({
			id,
			name: names.get(id) ?? knownNames.get(id) ?? seenNames.get(id) ?? id,
		}));
	}, [knownNames, options, seenNames, value]);

	const items = useMemo((): PickerItem[] => {
		if (atCap) return [];
		const chosen = new Set(value);
		const available: PickerItem[] = options.filter((option) => !chosen.has(option.id));
		const name = canCreate && !pending ? normalizeName(query) : undefined;
		if (name === undefined) return available;
		const lowered = name.toLowerCase();
		const exists = options.some((option) => option.name.toLowerCase() === lowered);
		return exists ? available : [...available, { creatable: true, id: `create:${lowered}`, name }];
	}, [atCap, canCreate, options, pending, query, value]);

	/**
	 * Calls `onCreate`, turning a rejection into the generic message so the
	 * field always has something to say.
	 *
	 * @param name - The normalized name to create.
	 * @returns What the create resolved to, or the generic failure.
	 */
	async function settle(name: string): Promise<TagCreateResult> {
		try {
			return await onCreate(name);
		} catch {
			return { error: t('errors.unknown') };
		}
	}

	/**
	 * Creates the typed name as a tag and chooses it, or keeps the failure to
	 * show on the field with the name back in the input, since Base UI
	 * cleared it when the list closed. Focus goes back to the input either
	 * way, unless the user has moved on to another field meanwhile.
	 *
	 * The chips may have changed while the call was in flight, so the new tag
	 * joins the latest value, and only while that is still below the cap.
	 *
	 * @param name - The normalized name to create.
	 */
	async function create(name: string): Promise<void> {
		setCreateError(undefined);
		setPending(true);
		const result = await settle(name);
		setPending(false);
		if ('tag' in result) {
			const { tag } = result;
			// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly, the same limitation `audit-actor.ts` documents.
			setSeenNames((names) => withNames(names, [tag]));
			const current = latestValue.current;
			if (current.length < TAGS_PER_LINK && !current.includes(tag.id)) {
				onChange([...current, tag.id]);
			}
			setQuery('');
		} else {
			setCreateError(result.error);
			setQuery(name);
		}
		const active = document.activeElement;
		if (active === null || active === document.body || root.current?.contains(active) === true) {
			input.current?.focus();
		}
	}

	const messages = [error, createError].filter(
		(message): message is string => message !== undefined,
	);
	const invalid = messages.length > 0;
	const describedBy = [atCap ? capId : undefined, invalid ? errorId : undefined]
		.filter((id): id is string => id !== undefined)
		.join(' ');

	return (
		<Field data-invalid={invalid} ref={root}>
			<FieldLabel htmlFor={inputId}>{label}</FieldLabel>
			<Combobox
				autoHighlight
				disabled={disabled}
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
						aria-busy={pending ? true : undefined}
						aria-describedby={describedBy === '' ? undefined : describedBy}
						aria-invalid={invalid ? true : undefined}
						id={inputId}
						// While a create is in flight the list is closed, so Enter
						// would submit the surrounding form without the new tag.
						onKeyDown={(event: Readonly<{ key: string; preventDefault: () => void }>) => {
							if (pending && event.key === 'Enter') event.preventDefault();
						}}
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
