import type { Link, UpdateLinkInputBodyWritable } from '@kurze-url/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, notFound, redirect, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ConfirmDelete } from '../../components/confirm-delete';
import { LinkForm, type LinkFormValues } from '../../components/link-form';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { deleteLinkFn, getLinkFn, updateLinkFn } from '../../server/links';
import { assertMembership } from '../_authed';

/**
 * The one shape `loadLink` below reaches through — a real `getLinkFn`
 * satisfies this structurally, so the loader needs no cast, and
 * `teams.$teamId.links.$linkId.test.ts` can pass a hand-built fake instead of
 * a real server function (which cannot run directly under Vitest — see
 * `server/links.ts`'s docstrings). Same shape `LinksDataSource` uses in the
 * list route (Task 9) for the identical reason.
 */
type LinkFetcher = (options: { data: { linkId: string } }) => Promise<Link>;

/**
 * A non-member of `teamId` never reaches this loader at all — `beforeLoad`'s
 * `assertMembership` throws first. This is the narrower case: a caller who
 * *is* a team member, but whose `linkId` names a link that either doesn't
 * exist or belongs to someone else. `internal/authz` answers that the same
 * way it answers a non-member team, 404 never 403 (see `assertMembership`'s
 * own docstring), so `classifyApiError`'s `notFound` is what this throws the
 * router's own `notFound()` for — a generic error page here would be exactly
 * the kind of leak `assertMembership` exists to prevent, just reached by a
 * different door.
 *
 * `unauthenticated` redirects for the same reason `loadLinks` does in the
 * list route: the narrow window where `_authed.tsx`'s own session check
 * passed but the token dies, or is rejected, by the time this route's own
 * fetch runs.
 */
export async function loadLink(fetchLink: LinkFetcher, linkId: string): Promise<Link> {
	try {
		return await fetchLink({ data: { linkId } });
	} catch (error) {
		const classified = classifyApiError(error);
		if (classified.kind === 'unauthenticated') throw redirect({ to: '/login' });
		if (classified.kind === 'notFound') throw notFound();
		throw error;
	}
}

/**
 * The `datetime-local` input's own format (`YYYY-MM-DDTHH:mm`, no seconds, no
 * zone) built from the instant's *local* wall-clock components — the same
 * assumption `link-form.new.tsx`'s `toRequestBody` makes on the way in
 * (`new Date(values.expires_at)` parses a zone-less date-time string as
 * local time; see its own docstring). Slicing the UTC `toISOString()` output
 * instead would silently reinterpret UTC components as local ones, shifting
 * the instant by the viewer's UTC offset on every round trip except exactly
 * UTC+0 — the kind of bug that only shows up once a real reviewer isn't
 * sitting in a UTC timezone. Building from `getFullYear`/`getMonth`/etc.
 * keeps both directions symmetric regardless of the runtime's zone.
 *
 * `null` (no expiry) becomes `''`, not an epoch date — `new Date(null)` is
 * the Unix epoch, and showing that in the input would read as "this link
 * expires January 1970" for a link that never expires at all.
 */
function pad(value: number): string {
	return String(value).padStart(2, '0');
}

export function toDateTimeLocal(expiresAt: string | null): string {
	if (expiresAt === null) return '';

	const date = new Date(expiresAt);
	if (Number.isNaN(date.getTime())) return '';

	const year = date.getFullYear();
	const month = pad(date.getMonth() + 1);
	const day = pad(date.getDate());
	const hours = pad(date.getHours());
	const minutes = pad(date.getMinutes());
	return `${year}-${month}-${day}T${hours}:${minutes}`;
}

/** Seeds `<LinkForm initial>` from the fetched `Link` — a `Pick`, not a spread, so the extra fields `Link` carries (`id`, `state`, `tags`, …) never reach `LinkFormValues` and trip an excess-property error. */
function toFormValues(link: Link): LinkFormValues {
	return {
		analytics_enabled: link.analytics_enabled,
		destination_url: link.destination_url,
		expires_at: toDateTimeLocal(link.expires_at),
		redirect_type: link.redirect_type,
		slug: link.slug,
	};
}

/**
 * The update counterpart of `link.new.tsx`'s `toRequestBody`. Same empty
 * string → `undefined` treatment for the same reason: Huma's `expires_at`
 * validation wants either a real timestamp or nothing, never `''`, and an
 * empty `slug` here means "don't change it" (there is no dedicated
 * clear-and-regenerate signal in `UpdateLinkInputBodyWritable`, unlike
 * `folder_id`'s explicit `null`-to-unfile) rather than the create form's
 * "generate one" — inherited from reusing the same `<LinkForm>` unmodified.
 */
function toUpdateBody(values: LinkFormValues): UpdateLinkInputBodyWritable {
	return {
		analytics_enabled: values.analytics_enabled,
		destination_url: values.destination_url,
		expires_at: values.expires_at === '' ? undefined : new Date(values.expires_at).toISOString(),
		redirect_type: values.redirect_type === 301 ? 301 : 302,
		slug: values.slug === '' ? undefined : values.slug,
	};
}

/** Same narrow slices as `link.new.tsx`'s `InvalidatableQueryClient`/`InvalidatableRouter` — real instances satisfy these structurally, fakes satisfy them for the test. */
interface InvalidatableQueryClient {
	invalidateQueries: (filters: { queryKey: readonly unknown[] }) => Promise<void>;
}
interface InvalidatableRouter {
	invalidate: () => Promise<void>;
}

/**
 * Shared by both mutations below: the loader owns the list's data, React
 * Query's cache holds this link's own — invalidating only one leaves them
 * disagreeing until the next full navigation, the same property
 * `link.new.tsx`'s `afterCreate` falsifies for creation. Delete's own
 * "navigate back to the list" step lives in its `onSuccess`, not here, since
 * update has no such step.
 */
export async function afterMutation(
	queryClient: InvalidatableQueryClient,
	router: InvalidatableRouter,
	teamId: string,
): Promise<void> {
	await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	await router.invalidate();
}

export const Route = createFileRoute('/_authed/teams/$teamId/links/$linkId')({
	beforeLoad: ({ context, params }) => {
		assertMembership(context.me.memberships, params.teamId);
	},
	loader: ({ params }) => loadLink(getLinkFn, params.linkId),
	component: RouteComponent,
});

function RouteComponent(): React.JSX.Element {
	const { linkId, teamId } = Route.useParams();
	const link = Route.useLoaderData();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<ApiFailure | null>(null);

	const updateMutation = useMutation({
		mutationFn: (values: LinkFormValues) =>
			updateLinkFn({ data: { body: toUpdateBody(values), linkId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// See `link.new.tsx`'s identical branch: a render can't throw a
			// redirect, and this is further still, an event-handler callback.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async () => {
			setFailure(null);
			await afterMutation(queryClient, router, teamId);
		},
	});

	const deleteMutation = useMutation({
		mutationFn: () => deleteLinkFn({ data: { linkId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async () => {
			setFailure(null);
			await afterMutation(queryClient, router, teamId);
			// Nothing restores a deleted link — navigate back to the list rather
			// than leaving this page rendering a link that no longer exists.
			await router.navigate({ params: { teamId }, to: '/teams/$teamId/links' });
		},
	});

	const fieldErrors = failure?.kind === 'fields' ? failure.fields : undefined;
	const formMessage = failure && failure.kind !== 'fields' ? t(`errors.${failure.kind}`) : null;

	return (
		<>
			<h1>{t('links.edit')}</h1>
			{formMessage ? <p role="alert">{formMessage}</p> : null}
			<LinkForm
				fieldErrors={fieldErrors}
				initial={toFormValues(link)}
				onSubmit={(values) => {
					updateMutation.mutate(values);
				}}
			/>
			<ConfirmDelete
				label={t('links.delete')}
				onConfirm={() => {
					deleteMutation.mutate();
				}}
			/>
		</>
	);
}
