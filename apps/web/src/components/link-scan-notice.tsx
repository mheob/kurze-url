/* oxlint-disable typescript/prefer-readonly-parameter-types -- `LinkScan` is generated `@kurze-url/api-client` output whose `threat_types` array is not marked readonly; that is codegen output, never edited by hand. */

import type { Link, LinkScan } from '@kurze-url/api-client';
import { ShieldAlertIcon } from 'lucide-react';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import {
	ADVISORY_URL,
	headingCategory,
	REPORT_ERROR_URL,
	threatCategories,
	threatDefinitions,
	type ThreatCategory,
} from '../lib/safe-browsing';

/** Each category's heading, qualified the way Google's terms require: "suspected", "possibly", never a verdict. */
const headingKeys: Record<ThreatCategory, string> = {
	harmful: 'links.scanHeadingHarmful',
	phishing: 'links.scanHeadingPhishing',
	unknown: 'links.scanHeadingUnknown',
};

/** Each category's sentence, qualified the same way. */
const bodyKeys: Record<ThreatCategory, string> = {
	harmful: 'links.scanHarmful',
	phishing: 'links.scanPhishing',
	unknown: 'links.scanUnknown',
};

export interface LinkScanNoticeProps {
	/** The nested verdict from `GET /v1/links/{link_id}`; absent until the current destination has been checked. */
	readonly scan?: LinkScan;
	/** The link's state. The notice shows only while it is `flagged`. */
	readonly state: Link['state'];
}

/**
 * The link page's notice for a link Google Safe Browsing reports. A status,
 * not an alarm: a labelled region rather than `role="alert"`, because it is
 * on the page on every visit while the block lasts and is news on none of
 * them. It carries what Google's terms attach to every warning — a qualified
 * claim, Google's definition of each threat, "Advisory provided by Google",
 * and the admission that Google can be wrong — and the two things a Verein
 * can do about it: report a wrong warning to Google, or change the
 * destination. There is deliberately no state control. A flag is lifted by
 * Google's later clean check or by a new destination, never by a request of
 * its own, so the API refuses a `state` on a flagged link with a 409 and the
 * page offers nothing that would send one.
 *
 * The state, not the scan, decides whether it shows: a link that is `flagged`
 * always gets a notice, and a scan that is missing, names no threat type or
 * names one this app does not know is shown under the generic qualified text
 * rather than as an error.
 *
 * @param props - The component's props.
 * @param props.scan - The nested verdict, when the destination has been checked.
 * @param props.state - The link's state; nothing renders unless it is `flagged`.
 * @returns The notice, or nothing for a link that is not blocked.
 */
export function LinkScanNotice({ scan, state }: LinkScanNoticeProps): React.JSX.Element | null {
	const { t } = useTranslation();
	const headingId = useId();
	if (state !== 'flagged') return null;

	const threatTypes = scan?.threat_types ?? [];
	const categories = threatCategories(threatTypes);

	return (
		<section
			aria-labelledby={headingId}
			className="flex flex-col gap-2 border border-destructive/50 bg-destructive/10 p-3 text-sm text-foreground"
		>
			<h2 className="flex items-center gap-2 font-semibold" id={headingId}>
				<ShieldAlertIcon aria-hidden />
				{t(headingKeys[headingCategory(categories)])}
			</h2>
			<p>{t('links.scanBlocked')}</p>
			{categories.map((category) => (
				<div key={category}>
					<p>{t(bodyKeys[category])}</p>
					<ul>
						{threatDefinitions(category, threatTypes).map((definition) => (
							// Keyed by label, not address: Google defines malware and
							// unwanted software on one page, so two links can share one.
							<li key={definition.labelKey}>
								{/* rel="noreferrer": the dashboard's own address is not Google's business. */}
								<a className="underline underline-offset-4" href={definition.url} rel="noreferrer">
									{t(definition.labelKey)}
								</a>
							</li>
						))}
					</ul>
				</div>
			))}
			<p>
				<a className="underline underline-offset-4" href={ADVISORY_URL} rel="noreferrer">
					{t('links.scanAdvisory')}
				</a>
			</p>
			<p>{t('links.scanDisclaimer')}</p>
			<p>
				<a className="underline underline-offset-4" href={REPORT_ERROR_URL} rel="noreferrer">
					{t('links.scanReport')}
				</a>
			</p>
			<p>{t('links.scanNextSteps')}</p>
		</section>
	);
}
