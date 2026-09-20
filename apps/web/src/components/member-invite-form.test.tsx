import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { TeamRole } from '../lib/team-roles';
import { MemberInviteForm, type InviteFailureKind } from './member-invite-form';

const roles = ['viewer', 'editor', 'admin'] as const satisfies readonly TeamRole[];

interface InviteValues {
	readonly email: string;
	readonly role: TeamRole;
}

/**
 * Same pattern as `link-form.test.tsx`: `MemberInviteForm` calls
 * `useTranslation`, so it needs an `I18nextProvider` in its tree or `t(...)`
 * throws looking up `react-i18next`'s default context. English is enough —
 * every assertion below quotes the English catalogue directly.
 *
 * @param props - The props to render `MemberInviteForm` with.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderForm(props: {
	readonly failure: InviteFailureKind | null;
	readonly onSubmit: (values: InviteValues) => void;
	readonly pending: boolean;
	readonly roles: readonly TeamRole[];
	readonly result: { readonly email: string; readonly invited: boolean } | null;
}): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<MemberInviteForm {...props} />
		</I18nextProvider>,
	);
}

describe(MemberInviteForm, () => {
	it('renders nothing for a member who may not invite', () => {
		const { container } = renderForm({
			failure: null,
			onSubmit: vi.fn<(values: InviteValues) => void>(),
			pending: false,
			result: null,
			roles: [],
		});
		expect(container).toBeEmptyDOMElement();
	});

	it('submits the address and the chosen role', async () => {
		const onSubmit = vi.fn<(values: InviteValues) => void>();
		renderForm({ failure: null, onSubmit, pending: false, result: null, roles });

		await userEvent.type(screen.getByLabelText('Email address'), 'neu@verein.test');
		await userEvent.selectOptions(screen.getByLabelText('Role'), 'editor');
		await userEvent.click(screen.getByRole('button', { name: 'Add to team' }));

		expect(onSubmit).toHaveBeenCalledWith({ email: 'neu@verein.test', role: 'editor' });
	});

	it('refuses an empty address without calling the server', async () => {
		const onSubmit = vi.fn<(values: InviteValues) => void>();
		renderForm({ failure: null, onSubmit, pending: false, result: null, roles });

		await userEvent.click(screen.getByRole('button', { name: 'Add to team' }));

		await expect(screen.findByText('An email address is required.')).resolves.toBeInTheDocument();
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('defaults to the least privilege', () => {
		renderForm({
			failure: null,
			onSubmit: vi.fn<(values: InviteValues) => void>(),
			pending: false,
			result: null,
			roles,
		});
		expect(screen.getByLabelText('Role')).toHaveValue('viewer');
	});

	// The whole reason Task 1 added `invited` to the API: an address that
	// already had an account is added with no notification at all, and saying
	// "invitation sent" there would be a false statement.
	it('says plainly when nobody was notified', () => {
		renderForm({
			failure: null,
			onSubmit: vi.fn<(values: InviteValues) => void>(),
			pending: false,
			result: { email: 'neu@verein.test', invited: false },
			roles,
		});
		expect(screen.getByRole('status')).toHaveTextContent('are not notified');
	});

	it('says an invitation went out when one did', () => {
		renderForm({
			failure: null,
			onSubmit: vi.fn<(values: InviteValues) => void>(),
			pending: false,
			result: { email: 'neu@verein.test', invited: true },
			roles,
		});
		expect(screen.getByRole('status')).toHaveTextContent('Invitation sent');
	});

	// Without this, the address stays in the field under the success banner
	// and the button re-enables, so an accidental second click spends another
	// unit of the instance's own invitation budget on a guaranteed 409 — see
	// `RATE_LIMIT_INVITE_GLOBAL_PER_MONTH` in `apps/api/.env.example`.
	it('resets the address and role after a successful add', async () => {
		const onSubmit = vi.fn<(values: InviteValues) => void>();
		const { rerender } = renderForm({
			failure: null,
			onSubmit,
			pending: false,
			result: null,
			roles,
		});

		await userEvent.type(screen.getByLabelText('Email address'), 'neu@verein.test');
		await userEvent.selectOptions(screen.getByLabelText('Role'), 'editor');

		// Simulates the route's own `onSuccess`: `result` flips from `null` to
		// the successful add, the same transition `clearStatusSlots` and
		// `inviteMutation.onSuccess` produce together in the real page.
		rerender(
			<I18nextProvider i18n={createI18n('en')}>
				<MemberInviteForm
					failure={null}
					onSubmit={onSubmit}
					pending={false}
					roles={roles}
					result={{ email: 'neu@verein.test', invited: true }}
				/>
			</I18nextProvider>,
		);

		expect(screen.getByLabelText('Email address')).toHaveValue('');
		expect(screen.getByLabelText('Role')).toHaveValue('viewer');
	});

	it('does not reset the address after a refused add', async () => {
		const onSubmit = vi.fn<(values: InviteValues) => void>();
		const { rerender } = renderForm({
			failure: null,
			onSubmit,
			pending: false,
			result: null,
			roles,
		});

		await userEvent.type(screen.getByLabelText('Email address'), 'neu@verein.test');

		// Simulates the route's own `onError`: `result` stays `null`, only
		// `failure` changes — the address must survive so the caller does not
		// have to retype it while fixing whatever the failure was about.
		rerender(
			<I18nextProvider i18n={createI18n('en')}>
				<MemberInviteForm
					failure="alreadyMember"
					onSubmit={onSubmit}
					pending={false}
					roles={roles}
					result={null}
				/>
			</I18nextProvider>,
		);

		expect(screen.getByLabelText('Email address')).toHaveValue('neu@verein.test');
	});

	it('tells the two rate limits apart', () => {
		const { rerender } = renderForm({
			failure: 'teamBurst',
			onSubmit: vi.fn<(values: InviteValues) => void>(),
			pending: false,
			result: null,
			roles,
		});
		expect(screen.getByRole('alert')).toHaveTextContent('wait an hour');

		rerender(
			<I18nextProvider i18n={createI18n('en')}>
				<MemberInviteForm
					failure="instanceBudget"
					onSubmit={vi.fn<(values: InviteValues) => void>()}
					pending={false}
					roles={roles}
					result={null}
				/>
			</I18nextProvider>,
		);
		expect(screen.getByRole('alert')).toHaveTextContent('Ask the maintainer');
	});
});
