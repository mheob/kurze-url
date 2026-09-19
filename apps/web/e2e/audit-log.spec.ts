import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';
import { waitForHydration } from './fixtures/hydration';

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
	const destinationUrl = `https://example.org/audit-${Date.now()}`;
	await createLink(page, teamSlug, { destinationUrl });

	await page.goto(`/teams/${teamSlug}/audit-log`);

	const toggle = page.getByRole('button', { name: /^Details/u }).first();
	// Not decorative: `goto` resolves before React hydrates this button, and a
	// click before hydration lands on a handler nothing has attached yet — the
	// exact failure `links.spec.ts` documents at its own `waitForHydration`
	// call on `remove`. It fails silently: the click appears to succeed, and
	// the following assertion times out against a page that looks correct.
	await waitForHydration(toggle);
	await expect(toggle).toHaveAttribute('aria-expanded', 'false');
	await toggle.click();
	await expect(toggle).toHaveAttribute('aria-expanded', 'true');

	// `aria-expanded` and the details row's own `isOpen && hasMetadata` render
	// gate are two independent expressions of the same state — a bug that
	// broke rendering inside `MetadataList` itself, leaving both of those
	// correct, would pass the two assertions above unchanged. Asserting a
	// real disclosed value proves the row actually rendered: `createLink`'s
	// own `link.created` row carries this exact destination as its
	// `destination_url` metadata value (`apps/api/internal/api/links.go:551-556`).
	await expect(page.getByText(destinationUrl)).toBeVisible();
});
