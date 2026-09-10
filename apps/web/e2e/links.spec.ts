// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `shell.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, type Page } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

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
async function createLink(page: Page, teamSlug: string, destinationUrl: string): Promise<void> {
	await page.goto(`/teams/${teamSlug}/links/new`);

	// Not decorative: `goto` resolves on `load`, which this server-rendered
	// form reaches well before React wires it up, and a value typed in that
	// window never reaches React's state — the form then submits empty. See
	// `waitForHydration`.
	const destination = page.getByLabel(/destination/i);
	await waitForHydration(destination);

	await destination.fill(destinationUrl);
	await page.getByRole('button', { name: /save/i }).click();

	// The create route navigates back to the list on success, so waiting for
	// the destination to appear also confirms that redirect happened.
	await expect(page.getByText(destinationUrl)).toBeVisible();
}

test('creates a link and shows it in the list', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, 'https://example.org/a-page');
});

test('warns that the short domain does not resolve', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, 'https://example.org/a-page');
	await expect(page.getByRole('note')).toBeVisible();
});

test('has no accessibility violations on the list', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, 'https://example.org/a-page');

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
	await createLink(page, teamSlug, destination);

	// A fresh team's list holds exactly this one row, so `edit` resolves
	// without scoping it to the row's own text.
	await page.getByRole('link', { name: /edit/i }).click();

	const password = page.getByLabel(/password/i);
	await waitForHydration(password);
	await password.fill('Kartoffelsalat!7');
	await page.getByRole('button', { name: /protect this link/i }).click();

	await expect(page.getByText('This link is protected by a password.')).toBeVisible();

	await page.goto(`/teams/${teamSlug}/links`);
	await expect(page.getByText('Password protected')).toBeVisible();

	await page.getByRole('link', { name: /edit/i }).click();
	await page.getByRole('button', { name: /remove protection/i }).click();
	await page.getByRole('button', { name: /yes, remove it/i }).click();

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
	await createLink(page, teamSlug, `https://example.org/qr-${Date.now()}`);

	await page.getByRole('link', { name: /edit/i }).click();

	const preview = page.getByRole('img', { name: /preview of this link/i });
	await expect(preview).toBeVisible();

	const download = page.waitForEvent('download');
	await page.getByRole('button', { name: /^download$/i }).click();

	expect((await download).suggestedFilename()).toMatch(/\.svg$/);
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

	await expect(page).toHaveURL(/\/login$/);

	await context.close();
});
