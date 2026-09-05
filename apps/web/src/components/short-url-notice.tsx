import { useTranslation } from 'react-i18next';

/**
 * RFC 2606 reserves `.invalid` as permanently unresolvable, which is why
 * `SHARED_DOMAIN_HOSTNAME` uses it as a placeholder until a real domain is
 * registered for the shared instance. Keying on the suffix rather than a
 * feature flag means this notice disappears by itself the day the domain
 * changes — there is nothing to remember to switch off.
 *
 * Every link this UI creates on the shared domain carries a `short_url` that
 * 404s until then; showing a copy button for a link that cannot resolve, with
 * no explanation, is the confusion this notice exists to prevent.
 */
export function ShortUrlNotice({
	hostname,
}: {
	readonly hostname: string;
}): React.JSX.Element | null {
	const { t } = useTranslation();
	if (!hostname.endsWith('.invalid')) return null;

	return <p role="note">{t('links.noShortDomain')}</p>;
}
