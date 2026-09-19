import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- Playwright's own `Page`, nested
 * inside the fixture argument object each `test` callback destructures; it has many mutating
 * methods (`goto`, `click`, ...) and is not ours to edit. Same note as `links.spec.ts` and
 * `stats.spec.ts`.
 */

test('shows what just happened', async ({ page, teamSlug }) => {
	// `createLink` writes a real `link.created` row — no seeding fixture needed.
	await createLink(page, teamSlug, { destinationUrl: `https://example.org/audit-${Date.now()}` });

	await page.goto(`/teams/${teamSlug}/audit-log`);

	await expect(page.getByRole('heading', { level: 1, name: 'History' })).toBeVisible();
	// `exact: true`: the "When" cell's own accessible name also contains "Link
	// created" as a substring — it absorbs its "Details" button's `aria-label`
	// ("Details of Link created on …", set for WCAG 2.5.3 Label in Name), not
	// just the button's visible text — so a non-exact match resolves to two
	// cells and fails in strict mode. Only the "What" cell's name is exactly
	// "Link created".
	await expect(page.getByRole('cell', { exact: true, name: 'Link created' })).toBeVisible();
});

test('discloses an entry’s details on request', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, { destinationUrl: `https://example.org/audit-${Date.now()}` });

	await page.goto(`/teams/${teamSlug}/audit-log`);

	const toggle = page.getByRole('button', { name: /^Details/u }).first();
	await expect(toggle).toHaveAttribute('aria-expanded', 'false');
	await toggle.click();
	await expect(toggle).toHaveAttribute('aria-expanded', 'true');
});
