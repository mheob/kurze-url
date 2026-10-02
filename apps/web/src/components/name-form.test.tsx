import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { NameForm } from './name-form';

describe(NameForm, () => {
	it('refuses a blank name on the client', async () => {
		const onSubmit = vi.fn<(name: string) => void>();
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<NameForm label="Folder name" onSubmit={onSubmit} submitLabel="Create folder" />
			</I18nextProvider>,
		);
		await userEvent.type(screen.getByRole('textbox', { name: 'Folder name' }), '   {Enter}');
		expect(onSubmit).not.toHaveBeenCalled();
		expect(screen.getByText('Enter a name of 1 to 60 characters.')).toBeVisible();
	});

	it('offers the shared Cancel button only when it can be cancelled', async () => {
		const onCancel = vi.fn<() => void>();
		const { rerender } = render(
			<I18nextProvider i18n={createI18n('en')}>
				<NameForm
					label="Folder name"
					onSubmit={vi.fn<(name: string) => void>()}
					submitLabel="Save"
				/>
			</I18nextProvider>,
		);
		expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument();

		rerender(
			<I18nextProvider i18n={createI18n('en')}>
				<NameForm
					label="Folder name"
					onCancel={onCancel}
					onSubmit={vi.fn<(name: string) => void>()}
					submitLabel="Save"
				/>
			</I18nextProvider>,
		);
		await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		expect(onCancel).toHaveBeenCalledOnce();
	});
});
