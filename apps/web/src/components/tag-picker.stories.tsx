import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, screen, userEvent, within } from 'storybook/test';

import { TAGS_PER_LINK } from '../lib/tags';
import type { TagCreateResult, TagOption } from './tag-picker';
import { TagPicker } from './tag-picker';

const tags = [
	'Jugend',
	'Presse',
	'Vorstand',
	'Sommerfest',
	'Mitglieder',
	'Training',
	'Spenden',
	'Newsletter',
	'Turnier',
	'Ehrenamt',
	'Archiv',
	'Satzung',
].map((name, index) => ({ id: `tag-${index}`, name }));

const meta = {
	args: {
		canCreate: false,
		deletedIds: new Set<string>(),
		inputId: 'tags',
		knownNames: new Map<string, string>(),
		label: 'Tags',
		onChange: fn<(ids: readonly string[]) => void>(),
		onCreate: fn<(name: string) => Promise<TagCreateResult>>(),
		options: tags,
		value: [],
	},
	component: TagPicker,
	title: 'Links/TagPicker',
} satisfies Meta<typeof TagPicker>;

export default meta;

/** Nothing chosen yet: the labelled input and its placeholder. */
export const Empty: StoryObj<typeof meta> = {};

/** Two chosen tags, each a chip with its own named remove button. */
export const WithTags: StoryObj<typeof meta> = {
	args: { value: ['tag-0', 'tag-1'] },
};

/** Ten chips, the most a link can carry: the hint below the field says why nothing more is offered. */
export const Full: StoryObj<typeof meta> = {
	args: { value: tags.slice(0, TAGS_PER_LINK).map((tag: TagOption) => tag.id) },
};

/**
 * An editor typing a name no tag has yet, so the list offers to create it.
 * The list renders through a portal, so the assertion queries `screen`, not
 * `within(canvasElement)`, the same reason `StatRangePicker`'s `CalendarOpen`
 * story gives.
 */
export const Creatable: StoryObj<typeof meta> = {
	args: { canCreate: true },
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.type(canvas.getByRole('combobox', { name: 'Tags' }), 'Kassenwart');
		await expect(
			await screen.findByRole('option', { name: 'Create tag "Kassenwart"' }),
		).toBeVisible();
	},
};

/** A chip whose tag was deleted meanwhile: its name comes from the link, and it says it is gone. */
export const DeletedChip: StoryObj<typeof meta> = {
	args: {
		deletedIds: new Set(['gone']),
		knownNames: new Map([['gone', 'Altpapier']]),
		value: ['tag-0', 'gone'],
	},
};

/** The server refused a chosen tag: the message sits on the field and the input is marked invalid. */
export const WithError: StoryObj<typeof meta> = {
	args: {
		error: 'A chosen tag no longer exists. Remove it and save again.',
		value: ['tag-0'],
	},
};
