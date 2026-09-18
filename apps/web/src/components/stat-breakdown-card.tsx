/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `StatBreakdown`/`StatValue` types, whose
   properties are not marked readonly; that is generated codegen output, never edited by hand. */

import type { StatBreakdown } from '@kurze-url/api-client';
import { useTranslation } from 'react-i18next';

import { formatCount } from '../lib/format';
import type { Language } from '../lib/preferences';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

/** Shares are reported as whole percentages; this is the ratio-to-percent multiplier. */
const PERCENT_SCALE = 100;

/**
 * A dimension's percentages are computed against the sum of its own
 * `values` plus its `other_clicks` — never against `totals.clicks` from
 * elsewhere on the page — because that sum is the dimension's true total.
 * It agrees with `totals.clicks` only when nothing was truncated into
 * `other_clicks`. Mirrors `splitTotal` in `stat-summary.tsx`, which computes
 * the same thing for a binary split.
 *
 * @param breakdown - The dimension to size.
 * @returns The denominator every one of its values' shares is computed against.
 */
function dimensionTotal(breakdown: StatBreakdown): number {
	const valuesTotal = (breakdown.values ?? []).reduce((sum, value) => sum + value.clicks, 0);
	return valuesTotal + breakdown.other_clicks;
}

/**
 * @param clicks - One value's own click count.
 * @param total - The dimension's total, from `dimensionTotal`.
 * @returns The value's share as a whole percentage, or 0 when the dimension has nothing to divide.
 */
function sharePercent(clicks: number, total: number): number {
	if (total === 0) return 0;
	return Math.round((clicks / total) * PERCENT_SCALE);
}

export interface StatBreakdownCardProps {
	/** The dimension's top-ten values plus its "other" remainder. */
	readonly breakdown: StatBreakdown;
	/** The active language, for number formatting. */
	readonly language: Language;
	/** The dimension's translated name — used as both the card heading and the value column's header. */
	readonly title: string;
}

/**
 * One analytics dimension's top-ten list — browser, OS, device, country,
 * referrer or `utm_source` — plus the "further values" row the API's
 * `other_*` fields exist to make honest: a list capped at ten would
 * otherwise silently misstate its own dimension's total.
 *
 * `referrer` and `utm_source` values are attacker-supplied text, truncated
 * to 128 bytes by the API and bounded in no other way. Every value renders
 * as plain text — never as a link, an image source, or anything else the
 * page would fetch — so nothing here can turn one into a navigation or a
 * request.
 *
 * Each row's share bar is `role="presentation"`: the click count beside it
 * is the information, the bar only repeats it visually, sized against the
 * dimension's own total (`dimensionTotal`) rather than `totals.clicks` from
 * elsewhere on the page — the two agree only when nothing was truncated.
 *
 * @param props - The component's props.
 * @param props.breakdown - The dimension's top-ten values plus its "other" remainder.
 * @param props.language - The active language, for number formatting.
 * @param props.title - The dimension's translated name.
 * @returns The rendered breakdown card.
 */
export function StatBreakdownCard({
	breakdown,
	language,
	title,
}: StatBreakdownCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const values = breakdown.values ?? [];
	const total = dimensionTotal(breakdown);

	return (
		<Card>
			<CardHeader>
				{/* Same reason as `StatSummary`/`StatSeriesChart`: `CardTitle` hardcodes
				    a `<div>` with no `render` prop, so a real `<h2>` nests inside it
				    rather than reaching for `role="heading"`, which
				    `jsx-a11y/prefer-tag-over-role` refuses on a `<div>`. */}
				<CardTitle>
					<h2>{title}</h2>
				</CardTitle>
			</CardHeader>
			<CardContent>
				{values.length === 0 ? (
					<p>{t('stats.breakdownEmpty')}</p>
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{title}</TableHead>
								<TableHead>{t('stats.clicks')}</TableHead>
								<TableHead>{t('stats.uniqueVisitors')}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{values.map((value) => (
								<TableRow key={value.value}>
									<TableCell>{value.value}</TableCell>
									<TableCell>
										<div className="flex items-center gap-2">
											{/* The track is a plain, fixed-width div — only the fill
											    below varies and carries the `role="presentation"` this
											    task calls for. */}
											<div className="h-2 w-16 shrink-0 rounded-full bg-muted">
												<div
													className="h-full rounded-full bg-chart-1"
													role="presentation"
													style={{ width: `${sharePercent(value.clicks, total)}%` }}
												/>
											</div>
											<span>{formatCount(value.clicks, language)}</span>
										</div>
									</TableCell>
									<TableCell>{formatCount(value.unique_visitors, language)}</TableCell>
								</TableRow>
							))}
							{breakdown.other_values > 0 ? (
								<TableRow>
									<TableCell>{t('stats.otherValues', { count: breakdown.other_values })}</TableCell>
									<TableCell>{formatCount(breakdown.other_clicks, language)}</TableCell>
									<TableCell>{formatCount(breakdown.other_unique_visitors, language)}</TableCell>
								</TableRow>
							) : null}
						</TableBody>
					</Table>
				)}
			</CardContent>
		</Card>
	);
}
