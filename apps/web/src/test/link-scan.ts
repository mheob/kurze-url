import type { LinkScan } from '@kurze-url/api-client';

/**
 * The one fixture for a link Google Safe Browsing reports, shared by the unit
 * tests and the stories rather than copied into each — a copy would drift from
 * the generated `LinkScan` shape in one file and not the others.
 *
 * @param threatTypes - What Google reported, as the API sends it.
 * @returns A flagged verdict naming them.
 */
export function flaggedScan(threatTypes: readonly string[]): LinkScan {
	return {
		checked_at: '2026-10-03T08:00:00.000Z',
		since: '2026-10-03T08:00:00.000Z',
		threat_types: [...threatTypes],
		verdict: 'flagged',
	};
}
