// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `links.spec.ts` and
// `domains.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own `Page`, nested inside the fixture argument object the `test` callback
 * destructures; it has many mutating methods (`goto`/`fill`/`click`, ...) and that type isn't ours
 * to edit. Same note as `links.spec.ts` and `domains.spec.ts`.
 */

/**
 * Drives the whole folders feature end to end, through the real API: filing a
 * new link into a freshly created folder, filtering the link list by it,
 * renaming the folder in place, deleting it, and confirming the link it held
 * is kept rather than lost (`folders.deleteQuestion`) — filed under "no
 * folder" once its folder is gone.
 *
 * `name` is built from `Date.now()`, the same reason `domains.spec.ts`'s
 * `claimDomain` builds its own hostname that way: this suite runs against a
 * shared preview database, so a fixed literal would collide with a rerun of
 * this same test.
 */
test('files a link into a folder, filters by it, renames and deletes it', async ({
	page,
	teamSlug,
}) => {
	const name = `Sommerfest ${Date.now()}`;

	await page.goto(`/teams/${teamSlug}/folders`);

	// Not decorative: `goto` resolves on `load`, which this server-rendered
	// form reaches well before React hydrates it — see `waitForHydration`.
	const field = page.getByLabel('Folder name');
	await waitForHydration(field);
	await field.fill(name);
	await page.getByRole('button', { name: 'Create folder' }).click();

	const folderLink = page.getByRole('link', { name });
	await expect(folderLink).toBeVisible();

	const createResults = await new AxeBuilder({ page }).analyze();
	expect(createResults.violations).toEqual([]);

	// Each folder name links to its own filtered link list (`folder-list.tsx`).
	await folderLink.click();
	await expect(page.getByText(`Folder: ${name}`)).toBeVisible();

	// Scoped to the page's own `<main>` landmark, not the first match anywhere
	// on the page: a sidebar or header link could also match a loose "create"
	// pattern first. `link-list.tsx` renders this control with the real,
	// translated `links.create` text ("Create link"), not "New link".
	await page.getByRole('main').getByRole('link', { name: 'Create link' }).click();

	// Confirms the "Create link" navigation carried the active folder filter
	// through as `?folder=<id>` (`link-list.tsx`'s own `newLinkSearch`) and
	// that `initialFolderId` (`teams.$teamSlug.links.new.tsx`) preselected it
	// on the create form — before this test does anything else that could
	// make that preselection look correct for the wrong reason.
	const destination = page.getByLabel(/destination/iu);
	await waitForHydration(destination);
	await expect(page.getByRole('combobox', { name: 'Folder' })).toHaveValue(/.+/u);

	const destinationUrl = `https://example.org/folders-${Date.now()}`;
	await destination.fill(destinationUrl);
	await page.getByRole('button', { name: /save/iu }).click();

	// The create route navigates back to the plain, unfiltered list on
	// success, so waiting for the destination to appear also confirms that
	// redirect happened — same reasoning as `fixtures/create-link.ts`'s own
	// wait.
	await expect(page.getByText(destinationUrl)).toBeVisible();

	await page.goto(`/teams/${teamSlug}/links`);
	const filter = page.getByRole('combobox', { name: 'Folder' });
	await waitForHydration(filter);
	await filter.selectOption({ label: name });
	await expect(page.getByRole('cell').getByRole('link', { name })).toBeVisible();

	const filteredResults = await new AxeBuilder({ page }).analyze();
	expect(filteredResults.violations).toEqual([]);

	await page.goto(`/teams/${teamSlug}/folders`);
	await waitForHydration(page.getByLabel('Folder name'));
	await page.getByRole('button', { name: `Rename folder ${name}` }).click();

	// Scoped to the row's own `<li>`: the top-level create form on this same
	// page shares the identical "Folder name" label, so an unscoped
	// `getByLabel` would be a strict-mode violation the moment a folder exists
	// to rename.
	const renameField = page.getByRole('listitem').getByLabel('Folder name');
	const renamed = `${name} 2`;
	await renameField.fill(renamed);
	await renameField.press('Enter');

	await expect(page.getByRole('link', { name: renamed })).toBeVisible();

	await page.getByRole('button', { name: `Delete folder ${renamed}` }).click();
	await page.getByRole('button', { name: /yes, delete it/iu }).click();
	await expect(page.getByRole('link', { name: renamed })).toHaveCount(0);

	// Deleting a folder keeps its links, unfiled (`folders.deleteQuestion`) —
	// the link this test created above should now show up under "no folder"
	// rather than disappearing.
	await page.goto(`/teams/${teamSlug}/links?folder=none`);
	await expect(page.getByRole('cell', { name: 'No folder' }).first()).toBeAttached();
});
