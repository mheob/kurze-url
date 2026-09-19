/** What the page can say about who wrote an entry. */
export type ActorDisplay =
	| { readonly email: string; readonly kind: 'member' }
	| { readonly kind: 'deletedAccount' }
	| { readonly kind: 'formerMember' };

/**
 * Decides which of the three things the page can truthfully say about an
 * entry's actor.
 *
 * The distinction between the last two is the point. "A former member" is a
 * fact about this team — the membership was deleted and the entries were
 * deliberately not. "A deleted account" is a fact about the person, and
 * reaches us as a null id because `audit_log.actor_user_id` is
 * `on delete set null`. Showing one for the other would tell a board that
 * somebody left when they did not.
 *
 * @param actorUserId - The entry's `actor_user_id`, absent when the account is gone.
 * @param membersById - The team's current members, keyed by user id.
 * @returns What to render for this actor.
 */
export function resolveActor(
	actorUserId: string | undefined,
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlyMap` is TypeScript's immutable map type; the rule does not recognise it as readonly.
	membersById: ReadonlyMap<string, string>,
): ActorDisplay {
	if (actorUserId === undefined) return { kind: 'deletedAccount' };

	const email = membersById.get(actorUserId);
	return email === undefined ? { kind: 'formerMember' } : { email, kind: 'member' };
}
