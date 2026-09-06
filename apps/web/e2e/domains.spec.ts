// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `links.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, type Page } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

/**
 * Claims a domain and waits for it to reappear in the list as `pending`.
 * `claimMutation`'s `onSuccess` (`teams.$teamId.domains.tsx`) invalidates the
 * domains query, so waiting for the freshly claimed hostname's own heading
 * also confirms that refetch landed — the state the DNS records table and
 * the "Check now" control both depend on being rendered at all.
 *
 * `domain.hostname` is globally unique (`domain` has a bare `unique` on the
 * column, not scoped by team — `supabase/migrations/20260902075125_initial_schema.sql`),
 * and this suite runs against a shared preview database, so every caller
 * gets its own hostname built from `Date.now()` rather than a fixed literal:
 * a rerun of this file, or `i18n.spec.ts`'s own claim, must not collide with
 * this one.
 */
async function claimDomain(page: Page, teamId: string): Promise<string> {
	await page.goto(`/teams/${teamId}/domains`);

	// Not decorative: `goto` resolves on `load`, which this server-rendered
	// form reaches well before React wires it up, and a value typed in that
	// window never reaches React's state — the form then submits empty. See
	// `waitForHydration`.
	const hostname = page.getByLabel(/hostname/i);
	await waitForHydration(hostname);

	const claimed = `links-${Date.now()}.e2e.test`;
	await hostname.fill(claimed);
	await page.getByRole('button', { name: /add domain/i }).click();

	// `getByText` matches substrings, and the claimed hostname also appears
	// inside the TXT challenge name and inside the "Delete <hostname>"
	// button — asserting a level-2 heading is what resolves to exactly one
	// element instead of a strict-mode violation, since `domain-list.tsx`
	// renders the bare hostname only as `<h2>{domain.hostname}</h2>`.
	await expect(page.getByRole('heading', { level: 2, name: claimed })).toBeVisible();

	return claimed;
}

test('claims a domain and shows the records to create', async ({ page, teamId }) => {
	const claimed = await claimDomain(page, teamId);

	// The TXT challenge name is the one string on this screen that proves the
	// claim actually reached the API and came back with real DNS instructions,
	// rather than the form merely accepting input.
	await expect(page.getByText(`_kurze-url-challenge.${claimed}`)).toBeVisible();
});

test('says which half of verification is missing', async ({ page, teamId }) => {
	// The success path cannot be exercised here: it needs real DNS records,
	// under our control, on a hostname that actually points at this preview
	// deployment, and this environment has neither. That is a limitation of
	// the environment, not an oversight — the failure path below still proves
	// the whole chain (form, server function, API, DNS lookup, and the reason
	// rendered back) is connected, which is the part that actually breaks.
	await claimDomain(page, teamId);

	await page.getByRole('button', { name: /check now/i }).click();
	await expect(page.getByText(/TXT record is not visible yet/i)).toBeVisible();
});

test('has no accessibility violations on the domains screen', async ({ page, teamId }) => {
	// Claimed first, the same reason `links.spec.ts` creates a link before its
	// own scan: `DomainList`'s empty-state branch returns before rendering the
	// DNS records table, the "Check now" control, or the delete control at
	// all, so scanning right after `goto` would only ever exercise the claim
	// form, never the populated list this feature actually adds.
	await claimDomain(page, teamId);

	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});
