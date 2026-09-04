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
