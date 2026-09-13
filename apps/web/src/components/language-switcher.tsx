import { useTranslation } from 'react-i18next';

import { LANGUAGE_COOKIE, LANGUAGES, type Language, preferenceCookie } from '../lib/preferences';
import { Button } from './ui/button';

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
 *
 * @param language - The language to switch to; written to the cookie before the reload.
 */
function choose(language: Language) {
	// The Cookie Store API's `set()` is Promise-based; this write has to land
	// before the synchronous reload on the next line, in the same tick a click
	// handler runs in, which is exactly what `document.cookie`'s synchronous
	// setter guarantees and an awaited alternative would not without deferring
	// the reload.
	// oxlint-disable-next-line unicorn/no-document-cookie
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
				<Button
					aria-pressed={i18n.language === language}
					key={language}
					onClick={() => {
						choose(language);
					}}
					size="sm"
					type="button"
					variant="outline"
				>
					{t(`language.${language}`)}
				</Button>
			))}
		</fieldset>
	);
}
