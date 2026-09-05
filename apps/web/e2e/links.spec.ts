// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `shell.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, type Page } from '@playwright/test';

import { test } from './fixtures/auth';

/**
 * Shared by every test below that needs a non-empty list. `LinkList`'s own
 * empty-state branch (`src/components/link-list.tsx`) returns before
 * rendering `<ShortUrlNotice>` at all, so a freshly provisioned team — which
 * starts with zero links — would make "warns that the short domain does not
 * resolve" fail for the wrong reason: there being nothing to warn about, not
 * the warning itself being broken. Creating a real link first is what makes
 * that assertion, and the accessibility scan below it, exercise the list's
 * actual populated markup rather than its empty one.
 */
async function createLink(page: Page, teamId: string, destinationUrl: string): Promise<void> {
	await page.goto(`/teams/${teamId}/links/new`);
	await page.getByLabel(/destination/i).fill(destinationUrl);
	await page.getByRole('button', { name: /save/i }).click();

	// The create route navigates back to the list on success, so waiting for
	// the destination to appear also confirms that redirect happened.
	await expect(page.getByText(destinationUrl)).toBeVisible();
}

test('creates a link and shows it in the list', async ({ page, teamId }) => {
	await createLink(page, teamId, 'https://example.org/a-page');
});

test('warns that the short domain does not resolve', async ({ page, teamId }) => {
	await createLink(page, teamId, 'https://example.org/a-page');
	await expect(page.getByRole('note')).toBeVisible();
});

test('has no accessibility violations on the list', async ({ page, teamId }) => {
	await createLink(page, teamId, 'https://example.org/a-page');

	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});

test('sends a signed-out visitor to login', async ({ browser, teamId }, testInfo) => {
	// A fresh context carries none of the `teamId` fixture's session cookies —
	// nothing has signed it in, so there is nothing to sign it out of.
	//
	// `testInfo.project.use`, not a bare `browser.newContext()`: a manually
	// created context does not inherit this project's `use` options the way
	// the built-in `context`/`page` fixtures do, and those options are exactly
	// `baseURL` and the Vercel protection-bypass header (see
	// `playwright.config.ts`'s own docstring). Skipping it would make this
	// assertion fail against a real protected preview for an unrelated reason
	// — Vercel's own SSO redirect, never `/login` — rather than proving
	// anything about this app's guard.
	//
	// Asserting the destination, not merely that rendering failed: a guard
	// that 500s would satisfy a weaker check, the exact trap this suite exists
	// to close (see `fixtures/auth.ts`'s own docstring on the same point).
	const context = await browser.newContext(testInfo.project.use);
	const page = await context.newPage();
	await page.goto(`/teams/${teamId}/links`);

	await expect(page).toHaveURL(/\/login$/);

	await context.close();
});
