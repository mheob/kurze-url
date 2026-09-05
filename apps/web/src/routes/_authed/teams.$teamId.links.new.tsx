import type { CreateLinkInputBodyWritable } from '@kurze-url/api-client';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { LinkForm, type LinkFormValues } from '../../components/link-form';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { createLinkFn } from '../../server/links';
import { assertMembership } from '../_authed';

export const Route = createFileRoute('/_authed/teams/$teamId/links/new')({
	beforeLoad: ({ context, params }) => {
		assertMembership(context.me.memberships, params.teamId);
	},
	component: RouteComponent,
});

/**
 * Turns the form's own value shape into the API's request body. Kept out of
 * `LinkForm` itself so that component stays a plain "here are the values"
 * contract the edit route (Task 11, per the plan's pre-flight scan) can reuse
 * without also inheriting how the create route's mutation is built.
 *
 * An empty `slug`/`expires_at` becomes `undefined`, not `''`: the API
 * generates a slug when the field is omitted (`CreateLinkInputBodyWritable`'s
 * own doc comment), and Huma's `expires_at` validation expects either a real
 * timestamp or nothing, never an empty string.
 */
function toRequestBody(values: LinkFormValues): CreateLinkInputBodyWritable {
	return {
		analytics_enabled: values.analytics_enabled,
		destination_url: values.destination_url,
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
				fieldErrors={fieldErrors}
				onSubmit={(values) => {
					mutation.mutate(values);
				}}
			/>
		</>
	);
}
