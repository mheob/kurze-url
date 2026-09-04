import { createInstance, type i18n } from 'i18next';
import { initReactI18next } from 'react-i18next';

import type { Language } from '../lib/preferences';
import de from './locales/de.json';
import en from './locales/en.json';

/**
 * A fresh instance per request rather than a module-level singleton: the
 * server renders for many people in one process, and a shared instance would
 * let one request's language leak into another's render.
 */
export function createI18n(language: Language): i18n {
	const instance = createInstance();

	void instance.use(initReactI18next).init({
		lng: language,
		fallbackLng: 'en',
		resources: { de: { translation: de }, en: { translation: en } },
		interpolation: { escapeValue: false },
		react: { useSuspense: false },
	});

	return instance;
}

/**
 * The root route's `head()` runs before `RootDocument` renders — no
 * `I18nextProvider` exists yet — so the `<title>` it emits can't go through
 * `useTranslation`. It reads the catalogue directly instead. A non-empty,
 * translated title is what axe's `document-title` check (WCAG 2.4.2) verifies.
 */
export function documentTitle(language: Language): string {
	return language === 'de' ? de.pageTitle : en.pageTitle;
}
