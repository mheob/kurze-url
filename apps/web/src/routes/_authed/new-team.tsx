import { useForm } from '@tanstack/react-form';
import { useMutation } from '@tanstack/react-query';
import { createFileRoute, notFound, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../../components/ui/button';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import {
	suggestTeamSlug,
	TEAM_SLUG_MAX_LENGTH,
	TEAM_SLUG_MIN_LENGTH,
	TEAM_SLUG_PATTERN,
} from '../../lib/team-slug';
import { createTeamFn } from '../../server/teams';
import type { Me } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file doesn't own: TanStack Router's own `beforeLoad` option shape, TanStack Form's `onSubmit`/
   field-validator options and its `field` render prop, React's `FormEvent`/`ChangeEvent` on the
   `<form>`/`<input>` handlers, or the generated `@kurze-url/api-client` `Team` type `createTeamFn`
   resolves to. `Readonly<>` is shallow and none of these is a declaration this file can edit. */

/**
 * 404, not 403, and the same reasoning `requireTeamId` gives: a route a
 * visitor may not use should not confirm that it exists. Maintainer status is
 * not tenant data, so nothing leaks either way — but two guards in one tree
 * answering differently is the kind of inconsistency that later gets copied
 * into a route where it does matter.
 *
 * @param me - The signed-in caller, from `GET /v1/me`.
 */
export function assertMaintainer(me: Me): void {
	// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `notFound()` is its control flow, not an Error.
	if (!me.is_maintainer) throw notFound();
}

/**
 * At `/new-team`, not `/teams/new`: a static segment under `/teams/` is matched
 * before the dynamic `$teamSlug`, so a team whose slug were `new` would have
 * this form rendered at its own URL forever. The Go API also refuses a
 * reserved slug (`reservedTeamSlugs` in `internal/api/teams.go`), which is what
 * covers the *next* static child route someone adds under `/teams/`.
 */
export const Route = createFileRoute('/_authed/new-team')({
	beforeLoad: ({ context }) => {
		assertMaintainer(context.me);
	},
	component: RouteComponent,
});

/**
 * Exported and translate-injected so it can be unit-tested without rendering
 * the form — the same shape `assertMaintainer` above uses for the same reason.
 *
 * @param value - The raw field value, trimmed before checking.
 * @param t - The translation function to render an error key through.
 * @returns The translated error message, or `undefined` if the value is valid.
 */
export function validateSlugField(value: string, t: (key: string) => string): string | undefined {
	const slug = value.trim();
	if (slug === '') return t('teams.slugRequired');
	if (
		slug.length < TEAM_SLUG_MIN_LENGTH ||
		slug.length > TEAM_SLUG_MAX_LENGTH ||
		!TEAM_SLUG_PATTERN.test(slug)
	) {
		return t('teams.slugInvalid');
	}
	return undefined;
}

/**
 * Exported for the same reason `validateSlugField` above is: the name-to-slug
 * suggestion wiring lives entirely in this component's JSX event handlers, so
 * proving it works means rendering it, not just calling a pure function.
 *
 * @returns The rendered new-team form.
 */
export function RouteComponent(): React.JSX.Element {
	const { t } = useTranslation();
	const router = useRouter();
	const [failure, setFailure] = useState<ApiFailure | null>(null);

	const mutation = useMutation({
		mutationFn: async ({ name, slug }: Readonly<{ name: string; slug: string }>) =>
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
			await router.navigate({ params: { teamSlug: team.slug }, to: '/teams/$teamSlug/links' });
		},
	});

	const form = useForm({
		defaultValues: { name: '', slug: '' },
		onSubmit: ({ value }) => {
			mutation.mutate({ name: value.name, slug: value.slug.trim() });
		},
	});

	const nameFieldError = failure?.kind === 'fields' ? failure.fields.name : undefined;
	// Two flat ternaries joined by `??`, not one nested inside the other: `failure.kind` can only
	// ever be one of the two at a time, so at most one side ever produces a value.
	const slugFieldError =
		(failure?.kind === 'slugTaken' ? t('teams.slugTaken') : undefined) ??
		(failure?.kind === 'fields' ? failure.fields.slug : undefined);
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
			{formMessage !== null ? <p role="alert">{formMessage}</p> : null}
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
							nameFieldError ??
							(field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

						return (
							<div>
								<label htmlFor="name">{t('teams.name')}</label>
								<input
									aria-describedby={errorMessage !== undefined ? errorId : undefined}
									aria-invalid={errorMessage !== undefined ? true : undefined}
									id="name"
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event) => {
										field.handleChange(event.target.value);
										if (form.getFieldMeta('slug')?.isTouched !== true) {
											// `dontUpdateMeta` alone is not enough: `setFieldValue`'s own
											// `validateField` call (run unless `dontValidate` is also set)
											// marks the field touched as a side effect of validating it,
											// independently of the `dontUpdateMeta` flag above — see
											// `FormApi.ts`'s `validateField`. Both are required to write the
											// suggestion without it counting as the maintainer's own edit.
											form.setFieldValue('slug', suggestTeamSlug(event.target.value), {
												dontUpdateMeta: true,
												dontValidate: true,
											});
										}
									}}
									required
									value={field.state.value}
								/>
								{errorMessage !== undefined ? (
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
					validators={{ onChange: ({ value }) => validateSlugField(value, t) }}
				>
					{(field) => {
						const hintId = 'slug-hint';
						const errorId = 'slug-error';
						const errorMessage =
							slugFieldError ??
							(field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

						return (
							<div>
								<label htmlFor="slug">{t('teams.slug')}</label>
								<input
									aria-describedby={errorMessage !== undefined ? `${hintId} ${errorId}` : hintId}
									aria-invalid={errorMessage !== undefined ? true : undefined}
									id="slug"
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event) => {
										field.handleChange(event.target.value);
									}}
									required
									value={field.state.value}
								/>
								<p id={hintId}>{t('teams.slugHint')}</p>
								{errorMessage !== undefined ? (
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
