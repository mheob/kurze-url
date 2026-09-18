// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `shell.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';
import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own `Page`/`Browser`/`TestInfo`, nested inside the fixture argument object
 * each `test` callback destructures; each has mutating methods (`goto`, `fill`, `newContext`, ...)
 * and none of these types are ours to edit.
 */

test('creates a link and shows it in the list', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, { destinationUrl: 'https://example.org/a-page' });
});

test('warns that the short domain does not resolve', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, { destinationUrl: 'https://example.org/a-page' });
	await expect(page.getByRole('note')).toBeVisible();
});

test('has no accessibility violations on the list', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, { destinationUrl: 'https://example.org/a-page' });

	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});

/**
 * Drives `<LinkPasswordCard>` (the detail route) and the list's own badge
 * (`link-list.tsx`) end to end, through the real API — set then remove. The
 * password interstitial itself is deliberately out of reach here: Preview's
 * shared hostname is `short.invalid`, which does not resolve on purpose (see
 * `warns that the short domain does not resolve` above), so a redirect from
 * this suite can never reach it. That path stays covered by the Go tests
 * (`TestSetLinkPasswordInvalidatesTheRedirectCache` and its `Remove`
 * counterpart, `apps/api/internal/api/link_password_test.go`), which drive
 * `HandleRedirect` directly instead.
 *
 * "Kartoffelsalat!7" is the same password every other layer's tests use
 * (Go, `link-password.test.ts`, `link-password-card.test.tsx`) — it clears
 * the policy against this fixture's own team without coming near its slug,
 * destination, or name.
 */
test('protects a link with a password and removes it again', async ({ page, teamSlug }) => {
	const destination = `https://example.org/password-${Date.now()}`;
	await createLink(page, teamSlug, { destinationUrl: destination });

	// A fresh team's list holds exactly this one row, so `edit` resolves
	// without scoping it to the row's own text.
	await page.getByRole('link', { name: /edit/iu }).click();

	const password = page.getByLabel(/password/iu);
	await waitForHydration(password);
	await password.fill('Kartoffelsalat!7');
	await page.getByRole('button', { name: /protect this link/iu }).click();

	await expect(page.getByText('This link is protected by a password.')).toBeVisible();

	await page.goto(`/teams/${teamSlug}/links`);
	await expect(page.getByText('Password protected')).toBeVisible();

	await page.getByRole('link', { name: /edit/iu }).click();

	// The `goto` above resolves on `load`, so the list can be on screen before
	// React attaches — and a click on an unhydrated `<a href>` is an ordinary
	// browser navigation, which delivers this page server-rendered and
	// unhydrated in turn. Clicking the button in that window moves focus to it
	// and nothing else: no handler is attached, so the confirmation dialog
	// never opens and the wait below runs out against a page that looks
	// correct. Seen exactly that way in CI on 2026-09-18, with the button
	// `[active]` and no dialog in the failure snapshot.
	const remove = page.getByRole('button', { name: /remove protection/iu });
	await waitForHydration(remove);
	await remove.click();

	await page.getByRole('button', { name: /yes, remove it/iu }).click();

	await expect(page.getByText('This link is not protected.')).toBeVisible();

	await page.goto(`/teams/${teamSlug}/links`);
	await expect(page.getByText('Password protected')).toHaveCount(0);
});

/**
 * The dashboard side only. The image itself is deliberately not asserted
 * here — that is what `apps/api/internal/qr`'s decode test is for, which
 * reads the rendered code back and compares the decoded string. What this
 * covers is the wiring nothing else does: the preview reaching the page at
 * all, and the download control producing a file.
 */
test('downloads a link’s QR code', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, { destinationUrl: `https://example.org/qr-${Date.now()}` });

	await page.getByRole('link', { name: /edit/iu }).click();

	// Longer than the suite-wide fifteen seconds, because this element waits on
	// more than one round trip. The preview is fetched client-side after
	// hydration by a query that keeps TanStack Query's default `retry: 3` with
	// exponential backoff, so a single failed attempt adds seven seconds of
	// backoff and three further requests before the <img> can appear, with the
	// loading line on screen throughout. This expectation flaked on a branch
	// that touches nothing near the QR card, which is what the arithmetic
	// predicts.
	const preview = page.getByRole('img', { name: /preview of this link/iu });
	await expect(preview).toBeVisible({ timeout: 30_000 });

	const downloadPromise = page.waitForEvent('download');
	await page.getByRole('button', { name: /^download$/iu }).click();
	const download = await downloadPromise;

	expect(download.suggestedFilename()).toMatch(/\.svg$/u);
});

test('sends a signed-out visitor to login', async ({ browser, teamSlug }, testInfo) => {
	// A fresh context carries none of the `teamSlug` fixture's session cookies —
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
	await page.goto(`/teams/${teamSlug}/links`);

	await expect(page).toHaveURL(/\/login$/u);

	await context.close();
});
