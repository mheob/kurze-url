// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `folders.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own `Page`, nested inside the fixture argument object the `test` callback
 * destructures; it has many mutating methods (`goto`/`fill`/`click`, ...) and that type isn't ours
 * to edit. Same note as `folders.spec.ts`.
 */

/**
 * Drives the whole tags feature end to end, through the real API: creating a
 * tag on its page, putting it on a new link next to a second tag created from
 * the link form's picker, filtering the link list by it (and by a folder at
 * the same time), renaming the tag in place, deleting it, and confirming the
 * link is kept with only the other tag left on it.
 *
 * Names are built from `Date.now()`, for the reason `folders.spec.ts` gives:
 * this suite runs against a shared preview database, so a fixed literal would
 * collide with a rerun of this same test. The auth fixture provisions a fresh
 * team per test, so the tags created here do not pile up between tests.
 */
test('tags a link, filters by tag and by folder, renames and deletes the tag', async ({
	page,
	teamSlug,
}) => {
	const tagName = `Presse ${Date.now()}`;
	const created = `Vorstand ${Date.now()}`;
	const destinationUrl = `https://example.org/tags-${Date.now()}`;

	await page.goto(`/teams/${teamSlug}/tags`);

	// Not decorative: `goto` resolves on `load`, which this server-rendered
	// form reaches well before React hydrates it — see `waitForHydration`.
	const nameField = page.getByLabel('Tag name');
	await waitForHydration(nameField);
	await nameField.fill(tagName);
	await page.getByRole('button', { name: 'Create tag' }).click();
	await expect(page.getByRole('link', { name: tagName })).toBeVisible();

	const tagsPageResults = await new AxeBuilder({ page }).analyze();
	expect(tagsPageResults.violations).toStrictEqual([]);

	await page.goto(`/teams/${teamSlug}/links/new`);
	const destination = page.getByLabel(/destination/iu);
	await waitForHydration(destination);
	await destination.fill(destinationUrl);

	// Both tags go in through the picker: the first is picked from the team's
	// existing tags, narrowed by typing its first six characters; the second
	// does not exist yet, so the picker's create entry makes it on the spot.
	const picker = page.getByRole('combobox', { name: 'Tags' });
	await waitForHydration(picker);
	await picker.fill(tagName.slice(0, 6));
	await page.getByRole('option', { name: tagName }).click();
	await picker.fill(created);
	await page.getByRole('option', { name: `Create tag "${created}"` }).click();
	await expect(page.getByRole('button', { name: `Remove tag ${created}` })).toBeVisible();

	// Not decorative either: while the picker's list is open, Base UI hides
	// everything outside it from assistive technology with `aria-hidden`, and
	// axe then reports `aria-hidden-focus` for the form's focusable controls —
	// a finding about the open list, not about the form. Escape closes the
	// list, and the picker keeps the chips when it does (Base UI's own Escape
	// would clear them all with the list closed; `tag-picker.tsx` cancels
	// that), which the two chip assertions below pin down before the scan.
	await picker.press('Escape');
	await expect(picker).toHaveAttribute('aria-expanded', 'false');
	await expect(page.getByRole('button', { name: `Remove tag ${tagName}` })).toBeVisible();
	await expect(page.getByRole('button', { name: `Remove tag ${created}` })).toBeVisible();

	const formResults = await new AxeBuilder({ page }).analyze();
	expect(formResults.violations).toStrictEqual([]);

	await page.getByRole('button', { name: /save/iu }).click();

	// The create route navigates back to the list on success, so waiting for
	// the destination to appear also confirms that redirect happened. Without
	// it, the `goto` below could abandon the request still in flight.
	await expect(page.getByText(destinationUrl)).toBeVisible();

	await page.goto(`/teams/${teamSlug}/links`);
	const tagFilter = page.getByRole('combobox', { name: 'Tag' });
	await waitForHydration(tagFilter);
	await tagFilter.selectOption({ label: tagName });
	await expect(page.getByRole('cell', { name: destinationUrl })).toBeVisible();
	await expect(page.getByText(`Tag: ${tagName}`)).toBeVisible();

	const listResults = await new AxeBuilder({ page }).analyze();
	expect(listResults.violations).toStrictEqual([]);

	// The two filters combine: choosing a folder keeps the tag. The link has no
	// folder, so it stays in the list under "No folder" as well. The URL is
	// what proves both filters are active; the cell alone was already visible
	// before the second filter was applied.
	await page.getByRole('combobox', { name: 'Folder' }).selectOption({ label: 'No folder' });
	await expect(page).toHaveURL(/folder=none/u);
	await expect(page).toHaveURL(/tag=/u);
	await expect(page.getByRole('cell', { name: destinationUrl })).toBeVisible();

	await page.goto(`/teams/${teamSlug}/tags`);
	await waitForHydration(page.getByLabel('Tag name'));
	await page.getByRole('button', { name: `Rename tag ${tagName}` }).click();

	// Scoped to the row's own `<li>`: the top-level create form on this same
	// page shares the identical "Tag name" label, so an unscoped `getByLabel`
	// would be a strict-mode violation the moment a tag exists to rename.
	const renameField = page.getByRole('listitem').getByLabel('Tag name');
	const renamed = `${tagName} 2`;
	await renameField.fill(renamed);
	await renameField.press('Enter');

	await expect(page.getByRole('link', { name: renamed })).toBeVisible();

	await page.getByRole('button', { name: `Delete tag ${renamed}` }).click();
	await page.getByRole('button', { name: /yes, delete it/iu }).click();
	await expect(page.getByRole('link', { name: renamed })).toHaveCount(0);

	// Deleting a tag keeps the link and its other tags (`tags.deleteQuestion`):
	// the row has lost the deleted tag's chip and kept the one created from the
	// picker. Found by the link's own destination URL, not by position, because
	// this suite runs against a shared preview database.
	await page.goto(`/teams/${teamSlug}/links`);
	const row = page.getByRole('row').filter({ hasText: destinationUrl });
	await expect(row.getByRole('link', { name: created })).toBeVisible();
	await expect(row.getByRole('link', { name: renamed })).toHaveCount(0);
});
