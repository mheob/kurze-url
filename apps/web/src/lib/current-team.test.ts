import { describe, expect, it } from 'vitest';

import { resolveCurrentTeam, TEAM_COOKIE, teamCookie } from './current-team';

const memberships = [
	{ name: 'Verein A', role: 'owner', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', team_id: 'b' },
];

describe('resolveCurrentTeam', () => {
	it('returns the team named by the cookie', () => {
		expect(resolveCurrentTeam('team=b', memberships)).toBe('b');
	});

	it('finds its cookie among others', () => {
		expect(resolveCurrentTeam('lang=de; team=b; theme=dark', memberships)).toBe('b');
	});

	it('falls back to the first membership when there is no cookie', () => {
		expect(resolveCurrentTeam(undefined, memberships)).toBe('a');
	});

	it('ignores a cookie naming a team you no longer belong to', () => {
		// Otherwise removal from a team locks someone out of their own
		// dashboard: every visit redirects to a not-found, and the only escape
		// is clearing cookies — which nobody will guess.
		expect(resolveCurrentTeam('team=gone', memberships)).toBe('a');
	});

	it('returns undefined when there are no memberships at all', () => {
		expect(resolveCurrentTeam('team=a', [])).toBeUndefined();
	});
});

describe('teamCookie', () => {
	it('writes a path-scoped cookie', () => {
		expect(teamCookie('b')).toContain(`${TEAM_COOKIE}=b`);
		expect(teamCookie('b')).toContain('Path=/');
	});
});
