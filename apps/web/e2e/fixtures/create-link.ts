import { expect, type Page } from '@playwright/test';

import { waitForHydration } from './hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- `page` is Playwright's own `Page`,
 * whose methods (`goto`, `fill`, `click`, ...) mutate; that type isn't ours to edit. Same note as
 * the specs that call this.
 */

interface CreateLinkOptions {
	/** Whether to leave click counting on. False unchecks it before saving. */
	readonly countClicks?: boolean;
	/** The URL the new link should redirect to. */
	readonly destinationUrl: string;
}

/**
 * Creates one link through the form, the way a person would, and returns the
 * short URL the API generated for it.
 *
 * Three specs need a link before they can assert anything: `links.spec.ts`
 * (a populated list renders markup the empty state never reaches),
 * `i18n.spec.ts` (same, plus the statistics page belongs to a link) and
 * `stats.spec.ts` (nothing to record clicks against otherwise). It lives here
 * rather than in any one of them because a third copy is where a shared hazard
 * starts drifting apart.
 *
 * The short URL is returned rather than reconstructed: its slug is generated
 * server-side, so there is no formula to build it from. It is the visible text
 * of the one element on the list whose `href` is an absolute http(s) URL —
 * every other link there is a TanStack Router `<Link>` to an app-relative
 * path. A locator that must match exactly one element fails loudly rather than
 * silently if that ever stops holding.
 *
 * @param page - The page to drive; must already be authenticated.
 * @param teamSlug - The team to create the link under; used to build the create-link URL.
 * @param options - The destination, and any form choice beyond it.
 * @returns The new link's short URL, as the list renders it.
 */
export async function createLink(
	page: Page,
	teamSlug: string,
	options: CreateLinkOptions,
): Promise<string> {
	await page.goto(`/teams/${teamSlug}/links/new`);

	// Not decorative: `goto` resolves on `load`, which this server-rendered
	// form reaches well before React wires it up, and a value typed in that
	// window never reaches React's state — the form then submits empty. See
	// `waitForHydration`.
	const destination = page.getByLabel(/destination/iu);
	await waitForHydration(destination);

	await destination.fill(options.destinationUrl);

	if (options.countClicks === false) {
		// Base UI renders this as a visible `role="checkbox"` span beside a
		// hidden native input (see `link-form.tsx`'s own note on why the `id`
		// there is load-bearing), so it is reached by role rather than as an
		// `input[type=checkbox]`.
		await page.getByRole('checkbox', { name: /count clicks/iu }).uncheck();
	}

	await page.getByRole('button', { name: /save/iu }).click();

	// The create route navigates back to the list on success, so waiting for
	// the destination to appear also confirms that redirect happened.
	await expect(page.getByText(options.destinationUrl)).toBeVisible();

	// `innerText` is the rendered, visible text, which is what `i18n.spec.ts` compares this value
	// against; `textContent` reads raw text-node content and could not be excluded reliably there.
	// oxlint-disable-next-line unicorn/prefer-dom-node-text-content -- see the note just above.
	return page.locator('a[href^="http"]').innerText();
}
