import { describe, expect, it } from 'vitest';

import {
	canManageMember,
	isSoleOwner,
	isTeamRole,
	rolesAssignableBy,
	TEAM_ROLES,
} from './team-roles';

describe('team roles', () => {
	it('lists the four roles the database allows, in ascending privilege', () => {
		expect(TEAM_ROLES).toStrictEqual(['viewer', 'editor', 'admin', 'owner']);
	});

	it('rejects a value outside the vocabulary', () => {
		expect(isTeamRole('viewer')).toBe(true);
		expect(isTeamRole('superuser')).toBe(false);
	});

	it('lets an admin grant every role except owner', () => {
		expect(rolesAssignableBy('admin')).toStrictEqual(['viewer', 'editor', 'admin']);
	});

	it('lets an owner grant every role', () => {
		expect(rolesAssignableBy('owner')).toStrictEqual(['viewer', 'editor', 'admin', 'owner']);
	});

	it('lets nobody below admin grant anything', () => {
		expect(rolesAssignableBy('editor')).toStrictEqual([]);
		expect(rolesAssignableBy('viewer')).toStrictEqual([]);
	});

	// An unknown role must not become a permission. The API answers 403 either
	// way, but an interface that offers a control the server will refuse is a
	// bug report waiting to be filed.
	it('grants nothing for a role it does not recognise', () => {
		expect(rolesAssignableBy('superuser')).toStrictEqual([]);
		expect(canManageMember('superuser', 'viewer')).toBe(false);
		expect(canManageMember('admin', 'superuser')).toBe(false);
	});

	it('lets an admin manage anyone below owner', () => {
		expect(canManageMember('admin', 'viewer')).toBe(true);
		expect(canManageMember('admin', 'admin')).toBe(true);
		expect(canManageMember('admin', 'owner')).toBe(false);
	});

	it('lets an owner manage anyone, including another owner', () => {
		expect(canManageMember('owner', 'owner')).toBe(true);
	});

	it('lets nobody below admin manage anyone', () => {
		expect(canManageMember('editor', 'viewer')).toBe(false);
	});

	// The server holds the real lock (refuseLastOwner takes a row lock inside
	// the mutation's transaction). This only stops the interface offering a
	// control that is certain to be refused.
	it('recognises the only owner', () => {
		const members = [
			{ role: 'owner', user_id: 'u1' },
			{ role: 'admin', user_id: 'u2' },
		];
		expect(isSoleOwner(members, 'u1')).toBe(true);
		expect(isSoleOwner(members, 'u2')).toBe(false);
	});

	it('does not call one of two owners the only one', () => {
		const members = [
			{ role: 'owner', user_id: 'u1' },
			{ role: 'owner', user_id: 'u2' },
		];
		expect(isSoleOwner(members, 'u1')).toBe(false);
	});
});
