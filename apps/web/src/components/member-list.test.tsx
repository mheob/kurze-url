import type { Member } from '@kurze-url/api-client';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { TeamRole } from '../lib/team-roles';
import { MemberList } from './member-list';

/**
 * @param overrides - Partial fields to override on the default member fixture.
 * @returns The member fixture.
 */
function member(overrides: Partial<Member> = {}): Member {
	return {
		created_at: '2026-09-01T08:00:00Z',
		email: 'a@verein.test',
		role: 'editor',
		user_id: 'u1',
		...overrides,
	};
}

// oxlint-disable-next-line eslint/no-empty-function -- a deliberate do-nothing callback for whichever prop a given test isn't exercising, the same role `vi.fn()` plays for the ones it asserts on.
function noop(): void {}

/**
 * Narrows `Element.closest()`'s result for TypeScript after
 * `expect(...).not.toBeNull()` has already asserted it at runtime — that
 * matcher has no type-guard signature, and a bare `as HTMLElement` on a
 * `closest()` result trips `no-unsafe-type-assertion`.
 *
 * @param element - The element to narrow.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `HTMLElement` is a DOM lib type, not one this codebase declares.
function assertElement(element: HTMLElement | null): asserts element is HTMLElement {
	if (element === null) throw new Error('Expected element to exist.');
}

/**
 * Same pattern as `domain-list.test.tsx`: `MemberList` calls `useTranslation`,
 * so it needs an `I18nextProvider` in its tree or `t(...)` throws looking up
 * `react-i18next`'s default context. English is enough — every assertion
 * below quotes the English catalogue directly.
 *
 * @param ui - The element to render, wrapped in the provider.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type, not one this codebase declares.
function renderWithI18n(ui: React.ReactElement): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n('en')}>{ui}</I18nextProvider>);
}

describe(MemberList, () => {
	it('shows every member with a translated role', () => {
		renderWithI18n(
			<MemberList
				actorRole="viewer"
				currentUserId="u9"
				failure={null}
				members={[member({ role: 'owner', user_id: 'u1' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('Owner')).toBeInTheDocument();
	});

	it('gives a viewer no controls at all', () => {
		renderWithI18n(
			<MemberList
				actorRole="viewer"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
	});

	it('marks the signed-in person', () => {
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u1"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('You')).toBeInTheDocument();
	});

	it('falls back to a sentence when the account has no address', () => {
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member({ email: '' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.getByText('No address on file')).toBeInTheDocument();
	});

	it('reports a role change with the row it belongs to', async () => {
		const onRoleChange = vi.fn<(userId: string, role: TeamRole) => void>();
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={noop}
				onRoleChange={onRoleChange}
				pendingUserId={null}
			/>,
		);

		await userEvent.selectOptions(screen.getByLabelText('Role for a@verein.test'), 'admin');

		expect(onRoleChange).toHaveBeenCalledWith('u1', 'admin');
	});

	it('does not offer an admin any control over an owner', () => {
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member({ role: 'owner' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);
		expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
	});

	// The server holds the lock; this only avoids offering a control that is
	// certain to be refused.
	it('locks the only owner even for another owner', () => {
		renderWithI18n(
			<MemberList
				actorRole="owner"
				currentUserId="u9"
				failure={null}
				members={[
					member({ role: 'owner', user_id: 'u1' }),
					member({ email: 'b@verein.test', user_id: 'u2' }),
				]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);

		const ownerRow = screen.getByText('a@verein.test').closest('tr');
		expect(ownerRow).not.toBeNull();
		assertElement(ownerRow);
		expect(within(ownerRow).getByLabelText('Role for a@verein.test')).toBeDisabled();
		expect(
			within(ownerRow).getByText('A team must always have at least one owner.'),
		).toBeInTheDocument();
	});

	it('only asks to remove after the dialog is confirmed', async () => {
		const onRemove = vi.fn<(userId: string) => void>();
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure={null}
				members={[member()]}
				onRemove={onRemove}
				onRoleChange={noop}
				pendingUserId={null}
			/>,
		);

		await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
		expect(onRemove).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole('button', { name: 'Yes, remove them' }));
		expect(onRemove).toHaveBeenCalledWith('u1');
	});

	it('shows a failure against the row it happened on', () => {
		renderWithI18n(
			<MemberList
				actorRole="admin"
				currentUserId="u9"
				failure="raced"
				members={[member(), member({ email: 'b@verein.test', user_id: 'u2' })]}
				onRemove={noop}
				onRoleChange={noop}
				pendingUserId="u2"
			/>,
		);

		const row = screen.getByText('b@verein.test').closest('tr');
		expect(row).not.toBeNull();
		assertElement(row);
		expect(within(row).getByRole('alert')).toHaveTextContent('list changed');
	});
});
