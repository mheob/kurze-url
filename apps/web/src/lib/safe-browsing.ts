import type { LinkScan } from '@kurze-url/api-client';

interface KnownThreat {
	readonly category: Exclude<ThreatCategory, 'unknown'>;
	readonly definition: ThreatDefinition;
	readonly threatType: string;
}

/**
 * The threat types Safe Browsing reports today, spelled exactly as the API
 * sends them, in the order the notice explains them. Google defines malware
 * and unwanted software on one page, so both link there under their own labels.
 */
const KNOWN_THREATS: readonly KnownThreat[] = [
	{
		category: 'phishing',
		definition: {
			labelKey: 'links.scanDefinitionSocialEngineering',
			url: 'https://developers.google.com/search/docs/monitor-debug/security/social-engineering',
		},
		threatType: 'SOCIAL_ENGINEERING',
	},
	{
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionMalware',
			url: 'https://developers.google.com/search/docs/monitor-debug/security/malware',
		},
		threatType: 'MALWARE',
	},
	{
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionUnwantedSoftware',
			url: 'https://developers.google.com/search/docs/monitor-debug/security/malware',
		},
		threatType: 'UNWANTED_SOFTWARE',
	},
	{
		category: 'harmful',
		definition: {
			labelKey: 'links.scanDefinitionHarmfulApplication',
			url: 'https://developers.google.com/android/play-protect/potentially-harmful-applications',
		},
		threatType: 'POTENTIALLY_HARMFUL_APPLICATION',
	},
];

const CATEGORY_ORDER: readonly ThreatCategory[] = ['phishing', 'harmful', 'unknown'];

const MS_PER_MINUTE = 60_000;

/** Thirty minutes, Google's limit, less the minute the API also keeps in hand (see `WARNING_WINDOW_MS`). */
const WARNING_WINDOW_MINUTES = 29;

/**
 * Google's Safe Browsing advisory, which the attribution line links to. The
 * redirect surface's block page (`apps/api/internal/pages/pages.go`) holds the
 * same address, the same per-threat definition pages and the same grouping into
 * categories. The lists are short and change only when Google moves a page, so
 * they are kept in step by hand rather than sent through the API: change one,
 * change both.
 */
export const ADVISORY_URL = 'https://developers.google.com/safe-browsing/v4/advisory';

/**
 * Google's form for reporting a warning that is wrong. Only this page offers it:
 * the block page is read by visitors, who have no say in the link's destination.
 */
export const REPORT_ERROR_URL = 'https://www.google.com/safebrowsing/report_error/';

/** Safe Browsing's own overview, the definition for a threat type this app does not know. */
export const SAFE_BROWSING_URL = 'https://safebrowsing.google.com/';

/** What the notice says about a threat: phishing, harmful software, or something Google added later. */
export type ThreatCategory = 'harmful' | 'phishing' | 'unknown';

/** One definition link: its catalogue key and Google's page. */
export interface ThreatDefinition {
	readonly labelKey: string;
	readonly url: string;
}

/**
 * @param threatTypes - The threat types Google reported, as the API sends them.
 * @returns Every category they fall into, phishing first; `unknown` for a type Google added after this list was written, and when Google named none.
 */
export function threatCategories(threatTypes: readonly string[]): readonly ThreatCategory[] {
	const present = new Set<ThreatCategory>(
		threatTypes.map(
			(threatType) =>
				KNOWN_THREATS.find((known) => known.threatType === threatType)?.category ?? 'unknown',
		),
	);
	if (present.size === 0) present.add('unknown');
	return CATEGORY_ORDER.filter((category) => present.has(category));
}

/**
 * One category gets its own heading; a mix gets the generic one — the same
 * rule the redirect surface's block page follows.
 *
 * @param categories - What `threatCategories` returned.
 * @returns The category whose heading the notice shows.
 */
export function headingCategory(categories: readonly ThreatCategory[]): ThreatCategory {
	const [only] = categories;
	return categories.length === 1 && only !== undefined ? only : 'unknown';
}

/**
 * @param category - One category the link was reported for.
 * @param threatTypes - The threat types Google reported.
 * @returns Google's definition of each reported type in that category, in a fixed order; for `unknown`, Safe Browsing's own overview.
 */
export function threatDefinitions(
	category: ThreatCategory,
	threatTypes: readonly string[],
): readonly ThreatDefinition[] {
	if (category === 'unknown') {
		return [{ labelKey: 'links.scanDefinitionUnknown', url: SAFE_BROWSING_URL }];
	}
	return KNOWN_THREATS.filter(
		(known) => known.category === category && threatTypes.includes(known.threatType),
	).map((known) => known.definition);
}

/**
 * How long after its check a flagged verdict may still back Google's warning.
 * Google's terms allow a warning only on information younger than thirty
 * minutes. `checked_at` is written a few seconds after Google answered, so the
 * page keeps the same minute of margin the API keeps for its own confirmation
 * (`confirmationMargin` in `apps/api/internal/api/scan.go`).
 */
export const WARNING_WINDOW_MS = WARNING_WINDOW_MINUTES * MS_PER_MINUTE;

/**
 * @param scan - The nested verdict from `GET /v1/links/{link_id}`, when there is one.
 * @returns When the verdict stops backing Google's warning, in epoch milliseconds; `undefined` when it never backed one.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `LinkScan` is generated `@kurze-url/api-client` output whose `threat_types` array is not marked readonly; `Readonly<>` is shallow and cannot reach it.
export function warningExpiresAt(scan: LinkScan | undefined): number | undefined {
	if (scan?.verdict !== 'flagged') return undefined;
	const checkedAt = Date.parse(scan.checked_at);
	return Number.isNaN(checkedAt) ? undefined : checkedAt + WARNING_WINDOW_MS;
}
