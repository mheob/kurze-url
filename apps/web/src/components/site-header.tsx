import { useTranslation } from 'react-i18next';

import type { Theme } from '../lib/preferences';
import { LanguageSwitcher } from './language-switcher';
import { ThemeToggle } from './theme-toggle';

export function SiteHeader({ theme }: { readonly theme: Theme }) {
	const { t } = useTranslation();

	return (
		<header className="border-border flex items-center justify-between border-b px-6 py-4">
			<span className="font-semibold">{t('brand')}</span>
			<div className="flex items-center gap-4">
				<LanguageSwitcher />
				<ThemeToggle theme={theme} />
			</div>
		</header>
	);
}
