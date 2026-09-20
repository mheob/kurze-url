/**
 * The four values `team_member.role` allows, in ascending privilege — the
 * same order and the same spelling as the database's own check constraint
 * (`supabase/migrations/20260902075125_initial_schema.sql:18`) and
 * `authz.Role` in the API. Kept as one list so the select, the permission
 * rules and their tests cannot drift from each other.
 */
const TEAM_ROLES = ['viewer', 'editor', 'admin', 'owner'] as const;

type TeamRole = (typeof TEAM_ROLES)[number];

/** The minimum a member needs before any of the controls on this page exist. */
const ADMIN_RANK = TEAM_ROLES.indexOf('admin');

/** Anything with a `role` the rules below have to read — a `Member` satisfies it structurally. */
interface RoleBearer {
	readonly role: string;
	readonly user_id: string;
}

/**
 * Roles arrive from the API as a bare `string`, so every rule here takes one
 * and narrows. A value this vocabulary does not know is not an error: it
 * falls through to "no permission", which is the safe direction.
 *
 * @param value - The role as the API spelled it.
 * @returns True when it is one of the four known roles.
 */
function isTeamRole(value: string): value is TeamRole {
	return (TEAM_ROLES as readonly string[]).includes(value);
}

/**
 * The roles an actor may grant. An admin may grant anything below owner; only
 * an owner may create another owner, which is the rule `addMember` and
 * `updateMember` both enforce with a 403. Offering `owner` to an admin would
 * be an interface promising something the server refuses.
 *
 * @param actorRole - The signed-in member's own role.
 * @returns The assignable roles, ascending, or an empty list below admin.
 */
function rolesAssignableBy(actorRole: string): readonly TeamRole[] {
	if (!isTeamRole(actorRole)) return [];
	if (TEAM_ROLES.indexOf(actorRole) < ADMIN_RANK) return [];
	return actorRole === 'owner' ? TEAM_ROLES : TEAM_ROLES.slice(0, TEAM_ROLES.indexOf('owner'));
}

/**
 * Whether an actor may change or remove a target at all. Admin or above, and
 * an owner's row is an owner's business — the same split `updateMember` and
 * `removeMember` apply.
 *
 * @param actorRole - The signed-in member's own role.
 * @param targetRole - The role of the member whose row this is.
 * @returns True when the actor may operate on that row.
 */
function canManageMember(actorRole: string, targetRole: string): boolean {
	if (!isTeamRole(actorRole) || !isTeamRole(targetRole)) return false;
	if (TEAM_ROLES.indexOf(actorRole) < ADMIN_RANK) return false;
	return targetRole !== 'owner' || actorRole === 'owner';
}

/**
 * Whether this member is the team's only owner, and therefore cannot be
 * demoted or removed.
 *
 * This is courtesy, not enforcement. The real lock is `refuseLastOwner` in
 * `apps/api/internal/api/members.go`, which locks the owner rows inside the
 * mutation's own transaction — without that, two concurrent demotions both
 * read "two owners" and both succeed, leaving the team ownerless.
 *
 * @param members - The team's members, as the list endpoint returned them.
 * @param userId - The member to ask about.
 * @returns True when that member is an owner and no other owner exists.
 */
function isSoleOwner(members: readonly RoleBearer[], userId: string): boolean {
	const owners = members.filter((member: RoleBearer) => member.role === 'owner');
	return owners.length === 1 && owners[0]?.user_id === userId;
}

export { TEAM_ROLES, canManageMember, isTeamRole, isSoleOwner, rolesAssignableBy };
export type { TeamRole };
