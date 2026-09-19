import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { createLink } from './fixtures/create-link';
import { waitForHydration } from './fixtures/hydration';
import { setFixtureTeamRole } from './fixtures/seed';

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

/**
 * Both gates, against the real API, for the one role that has to meet them:
 * a member of the team who is below admin.
 *
 * Neither half is reachable from a unit test. The sidebar's gate
 * (`app-sidebar.tsx`) is the only reader of `Membership.role` in the whole
 * app, and `Membership.role` is typed `string` — every unit test and every
 * Storybook fixture hands it a role literal written by hand, so nothing
 * anywhere proves that what `GET /v1/me` actually emits still matches the
 * `'admin'`/`'owner'` this comparison is written against. It does today
 * (`me.go` serialises `authz.Role`'s own constants), but a rename on the Go
 * side would hide the entry for every admin in every Verein with no test,
 * type or build failing. This case is the only thing that would notice.
 *
 * The refusal is the second half and is asserted here for a related reason:
 * every other test of that state feeds the loader a mocked error, so the
 * 403 that produces it — `authz.AdminScope`, which answers a non-member 404
 * and a member below admin 403 — is otherwise taken on trust. Were it a 404
 * instead, an editor would land on "page not found" for a team they are
 * looking at, and `statusOf(error) === 403` in `loadAuditLogPage` would be
 * dead code that still typechecks.
 *
 * The visible "Links" entry is not decoration either: without it, a sidebar
 * that failed to render at all would satisfy the absence assertion below
 * perfectly.
 */
test('hides the history from a member below admin', async ({ page, teamId, teamSlug }) => {
	await setFixtureTeamRole(teamId, 'editor');

	await page.goto(`/teams/${teamSlug}/links`);

	const sections = page.getByRole('navigation', { name: 'Sections' });
	await expect(sections.getByRole('link', { name: 'Links' })).toBeVisible();
	await expect(sections.getByRole('link', { name: 'History' })).toHaveCount(0);

	await page.goto(`/teams/${teamSlug}/audit-log`);

	await expect(
		page.getByRole('heading', { level: 2, name: 'This part of the team is for admins' }),
	).toBeVisible();
});
