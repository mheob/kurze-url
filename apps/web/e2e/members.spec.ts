import { expect } from '@playwright/test';

import { test } from './fixtures/auth';
import { removeMembershipOnly, seedSecondMember, setFixtureTeamRole } from './fixtures/seed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- Playwright's own `Page`, nested
 * inside the fixture argument object each `test` callback destructures; it has many mutating
 * methods (`goto`, `click`, ...) and is not ours to edit. Same note as `audit-log.spec.ts` and
 * `stats.spec.ts`.
 */

test('lists the team members', async ({ page, teamSlug }) => {
	await page.goto(`/teams/${teamSlug}/members`);
	await expect(page.getByRole('heading', { name: 'Who has access' })).toBeVisible();
	await expect(page.getByText('Owner')).toBeVisible();
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
		await page.getByLabel('Email address').fill(second.email);
		await page.getByRole('button', { name: 'Add to team' }).click();

		await expect(page.getByRole('status')).toContainText('are not notified');
		await expect(page.getByText(second.email)).toBeVisible();
	} finally {
		await second.cleanup();
	}
});

test('changes a member role', async ({ page, teamId, teamSlug }) => {
	const second = await seedSecondMember(teamId, 'viewer');
	try {
		await page.goto(`/teams/${teamSlug}/members`);
		await page.getByLabel(`Role for ${second.email}`).selectOption('editor');
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
		await row.getByRole('button', { name: 'Remove' }).click();
		await page.getByRole('button', { name: 'Yes, remove them' }).click();

		await expect(page.getByText(second.email)).toBeHidden();
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
