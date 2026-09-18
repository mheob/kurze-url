/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `StatBreakdown`/`StatCounts`/`StatValue`
   types, whose properties are not marked readonly; that is generated codegen output, never
   edited by hand. */

import type { StatBreakdown, StatCounts } from '@kurze-url/api-client';
import type { TFunction } from 'i18next';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import { formatCount } from '../lib/format';
import type { Language } from '../lib/preferences';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';

/** Shares are reported as whole percentages; this is the ratio-to-percent multiplier. */
const PERCENT_SCALE = 100;

interface StatSplitProps {
	readonly breakdown: StatBreakdown;
	readonly heading: string;
	readonly language: Language;
	readonly noValueLabel: string;
}

/**
 * A split's percentages are computed against the sum of its own values'
 * clicks — unlike `StatBreakdownCard`'s near-identical `dimensionTotal`,
 * which also adds its dimension's `other_clicks`. That term exists there
 * because the API caps an open dimension at its top ten values and folds
 * the remainder into `other_clicks`. `bot_status` and `qr_vs_regular` are
 * the only two dimensions with a closed, fixed value set of at most two
 * values each (see `splitValueLabel`'s own docstring), so nothing is ever
 * truncated here — `other_clicks` is always 0, and adding it would only
 * imply a truncation case this split cannot have.
 *
 * @param breakdown - The split to size.
 * @returns The denominator every one of its values' shares is computed against.
 */
function splitValuesTotal(breakdown: StatBreakdown): number {
	return (breakdown.values ?? []).reduce((sum, value) => sum + value.clicks, 0);
}

/**
 * @param clicks - One value's own click count.
 * @param total - The split's total, from `splitValuesTotal`.
 * @returns The value's share as a whole percentage, or 0 when the split has nothing to divide.
 */
function sharePercent(clicks: number, total: number): number {
	if (total === 0) return 0;
	return Math.round((clicks / total) * PERCENT_SCALE);
}

/**
 * `bot_status` and `qr_vs_regular` are the only two dimensions with a
 * closed, fixed value set — `human`/`bot` and `regular`/`qr`
 * (`apps/api/internal/analytics/dimensions.go`) — unlike `browser`,
 * `country`, `referrer` and `utm_source` (rendered by `StatBreakdownCard`),
 * which are unbounded and partly attacker-supplied text that must never be
 * run through a translation lookup. Mirrors `statusLabel`/`reasonLabel` in
 * `domain-list.tsx`: an unrecognised value is echoed back rather than
 * dropped or replaced with i18next's own missing-key marker, because the
 * API's dimension set can grow.
 *
 * @param t - The translation function.
 * @param value - The split's raw value, echoed back unchanged when unrecognised.
 * @returns The label to render for this value.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Readonly<TFunction>` strips i18next's call signature and produces a real TS2349 "not callable"; that was tried.
function splitValueLabel(t: TFunction, value: string): string {
	switch (value) {
		case 'human': {
			return t('stats.dimensionValueHuman');
		}
		case 'bot': {
			return t('stats.dimensionValueBot');
		}
		case 'regular': {
			return t('stats.dimensionValueRegular');
		}
		case 'qr': {
			return t('stats.dimensionValueQr');
		}
		default: {
			return value;
		}
	}
}

/**
 * One binary split — bot status or QR-vs-regular. Deliberately not the
 * top-ten breakdown layout: a split holds at most a couple of values (and,
 * for `qr_vs_regular` on a link with no scans yet, only one), so a plain
 * list of label-and-share rows is the whole shape, with no "other" row —
 * `other_clicks` is folded into `splitTotal`'s denominator instead of
 * getting a row of its own.
 *
 * @param props - The component's props.
 * @param props.breakdown - The split to render.
 * @param props.heading - The translated heading for this split.
 * @param props.language - The active language, used to format each share.
 * @param props.noValueLabel - The translated label shown when the split recorded nothing at all.
 * @returns The rendered split section.
 */
function StatSplit({
	breakdown,
	heading,
	language,
	noValueLabel,
}: StatSplitProps): React.JSX.Element {
	const { t } = useTranslation();
	const values = breakdown.values ?? [];
	const total = splitValuesTotal(breakdown);

	return (
		<div>
			<h3>{heading}</h3>
			{values.length === 0 ? (
				<p>{noValueLabel}</p>
			) : (
				<ul>
					{values.map((value) => (
						<li key={value.value}>
							<span>{splitValueLabel(t, value.value)}</span>{' '}
							<span>{`${formatCount(sharePercent(value.clicks, total), language)}%`}</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

export interface StatSummaryProps {
	readonly botStatus: StatBreakdown;
	readonly language: Language;
	readonly qrVsRegular: StatBreakdown;
	readonly totals: StatCounts;
}

/**
 * The summary block of a link's statistics page: the four running totals
 * plus the two dimensions the API can only ever report as a binary split
 * (`bot_status`, `qr_vs_regular` — no dimension can be crossed with
 * another, see `docs/planning/05-database-schema.md`). Every number renders
 * through `formatCount`; nothing here calls `Intl` directly.
 *
 * The visitor figure is the one total whose name says the opposite of what
 * it measures — `unique_visitors` is a sum of daily-unique counts, so a
 * person returning on three days counts three times — so its caveat is
 * visible text, `aria-describedby`-linked to the figure it qualifies, rather
 * than a tooltip nobody has to open to be warned.
 *
 * @param props - The component's props.
 * @param props.botStatus - The human/bot split.
 * @param props.language - The active language, for number formatting.
 * @param props.qrVsRegular - The QR/regular split.
 * @param props.totals - The four running totals.
 * @returns The rendered summary card.
 */
export function StatSummary({
	botStatus,
	language,
	qrVsRegular,
	totals,
}: StatSummaryProps): React.JSX.Element {
	const { t } = useTranslation();
	const visitorsNoteId = useId();

	return (
		<Card>
			<CardHeader>
				{/* `CardTitle` hardcodes a `<div>` with no `render` prop to hand it a
				    real heading tag, and `role="heading"` on a `<div>` is exactly what
				    `jsx-a11y/prefer-tag-over-role` refuses. Nesting a real `<h2>` keeps
				    the section in the page's heading structure without either
				    problem — Tailwind's preflight resets a heading's font-size/weight
				    to `inherit`, so `CardTitle`'s own classes still style it. */}
				<CardTitle>
					<h2>{t('stats.heading')}</h2>
				</CardTitle>
			</CardHeader>
			<CardContent>
				{/* axe's definition-list rule allows a `<dl>` to directly contain only
				    `<div>`s that themselves hold nothing but a `<dt>`/`<dd>` pair — a
				    `<p>` nested in one of those divs is a violation
				    (`test:storybook` caught this). The caveat therefore lives right
				    after the `<dl>` instead of inside the visitors pair's own div;
				    `aria-describedby` is what keeps it attached to the figure it
				    qualifies, not DOM nesting. */}
				<dl>
					<div>
						<dt>{t('stats.clicks')}</dt>
						<dd>{formatCount(totals.clicks, language)}</dd>
					</div>
					<div>
						<dt>{t('stats.uniqueVisitors')}</dt>
						<dd aria-describedby={visitorsNoteId}>
							{formatCount(totals.unique_visitors, language)}
						</dd>
					</div>
					<div>
						<dt>{t('stats.humanClicks')}</dt>
						<dd>{formatCount(totals.human_clicks, language)}</dd>
					</div>
					<div>
						<dt>{t('stats.humanUniqueVisitors')}</dt>
						<dd>{formatCount(totals.human_unique_visitors, language)}</dd>
					</div>
				</dl>
				<p id={visitorsNoteId}>{t('stats.visitorsNote')}</p>

				<StatSplit
					breakdown={botStatus}
					heading={t('stats.botStatus')}
					language={language}
					noValueLabel={t('stats.noValue')}
				/>
				<StatSplit
					breakdown={qrVsRegular}
					heading={t('stats.qrVsRegular')}
					language={language}
					noValueLabel={t('stats.noValue')}
				/>
			</CardContent>
		</Card>
	);
}
