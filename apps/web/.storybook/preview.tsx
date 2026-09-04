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
 */
const withPreferences: Decorator = (Story, context) => {
	const language = context.globals.language === 'de' ? 'de' : 'en';
	const isDark = context.globals.theme === 'dark';

	return (
		<I18nextProvider i18n={createI18n(language)}>
			<div className={isDark ? 'dark' : undefined}>
				<div className="bg-background text-foreground p-6">
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
