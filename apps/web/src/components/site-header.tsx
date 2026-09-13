import { useTranslation } from 'react-i18next';

import type { Theme } from '../lib/preferences';
import { LanguageSwitcher } from './language-switcher';
import { ThemeToggle } from './theme-toggle';

export function SiteHeader({ theme }: { readonly theme: Theme }) {
	const { t } = useTranslation();

	return (
		// The same slim treatment as the authenticated shell's own top bar
		// (`authed-shell.tsx`) — `h-12`, `border-b`, `font-semibold` brand — so a
		// visitor moving between the public pages and the signed-in app sees one
		// product, not two. `LanguageSwitcher`/`ThemeToggle` stay here rather than
		// moving to only the sidebar: a signed-out visitor never reaches the
		// sidebar at all, and this header is the only chrome they have.
		<header className="flex h-12 items-center justify-between border-b px-4">
			<span className="font-semibold">{t('brand')}</span>
			<div className="flex items-center gap-4">
				<LanguageSwitcher />
				<ThemeToggle theme={theme} />
			</div>
		</header>
	);
}
