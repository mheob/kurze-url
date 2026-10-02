import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, type Mock, vi } from 'vitest';

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

/** A create call the test settles later, while the picker shows it as pending. */
interface PendingCreate {
	readonly promise: Promise<TagCreateResult>;
	readonly resolve: (result: TagCreateResult) => void;
}

/**
 * A create call that stays in flight until the test resolves it. Not
 * `Promise.withResolvers`: `apps/web`'s `lib` stops at ES2022.
 *
 * @returns The pending promise and the function that settles it.
 */
function pendingCreate(): PendingCreate {
	let settle: ((result: TagCreateResult) => void) | undefined;
	// oxlint-disable-next-line promise/avoid-new -- a deliberately deferred promise: the test resolves it later, in response to what the UI does while the request is still in flight, the same reason `login.test.tsx` gives.
	const promise = new Promise<TagCreateResult>((resolve) => {
		settle = resolve;
	});
	return {
		promise,
		resolve: (result: TagCreateResult) => {
			settle?.(result);
		},
	};
}

/**
 * The picker with a viewer's defaults. It draws its own visible label, so
 * nothing here adds a `<label>`: the input then has exactly one accessible
 * name.
 *
 * @param overrides - The props this test cares about.
 * @returns The picker element.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `TagPickerProps` carries a `ReadonlySet` and a `ReadonlyMap`, TypeScript's immutable collection types; the rule does not recognise them as readonly, the same limitation `audit-actor.ts` documents.
function picker(overrides: Partial<TagPickerProps>): React.JSX.Element {
	return (
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
	);
}

/**
 * Renders the picker with a viewer's defaults.
 *
 * @param overrides - The props this test cares about.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same `ReadonlySet`/`ReadonlyMap` limitation as `picker` above.
function renderPicker(overrides: Partial<TagPickerProps> = {}): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n('en')}>{picker(overrides)}</I18nextProvider>);
}

/**
 * Renders the picker inside a form with a submit button, the way the link
 * form will, so a test can see whether Enter submits it.
 *
 * @param overrides - The props this test cares about.
 * @returns The form's submit handler, which prevents the real submission.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same `ReadonlySet`/`ReadonlyMap` limitation as `picker` above.
function renderInForm(overrides: Partial<TagPickerProps> = {}): Mock<() => void> {
	const onSubmit = vi.fn<() => void>();
	render(
		<I18nextProvider i18n={createI18n('en')}>
			<form
				onSubmit={(event: Readonly<{ preventDefault: () => void }>) => {
					event.preventDefault();
					onSubmit();
				}}
			>
				{picker(overrides)}
				<button aria-label="Save" type="submit" />
			</form>
		</I18nextProvider>,
	);
	return onSubmit;
}

/**
 * A caller that owns the chosen ids, so removing or adding a chip actually
 * changes what the picker is given, the way the link form will.
 *
 * @param props - The harness's props.
 * @param props.canCreate - Whether the picker offers creation.
 * @param props.initial - The ids chosen at first.
 * @param props.onChange - Observes every new id list, after the harness has stored it.
 * @param props.onCreate - The create call.
 * @param props.tags - The team's tags; they never gain a created tag, as before a refetch.
 * @returns The picker, wired to its own state.
 */
function StatefulPicker({
	canCreate = false,
	initial,
	onChange,
	onCreate = vi.fn<(name: string) => Promise<TagCreateResult>>(),
	tags,
}: Readonly<{
	canCreate?: boolean;
	initial: readonly string[];
	onChange?: (ids: readonly string[]) => void;
	onCreate?: (name: string) => Promise<TagCreateResult>;
	tags: readonly TagOption[];
}>): React.JSX.Element {
	const [value, setValue] = useState<readonly string[]>(initial);
	return (
		<TagPicker
			canCreate={canCreate}
			deletedIds={new Set()}
			inputId="tags-input"
			knownNames={new Map()}
			label="Tags"
			onChange={(ids: readonly string[]) => {
				setValue(ids);
				onChange?.(ids);
			}}
			onCreate={onCreate}
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
		const onSubmit = renderInForm({ canCreate: true, onCreate });
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand{Enter}');
		expect(onCreate).toHaveBeenCalledWith('Vorstand');
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('names a created chip from the create result before the tags refetch', async () => {
		const onCreate = vi.fn<(name: string) => Promise<TagCreateResult>>(
			// oxlint-disable-next-line typescript/require-await -- stands in for a create call `TagPicker` awaits; the fake has nothing to await itself.
			async () => ({ tag: { id: 't9', name: 'Vorstand' } }),
		);
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<StatefulPicker canCreate initial={[]} onCreate={onCreate} tags={options} />
			</I18nextProvider>,
		);
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		await expect(
			screen.findByRole('button', { name: 'Remove tag Vorstand' }),
		).resolves.toBeInTheDocument();
		expect(screen.getByText('Vorstand')).toBeVisible();
		expect(screen.queryByText('t9')).toBeNull();
	});

	it('keeps Enter from submitting the form and offers no second create while one is pending', async () => {
		const pending = pendingCreate();
		const onCreate = vi
			.fn<(name: string) => Promise<TagCreateResult>>()
			.mockReturnValue(pending.promise);
		const onSubmit = renderInForm({ canCreate: true, onCreate });
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'Vorstand{Enter}');
		expect(input).toHaveAttribute('aria-busy', 'true');
		await userEvent.keyboard('{Enter}');
		expect(onSubmit).not.toHaveBeenCalled();
		await userEvent.type(input, 'Vorstand');
		expect(screen.queryByRole('option', { name: 'Create tag "Vorstand"' })).toBeNull();
		await act(async () => {
			pending.resolve({ tag: { id: 't9', name: 'Vorstand' } });
			await pending.promise;
		});
		expect(input).not.toHaveAttribute('aria-busy');
	});

	it('shows a generic failure and keeps focus when the create call rejects', async () => {
		renderPicker({
			canCreate: true,
			onCreate: vi
				.fn<(name: string) => Promise<TagCreateResult>>()
				.mockRejectedValue(new Error('network')),
		});
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		await expect(
			screen.findByText('Something went wrong. Please try again.'),
		).resolves.toBeVisible();
		expect(input).toHaveFocus();
		expect(input).not.toHaveAttribute('aria-busy');
	});

	it('does not add a created tag once the cap was reached while it was pending', async () => {
		const many = numberedTags(11);
		const pending = pendingCreate();
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		const onCreate = vi
			.fn<(name: string) => Promise<TagCreateResult>>()
			.mockReturnValue(pending.promise);
		const nine = many.slice(0, 9).map((tag: TagOption) => tag.id);
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<StatefulPicker
					canCreate
					initial={nine}
					onChange={onChange}
					onCreate={onCreate}
					tags={many}
				/>
			</I18nextProvider>,
		);
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'Vorstand{Enter}');
		expect(onCreate).toHaveBeenCalledWith('Vorstand');
		await userEvent.type(input, 'X9');
		await userEvent.click(screen.getByRole('option', { name: 'X9' }));
		expect(onChange).toHaveBeenLastCalledWith([...nine, 'x9']);
		await act(async () => {
			pending.resolve({ tag: { id: 't9', name: 'Vorstand' } });
			await pending.promise;
		});
		expect(onChange).toHaveBeenLastCalledWith([...nine, 'x9']);
		expect(onChange).not.toHaveBeenCalledWith(expect.arrayContaining(['t9']));
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

	it('keeps the name of a chosen tag once a refetch no longer has it', async () => {
		const i18n = createI18n('en');
		const onChange = vi.fn<(ids: readonly string[]) => void>();
		const { rerender } = render(
			<I18nextProvider i18n={i18n}>{picker({ onChange })}</I18nextProvider>,
		);
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Pre');
		await userEvent.click(screen.getByRole('option', { name: 'Presse' }));
		expect(onChange).toHaveBeenLastCalledWith(['t2']);
		rerender(
			<I18nextProvider i18n={i18n}>
				{picker({
					deletedIds: new Set(['t2']),
					onChange,
					options: options.filter((option: TagOption) => option.id !== 't2'),
					value: ['t2'],
				})}
			</I18nextProvider>,
		);
		expect(screen.getByText('Presse')).toBeVisible();
		expect(screen.getByText('(deleted)')).toBeVisible();
		expect(screen.getByRole('button', { name: 'Remove tag Presse' })).toBeVisible();
	});

	it('leaves focus on another field the user moved to while a create was pending', async () => {
		const pending = pendingCreate();
		render(
			<I18nextProvider i18n={createI18n('en')}>
				{picker({
					canCreate: true,
					onCreate: vi
						.fn<(name: string) => Promise<TagCreateResult>>()
						.mockReturnValue(pending.promise),
				})}
				<input aria-label="Destination" />
			</I18nextProvider>,
		);
		await userEvent.type(screen.getByRole('combobox', { name: 'Tags' }), 'Vorstand{Enter}');
		const other = screen.getByRole('textbox', { name: 'Destination' });
		await userEvent.click(other);
		await act(async () => {
			pending.resolve({ tag: { id: 't9', name: 'Vorstand' } });
			await pending.promise;
		});
		expect(other).toHaveFocus();
	});

	it('keeps the typed name in the input when a create fails', async () => {
		renderPicker({
			canCreate: true,
			onCreate: vi.fn<(name: string) => Promise<TagCreateResult>>(
				// oxlint-disable-next-line typescript/require-await -- stands in for a create call `TagPicker` awaits; the fake has nothing to await itself.
				async () => ({ error: 'A team can have at most 200 tags.' }),
			),
		});
		const input = screen.getByRole('combobox', { name: 'Tags' });
		await userEvent.type(input, 'Vorstand');
		await userEvent.click(screen.getByRole('option', { name: 'Create tag "Vorstand"' }));
		await expect(screen.findByText('A team can have at most 200 tags.')).resolves.toBeVisible();
		expect(input).toHaveValue('Vorstand');
	});
});
