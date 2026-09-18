/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `StatDay` type, whose properties are not
   marked readonly; that is generated codegen output, never edited by hand. */

import type { StatDay } from '@kurze-url/api-client';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CartesianGrid, Line, LineChart, XAxis } from 'recharts';

import { formatCount, formatDay } from '../lib/format';
import type { Language } from '../lib/preferences';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';
import {
	ChartContainer,
	ChartLegend,
	ChartLegendContent,
	ChartTooltip,
	ChartTooltipContent,
	type ChartConfig,
} from './ui/chart';
import { Checkbox } from './ui/checkbox';
import { Field, FieldLabel } from './ui/field';

/** A window's totals, summed from its daily rows. */
interface WindowTotals {
	readonly clicks: number;
	readonly uniqueVisitors: number;
}

/**
 * @param series - The daily rows to sum.
 * @returns The window's total clicks and total unique visitors, each the
 *   plain sum of the daily figures. `StatSummary`'s caveat about summing
 *   `unique_visitors` across days applies here too — this helper just adds
 *   the numbers, the caveat is the caller's copy to state, not this
 *   function's to repeat.
 */
function windowTotals(series: readonly StatDay[]): WindowTotals {
	let clicks = 0;
	let uniqueVisitors = 0;
	for (const day of series) {
		clicks += day.clicks;
		uniqueVisitors += day.unique_visitors;
	}
	return { clicks, uniqueVisitors };
}

export interface StatSeriesChartProps {
	/** First day of the window, as YYYY-MM-DD. */
	readonly from: string;
	/** The active language, for date and number formatting. */
	readonly language: Language;
	/** One row per day in the window, gap-filled by the API — a day with no clicks is a real zero, not a missing row. */
	readonly series: readonly StatDay[];
	/** Last day of the window, as YYYY-MM-DD. */
	readonly to: string;
}

/**
 * The daily click series for a link's statistics page: a `LineChart` of two
 * or four lines that carries two independent channels, because the indigo
 * ramp cannot tell four hues apart from each other. Colour says which
 * metric — clicks on `--chart-1`, unique visitors on `--chart-5`, the
 * ramp's widest pair and still only about 3:1 apart. Stroke style says which
 * population: solid is everyone, `strokeDasharray="4 4"` is humans only — so
 * the two human lines deliberately repeat the metric colours instead of
 * taking two more ramp steps, because adjacent steps measure only 1.25:1 and
 * would be unreadable for exactly the readers this design protects.
 *
 * A chart of SVG paths carries no data to a screen reader, so the same rows
 * ship again as an `sr-only` table right below it — `aria-describedby` on
 * the `role="img"` wrapper ties the two together, and the table is the only
 * part of this component a screen reader can actually read. The bot-share
 * toggle controls both at once: two columns/lines when off, four when on.
 *
 * @param props - The component's props.
 * @param props.from - First day of the window, as YYYY-MM-DD.
 * @param props.language - The active language, for date and number formatting.
 * @param props.series - One row per day in the window.
 * @param props.to - Last day of the window, as YYYY-MM-DD.
 * @returns The rendered chart card.
 */
export function StatSeriesChart({
	from,
	language,
	series,
	to,
}: StatSeriesChartProps): React.JSX.Element {
	const { t } = useTranslation();
	const [showBots, setShowBots] = useState(false);
	const tableId = useId();
	const checkboxId = useId();

	const config = {
		clicks: { color: 'var(--chart-1)', label: t('stats.clicks') },
		human_clicks: { color: 'var(--chart-1)', label: t('stats.humanClicks') },
		human_unique_visitors: { color: 'var(--chart-5)', label: t('stats.humanUniqueVisitors') },
		unique_visitors: { color: 'var(--chart-5)', label: t('stats.uniqueVisitors') },
	} satisfies ChartConfig;

	const totals = windowTotals(series);
	const chartLabel = t('stats.chartLabel', {
		clicks: formatCount(totals.clicks, language),
		from: formatDay(from, language),
		to: formatDay(to, language),
		visitors: formatCount(totals.uniqueVisitors, language),
	});

	return (
		<Card>
			<CardHeader>
				{/* `CardTitle` hardcodes a `<div>` with no `render` prop, so a real
				    `<h2>` nests inside it rather than reaching for `role="heading"`,
				    which `jsx-a11y/prefer-tag-over-role` refuses on a `<div>`. Same
				    pattern as `StatSummary`. */}
				<CardTitle>
					<h2>{t('stats.seriesHeading')}</h2>
				</CardTitle>
			</CardHeader>
			<CardContent>
				<Field orientation="horizontal">
					{/* `id` is load-bearing: Base UI's `Checkbox` renders a hidden
					    native input for form semantics, and that hidden input's `id`
					    is how it finds `FieldLabel` below as its label and gives the
					    visible checkbox span an accessible name. Removing it breaks
					    `getByRole('checkbox', { name: … })`. */}
					<Checkbox
						checked={showBots}
						id={checkboxId}
						onCheckedChange={(checked: boolean) => {
							setShowBots(checked);
						}}
					/>
					<FieldLabel htmlFor={checkboxId}>{t('stats.showBots')}</FieldLabel>
				</Field>

				{/* oxlint-disable-next-line jsx-a11y/prefer-tag-over-role -- a literal `<img>` only
				    accepts a `src`, and this wraps a live chart (tooltip, legend) rather than a
				    static image; `role="img"` plus `aria-label` is the standard technique for
				    exposing a non-text visualisation as one accessible unit, with its data shipped
				    separately as the `aria-describedby`-linked table below. */}
				<div aria-describedby={tableId} aria-label={chartLabel} role="img">
					{/* oxlint-disable-next-line react/forbid-component-props -- `ChartContainer`
					    (components/ui/chart.tsx) forwards `className` straight to its own `<div>`;
					    this is how every caller sizes it. */}
					<ChartContainer className="aspect-video" config={config}>
						<LineChart accessibilityLayer data={series}>
							<CartesianGrid vertical={false} />
							<XAxis
								axisLine={false}
								dataKey="date"
								tickFormatter={(value: string) => formatDay(value, language)}
								tickLine={false}
							/>
							<ChartTooltip
								content={
									// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Recharts' own `content` render-prop idiom: `ChartTooltip`/`ChartLegend` clone and merge their own props onto this element. A stable reference would need a `useMemo` around a one-line static element per chart.
									<ChartTooltipContent
										labelFormatter={(value) => formatDay(String(value), language)}
									/>
								}
							/>
							{/* oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- same reason
							    as the tooltip above. */}
							<ChartLegend content={<ChartLegendContent />} />
							<Line dataKey="clicks" dot={false} stroke="var(--color-clicks)" type="monotone" />
							<Line
								dataKey="unique_visitors"
								dot={false}
								stroke="var(--color-unique_visitors)"
								type="monotone"
							/>
							{showBots ? (
								<Line
									dataKey="human_clicks"
									dot={false}
									stroke="var(--color-human_clicks)"
									strokeDasharray="4 4"
									type="monotone"
								/>
							) : null}
							{showBots ? (
								<Line
									dataKey="human_unique_visitors"
									dot={false}
									stroke="var(--color-human_unique_visitors)"
									strokeDasharray="4 4"
									type="monotone"
								/>
							) : null}
						</LineChart>
					</ChartContainer>
				</div>

				<table className="sr-only" id={tableId}>
					<caption>{t('stats.seriesTableCaption')}</caption>
					<thead>
						<tr>
							<th scope="col">{t('stats.columnDate')}</th>
							<th scope="col">{t('stats.clicks')}</th>
							<th scope="col">{t('stats.uniqueVisitors')}</th>
							{showBots ? (
								<>
									<th scope="col">{t('stats.humanClicks')}</th>
									<th scope="col">{t('stats.humanUniqueVisitors')}</th>
								</>
							) : null}
						</tr>
					</thead>
					<tbody>
						{series.map((day) => (
							<tr key={day.date}>
								<th scope="row">{formatDay(day.date, language)}</th>
								<td>{formatCount(day.clicks, language)}</td>
								<td>{formatCount(day.unique_visitors, language)}</td>
								{showBots ? (
									<>
										<td>{formatCount(day.human_clicks, language)}</td>
										<td>{formatCount(day.human_unique_visitors, language)}</td>
									</>
								) : null}
							</tr>
						))}
					</tbody>
				</table>
			</CardContent>
		</Card>
	);
}
