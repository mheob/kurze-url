import { useTranslation } from 'react-i18next';

import { LANGUAGE_COOKIE, LANGUAGES, type Language, preferenceCookie } from '../lib/preferences';

/**
 * Setting the cookie and reloading, rather than switching client-side only:
 * the language is server-rendered, so the server has to know about the change
 * for the next render to match. It also keeps one source of truth — the cookie
 * — instead of a cookie and a divergent client state.
 *
 * Module scope, not inside the component: it captures nothing from the
 * component's closure (`language` is a parameter), and defining it inside a
 * component body reads to the linter as a render-time mutation of `document`
 * rather than the click-time one it actually is.
 */
function choose(language: Language) {
	document.cookie = preferenceCookie(LANGUAGE_COOKIE, language);
	globalThis.location.reload();
}

export function LanguageSwitcher() {
	const { i18n, t } = useTranslation();

	return (
		// A `<fieldset>` carries the implicit ARIA role "group" natively, so this
		// satisfies both the a11y preference for real semantic elements over a
		// bolted-on `role` attribute and the test's `getByRole('group')` query.
		<fieldset aria-label={t('language.label')}>
			{LANGUAGES.map((language) => (
				<button
					aria-pressed={i18n.language === language}
					key={language}
					onClick={() => choose(language)}
					type="button"
				>
					{t(`language.${language}`)}
				</button>
			))}
		</fieldset>
	);
}
