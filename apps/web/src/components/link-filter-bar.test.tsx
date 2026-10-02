/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file is a DOM or React type a test helper receives (`HTMLElement`, `React.ReactElement`, the
   `option` elements `map` hands its callback), none of which this side of the testing-library
   boundary can mark readonly. */

import type { Folder, Tag } from '@kurze-url/api-client';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkFilterBar } from './link-filter-bar';

const folders: readonly Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'f1', name: 'Sommerfest', team_id: 'team-a' },
];

const tags: readonly Tag[] = [
	{ id: 't1', name: 'Jugend', team_id: 'team-a' },
	{ id: 't2', name: 'Presse', team_id: 'team-a' },
];

/** What `LinkFilterBar` reports: a folder and a tag, each absent when its select is on "all". */
type FilterChange = (next: Readonly<{ folder?: string; tag?: string }>) => void;

/**
 * `LinkFilterBar` calls `useTranslation`, which needs an `I18nextProvider`.
 *
 * @param ui - The element under test.
 * @returns Testing Library's render result.
 */
function renderWithI18n(ui: React.ReactElement): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n('en')}>{ui}</I18nextProvider>);
}

/**
 * The visible text of a select's options, in order.
 *
 * @param select - The `<select>` to read.
 * @returns Each option's text.
 */
function optionTexts(select: HTMLElement): (string | null)[] {
	return within(select)
		.getAllByRole('option')
		.map((option) => option.textContent);
}

describe(LinkFilterBar, () => {
	it('offers All folders, No folder, then the folders, and reports a change', async () => {
		const onChange = vi.fn<FilterChange>();
		renderWithI18n(
			<LinkFilterBar
				folder={undefined}
				folders={folders}
				onChange={onChange}
				tag={undefined}
				tags={tags}
			/>,
		);
		const select = screen.getByRole('combobox', { name: 'Folder' });
		expect(optionTexts(select)).toStrictEqual(['All folders', 'No folder', 'Sommerfest']);
		await userEvent.selectOptions(select, 'none');
		expect(onChange).toHaveBeenCalledWith({ folder: 'none', tag: undefined });
		await userEvent.selectOptions(select, '');
		expect(onChange).toHaveBeenLastCalledWith({ folder: undefined, tag: undefined });
	});

	it('offers All tags then the tags, and a change keeps the folder', async () => {
		const onChange = vi.fn<FilterChange>();
		renderWithI18n(
			<LinkFilterBar
				folder="f1"
				folders={folders}
				onChange={onChange}
				tag={undefined}
				tags={tags}
			/>,
		);
		const select = screen.getByRole('combobox', { name: 'Tag' });
		expect(optionTexts(select)).toStrictEqual(['All tags', 'Jugend', 'Presse']);
		await userEvent.selectOptions(select, 't2');
		expect(onChange).toHaveBeenCalledWith({ folder: 'f1', tag: 't2' });
	});

	it('a folder change keeps the tag', async () => {
		const onChange = vi.fn<FilterChange>();
		renderWithI18n(
			<LinkFilterBar
				folder={undefined}
				folders={folders}
				onChange={onChange}
				tag="t2"
				tags={tags}
			/>,
		);
		await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Folder' }), 'none');
		expect(onChange).toHaveBeenCalledWith({ folder: 'none', tag: 't2' });
	});

	it('choosing All tags clears the tag and keeps the folder', async () => {
		const onChange = vi.fn<FilterChange>();
		renderWithI18n(
			<LinkFilterBar folder="f1" folders={folders} onChange={onChange} tag="t2" tags={tags} />,
		);
		await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Tag' }), '');
		expect(onChange).toHaveBeenCalledWith({ folder: 'f1', tag: undefined });
	});

	it('shows the active folder and tag as the selected options', () => {
		renderWithI18n(
			<LinkFilterBar
				folder="f1"
				folders={folders}
				onChange={vi.fn<FilterChange>()}
				tag="t2"
				tags={tags}
			/>,
		);
		expect(screen.getByRole('combobox', { name: 'Folder' })).toHaveValue('f1');
		expect(screen.getByRole('combobox', { name: 'Tag' })).toHaveValue('t2');
	});

	/**
	 * `undefined` is how the route reports "the query has not resolved (or
	 * failed)", as opposed to `[]`, "it resolved and the team has none" — both
	 * render the same options, since neither "All folders"/"No folder" nor
	 * "All tags" depends on the team's actual lists.
	 */
	it('offers only the fixed options while the folders and tags have not loaded', () => {
		renderWithI18n(
			<LinkFilterBar
				folder={undefined}
				folders={undefined}
				onChange={vi.fn<FilterChange>()}
				tag={undefined}
				tags={undefined}
			/>,
		);
		expect(optionTexts(screen.getByRole('combobox', { name: 'Folder' }))).toStrictEqual([
			'All folders',
			'No folder',
		]);
		expect(optionTexts(screen.getByRole('combobox', { name: 'Tag' }))).toStrictEqual(['All tags']);
	});
});
