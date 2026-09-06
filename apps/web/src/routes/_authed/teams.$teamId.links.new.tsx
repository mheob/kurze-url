import type { CreateLinkInputBodyWritable, PageDomain } from '@kurze-url/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { LinkForm, type LinkFormValues } from '../../components/link-form';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { domainsQueryOptions } from '../../server/domains';
import { createLinkFn } from '../../server/links';
import { assertMembership } from '../_authed';

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `LinksDataSource` in the list route.
 */
interface DomainsDataSource {
	ensureQueryData: (options: ReturnType<typeof domainsQueryOptions>) => Promise<PageDomain>;
}

/**
 * Verified domains only: a `pending`/`failed` one has no working DNS yet, so
 * offering it in the picker would let a link get created on a hostname that
 * doesn't redirect. Falls back to an empty list on any failure — including
 * an expired session — rather than blocking the whole create-link page: the
 * picker is an enhancement over the shared hostname the form already falls
 * back to, and the create mutation's own `onError` already sends the visitor
 * to `/login` the moment they try to submit against a session that is
 * actually gone. Mirrors `listDomainsFor`'s own normalisation of a nil items
 * slice (Huma serialises it as JSON `null`).
 */
export async function loadVerifiedDomains(
	queryClient: DomainsDataSource,
	teamId: string,
): Promise<readonly { id: string; hostname: string }[]> {
	try {
		const page = await queryClient.ensureQueryData(domainsQueryOptions(teamId));
		return (page.items ?? [])
			.filter((domain) => domain.verification_status === 'verified')
			.map((domain) => ({ hostname: domain.hostname, id: domain.id }));
	} catch {
		return [];
	}
}

export const Route = createFileRoute('/_authed/teams/$teamId/links/new')({
	beforeLoad: ({ context, params }) => {
		assertMembership(context.me.memberships, params.teamId);
	},
	loader: ({ context, params }) => loadVerifiedDomains(context.queryClient, params.teamId),
	component: RouteComponent,
});

/**
 * Turns the form's own value shape into the API's request body. Kept out of
 * `LinkForm` itself so that component stays a plain "here are the values"
 * contract the edit route (Task 11, per the plan's pre-flight scan) can reuse
 * without also inheriting how the create route's mutation is built.
 *
 * An empty `slug`/`expires_at`/`domain_id` becomes `undefined`, not `''`: the
 * API generates a slug when the field is omitted
 * (`CreateLinkInputBodyWritable`'s own doc comment) and defaults to the
 * instance's shared domain when `domain_id` is omitted, and Huma's
 * `expires_at` validation expects either a real timestamp or nothing, never
 * an empty string.
 *
 * Exported so `domain_id`'s mapping is falsifiable directly: nothing in this
 * route renders through a real HTTP layer, so the mutation's `onSubmit`
 * wiring alone can't catch a dropped field here — the picker's own
 * component test only proves `LinkForm` hands back the right values, not
 * that this function forwards them (confirmed by deleting the `domain_id`
 * line below and re-running the suite: nothing failed until this file grew
 * its own test for it).
 */
export function toRequestBody(values: LinkFormValues): CreateLinkInputBodyWritable {
	return {
		analytics_enabled: values.analytics_enabled,
		destination_url: values.destination_url,
		domain_id: values.domain_id === '' ? undefined : values.domain_id,
		expires_at: values.expires_at === '' ? undefined : new Date(values.expires_at).toISOString(),
		redirect_type: values.redirect_type === 301 ? 301 : 302,
		slug: values.slug === '' ? undefined : values.slug,
	};
}

/**
 * The narrow slices of `QueryClient`/`Router` this needs — same reasoning as
 * `LinksDataSource` in the list route (Task 9): a real object satisfies these
 * structurally, so production code needs no cast, and a test can pass a
 * hand-built fake instead of standing up either one for real.
 */
interface InvalidatableQueryClient {
	invalidateQueries: (filters: { queryKey: readonly unknown[] }) => Promise<void>;
}
interface InvalidatableRouter {
	invalidate: () => Promise<void>;
}

/**
 * Extracted so this task's own explicit rule — "invalidate both the links
 * query key and the router... invalidating only one leaves them disagreeing
 * until the next full navigation" — is a falsifiable property against fakes,
 * rather than something only provable by clicking through a real app. Mirrors
 * `loadLinks`'s extraction in the list route (Task 9) for the same reason.
 *
 * Invalidates the whole `['links', teamId]` prefix, not one exact
 * `['links', teamId, page]` key: a newly created link can land on any page a
 * visitor currently has open (sort order isn't this task's concern), and
 * React Query's `invalidateQueries` already treats a queryKey as a prefix
 * match by default.
 */
export async function afterCreate(
	queryClient: InvalidatableQueryClient,
	router: InvalidatableRouter,
	teamId: string,
): Promise<void> {
	await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	await router.invalidate();
}

function RouteComponent(): React.JSX.Element {
	const { teamId } = Route.useParams();
	const domains = Route.useLoaderData();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<ApiFailure | null>(null);

	const mutation = useMutation({
		mutationFn: (values: LinkFormValues) =>
			createLinkFn({ data: { body: toRequestBody(values), teamId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// A render can't throw a redirect the way a loader/`beforeLoad` can —
			// see `LinksError`'s docstring in the list route for the same point —
			// and this is further still: an event-handler callback, not even a
			// render. `router.navigate` is the imperative call for exactly that.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async () => {
			setFailure(null);
			// `queryClient`/`router` here are the real instances from React
			// context, satisfying `afterCreate`'s narrower parameter types
			// structurally — no cast needed. Those narrower types are what let
			// the same function also be called with hand-built fakes in the test
			// for this property.
			await afterCreate(queryClient, router, teamId);
			await router.navigate({ params: { teamId }, to: '/teams/$teamId/links' });
		},
	});

	const fieldErrors = failure?.kind === 'fields' ? failure.fields : undefined;
	// `fields` renders on the form itself, via `fieldErrors` above — a second,
	// generic message here would be the "banner about an error" this task's
	// own rule says a field error must not become.
	const formMessage = failure && failure.kind !== 'fields' ? t(`errors.${failure.kind}`) : null;

	return (
		<>
			<h1>{t('links.create')}</h1>
			{formMessage ? <p role="alert">{formMessage}</p> : null}
			<LinkForm
				domains={domains}
				fieldErrors={fieldErrors}
				onSubmit={(values) => {
					mutation.mutate(values);
				}}
			/>
		</>
	);
}
