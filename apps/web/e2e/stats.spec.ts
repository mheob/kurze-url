import { expect, type Locator, type Page } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';
import { linkIdForTeam, seedLinkClicks } from './fixtures/seed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own `Page`, bare or nested inside the fixture argument object each `test`
 * callback destructures; it has many mutating methods (`goto`, `click`, ...) and is not ours to
 * edit. Same note as `links.spec.ts`.
 */

/**
 * Every breakdown card, by its English heading and the value seeded to lead
 * it. `i18n.spec.ts` is what proves these headings change with the language;
 * this file runs in the default one and asserts the figures under them.
 */
const CARDS: readonly (readonly [heading: string, leadingValue: string])[] = [
	['Browser', 'Chrome'],
	['Operating system', 'macOS'],
	['Device', 'desktop'],
	['Country', 'DE'],
	['Referrer', 'direct'],
	['Campaign source', 'newsletter'],
];

/**
 * Every level-2 heading the data view renders, in order: the summary, the
 * chart, then the six cards. `StatSummary`'s own two splits are `<h3>`s and so
 * are not here. Asserting the whole sequence at once pins the page's
 * composition — a card that failed to render leaves a gap rather than merely
 * failing its own separate assertion.
 */
const HEADINGS = ['Statistics', 'Over time', ...CARDS.map(([heading]) => heading)];

/**
 * The figure `StatSummary` renders for one of its four terms.
 *
 * `stat-summary.tsx` builds each pair as a `<div>` holding one `<dt>`/`<dd>`
 * (axe's definition-list rule allows nothing else directly inside a `<dl>`),
 * so the term is what identifies the pair. Matching the term exactly matters:
 * "Clicks" is a substring of "Human clicks", and a loose match would read the
 * wrong figure while still passing.
 *
 * @param page - The page showing the statistics.
 * @param term - The `<dt>` text to find, exactly as rendered.
 * @returns The `<dd>` beside that term.
 */
function summaryFigure(page: Page, term: string): Locator {
	return page
		.locator('dl > div')
		.filter({ has: page.getByText(term, { exact: true }) })
		.locator('dd');
}

/**
 * Nothing else asserts that this page renders real figures from the real API:
 * the unit tests around it all run against mocked documents, so a break
 * anywhere between the endpoint's SQL and the rendered table would leave every
 * one of them green. The rollup rows are seeded directly rather than produced
 * by redirects — see `./fixtures/seed` for why that is the only workable
 * source, and what it deliberately does not prove.
 */
test('shows the recorded clicks', async ({ page, teamId, teamSlug }) => {
	const shortUrl = await createLink(page, teamSlug, {
		destinationUrl: `https://example.org/stats-${Date.now()}`,
	});
	const linkId = await linkIdForTeam(teamId);
	const seeded = await seedLinkClicks(linkId);

	await page.goto(`/teams/${teamSlug}/links/${linkId}/stats`);

	await expect(page.getByRole('heading', { level: 1, name: shortUrl })).toBeVisible();

	await expect(summaryFigure(page, 'Clicks')).toHaveText(String(seeded.totals.clicks));
	await expect(summaryFigure(page, 'Visitors')).toHaveText(String(seeded.totals.uniqueVisitors));
	await expect(summaryFigure(page, 'Human clicks')).toHaveText(String(seeded.totals.humanClicks));
	await expect(summaryFigure(page, 'Human visitors')).toHaveText(
		String(seeded.totals.humanUniqueVisitors),
	);

	await expect(page.getByRole('heading', { level: 2 })).toHaveText(HEADINGS);

	// `Promise.all`, not a loop: these are read-only, auto-retrying assertions
	// against a page nothing is changing, so running them together costs one
	// polling window instead of six.
	await Promise.all(
		CARDS.map(async ([, leadingValue]) =>
			expect(page.getByRole('cell', { exact: true, name: leadingValue })).toBeVisible(),
		),
	);

	// Each card's leading row carries the same figures, so one assertion per
	// dimension would repeat itself; asserting the count of cells holding that
	// figure covers all six at once, and a card rendering the wrong dimension's
	// numbers changes it.
	await expect(
		page.getByRole('cell', { exact: true, name: String(seeded.leading.clicks) }),
	).toHaveCount(CARDS.length);

	// Counting is on for this link, so the historical-figures banner must not
	// be here. It is the page's only `role="note"`.
	await expect(page.getByRole('note')).toHaveCount(0);
});

/**
 * The other direction, and the only test anywhere that follows the
 * `analytics_enabled` flag all the way from the create form through the API to
 * what the statistics page decides to render. `statsView`'s unit tests cover
 * the decision itself; none of them can see whether the flag survives the
 * journey.
 */
test('says counting is off when the link has it disabled', async ({ page, teamId, teamSlug }) => {
	await createLink(page, teamSlug, {
		countClicks: false,
		destinationUrl: `https://example.org/stats-off-${Date.now()}`,
	});
	const linkId = await linkIdForTeam(teamId);

	await page.goto(`/teams/${teamSlug}/links/${linkId}/stats`);

	await expect(
		page.getByRole('heading', { level: 2, name: 'Click counting is off for this link' }),
	).toBeVisible();
});
