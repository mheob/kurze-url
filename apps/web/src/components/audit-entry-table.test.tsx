import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { AuditEntryTable } from './audit-entry-table.tsx';

/**
 * Same pattern as `stat-breakdown-card.test.tsx`: a component calling
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

const MEMBERS = new Map([['user-a', 'anna@example.org']]);

const ENTRY = {
	action: 'link.updated',
	actor_user_id: 'user-a',
	created_at: '2026-03-14T09:30:00.000Z',
	entity_id: 'link-1',
	entity_type: 'link',
	id: 7,
	metadata: { changed: ['slug'], slug: 'sommerfest' },
};

describe(AuditEntryTable, () => {
	it('names the action, the actor and the entity', () => {
		renderWithI18n(<AuditEntryTable entries={[ENTRY]} language="en" membersById={MEMBERS} />);

		expect(screen.getByRole('cell', { name: 'Link changed' })).toBeInTheDocument();
		expect(screen.getByRole('cell', { name: 'anna@example.org' })).toBeInTheDocument();
		expect(screen.getByRole('cell', { name: 'Link' })).toBeInTheDocument();
	});

	it('keeps the metadata hidden until it is asked for', async () => {
		renderWithI18n(<AuditEntryTable entries={[ENTRY]} language="en" membersById={MEMBERS} />);

		expect(screen.queryByText('sommerfest')).not.toBeInTheDocument();

		const toggle = screen.getByRole('button', { name: /details/iu });
		expect(toggle).toHaveAttribute('aria-expanded', 'false');
		await userEvent.click(toggle);

		expect(toggle).toHaveAttribute('aria-expanded', 'true');
		expect(screen.getByText('sommerfest')).toBeInTheDocument();
		// Keys render raw: they are protocol vocabulary, the way
		// `stat-breakdown-card` prints a dimension's values.
		expect(screen.getByText('slug')).toBeInTheDocument();
	});

	it('renders an action it has no label for without breaking the row', () => {
		// The taxonomy is closed and validated server-side, so this should not
		// happen — but a build of this page older than a new action would meet
		// it, and a blank cell would be worse than a plain admission.
		renderWithI18n(
			<AuditEntryTable
				entries={[{ ...ENTRY, action: 'gadget.polished' }]}
				language="en"
				membersById={MEMBERS}
			/>,
		);

		expect(screen.getByRole('cell', { name: 'Unrecognised change' })).toBeInTheDocument();
	});

	it('distinguishes a former member from a deleted account', () => {
		renderWithI18n(
			<AuditEntryTable
				entries={[
					{ ...ENTRY, actor_user_id: 'user-gone', id: 8 },
					{ ...ENTRY, actor_user_id: undefined, id: 9 },
				]}
				language="en"
				membersById={MEMBERS}
			/>,
		);

		expect(screen.getByRole('cell', { name: 'A former member' })).toBeInTheDocument();
		expect(screen.getByRole('cell', { name: 'A deleted account' })).toBeInTheDocument();
	});

	// Fix-round addition: a real `link.updated` entry nests a `{from, to}`
	// object per changed field (`apps/api/internal/api/links.go`), not the
	// flat string the brief's own fixture above uses. `String(value)` alone
	// would print the useless `[object Object]` for this — the single most
	// common action in the log.
	it('renders a nested object value instead of [object Object]', async () => {
		renderWithI18n(
			<AuditEntryTable
				entries={[
					{
						...ENTRY,
						metadata: { slug: { from: 'sommerfest', to: 'sommerfest-2026' } },
					},
				]}
				language="en"
				membersById={MEMBERS}
			/>,
		);

		await userEvent.click(screen.getByRole('button', { name: /details/iu }));

		expect(screen.getByText('{from: sommerfest, to: sommerfest-2026}')).toBeInTheDocument();
		expect(screen.queryByText(/object Object/iu)).not.toBeInTheDocument();
	});

	// Fix-round addition: `link.password_set`/`_changed`/`_removed` are
	// documented as carrying empty metadata on all three
	// (`apps/api/internal/audit/audit.go`) — a disclosure control that opens
	// onto nothing is worse than no control at all.
	it('offers no details toggle when the entry has no metadata', () => {
		renderWithI18n(
			<AuditEntryTable
				entries={[{ ...ENTRY, metadata: {} }]}
				language="en"
				membersById={MEMBERS}
			/>,
		);

		expect(screen.queryByRole('button', { name: /details/iu })).not.toBeInTheDocument();
	});
});
