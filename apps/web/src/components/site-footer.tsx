import { useTranslation } from 'react-i18next';

export function SiteFooter() {
	const { t } = useTranslation();

	return (
		<footer className="border-border text-muted-foreground border-t px-6 py-4 text-sm">
			{t('footer.tagline')}
		</footer>
	);
}
