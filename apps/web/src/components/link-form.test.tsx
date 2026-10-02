import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkForm, type LinkFormValues } from './link-form';
import type { TagCreateResult, TagOption } from './tag-picker';

const teamTags = [
	{ id: 't1', name: 'Jugend' },
	{ id: 't2', name: 'Presse' },
];

/**
 * Same pattern as `language-switcher.test.tsx`: any component that calls
 * `useTranslation` needs an `I18nextProvider` in its tree, or `t(...)` throws
 * looking up `react-i18next`'s default context. English is enough here — the
 * regexes below (`/redirect|weiterleitung/i`, `/destination|ziel/i`) already
 * match either language, and no test in this file asserts on German copy
 * specifically.
 *
 * @param props - The props to render `LinkForm` with.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactNode` is React's own type; not a declaration this file can edit. `tagNames` is a `ReadonlyMap`, TypeScript's immutable map type, which the rule does not recognise as readonly, the same limitation `audit-actor.ts` documents.
function renderForm(props: {
	readonly canCreateTags?: boolean;
	readonly domains?: readonly Readonly<{ id: string; hostname: string }>[];
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly folderHint?: React.ReactNode;
	readonly folders?: readonly Readonly<{ id: string; name: string }>[];
	readonly initial?: Partial<LinkFormValues>;
	readonly onCreateTag?: (name: string) => Promise<TagCreateResult>;
	readonly onSubmit: (values: LinkFormValues) => void;
	readonly tagNames?: ReadonlyMap<string, string>;
	readonly tags?: readonly TagOption[];
	readonly tagsLoaded?: boolean;
}): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LinkForm {...props} />
		</I18nextProvider>,
	);
}

describe(LinkForm, () => {
	it('warns inline when 301 is chosen', async () => {
		// CLAUDE.md requires this. A cached 301 stops clicks being counted and
		// stops later destination changes taking effect for anyone who has
		// visited once — breakage a user cannot diagnose and cannot undo.
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		await userEvent.selectOptions(screen.getByLabelText(/redirect|weiterleitung/iu), '301');

		await expect(screen.findByRole('note')).resolves.toBeInTheDocument();
	});

	it('does not warn for 302', () => {
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('shows a server field error on the field it belongs to', () => {
		renderForm({
			fieldErrors: { destination_url: 'must be https' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByLabelText(/destination|ziel/iu)).toHaveAccessibleDescription(
			/must be https/u,
		);
	});

	it('shows a server field error for expires_at', () => {
		// Finding 1: this field previously had no `aria-describedby`/error `<p>`
		// wiring at all, so a rejection naming it (a past expiry, say) rendered
		// nothing — no field message, no banner, a silent failure.
		renderForm({
			fieldErrors: { expires_at: 'must be in the future' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByLabelText(/expires|läuft ab/iu)).toHaveAccessibleDescription(
			/must be in the future/u,
		);
	});

	it('associates each error with its field', () => {
		// The whole reason for moving to `Field`: the association used to be four
		// hand-written attributes per field (`htmlFor`, `id`, `aria-describedby`,
		// `aria-invalid`), and a missed one is invisible until a screen reader hits
		// it. `Field` owns all four, so this asserts the outcome rather than the
		// attributes.
		renderForm({
			fieldErrors: { destination_url: 'Destination URL is required.' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});

		expect(screen.getByLabelText('Destination URL')).toHaveAccessibleDescription(
			'Destination URL is required.',
		);
	});

	it('marks a field with a server error as aria-invalid, not only described-by', () => {
		// Minor 11: `aria-describedby` alone tells assistive tech there is *a*
		// description, not that the field failed validation — `aria-invalid` is
		// what a screen reader announces unprompted, without the visitor having
		// to go hunting for the description text.
		renderForm({
			fieldErrors: { destination_url: 'must be https' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByLabelText(/destination|ziel/iu)).toHaveAttribute('aria-invalid', 'true');
	});

	it('does not mark a field invalid when it has no error', () => {
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.getByLabelText(/destination|ziel/iu)).not.toHaveAttribute('aria-invalid');
	});

	it('announces a field-level server error as an alert', () => {
		// Minor 11, other half: the error `<p>` had no `role="alert"`, so a
		// screen reader only reached it by hunting, the same gap
		// `aria-invalid` closes for the field itself.
		renderForm({
			fieldErrors: { destination_url: 'must be https' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByRole('alert')).toHaveTextContent('must be https');
	});

	it('shows a server field error for a field the form has no input for', () => {
		// A field the API might add later, or any name this form doesn't render
		// a specific input for — must still surface, not vanish.
		renderForm({
			fieldErrors: { some_future_field: 'not allowed' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByRole('alert')).toHaveTextContent('not allowed');
	});

	it('shows the slug placeholder saying one will be generated', () => {
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.getByLabelText(/short path|kurzpfad/iu)).toHaveAttribute(
			'placeholder',
			'Leave empty and one will be generated',
		);
	});

	it('lets the reader turn analytics off', async () => {
		// The one field this task moved onto the design system's `Checkbox`
		// rather than `Input`. `Checkbox` renders a visible, interactive
		// `role="checkbox"` `<span>` beside a hidden native input that only
		// exists for form semantics (Base UI's own doing, not this form's) —
		// `<label for>` resolves to that hidden input by plain HTML rules,
		// so `getByLabelText` matches both and this needs `getByRole`'s own
		// accessible-name lookup, which excludes the `aria-hidden` half,
		// instead. That is what proves the label association and the toggle
		// itself still work post-migration, not only that the form renders.
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({ onSubmit });

		await userEvent.click(screen.getByRole('checkbox', { name: /count clicks|klicks/iu }));
		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ analytics_enabled: false }));
	});

	it('offers a domain picker when the team has a verified domain', async () => {
		// The API has accepted an explicit domain_id since plan 3; the form
		// never asked, so every link landed on the shared hostname. A verified
		// domain with no way to put a link on it is not a feature.
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({ domains: [{ hostname: 'links.verein.test', id: 'd1' }], onSubmit });

		await userEvent.selectOptions(screen.getByLabelText(/domain/iu), 'd1');
		await userEvent.type(screen.getByLabelText(/destination/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ domain_id: 'd1' }));
	});

	it('omits the picker when there is nothing to pick', () => {
		// A select with one option is furniture.
		renderForm({ domains: [], onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.queryByLabelText(/domain/iu)).not.toBeInTheDocument();
	});

	it('omits the picker when no domains prop is passed at all', () => {
		// The edit route (Task 11) and any other caller that doesn't yet know
		// about domains must keep getting today's furniture-free form.
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.queryByLabelText(/domain/iu)).not.toBeInTheDocument();
	});

	it('offers "No folder" first, then the folders, and hands back the chosen id', async () => {
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({ folders: [{ id: 'f1', name: 'Sommerfest' }], onSubmit });
		const select = screen.getByRole('combobox', { name: 'Folder' });
		expect(
			within(select)
				.getAllByRole('option')
				// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `HTMLElement` is a DOM lib type, not one this codebase declares.
				.map((option) => option.textContent),
		).toStrictEqual(['No folder', 'Sommerfest']);

		await userEvent.selectOptions(select, 'f1');
		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ folder_id: 'f1' }));
	});

	it('shows the hint when the team has no folders', () => {
		renderForm({
			folderHint: 'No folders yet. Create them on the Folders page.',
			folders: [],
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByText('No folders yet. Create them on the Folders page.')).toBeVisible();
	});

	it('shows a folder_id field error on the folder field', () => {
		renderForm({
			fieldErrors: { folder_id: 'This folder no longer exists.' },
			folders: [{ id: 'f1', name: 'Sommerfest' }],
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.getByRole('combobox', { name: 'Folder' })).toHaveAccessibleDescription(
			'This folder no longer exists.',
		);
	});

	it('omits the folder field when no folders prop is passed at all', () => {
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.queryByRole('combobox', { name: 'Folder' })).not.toBeInTheDocument();
	});

	it('renders a deleted folder as its own option and lets "No folder" be chosen for real', async () => {
		// The finding this fixes: a folder_id the refetched `folders` no longer
		// carries used to leave the select *showing* "No folder" (DOM
		// selectedIndex 0) while `field.state.value` still held the stale id —
		// so Save resent the same id, and choosing "No folder" fired no change
		// event at all, since the select already looked selected on that
		// option. Asserting the select's own value (not just its rendered
		// options) is what proves the DOM and the form state now agree.
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({
			folders: [{ id: 'f1', name: 'Sommerfest' }],
			initial: { folder_id: 'f0' },
			onSubmit,
		});
		const select = screen.getByRole('combobox', { name: 'Folder' });
		expect(select).toHaveValue('f0');
		expect(
			within(select)
				.getAllByRole('option')
				// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `HTMLElement` is a DOM lib type, not one this codebase declares.
				.map((option) => option.textContent),
		).toStrictEqual(['No folder', 'Deleted folder', 'Sommerfest']);

		await userEvent.selectOptions(select, 'No folder');
		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ folder_id: '' }));
	});

	it('does not add a "Deleted folder" option when the current value is a real folder', () => {
		renderForm({
			folders: [{ id: 'f1', name: 'Sommerfest' }],
			initial: { folder_id: 'f1' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
		});
		expect(screen.queryByText('Deleted folder')).not.toBeInTheDocument();
	});

	it('offers the team tags and hands back the chosen ids', async () => {
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({ onSubmit, tags: teamTags, tagsLoaded: true });

		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Pre');
		await userEvent.click(screen.getByRole('option', { name: 'Presse' }));
		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ tag_ids: ['t2'] }));
	});

	it('omits the tags field when no tags prop is passed at all', () => {
		renderForm({ onSubmit: vi.fn<(values: LinkFormValues) => void>() });
		expect(screen.queryByRole('combobox', { name: 'Tags' })).not.toBeInTheDocument();
	});

	it('hands back no tags when none were chosen', async () => {
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({ onSubmit, tags: teamTags, tagsLoaded: true });

		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ tag_ids: [] }));
	});

	it('shows a tag_ids field error on the tags field', () => {
		renderForm({
			fieldErrors: { tag_ids: 'A chosen tag no longer exists. Remove it and save again.' },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
			tags: teamTags,
			tagsLoaded: true,
		});
		expect(screen.getByRole('combobox', { name: 'Tags' })).toHaveAccessibleDescription(
			'A chosen tag no longer exists. Remove it and save again.',
		);
		// Attached to its own field, not repeated by the alert for fields the
		// form has no input for.
		expect(screen.getAllByRole('alert')).toHaveLength(1);
	});

	it('offers no create option when the caller may not create tags', async () => {
		renderForm({
			canCreateTags: false,
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
			tags: teamTags,
			tagsLoaded: true,
		});
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand');
		expect(screen.queryByRole('option', { name: /create tag/iu })).toBeNull();
	});

	it('offers a create option to an editor and chooses the created tag', async () => {
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		const onCreateTag = vi.fn<(name: string) => Promise<TagCreateResult>>(
			// oxlint-disable-next-line typescript/require-await -- stands in for the create call the picker awaits; the fake has nothing to await itself.
			async () => ({ tag: { id: 't9', name: 'Vorstand' } }),
		);
		renderForm({ canCreateTags: true, onCreateTag, onSubmit, tags: teamTags, tagsLoaded: true });

		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		await expect(
			screen.findByRole('button', { name: 'Remove tag Vorstand' }),
		).resolves.toBeInTheDocument();
		await userEvent.type(screen.getByLabelText(/destination|ziel/iu), 'https://example.org/');
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onCreateTag).toHaveBeenCalledWith('Vorstand');
		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ tag_ids: ['t9'] }));
	});

	it('keeps a link’s chips, named from tagNames, while the tags are unavailable', async () => {
		// Review Focus 1: the tags fetch failed, so `tags` is empty — that says
		// nothing about whether the link's tags still exist, so no chip is
		// marked deleted, and saving hands the same ids back untouched.
		const onSubmit = vi.fn<(values: LinkFormValues) => void>();
		renderForm({
			initial: { destination_url: 'https://example.org/', tag_ids: ['t1', 't2'] },
			onSubmit,
			tagNames: new Map(teamTags.map((tag: TagOption) => [tag.id, tag.name])),
			tags: [],
			tagsLoaded: false,
		});

		expect(screen.getByText('Jugend')).toBeVisible();
		expect(screen.getByText('Presse')).toBeVisible();
		expect(screen.queryByText('(deleted)')).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole('button', { name: /save/iu }));

		expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ tag_ids: ['t1', 't2'] }));
	});

	it('marks a chosen tag the loaded tags no longer have as deleted', () => {
		renderForm({
			initial: { tag_ids: ['t1', 'gone'] },
			onSubmit: vi.fn<(values: LinkFormValues) => void>(),
			tagNames: new Map([['gone', 'Altpapier']]),
			tags: teamTags,
			tagsLoaded: true,
		});
		expect(screen.getByText('Altpapier')).toBeVisible();
		expect(screen.getAllByText('(deleted)')).toHaveLength(1);
	});
});
