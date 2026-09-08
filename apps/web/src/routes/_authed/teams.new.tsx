import { useForm } from '@tanstack/react-form';
import { useMutation } from '@tanstack/react-query';
import { createFileRoute, notFound, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../../components/ui/button';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { createTeamFn } from '../../server/teams';
import { type Me } from '../_authed';

/**
 * 404, not 403, and the same reasoning `assertMembership` gives: a route a
 * visitor may not use should not confirm that it exists. Maintainer status is
 * not tenant data, so nothing leaks either way — but two guards in one tree
 * answering differently is the kind of inconsistency that later gets copied
 * into a route where it does matter.
 */
export function assertMaintainer(me: Me): void {
	if (!me.is_maintainer) throw notFound();
}

export const Route = createFileRoute('/_authed/teams/new')({
	beforeLoad: ({ context }) => {
		assertMaintainer(context.me);
	},
	component: RouteComponent,
});

function RouteComponent(): React.JSX.Element {
	const { t } = useTranslation();
	const router = useRouter();
	const [failure, setFailure] = useState<ApiFailure | null>(null);

	const mutation = useMutation({
		mutationFn: ({ name, slug }: { name: string; slug: string }) =>
			createTeamFn({ data: { name, slug } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// A mutation callback is not a render and not a loader, so it cannot
			// throw a redirect — see the same note on the create-link route.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async (team) => {
			setFailure(null);
			// `router.invalidate()` before navigating, not after: `_authed`'s
			// `beforeLoad` caches `me` for the whole authenticated tree, and the
			// new team only exists in `me.memberships` once that runs again.
			// Without this the team switcher on the destination page renders the
			// membership list from before the team existed.
			await router.invalidate();
			await router.navigate({ params: { teamId: team.id }, to: '/teams/$teamId/links' });
		},
	});

	const form = useForm({
		defaultValues: { name: '', slug: '' },
		onSubmit: ({ value }) => {
			mutation.mutate({ name: value.name, slug: value.slug });
		},
	});

	const fieldError = failure?.kind === 'fields' ? failure.fields.name : undefined;
	const slugFieldError =
		failure?.kind === 'slugTaken'
			? t('teams.slugTaken')
			: failure?.kind === 'fields'
				? failure.fields.slug
				: undefined;
	// A field error renders on the field itself; a second generic banner for
	// the same failure is what the create-link route deliberately avoids. A
	// taken slug is the same case: it renders once, on the slug field above,
	// never in this banner too.
	const formMessage =
		failure && failure.kind !== 'fields' && failure.kind !== 'slugTaken'
			? t(`errors.${failure.kind}`)
			: null;

	return (
		<>
			<h1>{t('teams.create')}</h1>
			{formMessage ? <p role="alert">{formMessage}</p> : null}
			<form
				onSubmit={(event) => {
					event.preventDefault();
					event.stopPropagation();
					void form.handleSubmit();
				}}
			>
				<form.Field
					name="name"
					validators={{
						onChange: ({ value }) => (value.trim() === '' ? t('teams.nameRequired') : undefined),
					}}
				>
					{(field) => {
						const errorId = 'name-error';
						const errorMessage =
							fieldError ?? (field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

						return (
							<div>
								<label htmlFor="name">{t('teams.name')}</label>
								<input
									aria-describedby={errorMessage ? errorId : undefined}
									aria-invalid={errorMessage ? true : undefined}
									id="name"
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event) => field.handleChange(event.target.value)}
									required
									value={field.state.value}
								/>
								{errorMessage ? (
									<p id={errorId} role="alert">
										{errorMessage}
									</p>
								) : null}
							</div>
						);
					}}
				</form.Field>

				<form.Field
					name="slug"
					validators={{
						onChange: ({ value }) => (value.trim() === '' ? t('teams.slugRequired') : undefined),
					}}
				>
					{(field) => {
						const errorId = 'slug-error';
						const errorMessage =
							slugFieldError ??
							(field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

						return (
							<div>
								<label htmlFor="slug">{t('teams.slug')}</label>
								<input
									aria-describedby={errorMessage ? errorId : undefined}
									aria-invalid={errorMessage ? true : undefined}
									id="slug"
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event) => field.handleChange(event.target.value)}
									required
									value={field.state.value}
								/>
								{errorMessage ? (
									<p id={errorId} role="alert">
										{errorMessage}
									</p>
								) : null}
							</div>
						);
					}}
				</form.Field>

				<Button disabled={mutation.isPending} type="submit">
					{t('teams.save')}
				</Button>
			</form>
		</>
	);
}
