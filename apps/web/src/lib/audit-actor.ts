/**
 * The two actions the Safe Browsing scanner writes, always without an actor
 * (`apps/api/internal/audit/audit.go`). The API refuses a missing actor on
 * every other action, which is what makes the action enough to tell the two
 * kinds of null apart.
 */
const SYSTEM_ACTIONS: ReadonlySet<string> = new Set(['link.flagged', 'link.unflagged']);

/** What the page can say about who wrote an entry. */
export type ActorDisplay =
	| { readonly email: string; readonly kind: 'member' }
	| { readonly kind: 'deletedAccount' }
	| { readonly kind: 'formerMember' }
	| { readonly kind: 'safeBrowsing' };

/**
 * Decides which of the four things the page can truthfully say about an
 * entry's actor.
 *
 * The distinction between "a former member" and "a deleted account" is the
 * point. "A former member" is a fact about this team — the membership was
 * deleted and the entries were deliberately not. "A deleted account" is a fact
 * about the person, and reaches us as a null id because
 * `audit_log.actor_user_id` is `on delete set null`. Showing one for the other
 * would tell a board that somebody left when they did not. A null id means a
 * third thing on the two Safe Browsing actions: no account wrote those, Google
 * Safe Browsing did.
 *
 * @param actorUserId - The entry's `actor_user_id`, absent when the account is gone or the system wrote the entry.
 * @param membersById - The team's current members, keyed by user id.
 * @param action - The entry's `action`, which tells the scanner's missing actor from a deleted account's.
 * @returns What to render for this actor.
 */
export function resolveActor(
	actorUserId: string | undefined,
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly.
	membersById: ReadonlyMap<string, string>,
	action: string,
): ActorDisplay {
	if (actorUserId === undefined) {
		return SYSTEM_ACTIONS.has(action) ? { kind: 'safeBrowsing' } : { kind: 'deletedAccount' };
	}

	const email = membersById.get(actorUserId);
	return email === undefined ? { kind: 'formerMember' } : { email, kind: 'member' };
}
