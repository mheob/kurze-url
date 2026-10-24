import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- Playwright's own `Page`, nested
 * inside the fixture argument object each `test` callback destructures; it has many mutating
 * methods (`goto`, `click`, ...) and is not ours to edit. Same note as `members.spec.ts`.
 */

/**
 * Signing out navigates to `/` on the client, so `/`'s loader asks the server who is signed in
 * through a server function, and finds nobody. Until 2026-10-24 that answer crossed the boundary
 * as a thrown `UnauthenticatedError`, which TanStack's transport reduces to its message, so the
 * loader no longer recognised it and production showed the error page instead of the homepage.
 * A server-rendered visit to `/` never goes through that transport, which is why only a
 * client-side navigation like this one catches it.
 */
test('lands on the homepage after signing out', async ({ page, teamSlug }) => {
	await page.goto(`/teams/${teamSlug}/links`);

	const signOut = page.getByRole('button', { exact: true, name: 'Sign out' });
	await waitForHydration(signOut);
	await signOut.click();

	await expect(
		page.getByRole('heading', { level: 1, name: 'Short links for your association' }),
	).toBeVisible();
	expect(new URL(page.url()).pathname).toBe('/');
});
