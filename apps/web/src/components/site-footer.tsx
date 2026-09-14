import { useTranslation } from 'react-i18next';

import { Badge } from './ui/badge';

// A plain `string`, not `HealthStatus['status']` from '../server/health': this
// component renders client-side, and importing anything from server/ — even a
// type-only import that verbatimModuleSyntax erases at compile time — would
// blur the boundary the architecture depends on. The loader that produces this
// value is where HealthStatus belongs.
export function SiteFooter({ apiStatus }: { readonly apiStatus: string }) {
	const { t } = useTranslation();

	return (
		<footer
			className="flex flex-col gap-2 border-t p-4 text-sm text-muted-foreground"
			// Machine-readable twin of the badge's own text below, and the only
			// signal the e2e suite has that the deployment it is about to test is
			// wired to a real API. `e2e/global-setup.ts` refuses to run when this is
			// anything but 'ok'. Reading the prose instead would couple that check to
			// a translated string; reading nothing at all is what let a whole suite
			// run against a preview whose paired API had never been built.
			data-api-status={apiStatus}
		>
			<p>{t('footer.tagline')}</p>
			{/* `fetchHealth` degrades to 'unreachable' rather than throwing, so this
			    is a real, reachable state — not a theoretical one a colour alone
			    could get away with. `destructive` for it, `secondary` (neutral) for
			    everything else ('ok', and the near-unreachable 'unknown' fallback),
			    but the text is what actually carries the fact (WCAG 1.4.1): apiStatus
			    is a technical value ('ok' | 'unreachable' | 'unknown'), not prose —
			    only the surrounding label is translated, the value itself is
			    interpolated as data. */}
			<Badge variant={apiStatus === 'unreachable' ? 'destructive' : 'secondary'}>
				{t('footer.apiStatus', { status: apiStatus })}
			</Badge>
		</footer>
	);
}
