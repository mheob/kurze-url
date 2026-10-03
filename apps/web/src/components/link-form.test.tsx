import { act, fireEvent, render, screen, within } from '@testing-library/react';
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
	readonly readOnly?: boolean;
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

/**
 * Replaces what is in the slug field.
 *
 * @param value - The slug to type in.
 * @returns The slug input.
 */
async function typeSlug(value: string): Promise<HTMLElement> {
	const slug = screen.getByLabelText('Short path');
	await userEvent.clear(slug);
	await userEvent.type(slug, value);
	return slug;
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

	// A QR code and every link already shared encode the short path, so changing
	// it on a saved link retires the old address without telling anyone who holds
	// it. The warning is the whole of the answer: no dialog, no refusal.
	describe('slug change warning', () => {
		const warning = /Changing the short path retires the old address/u;
		const saved: Partial<LinkFormValues> = {
			destination_url: 'https://example.org/sommerfest',
			slug: 'sommerfest',
		};

		it('says nothing while the slug is still the saved one', () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			expect(screen.queryByText(warning)).not.toBeInTheDocument();
			expect(screen.getByLabelText('Short path')).not.toHaveAccessibleDescription();
		});

		it('warns once the slug differs from the saved one, and stops when it is put back', async () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			await typeSlug('herbstfest');
			expect(screen.getByText(warning)).toBeVisible();

			await typeSlug('sommerfest');
			expect(screen.queryByText(warning)).not.toBeInTheDocument();
		});

		it('treats a change of case alone as no change, since the API stores slugs lowercase', async () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			await typeSlug('Sommerfest');

			expect(screen.queryByText(warning)).not.toBeInTheDocument();
		});

		it('ignores whitespace around the slug when comparing', async () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			await typeSlug(' sommerfest ');

			expect(screen.queryByText(warning)).not.toBeInTheDocument();
		});

		it('describes the slug input with the warning', async () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			const slug = await typeSlug('herbstfest');

			expect(slug).toHaveAccessibleDescription(/retires the old address/u);
		});

		it('describes the slug input with its error first and the warning after it', async () => {
			renderForm({
				fieldErrors: { slug: 'That path is taken.' },
				initial: saved,
				onSubmit: vi.fn<(values: LinkFormValues) => void>(),
			});

			const slug = await typeSlug('herbstfest');

			expect(slug).toHaveAccessibleDescription(/^That path is taken\. Changing the short path/u);
		});

		it('announces the warning as a note, like the 301 warning', async () => {
			renderForm({ initial: saved, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

			await typeSlug('herbstfest');

			expect(screen.getByRole('note')).toHaveTextContent('retires the old address');
		});

		it.each([
			['no saved slug at all', undefined],
			['an empty saved slug', { slug: '' }],
		])(
			'never warns on the create form, which has %s',
			async (_name: string, initial: Partial<LinkFormValues> | undefined) => {
				renderForm({ initial, onSubmit: vi.fn<(values: LinkFormValues) => void>() });

				await typeSlug('herbstfest');

				expect(screen.queryByText(warning)).not.toBeInTheDocument();
				expect(screen.getByLabelText('Short path')).not.toHaveAccessibleDescription();
			},
		);

		it('does not warn on a read-only form, even if the value somehow differs', () => {
			// `userEvent` treats a disabled fieldset's contents as inert, so the
			// change is dispatched by hand: it is the only way to give the guard
			// something to refuse, since a read-only form's value cannot otherwise
			// leave the saved one.
			renderForm({
				initial: saved,
				onSubmit: vi.fn<(values: LinkFormValues) => void>(),
				readOnly: true,
			});
			expect(screen.queryByText(warning)).not.toBeInTheDocument();

			fireEvent.change(screen.getByLabelText('Short path'), { target: { value: 'herbstfest' } });

			expect(screen.getByLabelText('Short path')).toHaveValue('herbstfest');
			expect(screen.queryByText(warning)).not.toBeInTheDocument();
		});

		it('still saves a changed slug without asking', async () => {
			const onSubmit = vi.fn<(values: LinkFormValues) => void>();
			renderForm({ initial: saved, onSubmit });

			await typeSlug('herbstfest');
			await userEvent.click(screen.getByRole('button', { name: 'Save' }));

			expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ slug: 'herbstfest' }));
		});
	});

	// A viewer reads the link's settings here and cannot change them: the API
	// answers every write with a 403, so the form shows the values and offers
	// nothing to operate. Disabled, not merely hidden, so a screen reader still
	// reads each field with its value, and no control can be reached by keyboard.
	describe('read-only', () => {
		const stored: Partial<LinkFormValues> = {
			analytics_enabled: true,
			destination_url: 'https://example.org/sommerfest',
			domain_id: 'd1',
			expires_at: '2030-01-01T10:00',
			folder_id: 'f1',
			redirect_type: 301,
			slug: 'sommerfest',
			tag_ids: ['t1', 't2'],
		};

		/**
		 * Every control the form can render, filled from `stored`.
		 *
		 * @param onSubmit - The submit handler the form is given, for tests that assert it is never called.
		 * @returns The rendered test utilities from Testing Library's `render`.
		 */
		function renderReadOnly(
			onSubmit: (values: LinkFormValues) => void = vi.fn<(values: LinkFormValues) => void>(),
		): ReturnType<typeof render> {
			return renderForm({
				domains: [{ hostname: 'links.verein.test', id: 'd1' }],
				folders: [{ id: 'f1', name: 'Sommerfest' }],
				initial: stored,
				onSubmit,
				readOnly: true,
				tags: teamTags,
				tagsLoaded: true,
			});
		}

		it.each([
			['Destination URL', 'https://example.org/sommerfest'],
			['Short path', 'sommerfest'],
			['Redirect type', '301'],
			['Expires at', '2030-01-01T10:00'],
			['Domain', 'd1'],
			['Folder', 'f1'],
		])('shows %s with its stored value, disabled', (label, value) => {
			renderReadOnly();

			expect(screen.getByLabelText(label)).toBeDisabled();
			expect(screen.getByLabelText(label)).toHaveValue(value);
		});

		it('shows the analytics choice as disabled, and a click does not change it', async () => {
			renderReadOnly();

			const checkbox = screen.getByRole('checkbox', { name: 'Count clicks for this link' });
			expect(checkbox).toBeChecked();
			expect(checkbox).toHaveAttribute('aria-disabled', 'true');
			// A disabled fieldset does not take this `span` out of the tab order in a
			// browser, which only disables form controls; Base UI does, once told.
			expect(checkbox).toHaveAttribute('tabindex', '-1');

			await userEvent.click(checkbox);

			expect(checkbox).toBeChecked();
		});

		it('has no submit button', () => {
			renderReadOnly();

			expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
		});

		it('shows the tags as chips and disables the picker and every chip remove button', () => {
			renderReadOnly();

			expect(screen.getByText('Jugend')).toBeVisible();
			expect(screen.getByText('Presse')).toBeVisible();
			expect(screen.getByRole('combobox', { name: 'Tags' })).toBeDisabled();
			// Every button left on the form is a chip's remove button, and each is disabled.
			const buttons = screen.getAllByRole('button');
			expect(buttons).toHaveLength(2);
			for (const button of buttons) expect(button).toBeDisabled();
		});

		// Base UI's chips are focusable `div`s that remove themselves on Backspace.
		// A disabled fieldset does not reach them in a browser, which only disables
		// form controls, so the picker is told directly. The key event is
		// dispatched by hand: `userEvent` treats everything inside a disabled
		// fieldset as disabled and would swallow it, passing for the wrong reason.
		it('does not remove a chip on Backspace or Delete', () => {
			renderReadOnly();

			// Dispatched on the chip's label, which bubbles to the chip itself.
			fireEvent.keyDown(screen.getByText('Jugend'), { key: 'Backspace' });
			fireEvent.keyDown(screen.getByText('Jugend'), { key: 'Delete' });

			expect(screen.getByText('Jugend')).toBeVisible();
			expect(screen.getByText('Presse')).toBeVisible();
		});

		it('takes no keyboard focus at all', async () => {
			renderReadOnly();

			await userEvent.tab();
			expect(document.body).toHaveFocus();
		});

		// The hint is a link to the folders page, where a viewer can create
		// nothing, and a disabled fieldset does not reach an `<a>` in a browser:
		// shown, it would be the one focusable thing on a form that is otherwise
		// inert. `userEvent` treats everything inside a disabled fieldset as
		// unfocusable, so the Tab check at the end cannot fail by itself here (it
		// passes with the hint shown too); the link's absence is what pins this.
		it('leaves out the "no folders yet" hint link, so nothing in the form takes focus', async () => {
			renderForm({
				folderHint: (
					<a href="/teams/verein-a/folders">{'No folders yet. Create them on the Folders page.'}</a>
				),
				folders: [],
				initial: stored,
				onSubmit: vi.fn<(values: LinkFormValues) => void>(),
				readOnly: true,
				tags: teamTags,
				tagsLoaded: true,
			});

			expect(screen.getByLabelText('Folder')).toBeDisabled();
			expect(screen.queryByRole('link')).not.toBeInTheDocument();
			expect(screen.queryByText(/No folders yet/u)).not.toBeInTheDocument();

			await userEvent.tab();
			expect(document.body).toHaveFocus();
		});

		it('never calls onSubmit, even when a submit event reaches the form', async () => {
			const onSubmit = vi.fn<(values: LinkFormValues) => void>();
			renderReadOnly(onSubmit);

			// A submit event on a field bubbles to the form, the same path Enter takes.
			// `form.handleSubmit()` settles asynchronously, so an assertion made
			// straight after would pass whatever the form does with the event.
			await act(async () => {
				fireEvent.submit(screen.getByLabelText('Destination URL'));
				await Promise.resolve();
			});

			expect(onSubmit).not.toHaveBeenCalled();
		});

		it('is editable and saveable when readOnly is not set', async () => {
			const onSubmit = vi.fn<(values: LinkFormValues) => void>();
			renderForm({ initial: stored, onSubmit });

			expect(screen.getByLabelText('Destination URL')).toBeEnabled();
			await userEvent.click(screen.getByRole('button', { name: 'Save' }));

			expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ slug: 'sommerfest' }));
		});
	});

	describe('after a save reloads the record', () => {
		const i18n = createI18n('en');
		const before = { destination_url: 'https://example.org/', tag_ids: ['t1', 'gone'] };
		// The server dropped the tag deleted meanwhile, so the reload lacks it.
		const after = { destination_url: 'https://example.org/new', tag_ids: ['t1'] };

		/**
		 * The form as the edit route renders it, seeded from one version of the link.
		 *
		 * @param initial - The values the link held at that version.
		 * @param initialVersion - The link's `updated_at`.
		 * @param onSubmit - The submit handler.
		 * @returns The form element.
		 */
		function form(
			initial: Partial<LinkFormValues>,
			initialVersion: string,
			onSubmit: (values: LinkFormValues) => void,
		): React.JSX.Element {
			return (
				<I18nextProvider i18n={i18n}>
					<LinkForm
						initial={initial}
						initialVersion={initialVersion}
						onSubmit={onSubmit}
						tagNames={new Map([['gone', 'Altpapier']])}
						tags={teamTags}
						tagsLoaded
					/>
				</I18nextProvider>
			);
		}

		it('re-seeds in place from a new version, keeping focus on Save', async () => {
			const onSubmit = vi.fn<(values: LinkFormValues) => void>();
			const { rerender } = render(form(before, 'v1', onSubmit));
			const destination = screen.getByLabelText(/destination|ziel/iu);
			await userEvent.clear(destination);
			await userEvent.type(destination, after.destination_url);
			const save = screen.getByRole('button', { name: /save/iu });
			await userEvent.click(save);
			rerender(form(after, 'v2', onSubmit));

			expect(screen.queryByText('Altpapier')).not.toBeInTheDocument();
			expect(save).toHaveFocus();
			await userEvent.click(save);
			expect(onSubmit).toHaveBeenLastCalledWith(expect.objectContaining(after));
		});

		it('takes the slug the save just stored as the one to compare against', async () => {
			// The baseline follows `initial.slug`: once the save reloads the record
			// and `initial` carries the new slug, that slug is the saved one and the
			// old one counts as the change.
			const warning = /Changing the short path retires the old address/u;
			const onSubmit = vi.fn<(values: LinkFormValues) => void>();
			const { rerender } = render(form({ ...before, slug: 'sommerfest' }, 'v1', onSubmit));
			const slug = screen.getByLabelText('Short path');
			await userEvent.clear(slug);
			await userEvent.type(slug, 'herbstfest');
			expect(screen.getByText(warning)).toBeVisible();

			rerender(form({ ...after, slug: 'herbstfest' }, 'v2', onSubmit));

			expect(slug).toHaveValue('herbstfest');
			expect(screen.queryByText(warning)).not.toBeInTheDocument();

			await userEvent.clear(slug);
			await userEvent.type(slug, 'sommerfest');
			expect(screen.getByText(warning)).toBeVisible();
		});

		it('keeps unsaved edits while the version stays the same', async () => {
			const { rerender } = render(form(before, 'v1', vi.fn<(values: LinkFormValues) => void>()));
			const destination = screen.getByLabelText(/destination|ziel/iu);
			await userEvent.type(destination, 'typed');
			rerender(form(after, 'v1', vi.fn<(values: LinkFormValues) => void>()));

			expect(destination).toHaveValue('https://example.org/typed');
			expect(screen.getByText('Altpapier')).toBeVisible();
		});
	});
});
