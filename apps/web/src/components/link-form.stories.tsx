import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, screen, userEvent, waitFor, within } from 'storybook/test';

import { LinkForm, type LinkFormValues } from './link-form';
import type { TagCreateResult } from './tag-picker';

const teamTags = [
	{ id: 't1', name: 'Jugend' },
	{ id: 't2', name: 'Presse' },
	{ id: 't3', name: 'Vorstand' },
];

const meta = {
	component: LinkForm,
	title: 'Links/LinkForm',
} satisfies Meta<typeof LinkForm>;

export default meta;

/** The blank create-a-link state. */
export const Default: StoryObj<typeof meta> = {
	args: { onSubmit: fn<(values: LinkFormValues) => void>() },
};

/**
 * A rejected destination, lit up on the field it belongs to rather than as a
 * banner — so the a11y addon also covers the error-association wiring
 * (`aria-describedby`) here, not only on the empty-form state above.
 */
export const WithFieldError: StoryObj<typeof meta> = {
	args: {
		fieldErrors: { destination_url: 'The destination must use https://.' },
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

/**
 * A team with a verified domain — the only state that renders the domain
 * picker at all (Task 14), so this is what puts its `<select>`/`<label>`
 * pairing in front of the a11y addon rather than leaving it unexercised by
 * every other story here.
 */
export const WithDomainPicker: StoryObj<typeof meta> = {
	args: {
		domains: [{ hostname: 'links.verein.test', id: 'd1' }],
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

/** A team with folders to choose from — the folder field's `<select>`/`<label>` pairing in front of the a11y addon. */
export const WithFolders: StoryObj<typeof meta> = {
	args: {
		folders: [
			{ id: 'f1', name: 'Sommerfest' },
			{ id: 'f2', name: 'Vorstand' },
		],
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

/**
 * No folders yet: the select offers only "No folder", and the hint below it
 * points at the folders page — plain text here, since the real `<Link>` needs
 * a router this story doesn't set up.
 */
export const NoFoldersYet: StoryObj<typeof meta> = {
	args: {
		folderHint: 'No folders yet. Create them on the Folders page.',
		folders: [],
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

/** A team with tags, two of them chosen: the picker's chips and their named remove buttons in front of the a11y addon. */
export const WithTags: StoryObj<typeof meta> = {
	args: {
		initial: { tag_ids: ['t1', 't2'] },
		onSubmit: fn<(values: LinkFormValues) => void>(),
		tags: teamTags,
		tagsLoaded: true,
	},
};

/**
 * An editor typing a name no tag has yet, so the picker offers to create it.
 * The list renders through a portal, so the assertion queries `screen`, the
 * same reason `TagPicker`'s own `Creatable` story gives.
 *
 * The play closes the list again before it ends. While a typeable combobox
 * is open, Floating UI (under Base UI) sets `aria-hidden` on everything
 * outside it, so a screen reader's virtual cursor stays in the list. Here
 * that includes the form's other inputs, and axe reports them as hidden yet
 * focusable (`aria-hidden-focus`). The open state's own markup is covered by
 * `TagPicker`'s `Creatable` story, where the picker has no siblings.
 */
export const TagCreator: StoryObj<typeof meta> = {
	args: {
		canCreateTags: true,
		// Resolves like a successful create, so a manual "Create" click in
		// Storybook adds the chip instead of leaving the picker pending.
		onCreateTag: fn<(name: string) => Promise<TagCreateResult>>(
			// oxlint-disable-next-line typescript/require-await -- stands in for the create call `TagPicker` awaits; the fake has nothing to await itself.
			async (name: string) => ({ tag: { id: `created-${name.toLowerCase()}`, name } }),
		),
		onSubmit: fn<(values: LinkFormValues) => void>(),
		tags: teamTags,
		tagsLoaded: true,
	},
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.type(canvas.getByRole('combobox', { name: 'Tags' }), 'Kassenwart');
		await expect(
			await screen.findByRole('option', { name: 'Create tag "Kassenwart"' }),
		).toBeVisible();
		await userEvent.keyboard('{Escape}');
		await waitFor(async () => {
			await expect(screen.queryByRole('listbox')).toBeNull();
		});
	},
};

/**
 * The edit form of a saved link whose short path has just been changed: the
 * warning that the old address stops working is showing, and the slug input
 * is described by it. The play does the editing, since the warning only
 * exists once the value differs from the saved one, so the a11y addon runs
 * against the form with the note and the extra `aria-describedby` in place.
 */
export const SlugChanged: StoryObj<typeof meta> = {
	args: {
		initial: { destination_url: 'https://example.org/sommerfest', slug: 'sommerfest' },
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const slug = canvas.getByLabelText('Short path');
		await userEvent.clear(slug);
		await userEvent.type(slug, 'herbstfest');
		await expect(canvas.getByRole('note')).toHaveTextContent(/retires the old address/u);
		await expect(slug).toHaveAccessibleDescription(/retires the old address/u);
	},
};

/**
 * A viewer's view of a link: every field filled and disabled, the tags shown
 * as chips with their remove buttons off, and no Save button. Every control
 * the form can render is present, so the a11y addon covers the disabled
 * `fieldset`, the disabled Base UI checkbox and the disabled picker together.
 */
export const ReadOnly: StoryObj<typeof meta> = {
	args: {
		domains: [{ hostname: 'links.verein.test', id: 'd1' }],
		folders: [
			{ id: 'f1', name: 'Sommerfest' },
			{ id: 'f2', name: 'Vorstand' },
		],
		initial: {
			analytics_enabled: true,
			destination_url: 'https://example.org/sommerfest',
			domain_id: 'd1',
			expires_at: '2030-01-01T10:00',
			folder_id: 'f1',
			redirect_type: 301,
			slug: 'sommerfest',
			tag_ids: ['t1', 't2'],
		},
		onSubmit: fn<(values: LinkFormValues) => void>(),
		readOnly: true,
		tags: teamTags,
		tagsLoaded: true,
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Default.args },
	globals: { theme: 'dark' },
};
