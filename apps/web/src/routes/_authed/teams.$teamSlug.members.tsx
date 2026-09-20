import type { Member, PageMember } from '@kurze-url/api-client';
import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { MemberInviteForm, type InviteFailureKind } from '../../components/member-invite-form';
import { MemberList } from '../../components/member-list';
import {
	classifyApiError,
	inviteRateLimitTokenOf,
	statusOf,
	type ApiFailure,
} from '../../lib/api-errors';
import { reportUnexpected } from '../../lib/observability';
import { rolesAssignableBy, type TeamRole } from '../../lib/team-roles';
import {
	addMemberFn,
	membersQueryOptions,
	removeMemberFn,
	updateMemberRoleFn,
} from '../../server/members';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file doesn't own: TanStack Router's own `beforeLoad`/`loader` option shapes, TanStack Query's own
   `queryOptions()`/`useMutation()` return types, or the generated `@kurze-url/api-client`
   `Member`/`PageMember`/`AddedMember` types, whose properties are mutable — generated codegen
   output, never edited by hand. `Readonly<>` is shallow and none of these is a declaration this
   file can edit. */

/** RFC 9110 status codes this file branches on, named for the reader checking a case against the spec rather than the wire. */
const HTTP_CONFLICT = 409;
const HTTP_BAD_GATEWAY = 502;
const HTTP_SERVICE_UNAVAILABLE = 503;
const HTTP_TOO_MANY_REQUESTS = 429;
const HTTP_FORBIDDEN = 403;
const HTTP_NOT_FOUND = 404;

/**
 * Every reason `addMember` can refuse an invite, read from the raw status
 * (and, for a 429, its typed detail, via `lib/api-errors.ts`'s own
 * `inviteRateLimitTokenOf`) before ever asking `classifyApiError` — that
 * function deliberately collapses every 429 into one `rateLimited` kind and
 * 403/404 into one `notFound` kind, both of which throw away exactly what
 * this form needs to say something true. `unauthenticated` is not a case
 * here: the mutation's own `onError` intercepts it and navigates before this
 * is ever called, the same split `classifyVerifyFailure`
 * (`teams.$teamSlug.domains.tsx`) makes for its own mutation.
 *
 * Exported for its own direct test coverage — `teams.$teamSlug.members.test.tsx`
 * — the same reason `loadMembers` is exported.
 *
 * @param error - Whatever the failed `addMember` call threw.
 * @returns The reason the invite failed, or was silently not sent.
 */
export function classifyInviteFailure(error: unknown): InviteFailureKind {
	const status = statusOf(error);

	if (status === HTTP_CONFLICT) return 'alreadyMember';
	if (status === HTTP_BAD_GATEWAY) return 'mailFailed';
	if (status === HTTP_SERVICE_UNAVAILABLE) return 'notConfigured';
	if (status === HTTP_TOO_MANY_REQUESTS) {
		const token = inviteRateLimitTokenOf(error);
		if (token === 'instance_monthly') return 'instanceBudget';
		if (token === 'team_hourly') return 'teamBurst';
		return 'unknown';
	}

	// A 403/404 here means the admin who loaded this page lost that rank (or
	// the team) between load and submit — `classifyApiError` reports both as
	// `notFound`, and `raced` is `MemberList`'s own name for exactly that
	// story, not a new one invented for this form.
	return classifyApiError(error).kind === 'notFound' ? 'raced' : 'unknown';
}

/** Every reason a role-change or remove call can fail, once `unauthenticated` is peeled off for the redirect it gets instead. */
export type MutationFailureKind = 'raced' | 'unknown';

/**
 * `updateMember`/`removeMember` (`apps/api/internal/api/members.go`) answer a
 * non-member with 404 and a member below admin with 403 — `classifyApiError`
 * collapses both into `notFound`, which has no meaning for a mutation the
 * caller just triggered from a row they can currently see: the only way
 * either status reaches here is a race lost since the page loaded (someone
 * else removed this member, or demoted the caller), not a door that was
 * always closed.
 *
 * Exported for its own direct test coverage, the same reason
 * `classifyInviteFailure` above is.
 *
 * @param error - Whatever the failed role-change or remove call threw.
 * @returns `'raced'` for a 403/404, `'unknown'` for anything else.
 */
export function classifyMutationFailure(error: unknown): MutationFailureKind {
	const status = statusOf(error);
	return status === HTTP_FORBIDDEN || status === HTTP_NOT_FOUND ? 'raced' : 'unknown';
}

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `DomainsDataSource` in `teams.$teamSlug.domains.tsx`: a real
 * `QueryClient` satisfies this structurally, so the loader needs no cast.
 */
interface MembersDataSource {
	readonly ensureQueryData: (
		options: ReturnType<typeof membersQueryOptions>,
	) => Promise<PageMember>;
}

/**
 * Same shape and reasoning as `loadDomains`: a 401 that survives to this
 * loader (as opposed to the *no session at all* case `_authed.tsx`'s
 * `beforeLoad` already redirects) must not fall through to `errorComponent`
 * as dead-end inline text — it sends the visitor back to `/login` instead.
 * Every other error kind is rethrown unchanged.
 *
 * There is deliberately no 403 handling here, unlike the audit log's own
 * loader: listing members is a viewer-level right (`authz.ViewerScope`), so a
 * 403 can never reach a reader of this page at all.
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns The team's members.
 */
export async function loadMembers(
	queryClient: MembersDataSource,
	teamId: string,
): Promise<PageMember> {
	try {
		return await queryClient.ensureQueryData(membersQueryOptions(teamId));
	} catch (error) {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamSlug/members')({
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	errorComponent: MembersError,
	loader: async ({ context }) => loadMembers(context.queryClient, context.teamId),
});

/**
 * Same shape as `DomainsError`: a route-level `errorComponent` is the nearest
 * one TanStack Router will render, so without this call a 500 from listing
 * members never reaches `RootErrorPage` and no event is ever sent.
 * `reportUnexpected` refuses every kind rendered as ordinary UI below.
 *
 * No `statusOf(error) === 403` check, unlike the audit log's own
 * `errorComponent`: listing members is a viewer-level right, so a 403 cannot
 * reach a reader of this page at all — there is no refusal to tell apart from
 * a genuine failure here.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function MembersError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

export interface MembersPageBodyProps {
	/** The signed-in member's own role on this team. */
	readonly currentRole: string;
	/** The signed-in person's own user id, from `GET /v1/me`. */
	readonly currentUserId: string;
	/** The row the last role-change or remove failure happened on, or `null` — separate from `pendingUserId` so a failed row does not stay disabled. */
	readonly failedUserId: string | null;
	/** The reason the last invite failed, or `null`. */
	readonly inviteFailure: InviteFailureKind | null;
	/** Disables the invite form's submit button while the invite is in flight. */
	readonly invitePending: boolean;
	/** The last successful add, so the invite form can say whether an email went out. */
	readonly inviteResult: { readonly email: string; readonly invited: boolean } | null;
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Member` is the generated `@kurze-url/api-client` type; see the file-level disable above.
	readonly members: readonly Member[];
	/** The kind of the last role-change or remove failure, shown against the row `failedUserId` names. */
	readonly mutationFailure: MutationFailureKind | null;
	readonly onInvite: (values: { readonly email: string; readonly role: TeamRole }) => void;
	readonly onRemove: (userId: string) => void;
	readonly onRoleChange: (userId: string, role: TeamRole) => void;
	/** The id of the member a role-change or remove mutation is currently in flight for. */
	readonly pendingUserId: string | null;
	/** The address of the member the last successful removal took out, for `members.removed`; `''` for an account with no address on file. */
	readonly removedEmail: string | null;
	/** Whether the last role-change mutation succeeded, for `members.roleChanged`. */
	readonly roleChanged: boolean;
}

/**
 * The presentational body of the members page — pure and prop-driven, the
 * same idiom `AuditLogPageBody`/`StatsPageBody` already use so the route's
 * router wiring (`RouteComponent` below) can be tested separately from what
 * it renders, and so `teams.$teamSlug.members.a11y.test.tsx` can run axe over
 * the state production actually renders rather than a hand-kept copy of it.
 *
 * The two success lines render as `<output>` elements, not a bare
 * `role="status"` on a `<p>`: `jsx-a11y/prefer-tag-over-role` already pushed
 * `MemberInviteForm`'s own success line to that element for the identical
 * reason, and `<output>`'s implicit ARIA role is `status` — a
 * `getByRole('status')` query cannot tell the two apart.
 *
 * @param props - The component's props.
 * @param props.currentRole - The signed-in member's own role on this team.
 * @param props.currentUserId - The signed-in person's own user id.
 * @param props.failedUserId - The row the last role-change or remove failure happened on, or `null`.
 * @param props.inviteFailure - The reason the last invite failed, or `null`.
 * @param props.invitePending - Disables the invite form's submit button while in flight.
 * @param props.inviteResult - The last successful add.
 * @param props.members - The team's members, already fetched by the caller.
 * @param props.mutationFailure - The kind of the last failure, shown against the row `failedUserId` names.
 * @param props.onInvite - Submits an invite with the given address and role.
 * @param props.onRemove - Removes the member with the given user id.
 * @param props.onRoleChange - Changes the member with the given user id to the given role.
 * @param props.pendingUserId - The id of the member a mutation is currently in flight for.
 * @param props.removedEmail - The address the last successful removal took out.
 * @param props.roleChanged - Whether the last role-change mutation succeeded.
 * @returns The rendered page body.
 */
export function MembersPageBody({
	currentRole,
	currentUserId,
	failedUserId,
	inviteFailure,
	invitePending,
	inviteResult,
	members,
	mutationFailure,
	onInvite,
	onRemove,
	onRoleChange,
	pendingUserId,
	removedEmail,
	roleChanged,
}: MembersPageBodyProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<>
			<h1>{t('members.heading')}</h1>
			<MemberInviteForm
				failure={inviteFailure}
				onSubmit={onInvite}
				pending={invitePending}
				roles={rolesAssignableBy(currentRole)}
				result={inviteResult}
			/>
			<MemberList
				actorRole={currentRole}
				currentUserId={currentUserId}
				failedUserId={failedUserId}
				failure={mutationFailure}
				members={members}
				onRemove={onRemove}
				onRoleChange={onRoleChange}
				pendingUserId={pendingUserId}
			/>
			{roleChanged ? <output>{t('members.roleChanged')}</output> : null}
			{removedEmail === null ? null : (
				<output>
					{t('members.removed', {
						email: removedEmail === '' ? t('members.unknownAddress') : removedEmail,
					})}
				</output>
			)}
		</>
	);
}

function RouteComponent(): React.JSX.Element {
	const { me, teamId } = Route.useRouteContext();
	const router = useRouter();
	const queryClient = useQueryClient();
	const { data } = useSuspenseQuery(membersQueryOptions(teamId));

	// `items` is nullable on the wire, the same reason `DomainsRouteComponent`
	// normalises `data.items` — Huma serialises a nil Go slice as JSON `null`.
	const items = data.items ?? [];

	const currentRole =
		me.memberships.find((membership) => membership.team_id === teamId)?.role ?? '';

	const [inviteFailure, setInviteFailure] = useState<InviteFailureKind | null>(null);
	const [inviteResult, setInviteResult] = useState<{
		readonly email: string;
		readonly invited: boolean;
	} | null>(null);

	// `pendingUserId` and `failedUserId` answer two different questions about
	// the same row-scoped mutation pair (role-change and remove share both,
	// mirroring `MemberList`'s own split): `pendingUserId` is "which row has a
	// request in flight right now" and is cleared in both `onError` and
	// `onSuccess`, so a failed row is never stuck disabled; `failedUserId` is
	// "which row did the last failure happen on" and is cleared only by
	// `clearStatusSlots` below, at the start of the next mutation, so the
	// failed row keeps its alert until something new is attempted. Only one of
	// the two mutations is ever in flight for a given row at a time, so there
	// is nothing a second pair of slots per mutation would let this page say
	// that this one cannot.
	const [pendingUserId, setPendingUserId] = useState<string | null>(null);
	const [failedUserId, setFailedUserId] = useState<string | null>(null);
	const [mutationFailure, setMutationFailure] = useState<MutationFailureKind | null>(null);
	const [roleChanged, setRoleChanged] = useState(false);
	const [removedEmail, setRemovedEmail] = useState<string | null>(null);

	/**
	 * Clears every banner and row-failure slot this page owns — invite's own
	 * two, and the role-change/remove pair's three — so that starting a new
	 * action of any kind never leaves a stale result from a different action
	 * standing. Deliberately not `pendingUserId`: that slot belongs to
	 * whichever mutation is about to start, which sets it itself right after
	 * calling this.
	 */
	function clearStatusSlots(): void {
		setInviteFailure(null);
		setInviteResult(null);
		setMutationFailure(null);
		setFailedUserId(null);
		setRoleChanged(false);
		setRemovedEmail(null);
	}

	const inviteMutation = useMutation({
		mutationFn: async (values: { readonly email: string; readonly role: TeamRole }) =>
			addMemberFn({ data: { email: values.email, role: values.role, teamId } }),
		onError: (error: unknown) => {
			// A mutation callback is not a render and not a loader, so it cannot
			// throw a redirect — see the same note on the create-link route.
			if (classifyApiError(error).kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setInviteFailure(classifyInviteFailure(error));
		},
		onSuccess: async (added) => {
			setInviteFailure(null);
			setInviteResult({ email: added.email, invited: added.invited });
			await queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey });
		},
	});

	const roleMutation = useMutation({
		mutationFn: async (input: { readonly role: TeamRole; readonly userId: string }) =>
			updateMemberRoleFn({ data: { role: input.role, teamId, userId: input.userId } }),
		onError: (error: unknown, variables) => {
			if (classifyApiError(error).kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			const kind = classifyMutationFailure(error);
			setMutationFailure(kind);
			setFailedUserId(variables.userId);
			// Clearing this here, not only in `onSuccess`, is the fix for a
			// failed row getting stuck disabled with no way to retry it: the
			// disabling and the alert now come from two different slots, and
			// this one belongs to "in flight", not "failed".
			setPendingUserId(null);
			// A raced 403/404 means the row this page is showing is already
			// stale — refetching is what makes "the member list changed while
			// you were working" (`members.errorRaced`) true rather than a
			// message with nothing behind it.
			if (kind === 'raced') {
				void queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey });
			}
		},
		onSuccess: async () => {
			setMutationFailure(null);
			setFailedUserId(null);
			setPendingUserId(null);
			setRoleChanged(true);
			await queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey });
		},
	});

	const removeMutation = useMutation({
		mutationFn: async (input: { readonly email: string; readonly userId: string }) =>
			removeMemberFn({ data: { teamId, userId: input.userId } }),
		onError: (error: unknown, variables) => {
			if (classifyApiError(error).kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			const kind = classifyMutationFailure(error);
			setMutationFailure(kind);
			setFailedUserId(variables.userId);
			setPendingUserId(null);
			if (kind === 'raced') {
				void queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey });
			}
		},
		onSuccess: async (_data, variables) => {
			setMutationFailure(null);
			setFailedUserId(null);
			setPendingUserId(null);
			setRemovedEmail(variables.email);
			await queryClient.invalidateQueries({ queryKey: membersQueryOptions(teamId).queryKey });
		},
	});

	function handleInvite(values: { readonly email: string; readonly role: TeamRole }): void {
		clearStatusSlots();
		inviteMutation.mutate(values);
	}

	function handleRoleChange(userId: string, role: TeamRole): void {
		clearStatusSlots();
		setPendingUserId(userId);
		roleMutation.mutate({ role, userId });
	}

	function handleRemove(userId: string): void {
		const email = items.find((member) => member.user_id === userId)?.email ?? '';
		clearStatusSlots();
		setPendingUserId(userId);
		removeMutation.mutate({ email, userId });
	}

	return (
		<MembersPageBody
			currentRole={currentRole}
			currentUserId={me.user_id}
			failedUserId={failedUserId}
			inviteFailure={inviteFailure}
			invitePending={inviteMutation.isPending}
			inviteResult={inviteResult}
			members={items}
			mutationFailure={mutationFailure}
			onInvite={handleInvite}
			onRemove={handleRemove}
			onRoleChange={handleRoleChange}
			pendingUserId={pendingUserId}
			removedEmail={removedEmail}
			roleChanged={roleChanged}
		/>
	);
}
