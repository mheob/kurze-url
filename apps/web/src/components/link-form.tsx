/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file is either the same `(field) => {...}` render-prop parameter TanStack Form's `form.Field`
   supplies, where reconstructing that type by hand to mark it readonly was tried and reverted after
   a nested field came out subtly wrong, or `LinkForm`'s own props, whose `tagNames` is a
   `ReadonlyMap`: TypeScript's immutable map type, which the rule does not recognise as readonly,
   the same limitation `audit-actor.ts` documents. */

import { useForm } from '@tanstack/react-form';
import { useEffect, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { TagPicker, type TagCreateResult, type TagOption } from './tag-picker';
import { Button } from './ui/button';
import { Checkbox } from './ui/checkbox';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldSet } from './ui/field';
import { Input } from './ui/input';
import { NativeSelect, NativeSelectOption } from './ui/native-select';

const defaultValues: LinkFormValues = {
	analytics_enabled: true,
	destination_url: '',
	domain_id: '',
	expires_at: '',
	folder_id: '',
	redirect_type: 302,
	slug: '',
	tag_ids: [],
};

/**
 * Every field name this form renders an inline error for. A server field
 * error naming anything outside this set (a field this form has no input
 * for at all — e.g. one the API grows later) has nowhere to attach, and
 * without a fallback it would silently vanish instead of surfacing. See the
 * generic alert rendered below for those.
 */
/** The redirect status code CLAUDE.md's "301 vs 302" note warns about: cached by browsers, so clicks go uncounted and destination changes stop taking effect. */
const REDIRECT_PERMANENT = 301;

const KNOWN_FIELD_NAMES: ReadonlySet<string> = new Set([
	'analytics_enabled',
	'destination_url',
	'domain_id',
	'expires_at',
	'folder_id',
	'redirect_type',
	'slug',
	'tag_ids',
]);

/**
 * Whether the slug in the field is a different address from the saved one.
 * The API stores slugs trimmed and lowercase, so a change of case or of
 * surrounding whitespace alone is the same address and not a change.
 *
 * @param saved - The slug the link was saved with; absent or empty on the create form, which has nothing to retire.
 * @param current - The slug now in the field.
 * @returns True when saving would move the link to another address.
 */
function isSlugChange(saved: string | undefined, current: string): boolean {
	const savedSlug = saved?.trim().toLowerCase() ?? '';
	return savedSlug !== '' && current.trim().toLowerCase() !== savedSlug;
}

interface LinkFormProps {
	/** Whether the caller may create tags from the picker (editor and up). */
	readonly canCreateTags?: boolean;
	// A team's *verified* domains, or undefined/empty when there are none to
	// offer — either way the picker below renders nothing at all, per its own
	// docstring: a `<select>` with a single, forced option is furniture, not a
	// choice.
	readonly domains?: readonly Readonly<{ id: string; hostname: string }>[];
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly folderHint?: React.ReactNode;
	// Unlike `domains` above, this renders whenever the prop is passed at all
	// — even an empty list still offers "No folder", the one option that lets
	// an already-filed link be unfiled, so there is no furniture-check here.
	readonly folders?: readonly Readonly<{ id: string; name: string }>[];
	readonly initial?: Partial<LinkFormValues>;
	/** Which version of the record `initial` came from, e.g. `link.updated_at`; a new one re-seeds the form from `initial` in place. */
	readonly initialVersion?: string;
	/** Creates a tag by name from the picker; resolves to the tag or a message to show. */
	readonly onCreateTag?: (name: string) => Promise<TagCreateResult>;
	readonly onSubmit: (values: LinkFormValues) => void;
	/**
	 * Shows the values and nothing to operate: every field is disabled, there
	 * is no submit button and no `folderHint` link. For a member whose role
	 * cannot save them.
	 */
	readonly readOnly?: boolean;
	/** Names for chosen tags the loaded `tags` may lack, e.g. from `link.tags` on the edit route. */
	readonly tagNames?: ReadonlyMap<string, string>;
	// Like `folders`, this renders whenever the prop is passed at all: an
	// empty list (none yet, or the fetch failed) still shows the link's chips
	// and, for an editor, offers to create one.
	readonly tags?: readonly TagOption[];
	/** Whether `tags` is the team's real list rather than a stand-in for one that failed or is pending; only then can a chosen tag be called deleted. */
	readonly tagsLoaded?: boolean;
}

export interface LinkFormValues {
	readonly analytics_enabled: boolean;
	readonly destination_url: string;
	readonly domain_id: string;
	readonly expires_at: string;
	readonly folder_id: string;
	readonly redirect_type: number;
	readonly slug: string;
	readonly tag_ids: readonly string[];
}

/**
 * Shared by the create route (Task 10) and, per the plan's pre-flight scan,
 * the edit route (Task 11) — `initial` is what lets the same component seed
 * itself from an existing `Link` instead of starting blank.
 *
 * Uses `@tanstack/react-form`'s `useForm`/`form.Field`, not the plain
 * `useState` the plan's own sample code showed: CLAUDE.md and this task's own
 * "Validation stays thin" instruction are explicit that TanStack Form covers
 * required-field/shape checks only, and that a zod schema mirroring the API's
 * rules must not be added — `internal/destination`'s SSRF and DNS-rebinding
 * checks cannot be reproduced in a browser at all, so a client schema that
 * looked authoritative would be the more dangerous kind of wrong. The one
 * client-side check here is "destination is required", surfaced through
 * `form.Field`'s own `onChange` validator rather than a parallel schema.
 *
 * @param props - The component's props.
 * @param props.canCreateTags - Whether the caller may create tags from the picker; false when absent.
 * @param props.domains - The team's verified domains; the domain picker renders nothing when this is empty or undefined.
 * @param props.fieldErrors - Server-reported field errors, keyed by field name.
 * @param props.folderHint - Shown under the folder field when the team has no folders yet, e.g. a link to the folders page; never shown when read-only.
 * @param props.folders - The team's folders; the folder field renders whenever this is passed, even empty.
 * @param props.initial - Initial values to seed the form from, for the edit route; a non-empty `slug` here is the saved one the slug field warns about changing.
 * @param props.initialVersion - The version `initial` came from; when it changes, the form re-seeds from `initial`.
 * @param props.onCreateTag - Creates a tag by name from the picker; without it, a create attempt shows the generic failure.
 * @param props.onSubmit - Called with the form's values on submit.
 * @param props.readOnly - Disables every field and drops the submit button; false when absent.
 * @param props.tagNames - Names for chosen tags the loaded `tags` may lack, e.g. from `link.tags`.
 * @param props.tags - The team's tags; the tags field renders whenever this is passed, even empty.
 * @param props.tagsLoaded - Whether `tags` is the team's real list; only then is a chosen tag missing from it marked deleted.
 * @returns The rendered form.
 */
export function LinkForm({
	canCreateTags,
	domains,
	fieldErrors,
	folderHint,
	folders,
	initial,
	initialVersion,
	onCreateTag,
	onSubmit,
	readOnly = false,
	tagNames,
	tags,
	tagsLoaded,
}: LinkFormProps): React.JSX.Element {
	const { t } = useTranslation();
	// One per field with an inline error, not a hardcoded `'<field>-error'`
	// string: two hardcoded ids of the same shape (this file's own `slug` field
	// and `new-team.tsx`'s team-slug field) collide the instant both render on
	// one page, producing a duplicate id and a mis-pointed `aria-describedby`.
	// `link-password-card.tsx`/`link-qr-card.tsx` already use `useId()` for the
	// same reason; this standardises on it.
	const analyticsEnabledErrorId = useId();
	const destinationUrlErrorId = useId();
	const domainErrorId = useId();
	const expiresAtErrorId = useId();
	const folderErrorId = useId();
	const redirectTypeErrorId = useId();
	const slugChangeWarningId = useId();
	const slugErrorId = useId();

	const form = useForm({
		defaultValues: { ...defaultValues, ...initial },
		onSubmit: ({ value }: { readonly value: LinkFormValues }) => {
			onSubmit(value);
		},
	});

	// The edit route compares the next save against the link it reloaded
	// after this one, so the form has to hold that reload too, or a tag the
	// server dropped meanwhile is sent again and refused as gone. `useForm`
	// takes new defaults only while nothing is touched, hence the reset; it
	// happens in place rather than through a `key`, because remounting would
	// unmount the focused Save button and drop focus to the page.
	const seededVersion = useRef(initialVersion);
	useEffect(() => {
		if (seededVersion.current === initialVersion) return;
		seededVersion.current = initialVersion;
		form.reset({ ...defaultValues, ...initial });
	}, [form, initial, initialVersion]);

	// A server error naming a field this form doesn't render (see
	// `KNOWN_FIELD_NAMES` above) — surfaced as a generic alert rather than
	// nowhere at all.
	const unhandledFieldErrors = fieldErrors
		? Object.entries(fieldErrors).filter(
				([name]: readonly [string, string]) => !KNOWN_FIELD_NAMES.has(name),
			)
		: [];

	return (
		<form
			onSubmit={(event: Readonly<{ preventDefault: () => void; stopPropagation: () => void }>) => {
				event.preventDefault();
				event.stopPropagation();
				// Nothing in a read-only form can raise this, since there is no submit
				// button and every field is disabled; refusing here too keeps "a
				// read-only form never saves" true without leaning on that.
				if (readOnly) return;
				void form.handleSubmit();
			}}
		>
			{unhandledFieldErrors.length > 0 ? (
				<FieldError>
					{unhandledFieldErrors.map(([, message]: readonly [string, string]) => message).join(' ')}
				</FieldError>
			) : null}

			{/* A disabled `fieldset` disables every native control inside it at once,
			    which is why it wraps the group rather than each field carrying its own
			    flag. It does not reach Base UI's non-native parts: the analytics
			    checkbox and the tag picker's chips take `disabled` themselves below. */}
			<FieldSet disabled={readOnly}>
				<FieldGroup>
					<form.Field
						name="destination_url"
						validators={{
							onChange: ({ value }: { readonly value: string }) =>
								value.trim() === '' ? t('links.destinationRequired') : undefined,
						}}
					>
						{(field) => {
							const errorId = destinationUrlErrorId;
							const errorMessage =
								fieldErrors?.destination_url ??
								(field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

							return (
								<Field data-invalid={errorMessage !== undefined}>
									<FieldLabel htmlFor={field.name}>{t('links.destination')}</FieldLabel>
									<Input
										aria-describedby={errorMessage !== undefined ? errorId : undefined}
										aria-invalid={errorMessage !== undefined ? true : undefined}
										id={field.name}
										name={field.name}
										onBlur={field.handleBlur}
										onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
											field.handleChange(event.target.value);
										}}
										required
										type="url"
										value={field.state.value}
									/>
									{errorMessage === undefined ? null : (
										<FieldError id={errorId}>{errorMessage}</FieldError>
									)}
								</Field>
							);
						}}
					</form.Field>

					<form.Field name="slug">
						{(field) => {
							const errorId = slugErrorId;
							const errorMessage = fieldErrors?.slug;
							const warnsOfChange = !readOnly && isSlugChange(initial?.slug, field.state.value);
							// Error first, so a screen reader reads what went wrong before the
							// caution.
							const describedBy = [
								errorMessage === undefined ? undefined : errorId,
								warnsOfChange ? slugChangeWarningId : undefined,
							]
								.filter((id) => id !== undefined)
								.join(' ');

							return (
								<Field data-invalid={errorMessage !== undefined}>
									<FieldLabel htmlFor={field.name}>{t('links.slug')}</FieldLabel>
									{/* An empty slug means the API generates one. Said here, because a
								    blank required-looking field otherwise reads as an oversight. */}
									<Input
										aria-describedby={describedBy === '' ? undefined : describedBy}
										aria-invalid={errorMessage !== undefined ? true : undefined}
										id={field.name}
										name={field.name}
										onBlur={field.handleBlur}
										onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
											field.handleChange(event.target.value);
										}}
										placeholder={t('links.slugGenerated')}
										value={field.state.value}
									/>
									{errorMessage === undefined ? null : (
										<FieldError id={errorId}>{errorMessage}</FieldError>
									)}
									{/* A QR code encodes the short URL, and so does every link a Verein
									    has already shared: changing the slug retires the old address, and
									    nobody holding a printed flyer can be told. Warned as soon as the
									    value differs from the saved one, not refused — refusing would
									    mean remembering that a code was downloaded, a column and a
									    server-side rule for a hazard this names already. The input's
									    `aria-describedby` points at it, so a screen reader reads it as the
									    input's description when the field gains focus. */}
									{warnsOfChange ? (
										<FieldDescription id={slugChangeWarningId} role="note">
											{t('links.slugChangeWarning')}
										</FieldDescription>
									) : null}
								</Field>
							);
						}}
					</form.Field>

					{/* Not converted to the design system's `Select`: that control is a
				    custom popup listbox rather than a native `<select>`, and swapping
				    it in would change how this field is actually operated (and would
				    stop `userEvent.selectOptions` from working in the tests below) —
				    the opposite of this task's "behaviour does not change" rule.
				    `NativeSelect` is the design system's styling on a real `<select>`,
				    so it keeps both the behaviour and the shared look. */}
					<form.Field name="redirect_type">
						{(field) => {
							const errorId = redirectTypeErrorId;
							const errorMessage = fieldErrors?.redirect_type;

							return (
								<Field data-invalid={errorMessage !== undefined}>
									<FieldLabel htmlFor={field.name}>{t('links.redirectType')}</FieldLabel>
									<NativeSelect
										aria-describedby={errorMessage !== undefined ? errorId : undefined}
										aria-invalid={errorMessage !== undefined ? true : undefined}
										id={field.name}
										name={field.name}
										onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
											field.handleChange(Number(event.target.value));
										}}
										value={field.state.value}
									>
										<NativeSelectOption value={302}>{t('links.redirect302')}</NativeSelectOption>
										<NativeSelectOption value={301}>{t('links.redirect301')}</NativeSelectOption>
									</NativeSelect>
									{errorMessage === undefined ? null : (
										<FieldError id={errorId}>{errorMessage}</FieldError>
									)}
									{/* CLAUDE.md requires this. A cached 301 stops clicks being counted
								    and stops later destination changes taking effect for anyone who
								    has already visited — breakage a volunteer cannot diagnose and
								    cannot undo. It belongs next to the choice, not in a tooltip. */}
									{field.state.value === REDIRECT_PERMANENT ? (
										<FieldDescription role="note">{t('links.redirect301Warning')}</FieldDescription>
									) : null}
								</Field>
							);
						}}
					</form.Field>

					<form.Field name="expires_at">
						{(field) => {
							const errorId = expiresAtErrorId;
							const errorMessage = fieldErrors?.expires_at;

							return (
								<Field data-invalid={errorMessage !== undefined}>
									<FieldLabel htmlFor={field.name}>{t('links.expiresAt')}</FieldLabel>
									<Input
										aria-describedby={errorMessage !== undefined ? errorId : undefined}
										aria-invalid={errorMessage !== undefined ? true : undefined}
										id={field.name}
										name={field.name}
										onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
											field.handleChange(event.target.value);
										}}
										type="datetime-local"
										value={field.state.value}
									/>
									{errorMessage === undefined ? null : (
										<FieldError id={errorId}>{errorMessage}</FieldError>
									)}
								</Field>
							);
						}}
					</form.Field>

					<form.Field name="analytics_enabled">
						{(field) => {
							const errorId = analyticsEnabledErrorId;
							const errorMessage = fieldErrors?.analytics_enabled;

							return (
								<Field data-invalid={errorMessage !== undefined} orientation="horizontal">
									{/* `id` is load-bearing, not a leftover: Base UI's `Checkbox` renders
								    a visible `role="checkbox"` `<span>` (its own generated id,
								    unaffected by this prop) plus a hidden native input for form
								    semantics, which *does* take this `id`. That hidden input's id is
								    also how Base UI finds `FieldLabel` below as this checkbox's label
								    (matching its `htmlFor` against the hidden input's sibling
								    position) and gives the visible span an `aria-labelledby` pointing
								    at it — remove this prop and the span loses its accessible name,
								    which is exactly what breaks link-form.test.tsx's "lets the reader
								    turn analytics off" (`getByRole('checkbox', { name: ... })`). */}
									<Checkbox
										aria-describedby={errorMessage !== undefined ? errorId : undefined}
										aria-invalid={errorMessage !== undefined ? true : undefined}
										checked={field.state.value}
										disabled={readOnly}
										id={field.name}
										name={field.name}
										onCheckedChange={(checked: boolean) => {
											field.handleChange(checked);
										}}
									/>
									<FieldLabel htmlFor={field.name}>{t('links.analyticsEnabled')}</FieldLabel>
									{errorMessage === undefined ? null : (
										<FieldError id={errorId}>{errorMessage}</FieldError>
									)}
								</Field>
							);
						}}
					</form.Field>

					{/* Furniture check: a select offering only the shared domain is no
				    choice at all, so this renders nothing unless the team has at
				    least one verified domain to pick instead. The empty-valued
				    option is the shared instance hostname — `toRequestBody` in the
				    create route maps `''` back to `undefined`, exactly as it already
				    does for `slug`/`expires_at`, so leaving this untouched keeps
				    today's behaviour. Kept as a native `<select>` (via `NativeSelect`)
				    for the same reason `redirect_type` above is. */}
					{domains && domains.length > 0 ? (
						<form.Field name="domain_id">
							{(field) => {
								const errorId = domainErrorId;
								const errorMessage = fieldErrors?.domain_id;

								return (
									<Field data-invalid={errorMessage !== undefined}>
										<FieldLabel htmlFor={field.name}>{t('links.domain')}</FieldLabel>
										<NativeSelect
											aria-describedby={errorMessage !== undefined ? errorId : undefined}
											aria-invalid={errorMessage !== undefined ? true : undefined}
											id={field.name}
											name={field.name}
											onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
												field.handleChange(event.target.value);
											}}
											value={field.state.value}
										>
											<NativeSelectOption value="">{t('links.domainShared')}</NativeSelectOption>
											{domains.map((domain) => (
												<NativeSelectOption key={domain.id} value={domain.id}>
													{domain.hostname}
												</NativeSelectOption>
											))}
										</NativeSelect>
										{errorMessage === undefined ? null : (
											<FieldError id={errorId}>{errorMessage}</FieldError>
										)}
									</Field>
								);
							}}
						</form.Field>
					) : null}

					{folders === undefined ? null : (
						<form.Field name="folder_id">
							{(field) => {
								const errorMessage = fieldErrors?.folder_id;
								const currentFolderId = field.state.value;
								// A folder deleted between loading this form and the folders
								// refetch that follows a "folder gone" 422: the value the form
								// still holds is no longer among `folders`, and a controlled
								// `<select>` with no matching option silently falls back to its
								// first one ("No folder") while `field.state.value` keeps the
								// stale id — the select then *shows* "No folder" while the form
								// still *holds* the deleted id, so Save resends the same 422 and
								// picking "No folder" for real fires no change event at all,
								// since the select already looked selected on that option. Adding
								// this option one time keeps the visible selection and the form
								// state in agreement, so choosing "No folder" becomes a real
								// change again. Never auto-cleared: on the edit route, a folders
								// fetch that merely failed (not a deletion) would otherwise
								// silently unfile the link on save.
								const currentFolderIsUnknown =
									currentFolderId !== '' &&
									!folders.some((folder) => folder.id === currentFolderId);
								return (
									<Field data-invalid={errorMessage !== undefined}>
										<FieldLabel htmlFor={field.name}>{t('links.folder')}</FieldLabel>
										<NativeSelect
											aria-describedby={errorMessage !== undefined ? folderErrorId : undefined}
											aria-invalid={errorMessage !== undefined ? true : undefined}
											id={field.name}
											name={field.name}
											onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
												field.handleChange(event.target.value);
											}}
											value={field.state.value}
										>
											<NativeSelectOption value="">{t('links.folderNone')}</NativeSelectOption>
											{currentFolderIsUnknown ? (
												<NativeSelectOption value={currentFolderId}>
													{t('links.folderUnknown')}
												</NativeSelectOption>
											) : null}
											{folders.map((folder) => (
												<NativeSelectOption key={folder.id} value={folder.id}>
													{folder.name}
												</NativeSelectOption>
											))}
										</NativeSelect>
										{/* Not read-only: the hint is a link to the folders page, a disabled
										    fieldset does not reach an `<a>`, and a member who may not edit
										    cannot create folders there anyway. */}
										{folders.length === 0 && folderHint !== undefined && !readOnly ? (
											<FieldDescription>{folderHint}</FieldDescription>
										) : null}
										{errorMessage === undefined ? null : (
											<FieldError id={folderErrorId}>{errorMessage}</FieldError>
										)}
									</Field>
								);
							}}
						</form.Field>
					)}

					{tags === undefined ? null : (
						<form.Field name="tag_ids">
							{(field) => (
								<TagPicker
									canCreate={canCreateTags ?? false}
									// Only a loaded list can say a tag is gone. While the tags are
									// pending or failed, `tags` is an empty stand-in, and marking
									// every chip deleted against it would misreport a link whose
									// tags are fine.
									deletedIds={
										tagsLoaded === true
											? new Set(
													field.state.value.filter((id) => !tags.some((tag) => tag.id === id)),
												)
											: new Set()
									}
									disabled={readOnly}
									error={fieldErrors?.tag_ids}
									inputId={field.name}
									knownNames={tagNames ?? new Map()}
									label={t('links.tags')}
									onChange={(ids) => {
										field.handleChange(ids);
									}}
									onCreate={
										onCreateTag ??
										// oxlint-disable-next-line typescript/require-await -- `TagPicker`'s `onCreate` must return a `Promise`; this stand-in for a caller that wired no create call has nothing to await.
										(async () => ({ error: t('errors.unknown') }))
									}
									options={tags}
									value={field.state.value}
								/>
							)}
						</form.Field>
					)}
				</FieldGroup>
			</FieldSet>

			{readOnly ? null : <Button type="submit">{t('links.save')}</Button>}
		</form>
	);
}
