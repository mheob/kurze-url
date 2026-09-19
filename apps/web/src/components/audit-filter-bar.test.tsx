import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { AuditFilters } from '../lib/audit-filters';
import type { Language } from '../lib/preferences';
import { AuditFilterBar } from './audit-filter-bar.tsx';

/**
 * Same pattern as `audit-entry-table.test.tsx`: a component calling
 * `useTranslation` needs an `I18nextProvider` or `t(...)` throws.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

const MEMBERS = [
	{ email: 'anna@example.org', user_id: 'user-a' },
	{ email: 'bernd@example.org', user_id: 'user-b' },
];

describe(AuditFilterBar, () => {
	it('offers every entity type plus an unfiltered choice', () => {
		renderWithI18n(
			<AuditFilterBar
				filters={{ page: 1 }}
				members={MEMBERS}
				onChange={vi.fn<(filters: AuditFilters) => void>()}
			/>,
		);

		const select = screen.getByLabelText('Entity');
		// Six types plus "Anything". A missing option is a filter the reader
		// cannot reach even though the endpoint supports it.
		expect(within(select).getAllByRole('option')).toHaveLength(7);
		expect(within(select).getByRole('option', { name: 'Keyword' })).toBeInTheDocument();
	});

	it('reports a chosen entity type and sends the reader back to page one', async () => {
		const onChange = vi.fn<(filters: AuditFilters) => void>();
		renderWithI18n(<AuditFilterBar filters={{ page: 3 }} members={MEMBERS} onChange={onChange} />);

		await userEvent.selectOptions(screen.getByLabelText('Entity'), 'link');

		// Page 3 of an unfiltered log has nothing to do with page 3 of a
		// filtered one. Keeping the number shows an empty page and no reason
		// for it, which reads as "there is nothing" rather than "you are past
		// the end".
		expect(onChange).toHaveBeenCalledWith({ entityType: 'link', page: 1 });
	});

	it("lists the team's members as actors, by address", async () => {
		const onChange = vi.fn<(filters: AuditFilters) => void>();
		renderWithI18n(<AuditFilterBar filters={{ page: 1 }} members={MEMBERS} onChange={onChange} />);

		const select = screen.getByLabelText('Person');
		expect(within(select).getByRole('option', { name: 'bernd@example.org' })).toBeInTheDocument();

		await userEvent.selectOptions(select, 'user-b');

		// The value is the id the API filters on; the label is the address a
		// reader recognises.
		expect(onChange).toHaveBeenCalledWith({ actor: 'user-b', page: 1 });
	});

	it('reports a chosen day without disturbing the other bound', async () => {
		const onChange = vi.fn<(filters: AuditFilters) => void>();
		renderWithI18n(
			<AuditFilterBar
				filters={{ page: 1, to: '2026-03-31' }}
				members={MEMBERS}
				onChange={onChange}
			/>,
		);

		await userEvent.type(screen.getByLabelText('From'), '2026-03-01');

		expect(onChange).toHaveBeenLastCalledWith(
			expect.objectContaining({ from: '2026-03-01', to: '2026-03-31' }),
		);
	});

	it('removes a filter rather than sending it empty', async () => {
		const onChange = vi.fn<(filters: AuditFilters) => void>();
		renderWithI18n(
			<AuditFilterBar
				filters={{ entityType: 'link', page: 1 }}
				members={MEMBERS}
				onChange={onChange}
			/>,
		);

		await userEvent.selectOptions(screen.getByLabelText('Entity'), '');

		// An empty string would reach the endpoint as a filter matching
		// nothing, rather than as no filter at all.
		expect(onChange).toHaveBeenCalledWith({ page: 1 });
	});

	it('offers to clear the filters only when some are set', () => {
		const { rerender } = renderWithI18n(
			<AuditFilterBar
				filters={{ page: 1 }}
				members={MEMBERS}
				onChange={vi.fn<(filters: AuditFilters) => void>()}
			/>,
		);
		expect(screen.queryByRole('button', { name: 'Clear the filters' })).not.toBeInTheDocument();

		rerender(
			<I18nextProvider i18n={createI18n('en')}>
				<AuditFilterBar
					filters={{ entityType: 'link', page: 1 }}
					members={MEMBERS}
					onChange={vi.fn<(filters: AuditFilters) => void>()}
				/>
			</I18nextProvider>,
		);
		expect(screen.getByRole('button', { name: 'Clear the filters' })).toBeInTheDocument();
	});
});
