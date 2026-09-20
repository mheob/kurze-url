import type { Member } from '@kurze-url/api-client';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';

import { formatDateTime } from '../lib/format';
import { DEFAULT_LANGUAGE } from '../lib/preferences';
import {
	canManageMember,
	isSoleOwner,
	isTeamRole,
	rolesAssignableBy,
	type TeamRole,
} from '../lib/team-roles';
import { ConfirmDelete } from './confirm-delete';
import { Badge } from './ui/badge';
import { NativeSelect, NativeSelectOption } from './ui/native-select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

interface MemberListProps {
	readonly actorRole: string;
	/** The signed-in person's own user id, from `GET /v1/me`. */
	readonly currentUserId: string;
	/** The row the last role-change or remove failure happened on, or null — separate from `pendingUserId` so a failed row does not stay disabled. */
	readonly failedUserId: string | null;
	/** The kind of the last failure, shown against the row `failedUserId` names. */
	readonly failure: 'raced' | 'unknown' | null;
	readonly members: readonly Member[];
	readonly onRemove: (userId: string) => void;
	readonly onRoleChange: (userId: string, role: TeamRole) => void;
	/** Set only while a role-change or remove request for this row is in flight; disables its controls. */
	readonly pendingUserId: string | null;
}

/**
 * `role` arrives from the API as a plain `string` (`Member`'s generated
 * type), not the narrower `TeamRole` union this UI actually knows about — an
 * unrecognised value is echoed back rather than silently dropped, exactly
 * `statusLabel` in `domain-list.tsx`'s own reasoning: a role this screen
 * doesn't yet handle surfaces instead of disappearing.
 *
 * @param t - The translation function.
 * @param role - The member's raw `role`, echoed back unchanged when unrecognised.
 * @returns The label to render for this role.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Readonly<TFunction>` strips i18next's call signature and produces a real TS2349 "not callable"; that was tried.
function roleLabel(t: TFunction, role: string): string {
	switch (role) {
		case 'viewer': {
			return t('members.roleViewer');
		}
		case 'editor': {
			return t('members.roleEditor');
		}
		case 'admin': {
			return t('members.roleAdmin');
		}
		case 'owner': {
			return t('members.roleOwner');
		}
		default: {
			return role;
		}
	}
}

/**
 * The team's members: who is in it, and — for whoever is allowed to use
 * them — the controls for changing that. Presentational and prop-driven, the
 * same contract `DomainList` follows: it takes the already-fetched members as
 * a prop rather than owning a query, and callbacks (`onRoleChange`,
 * `onRemove`) for the mutations it offers rather than owning them itself.
 *
 * `failure` is scoped to a single row via `failedUserId`, not `pendingUserId`
 * — the two answer different questions. `pendingUserId` is "which row has a
 * request in flight right now" and disables that row's controls;
 * `failedUserId` is "which row did the last failure happen on" and shows
 * that row's alert. An earlier version used one slot for both, which left a
 * failed row disabled with no way to retry it until a different row was
 * touched or the page reloaded — the route now clears `pendingUserId` in
 * `onError` and sets `failedUserId` instead, so a failed row keeps its alert
 * but stays usable. Splitting them still follows the same one-slot-per-fact
 * idiom `DomainList` uses for `pendingReason`/`verifyingId` and
 * `deleteBlockedCount`/`deletingId`, just two slots because one mutation's
 * outcome now answers two independent questions.
 *
 * Per row, `manageable` combines two independent refusals into the one
 * boolean that decides whether a control renders at all:
 * `canManageMember(actorRole, member.role)` is the permission rule (an editor
 * or viewer gets no control over anyone; an admin gets no control over an
 * owner), and `isSoleOwner` is a courtesy against a mutation the server would
 * refuse anyway (a team's last owner can never be demoted or removed, even by
 * another owner). The two are told apart in the rendering, not only in the
 * boolean: a sole owner still gets a *visible*, disabled select plus
 * `members.soleOwner`'s explanation, because "why is there no control here"
 * is a real question for someone with the rank to expect one; a member
 * `canManageMember` refuses outright gets plain text and no control at all,
 * because there is nothing to explain.
 *
 * The select's own options come from `rolesAssignableBy(actorRole)`, the same
 * function `MemberInviteForm`'s `roles` prop is built from — never the full
 * `TEAM_ROLES` list — so an admin is never offered `owner` on a row it can
 * manage: the interface must not promise a change `updateMember` will 403.
 * The sole owner's own locked row is the one case that still needs `owner`
 * in the list, and it gets it for free: that branch only renders for an
 * owner acting on their own row, and `rolesAssignableBy('owner')` includes
 * every role, `owner` among them, so the disabled select still shows its
 * current value correctly.
 *
 * @param props - The component's props.
 * @param props.actorRole - The signed-in member's own role.
 * @param props.currentUserId - The signed-in person's own user id, from `GET /v1/me`.
 * @param props.failedUserId - The row the last failure happened on, or null.
 * @param props.failure - The kind of the last failure, shown against the row `failedUserId` names.
 * @param props.members - The team's members, already fetched by the caller.
 * @param props.onRemove - Removes the member with the given user id.
 * @param props.onRoleChange - Changes the member with the given user id to the given role.
 * @param props.pendingUserId - The id of the member a mutation is in flight for, or null.
 * @returns The rendered member list table.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `members` carries `@kurze-url/api-client`'s generated `Member` type, whose properties are not marked readonly; that is generated codegen output, never edited by hand.
export function MemberList({
	actorRole,
	currentUserId,
	failedUserId,
	failure,
	members,
	onRemove,
	onRoleChange,
	pendingUserId,
}: MemberListProps): React.JSX.Element {
	const { t, i18n } = useTranslation();
	// `i18n.language` is a plain `string`; `formatDateTime` takes the narrower
	// `Language` union, the same split `preferences.ts`'s own `isLanguage` guard
	// exists for. Only `'de'` is checked explicitly, so anything else — 'en'
	// included — falls back to `DEFAULT_LANGUAGE`, without an `as` assertion.
	const language = i18n.language === 'de' ? 'de' : DEFAULT_LANGUAGE;
	// See the docstring above: never `TEAM_ROLES` directly, or an admin would
	// be offered `owner` on a row it can manage.
	const assignableRoles = rolesAssignableBy(actorRole);

	return (
		<Table>
			<TableHeader>
				<TableRow>
					<TableHead>{t('members.columnEmail')}</TableHead>
					<TableHead>{t('members.columnRole')}</TableHead>
					<TableHead>{t('members.columnSince')}</TableHead>
					<TableHead>{t('members.columnActions')}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `member` is the generated `Member` type; see the disable above on this component's own `members` prop. */}
				{members.map((member) => {
					const displayEmail = member.email === '' ? t('members.unknownAddress') : member.email;
					const canManage = canManageMember(actorRole, member.role);
					const manageable = canManage && !isSoleOwner(members, member.user_id);
					const locked = canManage && isSoleOwner(members, member.user_id);
					const pending = pendingUserId === member.user_id;
					const failed = failedUserId === member.user_id;

					return (
						<TableRow key={member.user_id}>
							<TableCell>
								{displayEmail}
								{member.user_id === currentUserId ? <Badge>{t('members.you')}</Badge> : null}
							</TableCell>
							<TableCell>
								{manageable || locked ? (
									<>
										<NativeSelect
											aria-label={t('members.roleFor', { email: displayEmail })}
											disabled={!manageable || pending}
											onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
												if (isTeamRole(event.target.value)) {
													onRoleChange(member.user_id, event.target.value);
												}
											}}
											value={member.role}
										>
											{assignableRoles.map((role) => (
												<NativeSelectOption key={role} value={role}>
													{roleLabel(t, role)}
												</NativeSelectOption>
											))}
										</NativeSelect>
										{locked ? <span>{t('members.soleOwner')}</span> : null}
									</>
								) : (
									roleLabel(t, member.role)
								)}
							</TableCell>
							<TableCell>{formatDateTime(member.created_at, language)}</TableCell>
							<TableCell>
								{failure !== null && failed ? (
									<span role="alert">
										{failure === 'raced' ? t('members.errorRaced') : t('errors.unknown')}
									</span>
								) : null}
								{manageable ? (
									<ConfirmDelete
										confirmLabel={t('members.removeConfirm')}
										label={t('members.remove')}
										onConfirm={() => {
											onRemove(member.user_id);
										}}
										question={t('members.removeQuestion', { email: displayEmail })}
									/>
								) : null}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}
