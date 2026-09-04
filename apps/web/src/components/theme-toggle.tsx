import { MoonIcon, SunIcon } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { preferenceCookie, THEME_COOKIE, type Theme } from '../lib/preferences';
import { Button } from './ui/button';

/**
 * `theme` seeds local state rather than being read directly: it is only ever
 * the loader's value from the request that produced this render, and nothing
 * about a click causes the root loader to rerun. Deriving `next` from the
 * prop on every render, as an earlier version of this component did, freezes
 * the button after the first click — the class and cookie flip, but `theme`
 * (and so the button's own label) never does, so a second click re-requests
 * the mode already showing and the control announces the wrong action
 * (WCAG 4.1.2). Local state — seeded once from the prop, updated on click —
 * is what lets repeated clicks keep working without a reload or a router
 * round trip, matching the "instant" feel the class-toggle-on-click below is
 * already designed around.
 */
export function ThemeToggle({ theme: initialTheme }: { readonly theme: Theme }) {
	const { t } = useTranslation();
	const [theme, setTheme] = useState(initialTheme);
	const next: Theme = theme === 'dark' ? 'light' : 'dark';

	function toggle() {
		document.cookie = preferenceCookie(THEME_COOKIE, next);
		document.documentElement.classList.toggle('dark', next === 'dark');
		setTheme(next);
	}

	return (
		<Button
			aria-label={t(next === 'dark' ? 'theme.toDark' : 'theme.toLight')}
			onClick={toggle}
			size="icon"
			type="button"
			variant="ghost"
		>
			{next === 'dark' ? <MoonIcon aria-hidden /> : <SunIcon aria-hidden />}
		</Button>
	);
}
