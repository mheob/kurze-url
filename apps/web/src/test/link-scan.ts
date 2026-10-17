import type { LinkScan } from '@kurze-url/api-client';

/**
 * The one fixture for a link Google Safe Browsing reports, shared by the unit
 * tests and the stories rather than copied into each — a copy would drift from
 * the generated `LinkScan` shape in one file and not the others.
 *
 * The check time defaults to the moment of the call, because the notice
 * shows Google's warning only on a check younger than 29 minutes.
 *
 * @param threatTypes - What Google reported, as the API sends it.
 * @param checkedAt - When the verdict was last confirmed; now unless given.
 * @returns A flagged verdict naming them.
 */
export function flaggedScan(
	threatTypes: readonly string[],
	checkedAt: string = new Date().toISOString(),
): LinkScan {
	return {
		checked_at: checkedAt,
		since: '2026-10-03T08:00:00.000Z',
		threat_types: [...threatTypes],
		verdict: 'flagged',
	};
}
