import { describe, expect, it } from 'vitest';

import { resolveCurrentTeam, TEAM_COOKIE, teamCookie } from './current-team';

const memberships = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', slug: 'verein-b', team_id: 'b' },
];

describe('resolveCurrentTeam', () => {
	it('returns the team named by the cookie', () => {
		expect(resolveCurrentTeam('team=verein-b', memberships)).toBe('verein-b');
	});

	it('finds its cookie among others', () => {
		expect(resolveCurrentTeam('lang=de; team=verein-b; theme=dark', memberships)).toBe('verein-b');
	});

	it('falls back to the first membership when there is no cookie', () => {
		expect(resolveCurrentTeam(undefined, memberships)).toBe('verein-a');
	});

	it('ignores a cookie naming a team you no longer belong to', () => {
		// Otherwise removal from a team locks someone out of their own
		// dashboard: every visit redirects to a not-found, and the only escape
		// is clearing cookies — which nobody will guess.
		expect(resolveCurrentTeam('team=gone', memberships)).toBe('verein-a');
	});

	it('returns undefined when there are no memberships at all', () => {
		expect(resolveCurrentTeam('team=verein-a', [])).toBeUndefined();
	});
});

describe('teamCookie', () => {
	it('writes a path-scoped cookie', () => {
		expect(teamCookie('verein-b')).toContain(`${TEAM_COOKIE}=verein-b`);
		expect(teamCookie('verein-b')).toContain('Path=/');
	});
});
