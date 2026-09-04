import { useTranslation } from 'react-i18next';

// A plain `string`, not `HealthStatus['status']` from '../server/health': this
// component renders client-side, and importing anything from server/ — even a
// type-only import that verbatimModuleSyntax erases at compile time — would
// blur the boundary the architecture depends on. The loader that produces this
// value is where HealthStatus belongs.
export function SiteFooter({ apiStatus }: { readonly apiStatus: string }) {
	const { t } = useTranslation();

	return (
		<footer className="border-border text-muted-foreground flex flex-col gap-1 border-t px-6 py-4 text-sm">
			<p>{t('footer.tagline')}</p>
			{/* apiStatus is a technical value ('ok' | 'unreachable' | 'unknown'), not
			    prose — only the surrounding label is translated; the value itself is
			    interpolated as data. */}
			<p>{t('footer.apiStatus', { status: apiStatus })}</p>
		</footer>
	);
}
