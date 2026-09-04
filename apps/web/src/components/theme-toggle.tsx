import { MoonIcon, SunIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { preferenceCookie, THEME_COOKIE, type Theme } from '../lib/preferences';

/**
 * The class is toggled immediately so the control feels instant, and the
 * cookie is written so the next server render agrees. Without the cookie the
 * choice would be lost on navigation; without the immediate class change the
 * control would appear not to work until the reload finished.
 */
export function ThemeToggle({ theme }: { readonly theme: Theme }) {
	const { t } = useTranslation();
	const next: Theme = theme === 'dark' ? 'light' : 'dark';

	function toggle() {
		document.cookie = preferenceCookie(THEME_COOKIE, next);
		document.documentElement.classList.toggle('dark', next === 'dark');
	}

	return (
		<button
			aria-label={t(next === 'dark' ? 'theme.toDark' : 'theme.toLight')}
			onClick={toggle}
			type="button"
		>
			{next === 'dark' ? <MoonIcon aria-hidden /> : <SunIcon aria-hidden />}
		</button>
	);
}
