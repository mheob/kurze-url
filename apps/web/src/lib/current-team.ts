import type { Membership } from '../routes/_authed';
import { preferenceCookie } from './preferences';

/**
 * The same mechanism `preferences.ts` uses for language and theme, for the
 * same reason: a cookie is readable on the server during rendering, so the
 * redirect happens before the first paint rather than after a round trip.
 * localStorage cannot do that.
 */
export const TEAM_COOKIE = 'team';

/**
 * A private copy of `preferences.ts`'s own (unexported) `readCookie`, not an
 * import: that one is module-private parsing for `lang`/`theme` alone, and
 * duplicating four lines here keeps this module from reaching into another
 * module's internals for them.
 */
function readCookie(cookieHeader: string | undefined, name: string): string | undefined {
	if (!cookieHeader) return undefined;

	for (const part of cookieHeader.split(';')) {
		const [rawKey, ...rawValue] = part.split('=');
		if (rawKey?.trim() === name) return rawValue.join('=').trim();
	}

	return undefined;
}

/**
 * Validated against current memberships, not trusted outright: a cookie
 * naming a team the person was removed from would otherwise redirect them
 * into a not-found on every single visit — locking them out of their own
 * dashboard with no escape but clearing cookies, which nobody will guess.
 * Falls back to the first membership — arbitrary, but stable, and always
 * defined whenever there is at least one — when the cookie is absent, names
 * a team the caller no longer belongs to, or there simply is no cookie yet.
 */
export function resolveCurrentTeam(
	cookieHeader: string | undefined,
	memberships: Membership[],
): string | undefined {
	const remembered = readCookie(cookieHeader, TEAM_COOKIE);
	if (remembered && memberships.some((membership) => membership.team_id === remembered)) {
		return remembered;
	}

	return memberships[0]?.team_id;
}

export function teamCookie(teamId: string): string {
	return preferenceCookie(TEAM_COOKIE, teamId);
}
