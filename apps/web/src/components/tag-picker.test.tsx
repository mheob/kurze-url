import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { TagPicker, type TagCreateResult, type TagOption, type TagPickerProps } from './tag-picker';

const options = [
	{ id: 't1', name: 'Jugend' },
	{ id: 't2', name: 'Presse' },
];

/**
 * Tags named `X0`, `X1` and so on, for the tests about the per-link cap.
 *
 * @param count - How many to make.
 * @returns The tags, with ids `x0`, `x1` and so on.
 */
function numberedTags(count: number): TagOption[] {
	return Array.from({ length: count }, (_: unknown, index: number) => ({
		id: `x${index}`,
		name: `X${index}`,
	}));
}

/**
 * Renders the picker with a viewer's defaults. The picker draws its own
 * visible label, so this renders no `<label>` of its own: the input then has
 * exactly one accessible name.
 *
 * @param overrides - The props this test cares about.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `TagPickerProps` carries a `ReadonlySet` and a `ReadonlyMap`, TypeScript's immutable collection types; the rule does not recognise them as readonly, the same limitation `audit-actor.ts` documents.
function renderPicker(overrides: Partial<TagPickerProps> = {}): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<TagPicker
				canCreate={false}
				deletedIds={new Set()}
				inputId="tags-input"
				knownNames={new Map()}
				label="Tags"
				onChange={vi.fn<(ids: readonly string[]) => void>()}
				onCreate={vi.fn<(name: string) => Promise<TagCreateResult>>()}
				options={options}
				value={[]}
				{...overrides}
			/>
		</I18nextProvider>,
	);
}

/**
 * A caller that owns the chosen ids, so removing a chip actually changes what
 * the picker is given, the way the link form will.
 *
 * @param props - The harness's props.
 * @param props.initial - The ids chosen at first.
 * @param props.tags - The team's tags.
 * @returns The picker, wired to its own state.
 */
function StatefulPicker({
	initial,
	tags,
}: Readonly<{ initial: readonly string[]; tags: readonly TagOption[] }>): React.JSX.Element {
	const [value, setValue] = useState<readonly string[]>(initial);
	return (
		<TagPicker
			canCreate={false}
			deletedIds={new Set()}
			inputId="tags-input"
			knownNames={new Map()}
			label="Tags"
			onChange={setValue}
			onCreate={vi.fn<(name: string) => Promise<TagCreateResult>>()}
			options={tags}
			value={value}
		/>
	);
}

describe(TagPicker, () => {
	it('filters case-insensitively and does not offer chosen tags again', async () => {
		renderPicker({ value: ['t1'] });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'pre');
		expect(screen.getByRole('option', { name: 'Presse' })).toBeVisible();
		expect(screen.queryByRole('option', { name: 'Jugend' })).toBeNull();
	});

	it('chooses an option and reports the new id list', async () => {
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		renderPicker({ onChange });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Pre');
		await userEvent.click(screen.getByRole('option', { name: 'Presse' }));
		expect(onChange).toHaveBeenLastCalledWith(['t2']);
	});

	it('removes a chip through its named remove button and through Backspace', async () => {
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		renderPicker({ onChange, value: ['t1', 't2'] });
		await userEvent.click(screen.getByRole('button', { name: 'Remove tag Jugend' }));
		expect(onChange).toHaveBeenLastCalledWith(['t2']);
		await userEvent.click(screen.getByRole('combobox', { name: 'Tags' }));
		await userEvent.keyboard('{Backspace}');
		expect(onChange).toHaveBeenLastCalledWith(['t1']);
	});

	it('offers nothing more at 10 chips and says why', async () => {
		const many = numberedTags(11);
		renderPicker({ options: many, value: many.slice(0, 10).map((tag: TagOption) => tag.id) });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'X1');
		expect(screen.queryByRole('option', { name: 'X10' })).toBeNull();
		expect(screen.getByText('At most 10 tags per link')).toBeVisible();
	});

	it('ties the cap hint to the input', () => {
		const many = numberedTags(10);
		renderPicker({ options: many, value: many.map((tag: TagOption) => tag.id) });
		expect(screen.getByRole('combobox', { name: 'Tags' })).toHaveAccessibleDescription(
			'At most 10 tags per link',
		);
	});

	it('offers options again once a chip is removed at the cap', async () => {
		const many = numberedTags(11);
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<StatefulPicker initial={many.slice(0, 10).map((tag: TagOption) => tag.id)} tags={many} />
			</I18nextProvider>,
		);
		await userEvent.click(screen.getByRole('button', { name: 'Remove tag X0' }));
		expect(screen.queryByText('At most 10 tags per link')).toBeNull();
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'X1');
		expect(screen.getByRole('option', { name: 'X10' })).toBeVisible();
	});

	it('offers creation only to editors and only without an exact match, in any case', async () => {
		renderPicker({ canCreate: true });
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'presse');
		expect(screen.queryByRole('option', { name: 'Create tag "presse"' })).toBeNull();
		expect(screen.getByRole('option', { name: 'Presse' })).toBeVisible();
		await userEvent.clear(input);
		await userEvent.type(input, 'Vorstand');
		expect(screen.getByRole('option', { name: 'Create tag "Vorstand"' })).toBeVisible();
	});

	it('never offers creation to a viewer', async () => {
		renderPicker({ canCreate: false });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand');
		expect(screen.queryByRole('option', { name: /create tag/iu })).toBeNull();
	});

	it('creates a tag, adds it as a chip and keeps focus in the input', async () => {
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		const onCreate = vi.fn<(name: string) => Promise<TagCreateResult>>(
			// oxlint-disable-next-line typescript/require-await -- stands in for a create call `TagPicker` awaits; the fake has nothing to await itself.
			async () => ({ tag: { id: 't9', name: 'Vorstand' } }),
		);
		renderPicker({ canCreate: true, onChange, onCreate });
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		expect(onCreate).toHaveBeenCalledWith('Vorstand');
		await waitFor(() => {
			expect(onChange).toHaveBeenLastCalledWith(['t9']);
		});
		expect(input).toHaveFocus();
	});

	it('creates the typed name with Enter alone', async () => {
		const onCreate = vi.fn<(name: string) => Promise<TagCreateResult>>(
			// oxlint-disable-next-line typescript/require-await -- stands in for a create call `TagPicker` awaits; the fake has nothing to await itself.
			async () => ({ tag: { id: 't9', name: 'Vorstand' } }),
		);
		renderPicker({ canCreate: true, onCreate });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand{Enter}');
		expect(onCreate).toHaveBeenCalledWith('Vorstand');
	});

	it('shows a create failure on the field', async () => {
		renderPicker({
			canCreate: true,
			onCreate: vi.fn<(name: string) => Promise<TagCreateResult>>(
				// oxlint-disable-next-line typescript/require-await -- stands in for a create call `TagPicker` awaits; the fake has nothing to await itself.
				async () => ({ error: 'A team can have at most 200 tags.' }),
			),
		});
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		await expect(screen.findByText('A team can have at most 200 tags.')).resolves.toBeVisible();
	});

	it('marks the input invalid and describes it with a server error', () => {
		renderPicker({ error: 'A chosen tag no longer exists. Remove it and save again.' });
		const input = screen.getByRole('combobox', { name: 'Tags' });
		expect(input).toHaveAttribute('aria-invalid', 'true');
		expect(input).toHaveAccessibleDescription(
			'A chosen tag no longer exists. Remove it and save again.',
		);
	});

	it('keeps every chip when Escape is pressed with the list closed', async () => {
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		renderPicker({ onChange, value: ['t1', 't2'] });
		await userEvent.click(screen.getByRole('combobox', { name: 'Tags' }));
		await userEvent.keyboard('{Escape}{Escape}');
		expect(onChange).not.toHaveBeenCalled();
	});

	it('labels a chip from knownNames and marks a deleted one', () => {
		renderPicker({
			deletedIds: new Set(['gone']),
			knownNames: new Map([['gone', 'Alt']]),
			options: [],
			value: ['gone'],
		});
		expect(screen.getByText('Alt')).toBeVisible();
		expect(screen.getByText('(deleted)')).toBeVisible();
	});
});
