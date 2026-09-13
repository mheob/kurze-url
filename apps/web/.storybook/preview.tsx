import type { Decorator, Preview } from '@storybook/tanstack-react';
import { I18nextProvider } from 'react-i18next';

import { createI18n } from '../src/i18n';

// Global stylesheet, loaded for its side effect (registering the Tailwind
// layer and design tokens); there is nothing to assign.
// oxlint-disable-next-line import/no-unassigned-import
import '../src/styles/app.css';

/**
 * Language and theme are globals rather than per-story args so every story can
 * be checked in both without duplicating stories. German strings are reliably
 * longer than their English equivalents, which is a common way for a layout to
 * break — being able to flip a whole story set is the point.
 *
 * @param Story - The story being decorated.
 * @param context - The story's Storybook context; carries the active `language`/`theme` globals.
 * @returns The story wrapped in the i18n provider and theme/background wrapper.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `Decorator` context type; not this codebase's to mark readonly.
const withPreferences: Decorator = (Story, context) => {
	const language = context.globals.language === 'de' ? 'de' : 'en';
	const isDark = context.globals.theme === 'dark';

	return (
		<I18nextProvider i18n={createI18n(language)}>
			<div className={isDark ? 'dark' : undefined} data-theme="indigo">
				<div className="bg-background p-6 text-foreground">
					<Story />
				</div>
			</div>
		</I18nextProvider>
	);
};

const preview: Preview = {
	decorators: [withPreferences],
	globalTypes: {
		language: {
			defaultValue: 'en',
			toolbar: { items: ['en', 'de'], title: 'Language' },
		},
		theme: {
			defaultValue: 'light',
			toolbar: { items: ['light', 'dark'], title: 'Theme' },
		},
	},
	parameters: { a11y: { test: 'error' } },
};

export default preview;
