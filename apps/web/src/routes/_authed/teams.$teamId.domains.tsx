import type { PageDomain, VerifyDomainOutputBody } from '@kurze-url/api-client';
import { useForm } from '@tanstack/react-form';
import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { DomainList } from '../../components/domain-list';
import { Button } from '../../components/ui/button';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import {
	claimDomainFn,
	deleteDomainFn,
	domainsQueryOptions,
	verifyDomainFn,
} from '../../server/domains';
import { assertMembership } from '../_authed';

type VerifyReason = VerifyDomainOutputBody['reason'];

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `LinksDataSource` in the links list route: a real
 * `QueryClient` satisfies this structurally, so the loader needs no cast.
 */
interface DomainsDataSource {
	ensureQueryData: (options: ReturnType<typeof domainsQueryOptions>) => Promise<PageDomain>;
}

/**
 * Same shape and reasoning as `loadLinks`: a 401 that survives to this
 * loader (as opposed to the *no session at all* case `_authed.tsx`'s
 * `beforeLoad` already redirects) must not fall through to `errorComponent`
 * as dead-end inline text — it sends the visitor back to `/login` instead.
 * Every other error kind is rethrown unchanged.
 */
export async function loadDomains(
	queryClient: DomainsDataSource,
	teamId: string,
): Promise<PageDomain> {
	try {
		return await queryClient.ensureQueryData(domainsQueryOptions(teamId));
	} catch (error) {
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamId/domains')({
	beforeLoad: ({ context, params }) => {
		assertMembership(context.me.memberships, params.teamId);
	},
	loader: ({ context, params }) => loadDomains(context.queryClient, params.teamId),
	component: RouteComponent,
	errorComponent: DomainsError,
});

/**
 * Same reasoning as `LinksError`: a list that silently rendered empty on a
 * failed request would be indistinguishable from a team with no domains, so
 * this fails loudly instead. `kind: 'unauthenticated'` can still reach here
 * on a background refetch (React Query's default `refetchOnWindowFocus`),
 * a path `loadDomains`'s own try/catch never sees — hence the `<Navigate>`.
 */
export function DomainsError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

function RouteComponent(): React.JSX.Element {
	const { teamId } = Route.useParams();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const { data } = useSuspenseQuery(domainsQueryOptions(teamId));

	// `items` is nullable on the wire, the same reason `LinkList` normalises
	// `data.items` — Huma serialises a nil Go slice as JSON `null`.
	const items = data.items ?? [];

	const [claimFailure, setClaimFailure] = useState<ApiFailure | null>(null);
	// `verifyingId` names which domain `pendingReason` is about — see
	// `DomainList`'s own docstring for why one slot is enough. Set the moment
	// a check starts (not only once it settles), so a second click on a
	// *different* domain immediately stops attributing the previous result to
	// the wrong row, rather than waiting for the new response to arrive.
	const [verifyingId, setVerifyingId] = useState<string | null>(null);
	const [pendingReason, setPendingReason] = useState<VerifyReason | undefined>(undefined);
	// Same one-slot correlation as `verifyingId`/`pendingReason` above, for the
	// same reason: only one delete is ever in flight at a time, and
	// `deletingId` already names which domain it was for.
	const [deletingId, setDeletingId] = useState<string | null>(null);
	const [deleteFailure, setDeleteFailure] = useState<ApiFailure | null>(null);

	const claimMutation = useMutation({
		mutationFn: (hostname: string) => claimDomainFn({ data: { hostname, teamId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// A mutation callback is not a render and not a loader, so it cannot
			// throw a redirect — see the same note on the create-link route.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setClaimFailure(classified);
		},
		onSuccess: async () => {
			setClaimFailure(null);
			form.reset();
			await queryClient.invalidateQueries({ queryKey: domainsQueryOptions(teamId).queryKey });
		},
	});

	const verifyMutation = useMutation({
		mutationFn: (domainId: string) => verifyDomainFn({ data: { domainId } }),
		onError: (error: unknown) => {
			// A verify call failing outright (a 409 from a race just lost, or a
			// network error) has no reason to show — the domain's own status,
			// refetched on the next load, is what tells the rest of the story.
			if (classifyApiError(error).kind === 'unauthenticated')
				void router.navigate({ to: '/login' });
		},
		onSuccess: (result) => {
			// Reason is empty on success, including the already-verified
			// short-circuit (`VerifyDomainOutput.Body.Reason`'s own doc comment
			// on the Go side) — read off the returned domain's own status
			// rather than the reason string's emptiness, so this never depends
			// on `VerifyReason`'s type actually admitting `''` as a value.
			setPendingReason(
				result.domain.verification_status === 'verified' ? undefined : result.reason,
			);
			// Merges the freshly verified (or still-pending) domain into the
			// cached list in place, so the row updates without waiting for a
			// refetch — the same reason `afterCreate` invalidates on the links
			// route, but here there is nothing to invalidate: only one row
			// changed, and its replacement is already in hand.
			queryClient.setQueryData(domainsQueryOptions(teamId).queryKey, (old) =>
				old
					? {
							...old,
							items: (old.items ?? []).map((item) =>
								item.id === result.domain.id ? result.domain : item,
							),
						}
					: old,
			);
		},
	});

	const deleteMutation = useMutation({
		mutationFn: (domainId: string) => deleteDomainFn({ data: { domainId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setDeleteFailure(classified);
		},
		onSuccess: async () => {
			setDeleteFailure(null);
			setDeletingId(null);
			// A domain was removed, not merely one row updated — invalidate rather
			// than merge, the same reasoning `claimMutation` uses for creation.
			await queryClient.invalidateQueries({ queryKey: domainsQueryOptions(teamId).queryKey });
		},
	});

	const form = useForm({
		defaultValues: { hostname: '' },
		onSubmit: ({ value }) => {
			claimMutation.mutate(value.hostname);
		},
	});

	function handleVerify(domainId: string): void {
		setVerifyingId(domainId);
		setPendingReason(undefined);
		verifyMutation.mutate(domainId);
	}

	function handleDelete(domainId: string): void {
		setDeletingId(domainId);
		setDeleteFailure(null);
		deleteMutation.mutate(domainId);
	}

	const fieldError = claimFailure?.kind === 'fields' ? claimFailure.fields.hostname : undefined;
	// A field error renders on the field itself; a second generic banner for
	// the same failure is what the create-link route deliberately avoids.
	const claimMessage =
		claimFailure && claimFailure.kind !== 'fields' ? t(`errors.${claimFailure.kind}`) : null;

	// `domainHasLinks` renders next to the specific row via `DomainList`'s own
	// `deleteBlockedCount`/`deletingId` props — the count is the whole point
	// of the refusal, so it must not collapse into this generic banner. Every
	// other delete failure (network error, a 404 from a race with another
	// admin) has no per-domain story to tell, so it falls back here instead,
	// the same way `claimMessage` does above. `fields` never actually occurs
	// for a delete, but is excluded for the same type-safety reason it is above.
	const deleteBlockedCount =
		deleteFailure?.kind === 'domainHasLinks' ? deleteFailure.count : undefined;
	const deleteMessage =
		deleteFailure && deleteFailure.kind !== 'domainHasLinks' && deleteFailure.kind !== 'fields'
			? t(`errors.${deleteFailure.kind}`)
			: null;

	return (
		<>
			{/* `listDomainsFor` returns the full envelope (`total_count`) but takes
			    no `page` parameter — a team's domain list is expected to stay
			    small. This is what notices the rare case where it wasn't: silently
			    dropping domains a team owns would be worse than a plain count. */}
			{items.length < data.total_count ? (
				<output>{t('domains.truncated', { count: items.length, total: data.total_count })}</output>
			) : null}
			<DomainList
				deleteBlockedCount={deleteBlockedCount}
				deletingId={deletingId}
				domains={items}
				onDelete={handleDelete}
				onVerify={handleVerify}
				pendingReason={pendingReason}
				verifyingId={verifyingId}
			/>
			{deleteMessage ? <p role="alert">{deleteMessage}</p> : null}
			{claimMessage ? <p role="alert">{claimMessage}</p> : null}
			<form
				onSubmit={(event) => {
					event.preventDefault();
					event.stopPropagation();
					void form.handleSubmit();
				}}
			>
				<form.Field
					name="hostname"
					validators={{
						onChange: ({ value }) =>
							value.trim() === '' ? t('domains.hostnameRequired') : undefined,
					}}
				>
					{(field) => {
						const errorId = 'hostname-error';
						const hintId = 'hostname-hint';
						const errorMessage =
							fieldError ?? (field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

						return (
							<div>
								<label htmlFor="hostname">{t('domains.hostname')}</label>
								<input
									aria-describedby={errorMessage ? `${hintId} ${errorId}` : hintId}
									aria-invalid={errorMessage ? true : undefined}
									id="hostname"
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event) => field.handleChange(event.target.value)}
									required
									value={field.state.value}
								/>
								<p id={hintId}>{t('domains.hostnameHint')}</p>
								{errorMessage ? (
									<p id={errorId} role="alert">
										{errorMessage}
									</p>
								) : null}
							</div>
						);
					}}
				</form.Field>

				<Button disabled={claimMutation.isPending} type="submit">
					{t('domains.claim')}
				</Button>
			</form>
		</>
	);
}
