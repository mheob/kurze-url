import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { waitForHydration } from './fixtures/hydration';
import { removeMembershipOnly, seedSecondMember, setFixtureTeamRole } from './fixtures/seed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- Playwright's own `Page`, nested
 * inside the fixture argument object each `test` callback destructures; it has many mutating
 * methods (`goto`, `click`, ...) and is not ours to edit. Same note as `audit-log.spec.ts` and
 * `stats.spec.ts`.
 */

test('lists the team members', async ({ page, teamSlug }) => {
	await page.goto(`/teams/${teamSlug}/members`);
	await expect(page.getByRole('heading', { name: 'Who has access' })).toBeVisible();
	// Not `getByText('Owner')`: the fixture team's one member is its sole owner,
	// so `member-list.tsx` renders that row's role as a *disabled* select
	// (`<option value="owner">Owner</option>`) plus the sibling sentence "A team
	// must always have at least one owner." — both contain "Owner" as a
	// case-insensitive substring, so a text match resolves to two elements and
	// fails strict mode. Worse, the sentence alone would satisfy a text match
	// even if role rendering were broken outright. Scoping to the table's own
	// combobox and reading its actual value proves the row rendered the wire
	// role instead: `MemberInviteForm`'s role select sits outside the table, so
	// this is the table's only one.
	await expect(page.getByRole('table').getByRole('combobox')).toHaveValue('owner');
});

test('adds an existing account and says nobody was notified', async ({
	page,
	teamId,
	teamSlug,
}) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		// Remove the membership again so the address is addable but its account
		// still exists — the whole point of this case is the no-mail path.
		await removeMembershipOnly(teamId, second.userId);

		await page.goto(`/teams/${teamSlug}/members`);
		// Not decorative: `goto` resolves before React hydrates this
		// `@tanstack/react-form` field — see `waitForHydration` and
		// `create-link.ts`'s identical wait on its own destination field.
		const email = page.getByLabel('Email address');
		await waitForHydration(email);
		await email.fill(second.email);
		await page.getByRole('button', { name: 'Add to team' }).click();

		await expect(page.getByRole('status')).toContainText('are not notified');
		// Not `getByText(second.email)`: `members.invitedAddedSilently`'s own
		// banner text contains the address too ("{{email}} is in the team
		// now..."), so a bare text match can resolve against the banner alone —
		// passing before the refetch ever puts a row in the table, which is not
		// what this case claims to prove. Scoping to a table row, the same way
		// `removes a member` below locates the row it acts on, asks the actual
		// question: did the member land in the table.
		await expect(page.getByRole('row').filter({ hasText: second.email })).toHaveCount(1);
	} finally {
		await second.cleanup();
	}
});

test('changes a member role', async ({ page, teamId, teamSlug }) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		await page.goto(`/teams/${teamSlug}/members`);
		// Not decorative: `goto` resolves before React hydrates this row's native
		// select — see `waitForHydration`. `selectOption` dispatches a native
		// `change` event the same way `fill` dispatches `input`, so it is just as
		// silent when nothing is listening yet.
		const roleSelect = page.getByLabel(`Role for ${second.email}`);
		await waitForHydration(roleSelect);
		await roleSelect.selectOption('editor');
		await expect(page.getByRole('status')).toContainText('Role updated');
	} finally {
		await second.cleanup();
	}
});

test('removes a member', async ({ page, teamId, teamSlug }) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		await page.goto(`/teams/${teamSlug}/members`);
		const row = page.getByRole('row').filter({ hasText: second.email });
		// Not decorative: `goto` resolves before React hydrates this button — see
		// `waitForHydration` and `links.spec.ts`'s identical wait on its own
		// remove button. A click in that window lands on a handler nothing has
		// attached yet.
		const remove = row.getByRole('button', { name: 'Remove' });
		await waitForHydration(remove);
		await remove.click();
		await page.getByRole('button', { name: 'Yes, remove them' }).click();

		// Not `getByText(second.email)`: on a successful removal the route
		// renders `<output>{t('members.removed', { email })}</output>`, whose
		// English value ("{{email}} was removed.") contains the address too —
		// a bare text match resolves against that banner as well as the row it
		// briefly coexists with, a strict-mode violation, and once the
		// invalidating refetch lands the banner alone still matches, so the
		// assertion never settles on "hidden". `row` already names the table
		// row this test acted on; asking whether it is gone is the actual
		// question "removes a member" claims to answer.
		await expect(row).toHaveCount(0);
	} finally {
		await second.cleanup();
	}
});

// The gate this page's whole permission model rests on: `membership.role`'s
// wire value reaching the literals `canManageMember` compares against. Nothing
// in the unit suite can check that, because it supplies the role itself.
test('gives a viewer the list and no controls', async ({ page, teamId, teamSlug }) => {
	await setFixtureTeamRole(teamId, 'viewer');
	const second = await seedSecondMember(teamId, 'editor');
	try {
		await page.goto(`/teams/${teamSlug}/members`);

		await expect(page.getByText(second.email)).toBeVisible();
		await expect(page.getByLabel('Email address')).toBeHidden();
		await expect(page.getByRole('button', { name: 'Remove' })).toHaveCount(0);
	} finally {
		await second.cleanup();
	}
});
