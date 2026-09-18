import { describe, expect, it } from 'vitest';

import { statsView } from './teams.$teamSlug.links.$linkId_.stats.tsx';

const TOTALS_ZERO = { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 };
const EMPTY_BREAKDOWNS = {
	bot_status: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	browser: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	country: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	device: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	os: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	qr_vs_regular: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	referrer: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	utm_source: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
};

describe(statsView, () => {
	// The single easiest thing to get wrong on this page. An empty document
	// from a link with counting off means "not counted", not "not clicked",
	// and a chart of zeroes there would be a false statement.
	it('reports counting as off rather than showing empty charts', () => {
		expect(
			statsView({
				analytics_enabled: false,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('disabled');
	});

	it('reports an empty window when counting is on and nothing was clicked', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('empty');
	});

	it('reports data whenever there is at least one click', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: { ...TOTALS_ZERO, clicks: 1 },
			}),
		).toBe('data');
	});
});
